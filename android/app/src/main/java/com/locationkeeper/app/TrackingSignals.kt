package com.locationkeeper.app

/** Try any internet-capable default network; validation recovery is only a retry signal. */
class UploadNetworkState<T> {
    private var current: T? = null
    private var validated = false
    @Volatile var ready: Boolean = false
        private set

    fun available(network: T) {
        current = network
        ready = false
        validated = false
    }

    fun capabilitiesChanged(network: T, hasInternet: Boolean, validated: Boolean): Boolean {
        if (network != current) return false
        val wasReady = ready
        val wasValidated = this.validated
        // Android's connectivity probe may fail even when our HTTPS endpoint is reachable.
        ready = hasInternet
        this.validated = hasInternet && validated
        return ready && (!wasReady || (this.validated && !wasValidated))
    }

    fun lost(network: T) {
        if (network == current) {
            current = null
            ready = false
            validated = false
        }
    }
}
