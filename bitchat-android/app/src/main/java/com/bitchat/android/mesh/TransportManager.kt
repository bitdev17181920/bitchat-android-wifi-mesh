package com.bitchat.android.mesh

import android.util.Log
import com.bitchat.android.model.RoutedPacket
import com.bitchat.android.protocol.BitchatPacket

/**
 * Orchestrates one or more [MeshTransport] implementations.
 *
 * - [broadcastPacket]: forwards to **all** active transports so that every
 *   reachable peer receives the packet regardless of which transport they use.
 * - [sendToPeer] / [sendPacketToPeer]: tries transports in priority order
 *   (WiFi mesh first, then Bluetooth) and returns as soon as one succeeds.
 */
class TransportManager {

    companion object {
        private const val TAG = "TransportManager"
    }

    private val transports = mutableListOf<MeshTransport>()

    private val sendPriority = listOf(TransportType.WIFI_MESH, TransportType.BLUETOOTH)

    fun addTransport(transport: MeshTransport) {
        transports.add(transport)
        Log.i(TAG, "Added transport: ${transport.transportType}")
    }

    fun removeTransport(transport: MeshTransport) {
        transports.remove(transport)
        Log.i(TAG, "Removed transport: ${transport.transportType}")
    }

    fun broadcastPacket(routed: RoutedPacket) {
        for (transport in transports) {
            if (!transport.isActive) continue
            try {
                transport.broadcastPacket(routed)
            } catch (e: Exception) {
                Log.e(TAG, "broadcastPacket failed on ${transport.transportType}: ${e.message}")
            }
        }
    }

    fun sendToPeer(peerID: String, routed: RoutedPacket): Boolean {
        for (type in sendPriority) {
            val transport = transports.firstOrNull { it.transportType == type && it.isActive }
                ?: continue
            try {
                if (transport.sendToPeer(peerID, routed)) return true
            } catch (e: Exception) {
                Log.e(TAG, "sendToPeer failed on $type: ${e.message}")
            }
        }
        return false
    }

    fun sendPacketToPeer(peerID: String, packet: BitchatPacket): Boolean {
        for (type in sendPriority) {
            val transport = transports.firstOrNull { it.transportType == type && it.isActive }
                ?: continue
            try {
                if (transport.sendPacketToPeer(peerID, packet)) return true
            } catch (e: Exception) {
                Log.e(TAG, "sendPacketToPeer failed on $type: ${e.message}")
            }
        }
        return false
    }

    fun cancelTransfer(transferId: String): Boolean {
        return transports.any {
            try { it.cancelTransfer(transferId) } catch (_: Exception) { false }
        }
    }

    fun getReachablePeerIDs(): Set<String> {
        return transports
            .filter { it.isActive }
            .flatMap { it.getReachablePeerIDs() }
            .toSet()
    }

    fun getTransport(type: TransportType): MeshTransport? {
        return transports.firstOrNull { it.transportType == type }
    }

    fun hasActiveTransport(type: TransportType): Boolean {
        return transports.any { it.transportType == type && it.isActive }
    }
}
