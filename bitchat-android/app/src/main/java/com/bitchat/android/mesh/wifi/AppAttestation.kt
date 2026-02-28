package com.bitchat.android.mesh.wifi

import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.util.Log
import java.security.MessageDigest

/**
 * Extracts the SHA-256 hash of the APK signing certificate. The relay
 * daemon keeps an allowlist of permitted hashes, so only genuine
 * BitChat builds can connect — modified or repackaged APKs are rejected
 * during the handshake even without Google Play services.
 */
object AppAttestation {
    private const val TAG = "AppAttestation"

    fun getSigningCertHash(context: Context): ByteArray? {
        return try {
            val packageInfo = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                context.packageManager.getPackageInfo(
                    context.packageName,
                    PackageManager.GET_SIGNING_CERTIFICATES
                )
            } else {
                @Suppress("DEPRECATION")
                context.packageManager.getPackageInfo(
                    context.packageName,
                    PackageManager.GET_SIGNATURES
                )
            }

            val signature = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                packageInfo.signingInfo?.apkContentsSigners?.firstOrNull()
            } else {
                @Suppress("DEPRECATION")
                packageInfo.signatures?.firstOrNull()
            }

            if (signature == null) {
                Log.w(TAG, "No signing certificate found")
                return null
            }

            MessageDigest.getInstance("SHA-256").digest(signature.toByteArray())
        } catch (e: Exception) {
            Log.e(TAG, "Failed to get signing cert hash: ${e.message}")
            null
        }
    }
}
