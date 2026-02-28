package com.bitchat.android.mesh.wifi

import android.util.Log
import android.net.Network
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.IOException
import java.net.InetSocketAddress
import java.nio.ByteBuffer
import java.security.SecureRandom
import java.security.cert.X509Certificate
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

/**
 * Manages a single TLS connection to a relay daemon, including the
 * handshake (HELLO → CHALLENGE → SOLUTION → ACCEPT) and the
 * read/write loops for DATA and PING/PONG frames.
 */
class RelayConnection(
    private val context: android.content.Context,
    private val peerID: String,
    private val wifiNetwork: Network? = null,
    private val onDataReceived: (ByteArray) -> Unit,
    private val onDisconnected: () -> Unit
) {
    companion object {
        private const val TAG = "RelayConnection"
    }

    @Volatile private var socket: SSLSocket? = null
    @Volatile private var output: DataOutputStream? = null
    @Volatile private var connected = false

    val isConnected: Boolean get() = connected

    /**
     * Connects to the relay, performs the PoW handshake, and starts
     * the read loop (blocking). Returns only when the connection drops.
     */
    fun connectAndRun(host: String) {
        try {
            Log.i(TAG, "Connecting to $host:${WiFiMeshConfig.RELAY_PORT}")

            val sslContext = SSLContext.getInstance("TLS")
            sslContext.init(null, arrayOf<TrustManager>(AcceptAllTrustManager()), SecureRandom())

            val sslSocket = sslContext.socketFactory.createSocket() as SSLSocket
            socket = sslSocket // assign early so disconnect() can clean up on any failure

            // Bind socket to WiFi network so Android doesn't route through mobile data
            if (wifiNetwork != null) {
                wifiNetwork.bindSocket(sslSocket)
                Log.d(TAG, "Socket bound to WiFi network")
            }

            sslSocket.soTimeout = WiFiMeshConfig.KEEPALIVE_TIMEOUT_MS.toInt()
            sslSocket.connect(
                InetSocketAddress(host, WiFiMeshConfig.RELAY_PORT),
                WiFiMeshConfig.CONNECT_TIMEOUT_MS
            )
            sslSocket.startHandshake()
            val input = DataInputStream(sslSocket.inputStream)
            val out = DataOutputStream(sslSocket.outputStream)
            output = out

            performHandshake(input, out)
            connected = true
            Log.i(TAG, "Connected and authenticated to relay at $host")

            readLoop(input)
        } catch (e: Exception) {
            Log.e(TAG, "Connection failed: ${e.message}")
        } finally {
            disconnect()
        }
    }

    private fun performHandshake(input: DataInputStream, output: DataOutputStream) {
        // --- HELLO (with optional APK cert hash for attestation) ---
        val peerIdBytes = peerID.toByteArray(Charsets.UTF_8)
        if (peerIdBytes.size > 255)
            throw IOException("PeerID too long: ${peerIdBytes.size} bytes (max 255)")
        val certHash = AppAttestation.getSigningCertHash(context)
        val payloadSize = 3 + peerIdBytes.size + (if (certHash != null) WiFiMeshConfig.CERT_HASH_SIZE else 0)
        val helloPayload = ByteBuffer.allocate(payloadSize)
            .putShort(WiFiMeshConfig.PROTOCOL_VERSION)
            .put(peerIdBytes.size.toByte())
            .put(peerIdBytes)
        if (certHash != null) {
            helloPayload.put(certHash)
            Log.d(TAG, "HELLO includes APK cert hash (${certHash.size} bytes)")
        }
        writeFrame(output, WiFiMeshConfig.FRAME_HELLO, helloPayload.array())

        // --- CHALLENGE ---
        val challenge = readFrame(input)
        if (challenge.type != WiFiMeshConfig.FRAME_CHALLENGE)
            throw IOException("Expected CHALLENGE (0x02), got 0x${String.format("%02x", challenge.type)}")
        if (challenge.payload.size != 33)
            throw IOException("CHALLENGE wrong size: ${challenge.payload.size}")

        val nonce = challenge.payload.copyOfRange(0, 32)
        val difficulty = challenge.payload[32].toInt() and 0xFF
        Log.d(TAG, "Solving PoW (difficulty=$difficulty)")

        // --- SOLUTION ---
        val solution = ProofOfWork.solve(nonce, difficulty)
        val solutionPayload = ByteBuffer.allocate(8).putLong(solution).array()
        writeFrame(output, WiFiMeshConfig.FRAME_SOLUTION, solutionPayload)

        // --- ACCEPT/REJECT ---
        val response = readFrame(input)
        when (response.type) {
            WiFiMeshConfig.FRAME_ACCEPT -> Log.i(TAG, "Handshake accepted")
            WiFiMeshConfig.FRAME_REJECT -> {
                val reason = String(response.payload, Charsets.UTF_8)
                throw IOException("Handshake rejected: $reason")
            }
            else -> throw IOException("Unexpected frame 0x${String.format("%02x", response.type)}")
        }
    }

    private fun readLoop(input: DataInputStream) {
        while (connected) {
            val frame = readFrame(input)
            when (frame.type) {
                WiFiMeshConfig.FRAME_DATA -> onDataReceived(frame.payload)
                WiFiMeshConfig.FRAME_PONG -> { /* keepalive ack, nothing to do */ }
                else -> Log.w(TAG, "Unexpected frame 0x${String.format("%02x", frame.type)}")
            }
        }
    }

    fun sendData(data: ByteArray) {
        val out = output ?: return
        try {
            writeFrame(out, WiFiMeshConfig.FRAME_DATA, data)
        } catch (e: IOException) {
            Log.e(TAG, "sendData failed: ${e.message}")
            disconnect()
        }
    }

    fun sendPing() {
        val out = output ?: return
        try {
            writeFrame(out, WiFiMeshConfig.FRAME_PING, ByteArray(0))
        } catch (e: IOException) {
            Log.e(TAG, "sendPing failed: ${e.message}")
            disconnect()
        }
    }

    fun disconnect() {
        if (!connected && socket == null) return
        connected = false
        try { socket?.close() } catch (_: Exception) {}
        socket = null
        output = null
        onDisconnected()
    }

    // --- Wire protocol helpers (matches relay-daemon protocol.go) ---

    private data class Frame(val type: Byte, val payload: ByteArray)

    private fun readFrame(input: DataInputStream): Frame {
        val type = input.readByte()
        val length = input.readInt()
        if (length < 0 || length > WiFiMeshConfig.MAX_FRAME_PAYLOAD)
            throw IOException("Frame too large: $length")
        val payload = ByteArray(length)
        if (length > 0) input.readFully(payload)
        return Frame(type, payload)
    }

    private fun writeFrame(output: DataOutputStream, type: Byte, payload: ByteArray) {
        synchronized(output) {
            val buf = ByteArray(5 + payload.size)
            buf[0] = type
            ByteBuffer.wrap(buf, 1, 4).putInt(payload.size)
            System.arraycopy(payload, 0, buf, 5, payload.size)
            output.write(buf)
            output.flush()
        }
    }

    /**
     * Trust-all manager for self-signed relay certificates.
     * Security comes from the PoW handshake and app-level Noise encryption,
     * not from TLS CA trust. A future enhancement will add certificate pinning
     * (Phase 5) where the relay cert fingerprint is shared via QR code.
     */
    private class AcceptAllTrustManager : X509TrustManager {
        override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) {}
        override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {}
        override fun getAcceptedIssuers(): Array<X509Certificate> = arrayOf()
    }
}
