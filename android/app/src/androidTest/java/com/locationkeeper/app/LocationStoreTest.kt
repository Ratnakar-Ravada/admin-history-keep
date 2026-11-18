package com.locationkeeper.app

import android.content.Context
import android.database.sqlite.SQLiteConstraintException
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import java.time.Instant

/** Uses a separate database; real phone history and upload settings are untouched. */
@RunWith(AndroidJUnit4::class)
class LocationStoreTest {
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val databaseName = "location-store-test.db"

    @Before fun setUp() {
        context.deleteDatabase(databaseName)
    }

    @After fun tearDown() {
        context.deleteDatabase(databaseName)
    }

    @Test fun savedReadingSurvivesDatabaseReopenAndRemainsPending() {
        LocationStore(context, databaseName).use { it.save(sample()) }
        LocationStore(context, databaseName).use { store ->
            assertEquals(1, store.counts().first)
            assertEquals(1, store.counts().second)
            assertEquals(sample(), store.pending().single())
        }
    }

    @Test fun acknowledgmentDeletesOnlyThatConfirmedReading() {
        LocationStore(context, databaseName).use { store ->
            val first = sample()
            val second = first.copy(eventId = "feab369c-14e2-4d39-833f-952b493dfc6c")
            store.save(first)
            store.save(second)
            store.markUploaded(first.eventId, "2026-11-17T08:00:02Z")
            assertEquals(1, store.counts().first)
            assertEquals(1, store.counts().second)
            assertEquals(second.eventId, store.pending().single().eventId)
            assertEquals(1, store.recent().size)
            assertEquals(1L, store.syncSnapshot().uploaded)
        }
    }

    @Test fun orderingUsesEventTimeIncludingFractionalSecondsRatherThanOldFixTime() {
        LocationStore(context, databaseName).use { store ->
            val first = sample().copy(recordedAt = "2026-11-17T08:30:00Z")
            val second = first.copy(
                eventId = "feab369c-14e2-4d39-833f-952b493dfc6c",
                capturedAt = "2026-11-17T07:00:00Z",
                recordedAt = "2026-11-17T08:30:00.500Z",
                locationStatus = "last_known", fixAgeMs = 5_400_500,
            )
            store.save(first)
            store.save(second)
            assertEquals(second.eventId, store.recent().first().eventId)
            assertEquals(first.eventId, store.pending().first().eventId)
        }
    }

    @Test fun eventWithoutLocationSurvivesReopeningAndCanBeAcknowledged() {
        val event = event()
        LocationStore(context, databaseName).use { store ->
            assertNull(store.latestPosition())
            store.save(event)
            assertNull(store.latestPosition())
        }
        LocationStore(context, databaseName).use { store ->
            assertEquals(event, store.pending().single())
            store.markUploaded(event.eventId, "2026-11-17T08:30:01Z")
            assertTrue(store.pending().isEmpty())
            assertTrue(store.recent().isEmpty())
            assertEquals(1L, store.syncSnapshot().uploaded)
        }
    }

    @Test fun duplicateClockSlotNeverReplacesOriginalEventOrReceipt() {
        LocationStore(context, databaseName).use { store ->
            val first = sample().copy(reason = "periodic", scheduledFor = "2026-11-17T08:00:00Z")
            assertTrue(store.saveIfAbsent(first))
            val receipt = "2026-11-17T08:00:02Z"
            assertFalse(store.saveIfAbsent(first.copy(recordedAt = "2026-11-17T08:01:00Z", latitude = 10.0)))
            assertEquals(first,store.recent().single())
            store.markUploaded(first.eventId, receipt)
            assertEquals(0 to 0,store.counts())
            assertEquals(1L,store.syncSnapshot().uploaded)
        }
    }

    @Test(expected = SQLiteConstraintException::class)
    fun ordinarySaveStillRejectsADuplicateInsteadOfSilentlyReplacingIt() {
        LocationStore(context, databaseName).use { store ->
            store.save(sample())
            store.save(sample())
        }
    }

    @Test fun latestPositionIgnoresEventOnlyRowsAndPreservesTheNewestMeasuredFix() {
        LocationStore(context, databaseName).use { store ->
            val newestFix = sample()
            val olderFixAtLaterEvent = newestFix.copy(
                eventId = "feab369c-14e2-4d39-833f-952b493dfc6c",
                capturedAt = "2026-11-17T07:00:00Z", recordedAt = "2026-11-17T09:00:00Z",
                reason = "screen_wake", locationStatus = "last_known", fixAgeMs = 7_200_000,
            )
            store.save(newestFix)
            store.save(olderFixAtLaterEvent)
            store.save(event().copy(recordedAt = "2026-11-17T10:00:00Z"))
            assertEquals(newestFix, store.latestPosition())
        }
    }

    @Test fun versionOneMigrationPreservesPendingHistoryAndServerReceipts() {
        val first = sample()
        val uploaded = first.copy(eventId = "feab369c-14e2-4d39-833f-952b493dfc6c",
            receivedAt = "2026-11-17T08:00:02Z")
        createVersionOneDatabase(listOf(first, uploaded))

        LocationStore(context, databaseName).use { store ->
            assertEquals(4, store.readableDatabase.version)
            assertEquals(1 to 1, store.counts())
            assertEquals(first, store.pending().single())
            assertEquals(first, store.recent().first())
            assertEquals("legacy", store.recent().first().reason)
            assertEquals(first.capturedAt, store.recent().first().recordedAt)
            assertEquals(1L, store.syncSnapshot().uploaded)
            store.save(event())
        }
        LocationStore(context, databaseName).use { store ->
            assertEquals(2 to 2, store.counts())
            assertEquals(event(), store.recent().first())
            assertEquals(first, store.latestPosition())
        }
    }

    private fun createVersionOneDatabase(samples: List<LocationSample>) {
        context.openOrCreateDatabase(databaseName, Context.MODE_PRIVATE, null).use { db ->
            db.execSQL("""CREATE TABLE locations (
                event_id TEXT PRIMARY KEY NOT NULL, device_id TEXT NOT NULL,
                captured_at TEXT NOT NULL, captured_at_ms INTEGER NOT NULL,
                latitude REAL NOT NULL, longitude REAL NOT NULL, accuracy_m REAL NOT NULL,
                battery_percent INTEGER, is_mock INTEGER NOT NULL,
                permission_accuracy TEXT NOT NULL, fix_age_ms INTEGER NOT NULL, received_at TEXT
            )""")
            db.execSQL("CREATE INDEX locations_pending ON locations(received_at, captured_at_ms)")
            samples.forEach { sample ->
                val capturedAt = requireNotNull(sample.capturedAt)
                db.execSQL("INSERT INTO locations VALUES (?,?,?,?,?,?,?,?,?,?,?,?)", arrayOf<Any?>(
                    sample.eventId, sample.deviceId, capturedAt, Instant.parse(capturedAt).toEpochMilli(),
                    sample.latitude, sample.longitude, sample.accuracyM, sample.batteryPercent,
                    if (sample.isMock) 1 else 0, sample.permissionAccuracy, sample.fixAgeMs,
                    sample.receivedAt,
                ))
            }
            db.version = 1
        }
    }

    private fun sample() = LocationSample(
        "f61a379e-2780-405f-b4ef-f6393a7e4224", "42533311-a2c3-4b1a-9649-d788754fcf3c",
        "2026-11-17T08:00:00Z", 12.9716, 77.5946, 8.5, 75, false, "precise", 25,
    )

    private fun event() = LocationSample(
        "b59a96bb-7e66-4bc9-ab07-a54c5f15ce43", "42533311-a2c3-4b1a-9649-d788754fcf3c",
        null, null, null, null, 75, false, "none", null,
        recordedAt = "2026-11-17T08:30:00Z", reason = "internet_off",
        accuracyMode = "battery_saving", locationStatus = "unavailable", networkTransport = "none",
    )
}
