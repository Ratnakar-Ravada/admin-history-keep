package com.locationkeeper.app

import android.app.AlarmManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.work.*
import java.time.Instant
import java.util.UUID
import java.util.concurrent.TimeUnit

/** SQLite owns events and retry deadlines; WorkManager owns eligible execution. All methods run off main. */
object WorkScheduling {
    const val TRACKING_TAG = "locationkeeper.tracking"
    private const val MAINTENANCE = "locationkeeper.maintenance"
    private fun app(context: Context) = context.applicationContext as LocationKeeperApplication

    fun ensureMaintenance(context: Context, update: Boolean = false) {
        val settings = TrackingSettings(context)
        val interval = if (settings.enabled) settings.intervalMinutes else 30
        val request = PeriodicWorkRequestBuilder<MaintenanceWorker>(interval.toLong(),TimeUnit.MINUTES)
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL,1,TimeUnit.MINUTES).build()
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(MAINTENANCE,
            if (update) ExistingPeriodicWorkPolicy.UPDATE else ExistingPeriodicWorkPolicy.KEEP,request)
            .result.get(15,TimeUnit.SECONDS)
    }

    fun enqueueEvent(context: Context, reason: String, manual: Boolean = false,
        triggeredAt: Long = System.currentTimeMillis(), observedTransport: String? = null,
        observedLocationEnabled: Boolean? = null): String? {
        val app = app(context)
        val settings = TrackingSettings(context)
        if (!manual && !settings.enabled) return null
        val now = System.currentTimeMillis()
        val request = CaptureRequest(UUID.randomUUID().toString(),app.connectionSettings.deviceId,
            Instant.ofEpochMilli(triggeredAt).toString(),reason,null,
            if (manual) "high_precision" else settings.accuracyMode,observedTransport ?: LocationCapture.transport(context),
            observedLocationEnabled ?: LocationCapture.locationEnabled(context))
        if (!app.store.enqueueCapture(request)) return null
        ensureMaintenance(context)
        capture(context,request,now)
        return request.eventId
    }

    fun capture(context: Context, request: CaptureRequest, at: Long) {
        enqueueRecord(context,request.eventId,at,request.reason,request.reason!="manual")
    }

    fun uploadOne(context: Context) {
        val app=app(context)
        if(!TrackingSettings(context).autoUpload && app.store.jobValue("manual_upload")==0L) return
        app.store.nextReadyUpload(System.currentTimeMillis())?.let { uploadRecord(context,it.eventId) }
    }

    fun uploadRecord(context: Context, eventId: String, at: Long = System.currentTimeMillis(),
        important: Boolean = false) {
        enqueueRecord(context,eventId,at,if(important) "internet_on" else "upload_retry",false)
    }

    private fun enqueueRecord(context: Context, eventId: String, at: Long, reason: String, tracking: Boolean) {
        val now=System.currentTimeMillis()
        val deadline=if(at>now && at<=now+WorkPolicy.CAPTURE_WINDOW_MS) at else now/10_000*10_000
        val delayed=deadline>now
        val request=recordRequest(eventId,(deadline-now).coerceAtLeast(0),
            !delayed && android.os.Build.VERSION.SDK_INT>=31 && app(context).store.reserveExpedite(reason,now),
            delayed && !tracking)
        if(tracking) request.addTag(TRACKING_TAG)
        WorkManager.getInstance(context).enqueueUniqueWork("record:$eventId:$deadline",
            ExistingWorkPolicy.KEEP,request.build()).result.get(15,TimeUnit.SECONDS)
    }

    /** Expedited work is ready-to-run only. Delayed retries and pre-Android-12 jobs stay ordinary. */
    internal fun recordRequest(eventId: String, delay: Long, expedited: Boolean, networkRequired: Boolean):
        OneTimeWorkRequest.Builder {
        val builder=OneTimeWorkRequestBuilder<CaptureWorker>()
            .setInputData(workDataOf("event_id" to eventId))
            .setInitialDelay(delay,TimeUnit.MILLISECONDS)
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL,10,TimeUnit.SECONDS)
            .addTag("locationkeeper.record")
        if(networkRequired) builder.setConstraints(Constraints.Builder()
            .setRequiredNetworkType(NetworkType.CONNECTED).build())
        if(expedited && delay==0L) builder.setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
        return builder
    }

    fun upload(context: Context, at: Long = System.currentTimeMillis(), explicit: Boolean = false,
        networkRecovered: Boolean = false) {
        val app=app(context)
        val store=app.store
        val settings=TrackingSettings(context)
        val now=System.currentTimeMillis()
        if(explicit) store.setJobValue("manual_upload",1)
        if(!settings.autoUpload && store.jobValue("manual_upload")==0L) return
        val promptRetry=if(explicit||networkRecovered) store.recoverUploads(now,explicit) else false
        val pending=store.pending(50)
        if(pending.isEmpty()) { store.uploadSucceeded(); return }
        ensureMaintenance(context)
        pending.forEachIndexed { index,sample ->
            uploadRecord(context,sample.eventId,maxOf(at,store.uploadDueAt(sample.eventId,now)),
                important=promptRetry && index==0)
        }
    }

    fun reconcile(context: Context): Boolean {
        val app = app(context)
        ensureMaintenance(context)
        val settings = TrackingSettings(context)
        val now = System.currentTimeMillis()
        val more = if (settings.enabled) app.store.reconcileSchedule(now,
            settings.intervalMinutes*60_000L,app.connectionSettings.deviceId,settings.accuracyMode) else false
        app.store.pendingCaptures().forEach { (request,next) -> capture(context,request,next) }
        // A new independent wake also repairs a crash after commit but before enqueue, or a failed old worker.
        if (app.store.pending(1).isNotEmpty()) upload(context,now+1_000)
        settings.recoveryStatus = "Queue checked at ${Instant.ofEpochMilli(now)}."
        return more
    }

    fun maintenanceWake(context: Context, at: Long) {
        val request = OneTimeWorkRequestBuilder<MaintenanceWorker>()
            .setInitialDelay((at-System.currentTimeMillis()).coerceAtLeast(0),TimeUnit.MILLISECONDS)
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL,1,TimeUnit.MINUTES).build()
        WorkManager.getInstance(context).enqueueUniqueWork("maintenance-wake:$at",ExistingWorkPolicy.KEEP,request)
            .result.get(15,TimeUnit.SECONDS)
    }

    fun start(context: Context) {
        val settings = TrackingSettings(context)
        val wasEnabled = settings.enabled
        settings.enabled = true
        if (!wasEnabled) app(context).store.resetSchedule(System.currentTimeMillis(),settings.intervalMinutes*60_000L)
        ensureMaintenance(context,true)
        enqueueEvent(context,"tracking_start")
        settings.status = "Periodic saving scheduled. Android may delay execution."
    }

    fun stop(context: Context) {
        TrackingSettings(context).enabled = false
        app(context).store.cancelTrackingCaptures()
        WorkManager.getInstance(context).cancelAllWorkByTag(TRACKING_TAG).result.get(15,TimeUnit.SECONDS)
        ensureMaintenance(context,true)
        TrackingSettings(context).status = "Periodic capture stopped. Saved readings are retained."
        upload(context)
    }

    fun retireLegacyExecution(context: Context) {
        // Upgrade cleanup only: cancel the old clock intent and stop the retired service.
        val intent = Intent().setClassName(context,context.packageName+".TrackingClockReceiver")
        PendingIntent.getBroadcast(context,30,intent,PendingIntent.FLAG_NO_CREATE or PendingIntent.FLAG_IMMUTABLE)?.let {
            context.getSystemService(AlarmManager::class.java).cancel(it); it.cancel()
        }
        context.stopService(Intent().setClassName(context,context.packageName+".ManagedTrackingService"))
    }
}
