package com.bitchat.android.mesh.wifi

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.util.Log
import com.bitchat.android.mesh.MeshTransport
import com.bitchat.android.mesh.TransportType
import com.bitchat.android.model.RoutedPacket
import com.bitchat.android.protocol.BinaryProtocol
import com.bitchat.android.protocol.BitchatPacket
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * WiFi mesh transport that connects to a BitChat relay daemon running
 * on a mesh router. Automatically discovers the relay when the phone
 * joins a WiFi network, performs the PoW handshake, and exchanges
 * packets over a TLS connection.
 *
 * Lifecycle is driven by Android's [ConnectivityManager.NetworkCallback]:
 * WiFi available → discover relay → connect → active.
 * WiFi lost → disconnect → inactive.
 */
class WiFiMeshTransport(
    private val context: Context,
    private val myPeerID: String
) : MeshTransport {

    companion object {
        private const val TAG = "WiFiMeshTransport"
    }

    override val transportType = TransportType.WIFI_MESH

    private val _isActive = AtomicBoolean(false)
    override val isActive: Boolean get() = _isActive.get()

    private val executor = Executors.newCachedThreadPool()
    private val scheduler = Executors.newSingleThreadScheduledExecutor()

    private var connection: RelayConnection? = null
    private var keepaliveFuture: ScheduledFuture<*>? = null
    private var networkCallback: ConnectivityManager.NetworkCallback? = null
    private val started = AtomicBoolean(false)

    private var reconnectDelay = WiFiMeshConfig.RECONNECT_BASE_DELAY_MS
    private val reconnecting = AtomicBoolean(false)

    // Peers reachable via WiFi mesh are tracked by peer IDs seen in
    // incoming packets. The relay fans out to all connected phones, so
    // every peer on the mesh is reachable if we have an active connection.
    private val reachablePeers = ConcurrentHashMap.newKeySet<String>()

    /** The relay host we're currently connected to (null when disconnected). */
    @Volatile var relayHost: String? = null
        private set

    var onPacketReceived: ((BitchatPacket, String) -> Unit)? = null

    fun start() {
        if (started.getAndSet(true)) return
        Log.i(TAG, "Starting WiFi mesh transport")
        if (WiFiMeshConfig.DEBUG_SKIP_WIFI_CHECK) {
            Log.w(TAG, "DEBUG: skipping WiFi check, connecting directly")
            // Still need to find the WiFi network for binding
            findWifiNetwork()
            executor.submit { discoverAndConnect() }
        } else {
            registerNetworkCallback()
        }
    }

    fun stop() {
        if (!started.getAndSet(false)) return
        Log.i(TAG, "Stopping WiFi mesh transport")
        unregisterNetworkCallback()
        disconnectRelay()
        keepaliveFuture?.cancel(false)
        reachablePeers.clear()
    }

    // --- MeshTransport sending methods ---

    override fun broadcastPacket(routed: RoutedPacket) {
        val data = BinaryProtocol.encode(routed.packet) ?: return
        connection?.sendData(data)
    }

    override fun sendToPeer(peerID: String, routed: RoutedPacket): Boolean {
        if (!isActive) return false
        val data = BinaryProtocol.encode(routed.packet) ?: return false
        connection?.sendData(data)
        return true
    }

    override fun sendPacketToPeer(peerID: String, packet: BitchatPacket): Boolean {
        if (!isActive) return false
        val data = BinaryProtocol.encode(packet) ?: return false
        connection?.sendData(data)
        return true
    }

    override fun cancelTransfer(transferId: String): Boolean = false

    override fun getReachablePeerIDs(): Set<String> = reachablePeers.toSet()

    // --- Network monitoring ---

    @Volatile private var wifiNetwork: Network? = null

    private fun registerNetworkCallback() {
        val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val request = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
            .build()

        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                Log.i(TAG, "WiFi available — starting relay discovery")
                wifiNetwork = network
                reconnectDelay = WiFiMeshConfig.RECONNECT_BASE_DELAY_MS
                executor.submit { discoverAndConnect() }
            }

            override fun onLost(network: Network) {
                Log.i(TAG, "WiFi lost — disconnecting relay")
                wifiNetwork = null
                disconnectRelay()
            }
        }

        cm.registerNetworkCallback(request, callback)
        networkCallback = callback

        // Also check current state in case WiFi is already connected
        val activeNetwork = cm.activeNetwork
        val caps = activeNetwork?.let { cm.getNetworkCapabilities(it) }
        if (caps?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true) {
            wifiNetwork = activeNetwork
            executor.submit { discoverAndConnect() }
        }
    }

    private fun unregisterNetworkCallback() {
        networkCallback?.let { cb ->
            try {
                val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
                cm.unregisterNetworkCallback(cb)
            } catch (_: Exception) {}
        }
        networkCallback = null
    }

    private fun findWifiNetwork() {
        try {
            val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
            for (network in cm.allNetworks) {
                val caps = cm.getNetworkCapabilities(network)
                if (caps?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true) {
                    wifiNetwork = network
                    Log.i(TAG, "Found WiFi network: $network")
                    return
                }
            }
            Log.w(TAG, "No WiFi network found - connection will use default route")
        } catch (e: Exception) {
            Log.e(TAG, "Error finding WiFi network: ${e.message}")
        }
    }

    // --- Connection lifecycle ---

    private fun discoverAndConnect() {
        if (!started.get()) return
        if (connection?.isConnected == true) return

        val host = WiFiMeshConfig.DEBUG_RELAY_HOST
            ?: RelayDiscovery.discover(context, wifiNetwork)
        if (host == null) {
            Log.d(TAG, "No relay daemon found on this network")
            return
        }

        Log.i(TAG, "Discovered relay at $host")
        connectToRelay(host)
    }

    private fun connectToRelay(host: String) {
        relayHost = host
        val relay = RelayConnection(
            context = context,
            peerID = myPeerID,
            wifiNetwork = wifiNetwork,
            onDataReceived = { data -> handleIncomingData(data) },
            onDisconnected = { handleDisconnect(host) }
        )
        connection = relay

        // connectAndRun blocks until disconnect
        relay.connectAndRun(host)
    }

    private fun handleIncomingData(data: ByteArray) {
        val packet = BinaryProtocol.decode(data)
        if (packet == null) {
            Log.w(TAG, "Failed to decode incoming packet (${data.size} bytes)")
            return
        }

        val senderIDHex = packet.senderID.joinToString("") { "%02x".format(it) }
        if (senderIDHex == myPeerID) return

        reachablePeers.add(senderIDHex)

        _isActive.set(true)
        startKeepaliveIfNeeded()

        onPacketReceived?.invoke(packet, senderIDHex)
    }

    private fun handleDisconnect(lastHost: String) {
        _isActive.set(false)
        relayHost = null
        keepaliveFuture?.cancel(false)
        keepaliveFuture = null
        connection = null

        if (!started.get()) return

        if (reconnecting.getAndSet(true)) return
        Log.i(TAG, "Scheduling reconnect in ${reconnectDelay}ms")
        scheduler.schedule({
            reconnecting.set(false)
            if (started.get()) {
                executor.submit { discoverAndConnect() }
            }
        }, reconnectDelay, TimeUnit.MILLISECONDS)

        reconnectDelay = (reconnectDelay * 2).coerceAtMost(WiFiMeshConfig.RECONNECT_MAX_DELAY_MS)
    }

    private fun startKeepaliveIfNeeded() {
        if (keepaliveFuture != null) return
        keepaliveFuture = scheduler.scheduleAtFixedRate(
            { connection?.sendPing() },
            WiFiMeshConfig.KEEPALIVE_INTERVAL_MS,
            WiFiMeshConfig.KEEPALIVE_INTERVAL_MS,
            TimeUnit.MILLISECONDS
        )
    }

    private fun disconnectRelay() {
        _isActive.set(false)
        relayHost = null
        keepaliveFuture?.cancel(false)
        keepaliveFuture = null
        connection?.disconnect()
        connection = null
        reachablePeers.clear()
    }
}
