package com.locationkeeper.app

import android.app.Application
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.work.*
import androidx.work.testing.TestListenableWorkerBuilder
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.IOException
import java.time.Instant
import java.util.UUID

@RunWith(RobolectricTestRunner::class)
@Config(application=Application::class,sdk=[28],manifest=Config.NONE)
class WorkRecoveryTest {
    private lateinit var context: Context
    private lateinit var store: LocationStore
    private lateinit var runtime: FakeRuntime
    private val database="work-recovery-test.db"
    private val prefs="work-recovery-settings"
    private val device="42533311-a2c3-4b1a-9649-d788754fcf3c"
    private val base=Instant.parse("2026-11-17T08:00:00Z").toEpochMilli()
    private fun iso(at: Long)=Instant.ofEpochMilli(at).toString()

    @Before fun setup() {
        context=ApplicationProvider.getApplicationContext()
        context.deleteDatabase(database); context.deleteSharedPreferences(prefs)
        store=LocationStore(context,database)
        runtime=FakeRuntime(store,TrackingSettings(context,prefs)).apply { settings.enabled=true }
    }
    @After fun cleanup() { store.close(); context.deleteDatabase(database); context.deleteSharedPreferences(prefs) }
    private fun request(reason: String="manual",at: Long=base)=CaptureRequest(
        UUID.randomUUID().toString(),device,iso(at),reason,null,"battery_saving","wifi",true)
    private fun reading(at: Long=base)=WorkPolicy.emptySample(request(at=at),at,"unavailable")
    private fun capture(id: String): ListenableWorker.Result = runBlocking {
        TestListenableWorkerBuilder<CaptureWorker>(context)
            .setInputData(workDataOf("event_id" to id)).setWorkerFactory(factory()).build().doWork()
    }
    private fun upload(): ListenableWorker.Result {
        runtime.connected=true
        store.readyUploads(runtime.now()).map { it.eventId }.forEach { capture(it) }
        return ListenableWorker.Result.success()
    }
    private fun factory()=object : WorkerFactory() {
        override fun createWorker(appContext: Context,workerClassName: String,p: WorkerParameters): ListenableWorker =
            if (workerClassName==CaptureWorker::class.java.name) CaptureWorker(appContext,p,runtime)
            else UploadWorker(appContext,p,runtime)
    }

    @Test fun oneRecordCapturesAndUploadsInTheSameExecutionAndRetryDoesNotRecapture() {
        val req=request(); store.enqueueCapture(req)
        runtime.connected=true
        runtime.fix=MeasuredFix(iso(base+1_000),12.9,77.5,8.0,10,false)
        runtime.failedIds.add(req.eventId)
        capture(req.eventId)
        assertEquals(1,runtime.locationCalls)
        val original=store.pending().single()
        assertEquals(req.eventId,original.eventId)
        runtime.failedIds.clear()
        runtime.clock=store.uploadDueAt(req.eventId,base)
        runtime.fix=MeasuredFix(iso(runtime.clock),1.0,2.0,100.0,0,false)
        capture(req.eventId)
        assertEquals(1,runtime.locationCalls)
        assertEquals(listOf(req.eventId,req.eventId),runtime.uploadCalls)
        assertEquals(1L,store.syncSnapshot().uploaded)
        assertTrue(store.pending().isEmpty())
    }

    @Test fun aConfirmedReceiptStaysConfirmedIfSchedulingTheNextRecordFails() {
        val req=request(); store.enqueueCapture(req)
        runtime.connected=true; runtime.wakeNextFails=true
        runtime.fix=MeasuredFix(iso(base+1_000),12.9,77.5,8.0,10,false)
        assertEquals(ListenableWorker.Result.success(),capture(req.eventId))
        assertEquals(1L,store.syncSnapshot().uploaded)
        assertTrue(store.pending().isEmpty())
        store.deferUpload(req.eventId,base+2_000)
        store.readableDatabase.rawQuery("SELECT COUNT(*) FROM upload_attempts",null).use {
            it.moveToFirst(); assertEquals(0,it.getInt(0))
        }
    }

    @Test fun timingSnapshotsReferToTheSameRecordAndArePrunedAfterConfirmation() {
        val first=reading(); val second=reading(base+1_000)
        store.save(first); store.save(second)
        store.captureTiming(first.eventId,1_000,20)
        store.captureTiming(second.eventId,9_000,200)
        store.recordUploadTiming(first.eventId,150,80)
        assertEquals(1_000L,store.syncSnapshot().queueWaitMs)
        assertEquals(20L,store.syncSnapshot().captureMs)
        assertEquals(150L,store.syncSnapshot().httpMs)
        store.markUploaded(first.eventId,iso(base+2_000))
        assertEquals(0L,store.jobValue("capture_wait:"+first.eventId))
        assertEquals(9_000L,store.jobValue("capture_wait:"+second.eventId))
    }

    @Test fun differentRecordsNeverWaitForAnotherRecordsLease() {
        val first=request(); val second=request("screen_wake")
        store.enqueueCapture(first); store.enqueueCapture(second)
        assertNotNull(store.claimCapture(first.eventId,base))
        assertNotNull(store.claimCapture(second.eventId,base))
        assertNotNull(store.acquireUploadLease(base,first.eventId))
        assertNotNull(store.acquireUploadLease(base,second.eventId))
        assertNull(store.acquireUploadLease(base,first.eventId))
    }

    @Test fun networkRestorationResetsOldDeadlinesOnceWithoutDroppingRecords() {
        val event=reading(); store.save(event)
        repeat(6) { store.deferUpload(event.eventId,base) }
        assertEquals(base+15*WorkPolicy.MINUTE,store.uploadDueAt(event.eventId,base))
        assertTrue(store.recoverUploads(base+1_000))
        assertEquals(base+1_000,store.uploadDueAt(event.eventId,base+1_000))
        store.deferUpload(event.eventId,base+2_000)
        assertFalse(store.recoverUploads(base+3_000))
        assertEquals(1,store.counts().second)
    }

    @Test fun automaticExpeditingIsBoundedAndNeverCombinesOrDropsEvents() {
        assertTrue(store.reserveExpedite("internet_on",base))
        assertFalse(store.reserveExpedite("location_on",base+1_000))
        assertFalse(store.reserveExpedite("periodic",base+60_000))
        assertTrue(store.reserveExpedite("screen_wake",base+60_000))
        assertFalse(store.reserveExpedite("screen_wake",base+120_000))
        assertTrue(store.reserveExpedite("manual",base+120_000))
        val first=request("screen_wake"); val second=request("screen_wake",base+1)
        assertTrue(store.enqueueCapture(first)); assertTrue(store.enqueueCapture(second))
        assertEquals(2,store.captureCount())
    }

    @Test fun requestSurvivesReopenAndAbandonedLeaseExpiresWithoutStaleCompletion() {
        val req=request(); assertTrue(store.enqueueCapture(req))
        val abandoned=store.claimCapture(req.eventId,base)!!
        store.close(); store=LocationStore(context,database)
        assertNull(store.claimCapture(req.eventId,base+60_000))
        val replacement=store.claimCapture(req.eventId,base+WorkPolicy.CAPTURE_LEASE_MS+1)!!
        assertFalse(store.completeCapture(abandoned,WorkPolicy.emptySample(req,base,"unavailable")))
        assertTrue(store.completeCapture(replacement,WorkPolicy.emptySample(req,replacement.executedAtMs,"unavailable")))
        assertEquals(1,store.counts().second)
        assertNull(store.captureRequest(req.eventId))
        store.releaseLease("capture:${req.eventId}",abandoned.token)
        assertEquals(replacement.executedAtMs+WorkPolicy.CAPTURE_LEASE_MS,store.leaseUntil("capture:${req.eventId}"))
    }

    @Test fun workerPreservesTriggerExecutionAndMeasurementTimesAcrossRetry() {
        val req=request(); store.enqueueCapture(req)
        runtime.clock=base+2_000
        assertEquals(ListenableWorker.Result.success(),capture(req.eventId))
        val next=runtime.captureWakes.single().second
        assertEquals(base+62_000,next)
        assertEquals(0,store.counts().first)
        runtime.clock=next
        runtime.fix=MeasuredFix(iso(next+1_000),12.9,77.5,8.0,10,false)
        capture(req.eventId)
        val saved=store.pending().single()
        assertEquals(iso(base),saved.recordedAt)
        assertEquals(iso(next),saved.executedAt)
        assertEquals(iso(next+1_000),saved.capturedAt)
        assertEquals("completed",saved.executionStatus)
        assertEquals(req.eventId,saved.eventId)
        capture(req.eventId)
        assertEquals(1,store.counts().first)
    }

    @Test fun acquisitionExhaustionRetainsEventWithoutInventingPosition() {
        val req=request(); store.enqueueCapture(req)
        repeat(WorkPolicy.MAX_CAPTURE_ATTEMPTS) {
            capture(req.eventId)
            runtime.captureWakes.lastOrNull()?.let { runtime.clock=it.second }
        }
        assertEquals(WorkPolicy.MAX_CAPTURE_ATTEMPTS,runtime.locationCalls)
        val saved=store.pending().single()
        assertEquals("unavailable",saved.executionStatus)
        assertEquals(req.triggeredAt,saved.recordedAt)
        assertNull(saved.capturedAt); assertNull(saved.latitude)
        assertEquals(0,store.captureCount())
    }

    @Test fun lateEventIsAuditedInsteadOfTakingAFalseHistoricalPosition() {
        val req=request(); store.enqueueCapture(req)
        runtime.clock=base+WorkPolicy.CAPTURE_WINDOW_MS+1
        runtime.fix=MeasuredFix(iso(runtime.clock),12.0,77.0,5.0,0,false)
        capture(req.eventId)
        val sample=store.pending().single()
        assertEquals("missed",sample.executionStatus); assertEquals(req.triggeredAt,sample.recordedAt)
        assertNull(sample.capturedAt); assertEquals(0,runtime.locationCalls)
    }

    @Test fun cancellationLeavesCaptureRecoverableAndReleasesLease() {
        val req=request(); store.enqueueCapture(req); runtime.cancelLocation=true
        try { capture(req.eventId); fail("Cancellation swallowed") } catch (_: CancellationException) {}
        assertEquals(1,store.captureCount())
        assertEquals(0L,store.leaseUntil("capture:${req.eventId}"))
        assertTrue(store.pending().isEmpty())
        runtime.cancelLocation=false
        runtime.clock=store.pendingCaptures().single().second
        runtime.allowed=false
        capture(req.eventId)
        assertEquals("permission_missing",store.pending().single().executionStatus)
    }

    @Test fun missedPeriodicSlotsAreAuditedOnceAndNewestSlotGetsASeparateCapture() {
        val interval=30*WorkPolicy.MINUTE
        store.resetSchedule(base,interval)
        val now=base+interval*4+1_000
        assertFalse(store.reconcileSchedule(now,interval,device,"battery_saving"))
        assertEquals(3,store.pending().size); assertEquals(1,store.captureCount())
        assertTrue(store.pending().all { it.executionStatus=="missed" && it.capturedAt==null && it.executedAt==iso(now) })
        assertEquals(iso(base+interval),store.pending().first().scheduledFor)
        val req=store.pendingCaptures().single().first
        assertEquals(iso(base+interval*4),req.scheduledFor)
        assertEquals(iso(now),req.triggeredAt)
        store.reconcileSchedule(now,interval,device,"battery_saving")
        assertEquals(3,store.pending().size); assertEquals(1,store.captureCount())
    }

    @Test fun longRebootGapIsReconciledInBoundedResumableTransactions() {
        val interval=30*WorkPolicy.MINUTE
        store.resetSchedule(base,interval)
        val now=base+interval*200
        assertTrue(store.reconcileSchedule(now,interval,device,"battery_saving"))
        assertEquals(96,store.counts().first)
        store.close(); store=LocationStore(context,database)
        assertTrue(store.reconcileSchedule(now,interval,device,"battery_saving"))
        assertFalse(store.reconcileSchedule(now,interval,device,"battery_saving"))
        assertEquals(199,store.counts().first); assertEquals(1,store.captureCount())
    }

    @Test fun stopAuditsOnlyTrackingCapturesAndNeverDeletesUploadHistory() {
        val manual=request(); val event=request("internet_on")
        store.enqueueCapture(manual); store.enqueueCapture(event); store.save(reading())
        store.cancelTrackingCaptures()
        assertEquals(listOf(manual.eventId),store.pendingCaptures().map { it.first.eventId })
        assertEquals(2,store.counts().second)
        assertEquals("cancelled",store.recent().first { it.eventId==event.eventId }.executionStatus)
    }

    @Test fun failedUploadDoesNotBlockLaterRecordsAndReceiptLossCanRetrySafely() {
        val bad=reading(); val good=reading(base+1_000)
        store.save(bad); store.save(good); runtime.failedIds.add(bad.eventId)
        upload()
        assertEquals(listOf(bad.eventId),store.pending().map { it.eventId })
        assertEquals(1,store.counts().first)
        assertEquals(1L,store.syncSnapshot().uploaded)
        assertEquals(base+5_000,store.syncSnapshot().receivedMs)
        assertTrue(store.nextUploadAt(base)>base)
        runtime.failedIds.clear(); runtime.clock=store.nextUploadAt(base)
        upload()
        assertEquals(0,store.counts().second)
        assertEquals(listOf(bad.eventId,good.eventId,bad.eventId),runtime.uploadCalls)
    }

    @Test fun onlyAValidReceiptRemovesDataAndDuplicateCallbacksDoNotDoubleCount() {
        val sample=reading(); store.save(sample)
        store.deferUpload(sample.eventId,base)
        store.close(); store=LocationStore(context,database)
        assertTrue(store.readyUploads(base).isEmpty())
        assertEquals(sample.eventId,store.readyUploads(base+60_000).single().eventId)
        try { store.markUploaded(sample.eventId,"not-a-receipt"); fail("Invalid receipt accepted") }
        catch (_: java.time.format.DateTimeParseException) {}
        assertEquals(sample.eventId,store.pending().single().eventId)
        store.markUploaded(sample.eventId,iso(base+1_000))
        store.markUploaded(sample.eventId,iso(base+1_000))
        assertTrue(store.recent().isEmpty())
        assertEquals(1L,store.syncSnapshot().uploaded)
        assertEquals(base+1_000,store.syncSnapshot().receivedMs)
        store.close(); store=LocationStore(context,database)
        assertTrue(store.pending().isEmpty())
        assertEquals(1L,store.syncSnapshot().uploaded)
    }

    @Test fun confirmedCaptureAndRetryMetadataAreRemovedButPendingWorkSurvivesUpgrade() {
        val req=request()
        store.enqueueCapture(req)
        val claim=store.claimCapture(req.eventId,base)!!
        store.completeCapture(claim,WorkPolicy.emptySample(req,base,"unavailable"))
        val pending=reading(base+1_000); store.save(pending)
        val waiting=request(); store.enqueueCapture(waiting)
        store.deferUpload(req.eventId,base)
        store.writableDatabase.execSQL("UPDATE locations SET received_at=? WHERE event_id=?",arrayOf(iso(base+5_000),req.eventId))
        recreateLegacyLocationTable(store.writableDatabase)
        store.writableDatabase.version=3
        store.close(); store=LocationStore(context,database)
        assertEquals(listOf(pending.eventId),store.pending().map { it.eventId })
        assertEquals(waiting.eventId,store.pendingCaptures().single().first.eventId)
        assertEquals(1L,store.syncSnapshot().uploaded)
        for(table in listOf("capture_requests","upload_attempts")) store.readableDatabase.rawQuery(
            "SELECT COUNT(*) FROM $table WHERE event_id=?",arrayOf(req.eventId)).use {
            it.moveToFirst(); assertEquals(0,it.getInt(0))
        }
        store.close(); store=LocationStore(context,database)
        assertEquals(1L,store.syncSnapshot().uploaded)
        assertEquals(1,store.captureCount())
    }

    @Test fun versionOneUpgradePreservesMeasuredHistoryAndOriginalServerReceipt() {
        store.close(); context.deleteDatabase(database)
        val id=UUID.randomUUID().toString()
        val receipt=iso(base+5_000)
        context.openOrCreateDatabase(database,Context.MODE_PRIVATE,null).use { db ->
            db.execSQL("""CREATE TABLE locations (
                event_id TEXT PRIMARY KEY NOT NULL,device_id TEXT NOT NULL,captured_at TEXT NOT NULL,
                captured_at_ms INTEGER NOT NULL,latitude REAL NOT NULL,longitude REAL NOT NULL,
                accuracy_m REAL NOT NULL,battery_percent INTEGER,is_mock INTEGER NOT NULL,
                permission_accuracy TEXT NOT NULL,fix_age_ms INTEGER NOT NULL,received_at TEXT)""")
            db.execSQL("CREATE INDEX locations_pending ON locations(received_at,captured_at_ms)")
            db.execSQL("INSERT INTO locations VALUES (?,?,?,?,12.9,77.5,8.0,70,0,'precise',10,?)",
                arrayOf<Any>(id,device,iso(base),base,receipt))
            db.version=1
        }
        store=LocationStore(context,database)
        assertEquals(5,store.readableDatabase.version)
        assertTrue(store.recent().isEmpty())
        assertEquals(1L,store.syncSnapshot().uploaded)
        assertEquals(base+5_000,store.syncSnapshot().receivedMs)
        assertTrue(store.pending().isEmpty())
        assertTrue(store.enqueueCapture(request()))
    }

    @Test fun versionTwoUpgradePreservesHistoryReceiptAndPendingQueue() {
        store.close()
        context.deleteDatabase(database)
        val id=UUID.randomUUID().toString()
        context.openOrCreateDatabase(database,Context.MODE_PRIVATE,null).use { db ->
            db.execSQL("""CREATE TABLE locations (
                event_id TEXT PRIMARY KEY NOT NULL,device_id TEXT NOT NULL,captured_at TEXT,captured_at_ms INTEGER,
                latitude REAL,longitude REAL,accuracy_m REAL,battery_percent INTEGER,is_mock INTEGER NOT NULL,
                permission_accuracy TEXT NOT NULL,fix_age_ms INTEGER,received_at TEXT,recorded_at TEXT NOT NULL,
                recorded_at_ms INTEGER NOT NULL,reason TEXT NOT NULL,scheduled_for TEXT,accuracy_mode TEXT NOT NULL,
                location_status TEXT NOT NULL,network_transport TEXT NOT NULL)""")
            db.execSQL("CREATE INDEX locations_pending ON locations(received_at,recorded_at_ms)")
            db.execSQL("""INSERT INTO locations(event_id,device_id,is_mock,permission_accuracy,received_at,
                recorded_at,recorded_at_ms,reason,accuracy_mode,location_status,network_transport)
                VALUES (?,?,0,'none',NULL,?,?,'internet_off','battery_saving','unavailable','none')""",
                arrayOf<Any>(id,device,iso(base),base))
            db.version=2
        }
        store=LocationStore(context,database)
        assertEquals(5,store.readableDatabase.version)
        assertEquals(id,store.pending().single().eventId)
        assertNull(store.pending().single().executedAt)
        assertNull(store.pending().single().executionStatus)
        assertTrue(store.enqueueCapture(request()))
        assertEquals(1,store.captureCount())
    }

    private inner class FakeRuntime(override val store: LocationStore,override val settings: TrackingSettings): JobRuntime {
        var clock=base; var allowed=true; var enabled=true; var cancelLocation=false; var connected=false; var wakeNextFails=false
        var locationCalls=0
        var fix: MeasuredFix?=null
        val captureWakes=mutableListOf<Pair<CaptureRequest,Long>>()
        val uploadWakes=mutableListOf<Long>()
        val failedIds=mutableSetOf<String>()
        val uploadCalls=mutableListOf<String>()
        override fun now()=clock
        override fun networkAvailable()=connected
        override fun canLocate()=allowed
        override fun locationEnabled()=enabled
        override fun precise()=true
        override fun battery()=70
        override suspend fun locate(mode: String): MeasuredFix? {
            locationCalls++
            if (cancelLocation) throw CancellationException("Test interruption")
            return fix
        }
        override fun upload(sample: LocationSample): String {
            uploadCalls.add(sample.eventId)
            if (sample.eventId in failedIds) throw IOException("Test network failure")
            return iso(base+5_000)
        }
        override fun wakeCapture(request: CaptureRequest,at: Long) { captureWakes.add(request to at) }
        override fun wakeUpload(at: Long) { uploadWakes.add(at) }
        override fun wakeUploadRecord(eventId: String,at: Long) { uploadWakes.add(at) }
        override fun wakeNextUpload() { if(wakeNextFails) throw IOException("Test scheduler unavailable") }
    }
}
