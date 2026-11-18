package com.locationkeeper.app

import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import java.time.Instant

/** SQLite is the source of truth; failed network requests never remove a record. */
data class SyncSnapshot(val uploaded: Long, val receivedMs: Long, val successMs: Long, val failureMs: Long, val confirmed: Boolean = false,
    val queueWaitMs: Long = 0, val captureMs: Long = 0, val httpMs: Long = 0, val serverMs: Long = -1)

class LocationStore(context: Context, name: String = "locations.db", private val onChanged: () -> Unit = {}) :
    SQLiteOpenHelper(context.applicationContext, name, null, 5) {

    init {
        setWriteAheadLoggingEnabled(true)
    }

    override fun onCreate(db: SQLiteDatabase) { createSchema(db); createJobSchema(db) }

    private fun createSchema(db: SQLiteDatabase) {
        db.execSQL(
            """CREATE TABLE locations (
                event_id TEXT PRIMARY KEY NOT NULL,
                device_id TEXT NOT NULL,
                captured_at TEXT,
                captured_at_ms INTEGER,
                latitude REAL,
                longitude REAL,
                accuracy_m REAL,
                battery_percent INTEGER,
                is_mock INTEGER NOT NULL,
                permission_accuracy TEXT NOT NULL,
                fix_age_ms INTEGER,
                received_at TEXT,
                recorded_at TEXT NOT NULL,
                recorded_at_ms INTEGER NOT NULL,
                reason TEXT NOT NULL,
                scheduled_for TEXT,
                accuracy_mode TEXT NOT NULL,
                location_status TEXT NOT NULL,
                network_transport TEXT NOT NULL,
                executed_at TEXT,
                execution_status TEXT,
                airplane_mode_enabled INTEGER
            )""".trimIndent(),
        )
        db.execSQL("CREATE INDEX locations_pending ON locations(received_at, recorded_at_ms)")
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) {
        check(oldVersion in 1..4 && newVersion == 5) { "Missing migration from $oldVersion to $newVersion" }
        if (oldVersion == 1) {
            // SQLiteOpenHelper wraps this migration in a transaction, including the table rebuild.
            db.execSQL("ALTER TABLE locations RENAME TO locations_v1")
            db.execSQL("DROP INDEX IF EXISTS locations_pending")
            createSchema(db)
            db.execSQL(
                """INSERT INTO locations (
                    rowid, event_id, device_id, captured_at, captured_at_ms, latitude, longitude,
                    accuracy_m, battery_percent, is_mock, permission_accuracy, fix_age_ms, received_at,
                    recorded_at, recorded_at_ms, reason, scheduled_for, accuracy_mode, location_status,
                    network_transport
                ) SELECT rowid, event_id, device_id, captured_at, captured_at_ms, latitude, longitude,
                    accuracy_m, battery_percent, is_mock, permission_accuracy, fix_age_ms, received_at,
                    captured_at, captured_at_ms, 'legacy', NULL, 'legacy', 'fresh', 'unknown'
                  FROM locations_v1""".trimIndent(),
            )
            db.execSQL("DROP TABLE locations_v1")
        } else if (oldVersion == 2) {
            db.execSQL("ALTER TABLE locations ADD COLUMN executed_at TEXT")
            db.execSQL("ALTER TABLE locations ADD COLUMN execution_status TEXT")
        }
        if (oldVersion < 3) createJobSchema(db)
        if (oldVersion > 1) db.execSQL("ALTER TABLE locations ADD COLUMN airplane_mode_enabled INTEGER")
        pruneConfirmed(db)
    }

    fun save(sample: LocationSample) {
        writableDatabase.insertOrThrow("locations", null, values(sample))
        onChanged()
    }

    /** A claimed clock slot can be retried without replacing its original event or receipt. */
    fun saveIfAbsent(sample: LocationSample): Boolean =
        writableDatabase.insertWithOnConflict("locations", null, values(sample),
            SQLiteDatabase.CONFLICT_IGNORE) != -1L

    private fun values(sample: LocationSample): ContentValues = ContentValues().apply {
        put("event_id", sample.eventId)
        put("device_id", sample.deviceId)
        put("captured_at", sample.capturedAt)
        val capturedMs = sample.capturedAt?.let { Instant.parse(it).toEpochMilli() }
        if (capturedMs == null) putNull("captured_at_ms") else put("captured_at_ms", capturedMs)
        if (sample.latitude == null) putNull("latitude") else put("latitude", sample.latitude)
        if (sample.longitude == null) putNull("longitude") else put("longitude", sample.longitude)
        if (sample.accuracyM == null) putNull("accuracy_m") else put("accuracy_m", sample.accuracyM)
        if (sample.batteryPercent == null) putNull("battery_percent") else put("battery_percent", sample.batteryPercent)
        put("is_mock", if (sample.isMock) 1 else 0)
        put("permission_accuracy", sample.permissionAccuracy)
        if (sample.fixAgeMs == null) putNull("fix_age_ms") else put("fix_age_ms", sample.fixAgeMs)
        put("received_at", sample.receivedAt)
        put("recorded_at", sample.recordedAt)
        put("recorded_at_ms", Instant.parse(sample.recordedAt).toEpochMilli())
        put("reason", sample.reason)
        put("scheduled_for", sample.scheduledFor)
        put("accuracy_mode", sample.accuracyMode)
        put("location_status", sample.locationStatus)
        put("network_transport", sample.networkTransport)
        put("executed_at", sample.executedAt)
        put("execution_status", sample.executionStatus)
        if (sample.airplaneModeEnabled == null) putNull("airplane_mode_enabled")
        else put("airplane_mode_enabled", if (sample.airplaneModeEnabled) 1 else 0)
    }

    fun recent(limit: Int = 20): List<LocationSample> = read(
        "SELECT * FROM locations ORDER BY recorded_at_ms DESC, rowid DESC LIMIT ?",
        arrayOf(limit.toString()),
    )

    /** Original measured time is retained even when a later event reused this position. */
    fun latestPosition(): LocationSample? = read(
        "SELECT * FROM locations WHERE captured_at IS NOT NULL ORDER BY captured_at_ms DESC, rowid DESC LIMIT 1",
        emptyArray(),
    ).firstOrNull()

    fun pending(limit: Int = 100): List<LocationSample> = read(
        "SELECT * FROM locations WHERE received_at IS NULL ORDER BY recorded_at_ms ASC, rowid ASC LIMIT ?",
        arrayOf(limit.toString()),
    )

    /** Called only after UploadProtocol validates this exact event's server receipt. */
    fun markUploaded(eventId: String, receivedAt: String) {
        Instant.parse(receivedAt)
        transaction { db ->
            val current = db.rawQuery("SELECT received_at FROM locations WHERE event_id=?",arrayOf(eventId)).use {
                if (!it.moveToFirst()) return@transaction // An already-pruned receipt is harmless.
                it.getString(0)
            }
            check(current == null || Instant.parse(current) == Instant.parse(receivedAt)) { "Conflicting receipt" }
            db.execSQL("UPDATE locations SET received_at=? WHERE event_id=?",arrayOf(receivedAt,eventId))
            pruneConfirmed(db)
            putMeta(db,"server_success_ms",System.currentTimeMillis())
            putMeta(db,"server_state",1)
        }
        onChanged()
    }

    /** One-time upgrade cleanup plus receipt-time cleanup; unacknowledged rows are never removed. */
    private fun pruneConfirmed(db: SQLiteDatabase) {
        val summary = db.rawQuery("SELECT COUNT(*),MAX(received_at) FROM locations WHERE received_at IS NOT NULL",null).use {
            it.moveToFirst(); it.getLong(0) to it.getString(1)
        }
        if (summary.first == 0L) return
        val receivedMs = summary.second?.let { runCatching { Instant.parse(it).toEpochMilli() }.getOrNull() } ?: 0L
        putMeta(db,"uploaded_total",meta(db,"uploaded_total")+summary.first)
        putMeta(db,"last_received_ms",maxOf(meta(db,"last_received_ms"),receivedMs))
        db.execSQL("DELETE FROM upload_attempts WHERE event_id IN (SELECT event_id FROM locations WHERE received_at IS NOT NULL)")
        db.execSQL("DELETE FROM capture_requests WHERE state<>'pending' AND event_id IN (SELECT event_id FROM locations WHERE received_at IS NOT NULL)")
        db.execSQL("""DELETE FROM work_leases WHERE name IN
            (SELECT 'capture:' || event_id FROM locations WHERE received_at IS NOT NULL
             UNION ALL SELECT 'upload:' || event_id FROM locations WHERE received_at IS NOT NULL)""")
        db.execSQL("""DELETE FROM job_meta WHERE meta_key IN
            (SELECT 'capture_wait:' || event_id FROM locations WHERE received_at IS NOT NULL
             UNION ALL SELECT 'capture_run:' || event_id FROM locations WHERE received_at IS NOT NULL)""")
        db.delete("locations","received_at IS NOT NULL",null)
        // SQLite reuses the freed pages. No periodic VACUUM or additional wakeup is needed.
    }

    fun serverContact(success: Boolean, now: Long = System.currentTimeMillis()) = transaction { db ->
        putMeta(db,if (success) "server_success_ms" else "server_failure_ms",now)
        putMeta(db,"server_state",if (success) 1 else 0)
    }.also { onChanged() }

    fun syncSnapshot(): SyncSnapshot {
        val db=readableDatabase
        return SyncSnapshot(meta(db,"uploaded_total"),meta(db,"last_received_ms"),
            meta(db,"server_success_ms"),meta(db,"server_failure_ms"),meta(db,"server_state")==1L,meta(db,"last_queue_wait_ms"),meta(db,"last_capture_ms"),
            meta(db,"last_upload_http_ms"),if (meta(db,"last_server_timing_known")==1L) meta(db,"last_upload_server_ms") else -1)
    }

    fun counts(): Pair<Int, Int> = readableDatabase.rawQuery(
        "SELECT COUNT(*), COALESCE(SUM(CASE WHEN received_at IS NULL THEN 1 ELSE 0 END), 0) FROM locations",
        null,
    ).use { cursor ->
        cursor.moveToFirst()
        cursor.getInt(0) to cursor.getInt(1)
    }

    private fun createJobSchema(db: SQLiteDatabase) {
        db.execSQL("""CREATE TABLE capture_requests (
            event_id TEXT PRIMARY KEY NOT NULL, device_id TEXT NOT NULL,
            triggered_at TEXT NOT NULL, triggered_at_ms INTEGER NOT NULL, reason TEXT NOT NULL,
            scheduled_for TEXT, accuracy_mode TEXT NOT NULL, network_transport TEXT NOT NULL,
            location_enabled INTEGER, state TEXT NOT NULL DEFAULT 'pending',
            attempts INTEGER NOT NULL DEFAULT 0, next_attempt_ms INTEGER NOT NULL,
            lease_token TEXT, first_executed_at TEXT, last_executed_at TEXT, last_error TEXT
        )""")
        db.execSQL("CREATE INDEX captures_pending ON capture_requests(state,next_attempt_ms,triggered_at_ms)")
        db.execSQL("CREATE TABLE job_meta (meta_key TEXT PRIMARY KEY NOT NULL,value INTEGER NOT NULL)")
        db.execSQL("CREATE TABLE work_leases (name TEXT PRIMARY KEY NOT NULL,token TEXT NOT NULL,until_ms INTEGER NOT NULL)")
        db.execSQL("""CREATE TABLE upload_attempts (event_id TEXT PRIMARY KEY NOT NULL,
            attempts INTEGER NOT NULL,next_attempt_ms INTEGER NOT NULL,last_error TEXT)""")
    }

    private inline fun <T> transaction(block: (SQLiteDatabase) -> T): T {
        val db = writableDatabase
        db.beginTransaction()
        try { return block(db).also { db.setTransactionSuccessful() } }
        finally { db.endTransaction() }
    }

    private fun meta(db: SQLiteDatabase, key: String): Long =
        db.rawQuery("SELECT value FROM job_meta WHERE meta_key=?", arrayOf(key)).use {
            if (it.moveToFirst()) it.getLong(0) else 0L
        }

    private fun putMeta(db: SQLiteDatabase, key: String, value: Long) {
        db.execSQL("INSERT OR REPLACE INTO job_meta(meta_key,value) VALUES (?,?)", arrayOf<Any>(key,value))
    }

    fun jobValue(key: String): Long = meta(readableDatabase, key)
    fun setJobValue(key: String, value: Long) = transaction { putMeta(it, key, value) }

    /** Persist before enqueueing WorkManager. A process death between those steps is repaired later. */
    fun enqueueCapture(request: CaptureRequest, debounceMs: Long = 0): Boolean = transaction { db ->
        val at = Instant.parse(request.triggeredAt).toEpochMilli()
        val previous = meta(db, "trigger:" + request.reason)
        if (debounceMs > 0 && at >= previous && at - previous < debounceMs) return@transaction false
        val cv = ContentValues().apply {
            put("event_id", request.eventId); put("device_id", request.deviceId)
            put("triggered_at", request.triggeredAt); put("triggered_at_ms", at); put("reason", request.reason)
            put("scheduled_for", request.scheduledFor); put("accuracy_mode", request.accuracyMode)
            put("network_transport", request.networkTransport); put("next_attempt_ms", at)
            if (request.locationEnabled == null) putNull("location_enabled")
            else put("location_enabled", if (request.locationEnabled) 1 else 0)
        }
        val added = db.insertWithOnConflict("capture_requests", null, cv, SQLiteDatabase.CONFLICT_IGNORE) != -1L
        if (added) putMeta(db, "trigger:" + request.reason, at)
        added
    }.also { if(it) onChanged() }

    fun captureRequest(id: String): CaptureRequest? =
        readableDatabase.rawQuery("SELECT * FROM capture_requests WHERE event_id=? AND state='pending'",
            arrayOf(id)).use { if (it.moveToFirst()) it.toRequest() else null }

    fun pendingCaptures(limit: Int = 50): List<Pair<CaptureRequest,Long>> =
        readableDatabase.rawQuery("SELECT * FROM capture_requests WHERE state='pending' ORDER BY triggered_at_ms,event_id LIMIT ?",
            arrayOf(limit.toString())).use { c ->
            buildList { while (c.moveToNext()) add(c.toRequest() to c.getLong(c.getColumnIndexOrThrow("next_attempt_ms"))) }
        }

    fun captureCount(): Int = readableDatabase.rawQuery(
        "SELECT COUNT(*) FROM capture_requests WHERE state='pending'", null).use { it.moveToFirst(); it.getInt(0) }

    fun leaseUntil(name: String): Long = readableDatabase.rawQuery(
        "SELECT until_ms FROM work_leases WHERE name=?", arrayOf(name)).use { if (it.moveToFirst()) it.getLong(0) else 0 }

    private fun acquireLease(db: SQLiteDatabase, name: String, now: Long, duration: Long): String? {
        val until = db.rawQuery("SELECT until_ms FROM work_leases WHERE name=?", arrayOf(name)).use {
            if (it.moveToFirst()) it.getLong(0) else 0L
        }
        if (!WorkPolicy.due(until, now, duration)) return null
        val token = java.util.UUID.randomUUID().toString()
        db.execSQL("INSERT OR REPLACE INTO work_leases(name,token,until_ms) VALUES (?,?,?)", arrayOf<Any>(name,token,now+duration))
        return token
    }

    fun acquireUploadLease(now: Long, eventId: String): String? = transaction {
        acquireLease(it,"upload:$eventId",now,WorkPolicy.UPLOAD_LEASE_MS)
    }

    fun releaseLease(name: String, token: String) {
        writableDatabase.delete("work_leases","name=? AND token=?",arrayOf(name,token))
    }

    fun claimCapture(id: String, now: Long): CaptureClaim? = transaction { db ->
        val row = db.rawQuery("SELECT * FROM capture_requests WHERE event_id=? AND state='pending'",arrayOf(id)).use {
            if (!it.moveToFirst()) null else it.toRequest() to it.getLong(it.getColumnIndexOrThrow("next_attempt_ms"))
        } ?: return@transaction null
        if (!WorkPolicy.due(row.second,now)) return@transaction null
        val token = acquireLease(db,"capture:$id",now,WorkPolicy.CAPTURE_LEASE_MS) ?: return@transaction null
        val executed = Instant.ofEpochMilli(now).toString()
        db.execSQL("""UPDATE capture_requests SET attempts=attempts+1,lease_token=?,
            first_executed_at=COALESCE(first_executed_at,?),last_executed_at=? WHERE event_id=?""",
            arrayOf(token,executed,executed,id))
        CaptureClaim(row.first.copy(attempts=row.first.attempts+1),token,now)
    }

    fun completeCapture(claim: CaptureClaim, sample: LocationSample): Boolean = transaction { db ->
        val cv = ContentValues().apply { put("state","done"); putNull("lease_token"); putNull("last_error") }
        val changed = db.update("capture_requests",cv,"event_id=? AND state='pending' AND lease_token=?",
            arrayOf(claim.request.eventId,claim.token))
        if (changed != 1) return@transaction false
        db.insertWithOnConflict("locations",null,values(sample),SQLiteDatabase.CONFLICT_IGNORE)
        true
    }.also { if(it) onChanged() }

    fun retryCapture(claim: CaptureClaim, now: Long, message: String): Long = transaction { db ->
        val next = now + WorkPolicy.backoff(claim.request.attempts)
        val cv = ContentValues().apply { put("next_attempt_ms",next); putNull("lease_token"); put("last_error",message.take(200)) }
        db.update("capture_requests",cv,"event_id=? AND state='pending' AND lease_token=?",
            arrayOf(claim.request.eventId,claim.token))
        next
    }

    fun resetSchedule(now: Long, intervalMs: Long) = transaction { db ->
        putMeta(db,"periodic_next_ms",now+intervalMs)
        putMeta(db,"periodic_interval_ms",intervalMs)
    }

    /** Older missing slots become honest audit rows; only the latest due slot requests a position. */
    fun reconcileSchedule(now: Long, intervalMs: Long, deviceId: String, mode: String): Boolean = transaction { db ->
        var next = meta(db,"periodic_next_ms")
        if (next == 0L || meta(db,"periodic_interval_ms") != intervalMs || next > now + intervalMs * 2) {
            putMeta(db,"periodic_next_ms",now+intervalMs)
            putMeta(db,"periodic_interval_ms",intervalMs)
            return@transaction false
        }
        var count = 0
        while (next <= now && count++ < WorkPolicy.MAX_RECONCILE_SLOTS) {
            val req = CaptureRequest(WorkPolicy.periodicId(deviceId,next),deviceId,
                Instant.ofEpochMilli(now).toString(),"periodic",Instant.ofEpochMilli(next).toString(),mode,"unknown",null)
            if (next + intervalMs <= now) {
                db.insertWithOnConflict("locations",null,values(WorkPolicy.emptySample(req,now,"missed")),
                    SQLiteDatabase.CONFLICT_IGNORE)
            } else {
                val cv = ContentValues().apply {
                    put("event_id",req.eventId); put("device_id",req.deviceId)
                    put("triggered_at",req.triggeredAt); put("triggered_at_ms",now); put("reason","periodic")
                    put("scheduled_for",req.scheduledFor); put("accuracy_mode",mode); put("network_transport","unknown")
                    putNull("location_enabled"); put("next_attempt_ms",now)
                }
                db.insertWithOnConflict("capture_requests",null,cv,SQLiteDatabase.CONFLICT_IGNORE)
            }
            next += intervalMs
        }
        putMeta(db,"periodic_next_ms",next)
        next <= now
    }

    fun cancelTrackingCaptures() = transaction { db ->
        val pending = db.rawQuery("SELECT * FROM capture_requests WHERE state='pending' AND reason<>'manual'",null).use {
            buildList { while (it.moveToNext()) add(it.toRequest()) }
        }
        pending.forEach { req ->
            db.insertWithOnConflict("locations",null,values(WorkPolicy.emptySample(req,null,"cancelled")),
                SQLiteDatabase.CONFLICT_IGNORE)
        }
        db.execSQL("UPDATE capture_requests SET state='cancelled',lease_token=NULL,last_error='Stopped by user' WHERE state='pending' AND reason<>'manual'")
        putMeta(db,"periodic_next_ms",0)
    }

    /** Failed records are deferred independently, so one rejected record cannot block newer readings. */
    fun readyUploads(now: Long, limit: Int = 20): List<LocationSample> = read(
        """SELECT l.* FROM locations l LEFT JOIN upload_attempts a ON a.event_id=l.event_id
            WHERE l.received_at IS NULL AND (a.event_id IS NULL OR a.next_attempt_ms<=? OR a.next_attempt_ms>?)
            ORDER BY l.recorded_at_ms,l.rowid LIMIT ?""",
        arrayOf(now.toString(),(now+WorkPolicy.CAPTURE_WINDOW_MS).toString(),limit.toString()))

    fun nextReadyUpload(now: Long): LocationSample? = read(
        """SELECT l.* FROM locations l LEFT JOIN upload_attempts a ON a.event_id=l.event_id
            LEFT JOIN work_leases w ON w.name=('upload:' || l.event_id)
            WHERE l.received_at IS NULL
            AND (a.event_id IS NULL OR a.next_attempt_ms<=? OR a.next_attempt_ms>?)
            AND (w.name IS NULL OR w.until_ms<=? OR w.until_ms>?)
            ORDER BY l.recorded_at_ms,l.rowid LIMIT 1""",
        arrayOf(now.toString(),(now+WorkPolicy.CAPTURE_WINDOW_MS).toString(),
            now.toString(),(now+WorkPolicy.UPLOAD_LEASE_MS).toString())).firstOrNull()

    fun pendingSample(eventId: String): LocationSample? = read(
        "SELECT * FROM locations WHERE event_id=? AND received_at IS NULL",arrayOf(eventId)).firstOrNull()

    fun uploadDueAt(eventId: String, now: Long): Long = readableDatabase.rawQuery(
        "SELECT next_attempt_ms FROM upload_attempts WHERE event_id=?",arrayOf(eventId)).use {
        val at=if(it.moveToFirst()) it.getLong(0) else now
        if(WorkPolicy.due(at,now)) now else at
    }

    /** One prompt retry on real connectivity recovery, not on every capability callback. */
    fun recoverUploads(now: Long, explicit: Boolean = false): Boolean = transaction { db ->
        val previous=meta(db,"network_retry_ms")
        if(!explicit && previous>0 && now-previous in 0 until 60_000) return@transaction false
        db.execSQL("UPDATE upload_attempts SET next_attempt_ms=?",arrayOf<Any>(now))
        putMeta(db,"upload_retry_at",0); putMeta(db,"network_retry_ms",now)
        true
    }

    fun reserveExpedite(reason: String, now: Long): Boolean = transaction { db ->
        if(!WorkPolicy.urgentReason(reason)) return@transaction false
        if(reason=="manual") return@transaction true
        val key=if(reason=="screen_wake") "screen_expedited_ms" else "event_expedited_ms"
        val gap=if(reason=="screen_wake") 5*WorkPolicy.MINUTE else WorkPolicy.MINUTE
        val previous=meta(db,key)
        if(previous>0 && now-previous in 0 until gap) return@transaction false
        val global=meta(db,"event_expedited_ms")
        if(global>0 && now-global in 0 until WorkPolicy.MINUTE) return@transaction false
        putMeta(db,key,now); putMeta(db,"event_expedited_ms",now)
        true
    }

    fun captureTiming(eventId: String, queueMs: Long? = null, captureMs: Long? = null) = transaction { db ->
        queueMs?.let { putMeta(db,"capture_wait:$eventId",it) }
        captureMs?.let { putMeta(db,"capture_run:$eventId",it) }
    }

    fun recordUploadTiming(eventId: String, httpMs: Long, serverMs: Long?) = transaction { db ->
        fun measured(key: String)=db.rawQuery("SELECT value FROM job_meta WHERE meta_key=?",arrayOf(key)).use {
            if(it.moveToFirst()) it.getLong(0) else -1L
        }
        putMeta(db,"last_queue_wait_ms",measured("capture_wait:$eventId"))
        putMeta(db,"last_capture_ms",measured("capture_run:$eventId"))
        putMeta(db,"last_upload_http_ms",httpMs)
        putMeta(db,"last_upload_server_ms",serverMs ?: 0)
        putMeta(db,"last_server_timing_known",if(serverMs==null) 0 else 1)
    }

    fun nextUploadAt(now: Long): Long = readableDatabase.rawQuery(
        """SELECT MIN(COALESCE(a.next_attempt_ms,0)) FROM locations l
            LEFT JOIN upload_attempts a ON a.event_id=l.event_id WHERE l.received_at IS NULL""",null).use {
        it.moveToFirst()
        val at=if (it.isNull(0)) now else it.getLong(0)
        if (WorkPolicy.due(at,now)) now else at
    }

    fun deferUpload(eventId: String, now: Long) = transaction { db ->
        val pending=db.rawQuery("SELECT 1 FROM locations WHERE event_id=? AND received_at IS NULL",arrayOf(eventId)).use { it.moveToFirst() }
        if(!pending) return@transaction
        val attempts=db.rawQuery("SELECT attempts FROM upload_attempts WHERE event_id=?",arrayOf(eventId)).use {
            if (it.moveToFirst()) it.getInt(0).coerceAtMost(100)+1 else 1
        }
        db.execSQL("INSERT OR REPLACE INTO upload_attempts(event_id,attempts,next_attempt_ms,last_error) VALUES (?,?,?,?)",
            arrayOf<Any>(eventId,attempts,now+WorkPolicy.backoff(attempts),"Upload not confirmed; retry pending"))
        putMeta(db,"server_failure_ms",now); putMeta(db,"server_state",0)
    }.also { onChanged() }

    fun uploadRetryAt(now: Long): Long = jobValue("upload_retry_at").let {
        if (WorkPolicy.due(it,now)) now else it
    }
    fun uploadFailed(now: Long): Long = transaction { db ->
        val attempts = meta(db,"upload_failures").coerceAtMost(100).toInt()+1
        val next = now + WorkPolicy.backoff(attempts)
        putMeta(db,"upload_failures",attempts.toLong()); putMeta(db,"upload_retry_at",next)
        putMeta(db,"server_failure_ms",now); putMeta(db,"server_state",0)
        next
    }
    fun uploadSucceeded() = transaction { db ->
        putMeta(db,"upload_failures",0); putMeta(db,"upload_retry_at",0)
        val pending = db.rawQuery("SELECT 1 FROM locations WHERE received_at IS NULL LIMIT 1",null).use { it.moveToFirst() }
        val manualCapture=db.rawQuery("SELECT 1 FROM capture_requests WHERE state=\'pending\' AND reason=\'manual\' LIMIT 1",null).use { it.moveToFirst() }
        if (!pending && !manualCapture) putMeta(db,"manual_upload",0)
    }

    private fun Cursor.toRequest(): CaptureRequest {
        fun s(name: String) = getString(getColumnIndexOrThrow(name))
        val enabled = getColumnIndexOrThrow("location_enabled")
        return CaptureRequest(s("event_id"),s("device_id"),s("triggered_at"),s("reason"),s("scheduled_for"),
            s("accuracy_mode"),s("network_transport"),if (isNull(enabled)) null else getInt(enabled)!=0,
            getInt(getColumnIndexOrThrow("attempts")))
    }

    private fun read(sql: String, args: Array<String>): List<LocationSample> =
        readableDatabase.rawQuery(sql, args).use { cursor ->
            buildList { while (cursor.moveToNext()) add(cursor.toSample()) }
        }

    private fun Cursor.toSample(): LocationSample {
        fun string(column: String): String? = getString(getColumnIndexOrThrow(column))
        fun number(column: String): Double? = getColumnIndexOrThrow(column).let {
            if (isNull(it)) null else getDouble(it)
        }
        val batteryIndex = getColumnIndexOrThrow("battery_percent")
        val ageIndex = getColumnIndexOrThrow("fix_age_ms")
        return LocationSample(
            eventId = requireNotNull(string("event_id")),
            deviceId = requireNotNull(string("device_id")),
            capturedAt = string("captured_at"),
            latitude = number("latitude"),
            longitude = number("longitude"),
            accuracyM = number("accuracy_m"),
            batteryPercent = if (isNull(batteryIndex)) null else getInt(batteryIndex),
            isMock = getInt(getColumnIndexOrThrow("is_mock")) == 1,
            permissionAccuracy = requireNotNull(string("permission_accuracy")),
            fixAgeMs = if (isNull(ageIndex)) null else getLong(ageIndex),
            receivedAt = string("received_at"),
            recordedAt = requireNotNull(string("recorded_at")),
            reason = requireNotNull(string("reason")),
            scheduledFor = string("scheduled_for"),
            accuracyMode = requireNotNull(string("accuracy_mode")),
            locationStatus = requireNotNull(string("location_status")),
            networkTransport = requireNotNull(string("network_transport")),
            executedAt = string("executed_at"), executionStatus = string("execution_status"),
            airplaneModeEnabled = getColumnIndexOrThrow("airplane_mode_enabled").let {
                if (isNull(it)) null else getInt(it) != 0
            },
        )
    }
}
