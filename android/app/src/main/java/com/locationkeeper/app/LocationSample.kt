package com.locationkeeper.app

import java.time.Instant
import java.util.UUID

/** An observed event, optionally with a measured position. Upload time is always separate. */
data class LocationSample(
    val eventId: String,
    val deviceId: String,
    val capturedAt: String?,
    val latitude: Double?,
    val longitude: Double?,
    val accuracyM: Double?,
    val batteryPercent: Int?,
    val isMock: Boolean,
    val permissionAccuracy: String,
    val fixAgeMs: Long?,
    val receivedAt: String? = null,
    val recordedAt: String = capturedAt ?: Instant.now().toString(),
    val reason: String = "legacy",
    val scheduledFor: String? = null,
    val accuracyMode: String = "legacy",
    val locationStatus: String = if (capturedAt != null) "fresh" else "unavailable",
    val networkTransport: String = "unknown",
    val executedAt: String? = null,
    val executionStatus: String? = null,
    val airplaneModeEnabled: Boolean? = null,
) {
    init {
        UUID.fromString(eventId)
        UUID.fromString(deviceId)
        Instant.parse(recordedAt)
        executedAt?.let(Instant::parse)
        require(executionStatus == null || executionStatus in EXECUTION_STATUSES)
        require(airplaneModeEnabled == null || reason !in setOf("airplane_mode_on", "airplane_mode_off") ||
            airplaneModeEnabled == (reason == "airplane_mode_on"))
        capturedAt?.let(Instant::parse)
        receivedAt?.let(Instant::parse)
        scheduledFor?.let(Instant::parse)
        require(batteryPercent == null || batteryPercent in 0..100)
        require(reason in REASONS)
        require(accuracyMode in ACCURACY_MODES)
        require(locationStatus in LOCATION_STATUSES)
        require(networkTransport in NETWORK_TRANSPORTS)
        require((reason == "periodic") == (scheduledFor != null))

        val position = listOf(capturedAt, latitude, longitude, accuracyM, fixAgeMs)
        require(position.all { it == null } || position.all { it != null }) {
            "Position fields must be supplied together or all omitted."
        }
        if (capturedAt != null) {
            require(locationStatus == "fresh" || locationStatus == "last_known")
            require(permissionAccuracy == "precise" || permissionAccuracy == "approximate")
            require(requireNotNull(latitude).isFinite() && latitude in -90.0..90.0)
            require(requireNotNull(longitude).isFinite() && longitude in -180.0..180.0)
            require(requireNotNull(accuracyM).isFinite() && accuracyM in 0.0..10_000_000.0)
            require(requireNotNull(fixAgeMs) in 0..9_007_199_254_740_991L)
        } else {
            require(locationStatus == "unavailable" || locationStatus == "disabled")
            require(permissionAccuracy == "none")
            require(!isMock)
        }
    }

    companion object {
        const val MAX_FRESH_FIX_AGE_MS = 45_000L
        val REASONS = setOf("legacy", "manual", "periodic", "screen_wake", "location_on",
            "location_off", "internet_on", "internet_off", "network_changed", "tracking_start",
            "boot_recovery", "location_available", "app_launch", "airplane_mode_on", "airplane_mode_off")
        val ACCURACY_MODES = setOf("legacy", "battery_saving", "high_precision")
        val LOCATION_STATUSES = setOf("fresh", "last_known", "unavailable", "disabled")
        val EXECUTION_STATUSES = setOf("completed", "unavailable", "disabled", "permission_missing", "missed", "cancelled")
        val NETWORK_TRANSPORTS = setOf("unknown", "none", "wifi", "cellular", "other")

        fun isFresh(fixAgeMs: Long): Boolean = fixAgeMs in 0..MAX_FRESH_FIX_AGE_MS
    }
}
