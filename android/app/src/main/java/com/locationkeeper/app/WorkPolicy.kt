package com.locationkeeper.app

import java.time.Instant
import java.util.UUID

data class CaptureRequest(
    val eventId: String, val deviceId: String, val triggeredAt: String, val reason: String,
    val scheduledFor: String?, val accuracyMode: String, val networkTransport: String,
    val locationEnabled: Boolean?, val attempts: Int = 0,
)
data class CaptureClaim(val request: CaptureRequest, val token: String, val executedAtMs: Long)
data class MeasuredFix(
    val measuredAt: String, val latitude: Double, val longitude: Double, val accuracyM: Double,
    val ageMs: Long, val isMock: Boolean,
)

object WorkPolicy {
    fun observedAirplaneState(reason: String): Boolean? = when (reason) {
        "airplane_mode_on" -> true
        "airplane_mode_off" -> false
        else -> null
    }
    const val MINUTE = 60_000L
    const val CAPTURE_WINDOW_MS = 15 * MINUTE
    const val CAPTURE_LEASE_MS = 2 * MINUTE
    const val UPLOAD_LEASE_MS = 5 * MINUTE
    const val MAX_CAPTURE_ATTEMPTS = 5
    const val MAX_RECONCILE_SLOTS = 96

    fun urgentReason(reason: String) = reason in setOf("manual","tracking_start","app_launch",
        "location_on","location_off","internet_on","network_changed","airplane_mode_on","airplane_mode_off","screen_wake")

    fun backoff(attempts: Int): Long = MINUTE * (1L shl (attempts - 1).coerceIn(0, 4)).coerceAtMost(15)
    fun due(at: Long, now: Long, horizon: Long = CAPTURE_WINDOW_MS): Boolean =
        at <= now || at > now + horizon
    fun expired(request: CaptureRequest, now: Long): Boolean =
        now - Instant.parse(request.triggeredAt).toEpochMilli() > CAPTURE_WINDOW_MS
    fun periodicId(deviceId: String, slot: Long): String =
        UUID.nameUUIDFromBytes("$deviceId:work-periodic:$slot".toByteArray(Charsets.UTF_8)).toString()

    fun emptySample(request: CaptureRequest, executedAt: Long?, outcome: String, battery: Int? = null) =
        LocationSample(request.eventId, request.deviceId, null, null, null, null, battery, false, "none", null,
            recordedAt = request.triggeredAt, reason = request.reason, scheduledFor = request.scheduledFor,
            accuracyMode = request.accuracyMode,
            locationStatus = if (outcome == "disabled") "disabled" else "unavailable",
            networkTransport = request.networkTransport,
            executedAt = executedAt?.let { Instant.ofEpochMilli(it).toString() }, executionStatus = outcome,
            airplaneModeEnabled = observedAirplaneState(request.reason))
}
