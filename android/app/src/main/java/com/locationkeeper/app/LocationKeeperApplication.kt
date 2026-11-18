package com.locationkeeper.app

import android.app.Activity
import android.app.Application
import android.os.Bundle
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

class LocationKeeperApplication : Application() {
    val io = Executors.newSingleThreadExecutor()
    val uploads = Executors.newSingleThreadExecutor()
    private val queueObservers=java.util.concurrent.CopyOnWriteArraySet<() -> Unit>()
    fun addQueueObserver(observer: () -> Unit) { queueObservers.add(observer) }
    fun removeQueueObserver(observer: () -> Unit) { queueObservers.remove(observer) }
    private fun queueChanged() { queueObservers.forEach { runCatching { it() } } }
    val store by lazy { LocationStore(this,connectionSettings.storeName) { queueChanged() } }
    val connectionSettings by lazy { ConnectionSettings(this) }
    val uploadClient by lazy { UploadClient(DeviceProfile(this),CellularUploadNetwork(this).retry,
        onConnectionResult={ connected -> store.serverContact(connected) },
        onUploadTiming={ eventId,httpMs,serverMs -> store.recordUploadTiming(eventId,httpMs,serverMs) }) }
    private val visibleActivities = AtomicInteger()
    val isVisible: Boolean get() = visibleActivities.get() > 0
    private lateinit var events: TrackingEvents

    override fun onCreate() {
        super.onCreate()
        registerActivityLifecycleCallbacks(object : ActivityLifecycleCallbacks {
            override fun onActivityStarted(activity: Activity) { visibleActivities.incrementAndGet() }
            override fun onActivityStopped(activity: Activity) { visibleActivities.updateAndGet { (it-1).coerceAtLeast(0) } }
            override fun onActivityCreated(activity: Activity, state: Bundle?) {}
            override fun onActivityResumed(activity: Activity) {}
            override fun onActivityPaused(activity: Activity) {}
            override fun onActivitySaveInstanceState(activity: Activity, state: Bundle) {}
            override fun onActivityDestroyed(activity: Activity) {}
        })
        events = TrackingEvents(this,onTrigger={ event ->
            io.execute { runCatching { WorkScheduling.enqueueEvent(this,event.reason,triggeredAt=event.at,
                observedTransport=event.transport,observedLocationEnabled=event.locationEnabled) } }
        },onInternet={
            io.execute { runCatching { WorkScheduling.upload(this,networkRecovered=true) } }
        }).also { it.register() }
        io.execute {
            runCatching {
                WorkScheduling.retireLegacyExecution(this)
                if (DeviceProfile(this).complete) WorkScheduling.reconcile(this)
            }
        }
    }
}
