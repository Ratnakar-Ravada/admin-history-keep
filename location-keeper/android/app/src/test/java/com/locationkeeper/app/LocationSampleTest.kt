package com.locationkeeper.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class LocationSampleTest {
    @Test fun freshnessRejectsOldAndInvalidClockReadings() {
        assertTrue(LocationSample.isFresh(0))
        assertTrue(LocationSample.isFresh(45_000))
        assertFalse(LocationSample.isFresh(-1))
        assertFalse(LocationSample.isFresh(45_001))
    }

    @Test(expected = IllegalArgumentException::class)
    fun invalidLatitudeCannotBeSaved() { sample().copy(latitude = 91.0) }

    @Test(expected = IllegalArgumentException::class)
    fun nonFiniteCoordinatesCannotBeSaved() { sample().copy(longitude = Double.NaN) }

    @Test(expected = IllegalArgumentException::class)
    fun invalidAccuracyCannotBeSaved() { sample().copy(accuracyM = -1.0) }

    @Test fun missingBatteryReadingIsAllowed() { sample().copy(batteryPercent = null) }

    @Test fun locationOffIsAValidEventWithoutInventedCoordinates() {
        val event = event()
        assertEquals("location_off", event.reason)
        assertEquals("disabled", event.locationStatus)
        assertNull(event.capturedAt)
        assertNull(event.latitude)
        assertNull(event.longitude)
        assertNull(event.accuracyM)
        assertNull(event.fixAgeMs)
        assertEquals("none", event.permissionAccuracy)
        assertFalse(event.isMock)
    }

    @Test fun partialPositionsAreRejectedAtEveryBoundary() {
        expectRejected { sample().copy(capturedAt = null) }
        expectRejected { sample().copy(latitude = null) }
        expectRejected { sample().copy(longitude = null) }
        expectRejected { sample().copy(accuracyM = null) }
        expectRejected { sample().copy(fixAgeMs = null) }
        expectRejected { event().copy(latitude = 0.0) }
    }

    @Test fun absentPositionCannotClaimAFixOrPermissionAccuracy() {
        expectRejected { event().copy(locationStatus = "fresh") }
        expectRejected { event().copy(locationStatus = "last_known") }
        expectRejected { event().copy(permissionAccuracy = "precise") }
        expectRejected { event().copy(isMock = true) }
        expectRejected { sample().copy(locationStatus = "unavailable") }
    }

    @Test fun onlyPeriodicEventsHaveARequiredClockSlot() {
        val scheduled = "2026-11-17T08:30:00Z"
        val periodic = event().copy(reason = "periodic", scheduledFor = scheduled)
        assertEquals(scheduled, periodic.scheduledFor)
        expectRejected { sample().copy(reason = "periodic") }
        expectRejected { sample().copy(scheduledFor = scheduled) }
    }

    @Test fun lastKnownRetainsItsMeasuredTimeAndAgeSeparatelyFromTheEvent() {
        val previous = sample()
        val event = previous.copy(recordedAt = "2026-11-17T08:30:00Z",
            reason = "screen_wake", fixAgeMs = 1_800_000, locationStatus = "last_known")
        assertEquals(previous.capturedAt, event.capturedAt)
        assertEquals("2026-11-17T08:30:00Z", event.recordedAt)
        assertFalse(LocationSample.isFresh(requireNotNull(event.fixAgeMs)))
    }

    @Test fun unknownMetadataIsRejectedBeforePersistenceOrUpload() {
        expectRejected { sample().copy(reason = "made_up") }
        expectRejected { sample().copy(accuracyMode = "made_up") }
        expectRejected { sample().copy(locationStatus = "made_up") }
        expectRejected { sample().copy(networkTransport = "made_up") }
    }

    private fun expectRejected(block: () -> Unit) {
        try { block(); fail("Invalid event was accepted") }
        catch (_: IllegalArgumentException) { /* expected */ }
    }

    companion object {
        fun sample() = LocationSample(
            eventId = "f61a379e-2780-405f-b4ef-f6393a7e4224",
            deviceId = "42533311-a2c3-4b1a-9649-d788754fcf3c",
            capturedAt = "2026-11-17T08:00:00Z",
            latitude = 12.9716,
            longitude = 77.5946,
            accuracyM = 8.5,
            batteryPercent = 75,
            isMock = false,
            permissionAccuracy = "precise",
            fixAgeMs = 25,
        )

        fun event() = LocationSample(
            eventId = "feab369c-14e2-4d39-833f-952b493dfc6c",
            deviceId = "42533311-a2c3-4b1a-9649-d788754fcf3c",
            capturedAt = null, latitude = null, longitude = null, accuracyM = null,
            batteryPercent = 75, isMock = false, permissionAccuracy = "none", fixAgeMs = null,
            recordedAt = "2026-11-17T08:30:00Z", reason = "location_off",
            accuracyMode = "battery_saving", locationStatus = "disabled", networkTransport = "none",
        )
    }
}
