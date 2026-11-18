package com.locationkeeper.app

import android.content.*
import android.location.LocationManager
import android.net.*
import android.os.*
import android.provider.Settings

/** System observers only: no wake lock, polling, alarm, or service is started to keep them alive. */
data class ObservedTrigger(val reason: String,val at: Long,val transport: String,val locationEnabled: Boolean)

class TrackingEvents(private val app: Context,
    private val onTrigger: (ObservedTrigger)->Unit,
    private val onInternet: ()->Unit) {
    private var locationEnabled=LocationCapture.locationEnabled(app)
    private var seededNetwork=app.getSystemService(ConnectivityManager::class.java).activeNetwork==null
    private var current: Network?=null
    private var lastTransport="unknown"
    private val state=UploadNetworkState<Network>()
    private fun event(reason: String) {
        val at=System.currentTimeMillis()
        val transport=LocationCapture.transport(app)
        val enabled=LocationCapture.locationEnabled(app)
        onTrigger(ObservedTrigger(reason,at,transport,enabled))
    }
    private fun flush() {
        onInternet()
    }
    private val receiver=object: BroadcastReceiver() {
        override fun onReceive(context: Context,intent: Intent) {
            when(intent.action) {
                Intent.ACTION_SCREEN_ON->event("screen_wake")
                Intent.ACTION_AIRPLANE_MODE_CHANGED->{
                    val enabled=intent.getBooleanExtra("state",Settings.Global.getInt(
                        app.contentResolver,Settings.Global.AIRPLANE_MODE_ON,0)!=0)
                    if(!isInitialStickyBroadcast) event(if(enabled) "airplane_mode_on" else "airplane_mode_off")
                }
                LocationManager.MODE_CHANGED_ACTION->{
                    val enabled=LocationCapture.locationEnabled(context)
                    if(enabled!=locationEnabled) event(if(enabled) "location_on" else "location_off")
                    locationEnabled=enabled
                }
            }
        }
    }
    private val callback=object: ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) { current=network; state.available(network) }
        override fun onCapabilitiesChanged(network: Network,capabilities: NetworkCapabilities) {
            if(network!=current) return
            val wasReady=state.ready
            val restored=state.capabilitiesChanged(network,
                capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET),
                capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED))
            val transport=when {
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)->"wifi"
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)->"cellular"
                else->"other"
            }
            if(seededNetwork) {
                if(!wasReady && state.ready) event("internet_on")
                else if(wasReady && !state.ready) event("internet_off")
                else if(transport!=lastTransport) event("network_changed")
            }
            seededNetwork=true; lastTransport=transport
            if(restored) flush()
        }
        override fun onLost(network: Network) {
            if(network!=current) return
            current=null; state.lost(network)
            if(seededNetwork) event("internet_off")
        }
    }
    fun close() {
        runCatching { app.unregisterReceiver(receiver) }
        runCatching { app.getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(callback) }
    }
    fun register() {
        val filter=IntentFilter(LocationManager.MODE_CHANGED_ACTION).apply {
            addAction(Intent.ACTION_SCREEN_ON)
            addAction(Intent.ACTION_AIRPLANE_MODE_CHANGED)
        }
        if(Build.VERSION.SDK_INT>=33) app.registerReceiver(receiver,filter,Context.RECEIVER_NOT_EXPORTED)
        else {
            @Suppress("UnspecifiedRegisterReceiverFlag")
            app.registerReceiver(receiver,filter)
        }
        try {
            app.getSystemService(ConnectivityManager::class.java).registerDefaultNetworkCallback(callback,Handler(Looper.getMainLooper()))
        } catch(_: SecurityException) { /* Durable jobs still reconcile pending work. */ }
    }
}


