package com.bitchat.android.mesh

import com.bitchat.android.model.RoutedPacket
import com.bitchat.android.protocol.BitchatPacket

enum class TransportType {
    BLUETOOTH,
    WIFI_MESH
}

/**
 * Abstraction for a local mesh transport (BLE, WiFi mesh, etc.).
 *
 * Each implementation handles the physical sending/receiving of
 * [BitchatPacket] data over its specific radio or network link.
 * The [TransportManager] orchestrates one or more transports so that
 * higher-level code (BluetoothMeshService, MessageRouter) stays
 * transport-agnostic.
 */
interface MeshTransport {
    val transportType: TransportType
    val isActive: Boolean

    fun broadcastPacket(routed: RoutedPacket)
    fun sendToPeer(peerID: String, routed: RoutedPacket): Boolean
    fun sendPacketToPeer(peerID: String, packet: BitchatPacket): Boolean
    fun cancelTransfer(transferId: String): Boolean
    fun getReachablePeerIDs(): Set<String>
}
