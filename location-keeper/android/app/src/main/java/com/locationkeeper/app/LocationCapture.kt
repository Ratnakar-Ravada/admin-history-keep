package com.locationkeeper.app

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.location.LocationManager
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.BatteryManager
import android.os.Build
import android.os.SystemClock
import com.google.android.gms.location.CurrentLocationRequest
import com.google.android.gms.location.Granularity
import com.google.android.gms.location.LocationServices
import com.google.android.gms.location.Priority
import com.google.android.gms.tasks.CancellationTokenSource
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull
import java.time.Instant
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

object LocationCapture {
    fun hasForegroundPermission(context: Context): Boolean =
        context.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED ||
            context.checkSelfPermission(Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED
    fun hasBackgroundPermission(context: Context): Boolean = Build.VERSION.SDK_INT < 29 ||
        context.checkSelfPermission(Manifest.permission.ACCESS_BACKGROUND_LOCATION) == PackageManager.PERMISSION_GRANTED
    fun locationEnabled(context: Context): Boolean {
        val manager = context.getSystemService(LocationManager::class.java)
        return if (Build.VERSION.SDK_INT >= 28) manager.isLocationEnabled else {
            @Suppress("DEPRECATION")
            (manager.isProviderEnabled(LocationManager.GPS_PROVIDER) || manager.isProviderEnabled(LocationManager.NETWORK_PROVIDER))
        }
    }
    fun transport(context: Context): String = try {
        val manager = context.getSystemService(ConnectivityManager::class.java)
        val capabilities = manager.getNetworkCapabilities(manager.activeNetwork)
        when {
            capabilities == null -> "none"
            capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "wifi"
            capabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "cellular"
            else -> "other"
        }
    } catch (_: SecurityException) { "unknown" }

    @Suppress("MissingPermission")
    suspend fun fresh(context: Context, mode: String): MeasuredFix? = withTimeoutOrNull(32_000) {
        val started = SystemClock.elapsedRealtimeNanos()
        val token = CancellationTokenSource()
        try {
            val request = CurrentLocationRequest.Builder()
                .setPriority(if (mode == "high_precision") Priority.PRIORITY_HIGH_ACCURACY
                    else Priority.PRIORITY_BALANCED_POWER_ACCURACY)
                .setGranularity(Granularity.GRANULARITY_PERMISSION_LEVEL)
                .setMaxUpdateAgeMillis(0).setDurationMillis(30_000).build()
            val fix = suspendCancellableCoroutine<android.location.Location?> { continuation ->
                continuation.invokeOnCancellation { token.cancel() }
                LocationServices.getFusedLocationProviderClient(context).getCurrentLocation(request,token.token)
                    .addOnSuccessListener { if (continuation.isActive) continuation.resume(it) }
                    .addOnFailureListener { if (continuation.isActive) continuation.resumeWithException(it) }
                    .addOnCanceledListener { continuation.cancel() }
            } ?: return@withTimeoutOrNull null
            val age = (SystemClock.elapsedRealtimeNanos()-fix.elapsedRealtimeNanos)/1_000_000
            if (!fix.hasAccuracy() || !LocationSample.isFresh(age) || fix.elapsedRealtimeNanos < started)
                return@withTimeoutOrNull null
            val mock = if (Build.VERSION.SDK_INT >= 31) fix.isMock else {
                @Suppress("DEPRECATION") fix.isFromMockProvider
            }
            MeasuredFix(Instant.ofEpochMilli(fix.time).toString(),fix.latitude,fix.longitude,fix.accuracy.toDouble(),age,mock)
        } finally { token.cancel() }
    }
}

interface JobRuntime {
    val store: LocationStore
    val settings: TrackingSettings
    fun now(): Long
    fun canLocate(): Boolean
    fun networkAvailable(): Boolean
    fun locationEnabled(): Boolean
    fun precise(): Boolean
    fun battery(): Int?
    suspend fun locate(mode: String): MeasuredFix?
    fun upload(sample: LocationSample): String
    fun wakeCapture(request: CaptureRequest, at: Long)
    fun wakeUpload(at: Long)
    fun wakeNextUpload()
    fun wakeUploadRecord(eventId: String, at: Long)
}

class AndroidJobRuntime(private val context: Context) : JobRuntime {
    private val app get() = context.applicationContext as LocationKeeperApplication
    override val store get() = app.store
    override val settings get() = TrackingSettings(context)
    override fun now() = System.currentTimeMillis()
    override fun canLocate() = LocationCapture.hasForegroundPermission(context) &&
        (LocationCapture.hasBackgroundPermission(context) || app.isVisible)
    override fun networkAvailable(): Boolean {
        val manager=context.getSystemService(ConnectivityManager::class.java)
        return manager.getNetworkCapabilities(manager.activeNetwork)?.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)==true
    }
    override fun locationEnabled() = LocationCapture.locationEnabled(context)
    override fun precise() = context.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED
    override fun battery() = context.getSystemService(BatteryManager::class.java)
        .getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY).takeIf { it in 0..100 }
    override suspend fun locate(mode: String) = LocationCapture.fresh(context,mode)
    override fun upload(sample: LocationSample): String {
        val connection = app.connectionSettings.load() ?: throw java.io.IOException("Database uploads are switched off.")
        return app.uploadClient.send(connection,sample)
    }
    override fun wakeCapture(request: CaptureRequest, at: Long) = WorkScheduling.capture(context,request,at)
    override fun wakeUpload(at: Long) = WorkScheduling.upload(context,at)
    override fun wakeNextUpload() = WorkScheduling.uploadOne(context)
    override fun wakeUploadRecord(eventId: String, at: Long) = WorkScheduling.uploadRecord(context,eventId,at)
}
