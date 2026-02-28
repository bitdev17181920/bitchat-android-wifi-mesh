package com.bitchat.android.mesh.wifi

object WiFiMeshConfig {
    const val RELAY_PORT = 7275
    const val PROTOCOL_VERSION: Short = 1
    const val MAX_FRAME_PAYLOAD = 65536

    // Frame types — must match relay-daemon protocol.go
    const val FRAME_HELLO: Byte = 0x01
    const val FRAME_CHALLENGE: Byte = 0x02
    const val FRAME_SOLUTION: Byte = 0x03
    const val FRAME_ACCEPT: Byte = 0x04
    const val FRAME_REJECT: Byte = 0x05
    const val FRAME_DATA: Byte = 0x10
    const val FRAME_PING: Byte = 0x20
    const val FRAME_PONG: Byte = 0x21

    const val KEEPALIVE_INTERVAL_MS = 30_000L
    const val KEEPALIVE_TIMEOUT_MS = 90_000L
    const val HANDSHAKE_TIMEOUT_MS = 30_000L
    const val CONNECT_TIMEOUT_MS = 5_000
    const val DISCOVERY_TIMEOUT_MS = 3_000

    const val RECONNECT_BASE_DELAY_MS = 2_000L
    const val RECONNECT_MAX_DELAY_MS = 60_000L

    const val CERT_HASH_SIZE = 32

    /**
     * Debug override: set to a relay IP/hostname to bypass WiFi network
     * detection. Useful for emulator testing where the emulator's network
     * isn't recognized as WiFi. Set to null for production.
     * Example: "10.0.2.2" (host loopback from Android emulator)
     */
    @JvmStatic
    var DEBUG_RELAY_HOST: String? = null

    /**
     * When true, skip WiFi NetworkCallback and connect immediately on start.
     * Enables emulator and desktop testing without a real WiFi network.
     */
    @JvmStatic
    var DEBUG_SKIP_WIFI_CHECK: Boolean = false

    /**
     * When true, accept unsigned packets from WiFi mesh peers.
     * Only for testing with the Python/Go test client that doesn't implement
     * Ed25519 signing. MUST be false in production.
     */
    @JvmStatic
    var DEBUG_SKIP_SIGNATURE_CHECK: Boolean = false
}
