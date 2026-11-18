package com.locationkeeper.app

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import java.io.IOException
import java.io.InterruptedIOException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference

/** Uses cellular only after Wi-Fi TLS fails. Never changes device data settings or the process route. */
class CellularUploadNetwork(private val context: Context) {
    private val connectivity = context.getSystemService(ConnectivityManager::class.java)
    val retry = UploadNetworkRetry<Network>(::fallbackAllowed, ::acquire)

    private fun fallbackAllowed(): Boolean = try {
        val current = connectivity.getNetworkCapabilities(connectivity.activeNetwork)
        context.checkSelfPermission(Manifest.permission.CHANGE_NETWORK_STATE) == PackageManager.PERMISSION_GRANTED &&
            current?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true &&
            !current.hasTransport(NetworkCapabilities.TRANSPORT_VPN)
    } catch (_: SecurityException) {
        false
    }

    /** Called only by the app's background upload executor. The request is bounded and always released. */
    private fun acquire(): UploadNetworkLease<Network> {
        val signal = CountDownLatch(1)
        val found = AtomicReference<Network?>()
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                found.set(network)
                signal.countDown()
            }
            override fun onUnavailable() { signal.countDown() }
            override fun onLost(network: Network) { found.compareAndSet(network, null) }
        }
        var registered = false
        var handedOff = false
        fun release() {
            if (registered) runCatching { connectivity.unregisterNetworkCallback(callback) }
        }
        try {
            val request = NetworkRequest.Builder()
                .addTransportType(NetworkCapabilities.TRANSPORT_CELLULAR)
                .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                .build()
            connectivity.requestNetwork(request, callback, 10_000)
            registered = true
            if (!signal.await(11, TimeUnit.SECONDS)) throw IOException(UNAVAILABLE)
            val network = found.get() ?: throw IOException(UNAVAILABLE)
            // Respect a VPN enabled while the cellular request was being established.
            if (connectivity.getNetworkCapabilities(connectivity.activeNetwork)
                    ?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true) {
                throw IOException("A VPN is active. Retry using the VPN connection. Your readings remain saved on this phone.")
            }
            return UploadNetworkLease(network, ::release).also { handedOff = true }
        } catch (error: InterruptedException) {
            Thread.currentThread().interrupt()
            throw InterruptedIOException("Upload interrupted. Your readings remain saved on this phone.").apply { initCause(error) }
        } catch (error: SecurityException) {
            throw IOException(UNAVAILABLE, error)
        } catch (error: RuntimeException) {
            throw IOException(UNAVAILABLE, error)
        } finally {
            if (!handedOff) release()
        }
    }

    companion object {
        private const val UNAVAILABLE =
            "Wi-Fi HTTPS failed and mobile data is unavailable. Complete Wi-Fi sign-in or enable mobile data. Your readings remain saved on this phone."
    }
}
