package com.bitchat.android.mesh.wifi

import java.nio.ByteBuffer
import java.security.MessageDigest

/**
 * Solves proof-of-work challenges from the relay daemon.
 * Finds a uint64 `solution` such that SHA-256(nonce || solution)
 * has at least [difficulty] leading zero bits.
 */
object ProofOfWork {

    fun solve(nonce: ByteArray, difficulty: Int): Long {
        require(nonce.size == 32) { "Nonce must be 32 bytes" }
        require(difficulty in 1..64) { "Difficulty must be 1..64" }

        val buf = ByteArray(40)
        System.arraycopy(nonce, 0, buf, 0, 32)
        val digest = MessageDigest.getInstance("SHA-256")

        var solution = 0L
        while (true) {
            ByteBuffer.wrap(buf, 32, 8).putLong(solution)
            digest.update(buf)
            val hash = digest.digest()
            if (hasLeadingZeros(hash, difficulty)) return solution
            solution++
        }
    }

    fun verify(nonce: ByteArray, solution: Long, difficulty: Int): Boolean {
        val buf = ByteArray(40)
        System.arraycopy(nonce, 0, buf, 0, 32)
        ByteBuffer.wrap(buf, 32, 8).putLong(solution)
        val hash = MessageDigest.getInstance("SHA-256").digest(buf)
        return hasLeadingZeros(hash, difficulty)
    }

    private fun hasLeadingZeros(hash: ByteArray, n: Int): Boolean {
        val fullBytes = n / 8
        val remainBits = n % 8
        for (i in 0 until fullBytes) {
            if (hash[i] != 0.toByte()) return false
        }
        if (remainBits > 0) {
            val mask = (0xFF shl (8 - remainBits)).toByte()
            if ((hash[fullBytes].toInt() and mask.toInt()) != 0) return false
        }
        return true
    }
}
