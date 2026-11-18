package com.locationkeeper.app

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.time.Instant

class CaptureWorker @JvmOverloads constructor(context: Context, parameters: WorkerParameters,
    private val runtime: JobRuntime = AndroidJobRuntime(context)) : CoroutineWorker(context,parameters) {
    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        val id = inputData.getString("event_id") ?: return@withContext Result.success()
        val store = runtime.store
        var claim: CaptureClaim? = null
        try {
            if (store.pendingSample(id) != null) {
                uploadRecord(id)
                return@withContext Result.success()
            }
            val pending = store.captureRequest(id) ?: return@withContext Result.success()
            claim = store.claimCapture(id,runtime.now())
            val current = claim
            if (current == null) {
                val until = store.leaseUntil("capture:$id")
                val next = store.pendingCaptures().firstOrNull { it.first.eventId == id }?.second ?: runtime.now()
                val leaseAt = if (!WorkPolicy.due(until,runtime.now(),WorkPolicy.CAPTURE_LEASE_MS)) until+1_000 else runtime.now()+60_000
                runtime.wakeCapture(pending,maxOf(leaseAt,if (WorkPolicy.due(next,runtime.now())) runtime.now() else next))
                return@withContext Result.success()
            }
            val request = current.request
            store.captureTiming(id,queueMs=(current.executedAtMs-Instant.parse(request.triggeredAt).toEpochMilli()).coerceAtLeast(0),captureMs=0)
            val outcome = when {
                request.reason != "manual" && !runtime.settings.enabled -> "cancelled"
                WorkPolicy.expired(request,runtime.now()) -> if (request.attempts == 1) "missed" else "unavailable"
                !runtime.canLocate() -> "permission_missing"
                request.locationEnabled == false || !runtime.locationEnabled() -> "disabled"
                else -> null
            }
            if (outcome != null) {
                store.completeCapture(current,WorkPolicy.emptySample(request,current.executedAtMs,outcome,runtime.battery()))
                runtime.settings.status = "Capture recorded: $outcome."
                uploadRecord(id)
                return@withContext Result.success()
            }
            val locationStarted = android.os.SystemClock.elapsedRealtime()
            val fix = try { runtime.locate(request.accuracyMode) }
            catch (cancel: CancellationException) { throw cancel }
            catch (_: Exception) { null }
            store.captureTiming(id,captureMs=android.os.SystemClock.elapsedRealtime()-locationStarted)
            if (request.reason != "manual" && !runtime.settings.enabled) {
                store.completeCapture(current,WorkPolicy.emptySample(request,current.executedAtMs,"cancelled"))
            } else if (fix == null && request.attempts < WorkPolicy.MAX_CAPTURE_ATTEMPTS &&
                !WorkPolicy.expired(request,runtime.now())) {
                val next = store.retryCapture(current,runtime.now(),"No fresh position; retry scheduled")
                runtime.settings.status = "No fresh position yet. Capture retry scheduled; trigger time is preserved."
                runtime.wakeCapture(request,next)
                return@withContext Result.success()
            } else {
                val sample = if (fix == null) WorkPolicy.emptySample(request,current.executedAtMs,"unavailable",runtime.battery())
                else LocationSample(request.eventId,request.deviceId,fix.measuredAt,fix.latitude,fix.longitude,
                    fix.accuracyM,runtime.battery(),fix.isMock,if (runtime.precise()) "precise" else "approximate",fix.ageMs,
                    recordedAt=request.triggeredAt,reason=request.reason,scheduledFor=request.scheduledFor,
                    accuracyMode=request.accuracyMode,locationStatus="fresh",networkTransport=request.networkTransport,
                    executedAt=Instant.ofEpochMilli(current.executedAtMs).toString(),executionStatus="completed",
                    airplaneModeEnabled=WorkPolicy.observedAirplaneState(request.reason))
                store.completeCapture(current,sample)
                runtime.settings.status = if (fix == null) "Capture attempts finished without a fresh fix; event retained."
                    else "Location saved. Pending upload remains recoverable."
            }
            uploadRecord(id)
            Result.success()
        } catch (cancel: CancellationException) {
            claim?.let { runCatching { store.retryCapture(it,runtime.now(),"Execution interrupted; recoverable") } }
            throw cancel
        } catch (_: Exception) {
            claim?.let { runCatching { store.retryCapture(it,runtime.now(),"Execution interrupted; retry scheduled") } }
            Result.retry()
        } finally {
            claim?.let { runCatching { store.releaseLease("capture:$id",it.token) } }
        }
    }
    /** Runs immediately after local capture, or resumes just this immutable reading on retry. */
    private fun uploadRecord(id: String) {
        val store=runtime.store
        if(!runtime.settings.autoUpload && store.jobValue("manual_upload")==0L) return
        val due=store.uploadDueAt(id,runtime.now())
        if(due>runtime.now()) { runtime.wakeUploadRecord(id,due); return }
        if(!runtime.networkAvailable()) {
            store.deferUpload(id,runtime.now())
            runtime.settings.status="Reading saved. Upload will retry when connected."
            runtime.wakeUploadRecord(id,store.uploadDueAt(id,runtime.now()))
            return
        }
        val lease=store.acquireUploadLease(runtime.now(),id)
        if(lease==null) {
            val until=store.leaseUntil("upload:$id")
            runtime.wakeUploadRecord(id,if(WorkPolicy.due(until,runtime.now(),WorkPolicy.UPLOAD_LEASE_MS))
                runtime.now()+10_000 else until+1_000)
            return
        }
        try {
            val sample=store.pendingSample(id) ?: return
            val receipt=runtime.upload(sample)
            store.markUploaded(id,receipt)
            store.uploadSucceeded()
            runtime.settings.status="Reading confirmed by the database."
            runCatching { runtime.wakeNextUpload() }
        } catch(cancel: CancellationException) { throw cancel }
        catch(error: Exception) {
            store.deferUpload(id,runtime.now())
            runtime.settings.status="Upload pending: ${UploadClient.friendlyError(error)} Retry scheduled."
            runtime.wakeUploadRecord(id,store.uploadDueAt(id,runtime.now()))
        } finally {
            store.releaseLease("upload:$id",lease)
        }
    }

}

/** Compatibility for previously persisted upload jobs: fan out, never combine record payloads. */
class UploadWorker @JvmOverloads constructor(context: Context, parameters: WorkerParameters,
    private val runtime: JobRuntime = AndroidJobRuntime(context)) : CoroutineWorker(context,parameters) {
    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        try { runtime.wakeUpload(runtime.now()); Result.success() }
        catch (cancel: CancellationException) { throw cancel }
        catch (_: Exception) { Result.retry() }
    }
}

class MaintenanceWorker(context: Context, parameters: WorkerParameters) : CoroutineWorker(context,parameters) {
    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        try {
            val more = WorkScheduling.reconcile(applicationContext)
            if (more) WorkScheduling.maintenanceWake(applicationContext,System.currentTimeMillis()+60_000)
            Result.success()
        } catch (cancel: CancellationException) { throw cancel }
        catch (_: Exception) { Result.retry() }
    }
}
