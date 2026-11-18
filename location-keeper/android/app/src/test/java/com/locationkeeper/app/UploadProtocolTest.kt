package com.locationkeeper.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UploadProtocolTest {
    private val sample = LocationSampleTest.sample()

    @Test fun payloadPreservesOriginalFixTimeAndHasNoCredentials() {
        val json = UploadProtocol.payload(sample.copy(batteryPercent = null))
        assertEquals(sample.eventId, json.getString("event_id"))
        assertEquals(sample.capturedAt, json.getString("captured_at"))
        assertEquals(requireNotNull(sample.latitude), json.getDouble("latitude"), 0.0)
        assertEquals(requireNotNull(sample.fixAgeMs), json.getLong("fix_age_ms"))
        assertTrue(json.isNull("battery_percent"))
        assertFalse(json.has("token"))
        assertFalse(json.has("received_at"))
    }

    @Test fun eventWithoutLocationKeepsExplicitNullFieldsAndItsReason() {
        val event = LocationSampleTest.event()
        val json = UploadProtocol.payload(event)
        for (key in listOf("captured_at", "latitude", "longitude", "accuracy_m", "fix_age_ms", "scheduled_for")) {
            assertTrue("Missing field: $key", json.has(key))
            assertTrue("Position must remain null: $key", json.isNull(key))
        }
        assertEquals(event.recordedAt, json.getString("recorded_at"))
        assertEquals("location_off", json.getString("reason"))
        assertEquals("disabled", json.getString("location_status"))
        assertEquals("battery_saving", json.getString("accuracy_mode"))
        assertEquals("none", json.getString("network_transport"))
        assertEquals("none", json.getString("permission_accuracy"))
        assertFalse(json.getBoolean("is_mock"))
    }

    @Test fun periodicPayloadSeparatesTheSlotFromEventAndPositionTimes() {
        val event = sample.copy(reason = "periodic", accuracyMode = "high_precision",
            scheduledFor = "2026-11-17T08:30:00Z", recordedAt = "2026-11-17T08:30:03Z",
            locationStatus = "last_known", fixAgeMs = 1_803_000, networkTransport = "cellular")
        val json = UploadProtocol.payload(event)
        assertEquals("2026-11-17T08:30:00Z", json.getString("scheduled_for"))
        assertEquals("2026-11-17T08:30:03Z", json.getString("recorded_at"))
        assertEquals(sample.capturedAt, json.getString("captured_at"))
        assertEquals("last_known", json.getString("location_status"))
        assertFalse(json.has("received_at"))
    }

    @Test fun matchingServerConfirmationAcknowledgesReading() {
        val body = """{"saved":true,"event_id":"${sample.eventId}","received_at":"2026-11-17T08:00:02Z"}"""
        assertEquals("2026-11-17T08:00:02Z", UploadProtocol.acknowledgedAt(body, sample.eventId))
    }

    @Test(expected = IllegalArgumentException::class)
    fun confirmationOfDifferentReadingCannotEmptyQueue() {
        UploadProtocol.acknowledgedAt(
            """{"saved":true,"event_id":"another-reading","received_at":"2026-11-17T08:00:02Z"}""",
            sample.eventId,
        )
    }

    @Test(expected = IllegalArgumentException::class)
    fun rejectedSaveCannotEmptyQueue() {
        UploadProtocol.acknowledgedAt(
            """{"saved":false,"event_id":"${sample.eventId}","received_at":"2026-11-17T08:00:02Z"}""",
            sample.eventId,
        )
    }

    @Test(expected = java.time.format.DateTimeParseException::class)
    fun invalidServerTimestampCannotEmptyQueue() {
        UploadProtocol.acknowledgedAt(
            """{"saved":true,"event_id":"${sample.eventId}","received_at":"invalid"}""",
            sample.eventId,
        )
    }
}

