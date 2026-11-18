package com.locationkeeper.app

import java.io.Closeable
import javax.net.ssl.SSLException

/** Scoped routing for one idempotent upload operation; the default route is always tried first. */
class UploadNetworkRetry<N>(
    private val fallbackAllowed: () -> Boolean,
    private val acquireFallback: () -> UploadNetworkLease<N>,
) {
    fun <T> run(request: (N?) -> T): T {
        try {
            return request(null)
        } catch (error: SSLException) {
            if (!fallbackAllowed()) throw error
            return acquireFallback().use { request(it.network) }
        }
    }
}

class UploadNetworkLease<N>(val network: N, private val release: () -> Unit) : Closeable {
    override fun close() = release()
}
