package com.bitchat.android.mesh.wifi

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.wifi.WifiManager
import android.util.Log
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket

/**
 * Discovers the relay daemon on the current WiFi network.
 *
 * Strategy (tried in order):
 * 1. Modern API: read gateway from [ConnectivityManager.getLinkProperties]
 * 2. Legacy API: read gateway from [WifiManager.getDhcpInfo]
 * 3. Fallback: probe well-known mesh router IPs (192.168.1.1, 192.168.8.1)
 *
 * All probe sockets are bound to the WiFi [Network] so traffic never
 * leaks onto mobile data.
 */
object RelayDiscovery {

    private const val TAG = "RelayDiscovery"

    private val FALLBACK_IPS = listOf("192.168.1.1", "192.168.8.1", "10.0.0.1")

    /**
     * Returns the relay daemon address (IP string) or null if not found.
     * [wifiNetwork] is used to bind probe sockets to WiFi.
     * Must be called from a background thread.
     */
    fun discover(context: Context, wifiNetwork: Network? = null): String? {
        val candidates = mutableListOf<String>()

        getGatewayModern(context)?.let { candidates.add(it) }
        getGatewayLegacy(context)?.let { if (it !in candidates) candidates.add(it) }
        FALLBACK_IPS.forEach { if (it !in candidates) candidates.add(it) }

        for (ip in candidates) {
            Log.d(TAG, "Probing $ip:${WiFiMeshConfig.RELAY_PORT}")
            if (probeHost(ip, WiFiMeshConfig.RELAY_PORT, wifiNetwork)) {
                Log.i(TAG, "Relay daemon found at $ip")
                return ip
            }
        }
        Log.d(TAG, "No relay daemon found (tried ${candidates.size} candidates)")
        return null
    }

    private fun getGatewayModern(context: Context): String? {
        try {
            val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
            val wifiNet = cm.allNetworks.firstOrNull { net ->
                cm.getNetworkCapabilities(net)
                    ?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true
            } ?: return null

            val linkProps = cm.getLinkProperties(wifiNet) ?: return null
            for (route in linkProps.routes) {
                if (route.isDefaultRoute && route.gateway != null) {
                    val gw = route.gateway!!.hostAddress
                    Log.d(TAG, "Modern gateway: $gw")
                    return gw
                }
            }
        } catch (e: Exception) {
            Log.w(TAG, "Modern gateway lookup failed: ${e.message}")
        }
        return null
    }

    private fun getGatewayLegacy(context: Context): String? {
        try {
            val wifiManager = context.applicationContext
                .getSystemService(Context.WIFI_SERVICE) as? WifiManager ?: return null
            @Suppress("DEPRECATION")
            val dhcp = wifiManager.dhcpInfo ?: return null
            val gw = dhcp.gateway
            if (gw == 0) return null
            val ip = InetAddress.getByAddress(
                byteArrayOf(
                    (gw and 0xFF).toByte(),
                    (gw shr 8 and 0xFF).toByte(),
                    (gw shr 16 and 0xFF).toByte(),
                    (gw shr 24 and 0xFF).toByte()
                )
            ).hostAddress
            Log.d(TAG, "Legacy gateway: $ip")
            return ip
        } catch (e: Exception) {
            Log.w(TAG, "Legacy gateway lookup failed: ${e.message}")
            return null
        }
    }

    private fun probeHost(host: String, port: Int, wifiNetwork: Network?): Boolean {
        return try {
            Socket().use { socket ->
                wifiNetwork?.bindSocket(socket)
                socket.connect(
                    InetSocketAddress(host, port),
                    WiFiMeshConfig.DISCOVERY_TIMEOUT_MS
                )
                true
            }
        } catch (_: Exception) {
            false
        }
    }
}
