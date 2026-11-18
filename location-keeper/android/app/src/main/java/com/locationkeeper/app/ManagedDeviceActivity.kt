package com.locationkeeper.app

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.app.KeyguardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.CheckBox
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Spinner
import android.widget.TextView

/** Background-job controls. No activity or service is kept alive for periodic capture. */
class ManagedDeviceActivity : Activity() {
    private val app get() = application as LocationKeeperApplication
    private val tracking by lazy { TrackingSettings(this) }
    private val controller by lazy { ManagedDeviceController(this) }
    private val main = Handler(Looper.getMainLooper())
    private lateinit var state: TextView
    private lateinit var message: TextView
    private lateinit var interval: Spinner
    private lateinit var accuracy: Spinner
    private lateinit var autoUpload: CheckBox
    private val controls = mutableListOf<Button>()
    private var pendingAction: String? = null
    private var waitingToStart = false
    private var busy = false
    private var visible = false
    private var refreshing = false
    private val ticker = object : Runnable {
        override fun run() { if (visible) { refresh(); main.postDelayed(this,30_000) } }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        pendingAction = savedInstanceState?.getString("pending_action")
        waitingToStart = savedInstanceState?.getBoolean("waiting_start") ?: false
        val content = KeeperUi.page(this)
        fun text(value: String, size: Float = 16f): TextView = KeeperUi.text(this,value,size,
            if(size>=24f) KeeperUi.ink else KeeperUi.muted,size>=24f).also { content.addView(it.top(16)) }
        fun action(title: String, block: () -> Unit) {
            controls += KeeperUi.button(this,title,title=="Start / resume tracking",block).also { content.addView(it.top(10)) }
        }
        text("Saving settings",30f)
        text("Choose how often this phone saves a location. Stop saving before changing the interval or accuracy.")
        text("Allow location all the time for background readings. Android may delay them; force-stop requires reopening the app.",14f)
        state=text(""); message=text("")
        text("Interval (stop periodic saving before changing options)")
        interval=Spinner(this).apply {
            id=R.id.tracking_interval
            adapter=ArrayAdapter(this@ManagedDeviceActivity,android.R.layout.simple_spinner_dropdown_item,
                TrackingSettings.INTERVALS.map { "$it minute(s)" })
            setSelection(savedInstanceState?.getInt("selected_interval")
                ?: TrackingSettings.INTERVALS.indexOf(tracking.intervalMinutes)); content.addView(this)
        }
        text(getString(R.string.tracking_accuracy_label))
        accuracy=Spinner(this).apply {
            id=R.id.tracking_accuracy_mode
            adapter=ArrayAdapter(this@ManagedDeviceActivity,android.R.layout.simple_spinner_dropdown_item,
                listOf(getString(R.string.tracking_accuracy_battery),getString(R.string.tracking_accuracy_high)))
            setSelection(savedInstanceState?.getInt("selected_accuracy")
                ?: TrackingSettings.ACCURACY_MODES.indexOf(tracking.accuracyMode)); content.addView(this)
        }
        autoUpload=CheckBox(this).apply {
            text="Automatically upload saved readings"; isChecked=savedInstanceState?.getBoolean("selected_upload") ?: tracking.autoUpload
            content.addView(this)
        }
        action("Save tracking options") { authenticate("SAVE") }
        action("Start / resume tracking") { requestStart() }
        action("Stop tracking") { authenticate("STOP") }
        action("Retry queued jobs") { perform("RECONCILE") }
        action("Location permission / app settings") { permissionSettings() }
        action("Battery / background activity settings") {
            AlertDialog.Builder(this).setMessage("Allowing background activity can reduce delays, but Android still controls job timing.")
                .setPositiveButton("Open settings") { _,_ -> permissionSettings() }.setNegativeButton("Cancel",null).show()
        }
        if (controller.isOwner) {
            action("Apply device protection") { authenticate("APPLY") }
            action("Restore policies and stop tracking") { authenticate("RESTORE") }
        }
        action("Back to readings") { finish() }
    }

    override fun onResume() {
        super.onResume(); visible=true; main.post(ticker)
        app.io.execute { runCatching { WorkScheduling.reconcile(this) } }
        if (waitingToStart && LocationCapture.hasForegroundPermission(this) &&
            LocationCapture.hasBackgroundPermission(this)) { waitingToStart=false; authenticate("START") }
    }
    override fun onPause() { visible=false; main.removeCallbacks(ticker); super.onPause() }
    override fun onDestroy() { main.removeCallbacksAndMessages(null); super.onDestroy() }
    override fun onSaveInstanceState(outState: Bundle) {
        outState.putString("pending_action",pendingAction); outState.putBoolean("waiting_start",waitingToStart)
        outState.putInt("selected_interval",interval.selectedItemPosition)
        outState.putInt("selected_accuracy",accuracy.selectedItemPosition)
        outState.putBoolean("selected_upload",autoUpload.isChecked); super.onSaveInstanceState(outState)
    }

    private fun refresh() {
        if (refreshing) return
        refreshing=true
        app.io.execute {
            val detail=runCatching {
                val counts=app.store.counts()
                val permission=LocationCapture.hasBackgroundPermission(this) && LocationCapture.hasForegroundPermission(this)
                "Periodic saving: ${if (tracking.enabled) "scheduled" else "off"} · ${tracking.intervalMinutes} min\n" +
                    "Background location: ${if (permission) "allowed" else "permission needed"}\n" +
                    "Queued captures: ${app.store.captureCount()} · Pending uploads: ${counts.second}\n" +
                    tracking.status+"\n"+tracking.recoveryStatus
            }.getOrDefault("Could not read job status. Check free storage.")
            main.post {
                refreshing=false
                if (!isDestroyed) {
                    state.text=detail
                    controls.forEach { it.isEnabled=!busy }
                    interval.isEnabled=!busy && !tracking.enabled
                    accuracy.isEnabled=!busy && !tracking.enabled
                    autoUpload.isEnabled=!busy && !tracking.enabled
                }
            }
        }
    }

    private fun requestStart() {
        waitingToStart=true
        if (!LocationCapture.hasForegroundPermission(this)) {
            val asked=getPreferences(MODE_PRIVATE).getBoolean("location_asked",false)
            if (asked && !shouldShowRequestPermissionRationale(Manifest.permission.ACCESS_COARSE_LOCATION)) {
                permissionSettings(); return
            }
            getPreferences(MODE_PRIVATE).edit().putBoolean("location_asked",true).apply()
            requestPermissions(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION,Manifest.permission.ACCESS_COARSE_LOCATION),100)
        } else if (!LocationCapture.hasBackgroundPermission(this)) {
            AlertDialog.Builder(this).setTitle("Allow background location")
                .setMessage("Choose Permissions → Location → Allow all the time so scheduled jobs can collect after you leave the app.")
                .setPositiveButton("Open settings") { _,_ ->
                    if (Build.VERSION.SDK_INT == 29) requestPermissions(arrayOf(Manifest.permission.ACCESS_BACKGROUND_LOCATION),101)
                    else permissionSettings()
                }.setNegativeButton("Not now") { _,_ -> waitingToStart=false }.show()
        } else { waitingToStart=false; authenticate("START") }
    }
    override fun onRequestPermissionsResult(code: Int, permissions: Array<out String>, results: IntArray) {
        super.onRequestPermissionsResult(code,permissions,results)
        if (waitingToStart && LocationCapture.hasForegroundPermission(this)) requestStart()
        else { waitingToStart=false; message.text="Location access is needed for capture. Existing readings are retained." }
    }
    private fun permissionSettings() =
        startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS,Uri.parse("package:$packageName")))

    @Suppress("DEPRECATION")
    private fun authenticate(action: String) {
        if (busy || pendingAction != null) return
        if (!controller.isOwner) { perform(action); return }
        val keyguard=getSystemService(KeyguardManager::class.java)
        val intent=keyguard.createConfirmDeviceCredentialIntent("Location Keeper","Confirm management changes")
        if (!keyguard.isDeviceSecure || intent == null) { message.setText(R.string.managed_lock_required); return }
        pendingAction=action; startActivityForResult(intent,102)
    }
    @Deprecated("Framework credential confirmation")
    override fun onActivityResult(code: Int, result: Int, data: Intent?) {
        super.onActivityResult(code,result,data)
        val action=pendingAction; pendingAction=null
        if (code==102 && result==RESULT_OK && action!=null) perform(action)
    }
    private fun perform(action: String) {
        if (busy) return
        val minutes=TrackingSettings.INTERVALS[interval.selectedItemPosition]
        val mode=TrackingSettings.ACCURACY_MODES[accuracy.selectedItemPosition]
        val upload=autoUpload.isChecked
        busy=true; refresh()
        app.io.execute {
            val result=try {
                when (action) {
                    "SAVE","START" -> {
                        if (!tracking.enabled) {
                            tracking.intervalMinutes=minutes; tracking.accuracyMode=mode; tracking.autoUpload=upload
                        }
                        if (action=="START") WorkScheduling.start(this) else WorkScheduling.ensureMaintenance(this,true)
                        if (upload) WorkScheduling.upload(this)
                        if (action=="START") "Periodic capture scheduled. Android controls execution time." else "Options saved."
                    }
                    "STOP" -> { WorkScheduling.stop(this); "Periodic capture stopped. Saved readings are retained." }
                    "RECONCILE" -> { WorkScheduling.reconcile(this); "Queues checked. Eligible jobs are scheduled with retry." }
                    "APPLY" -> controller.applyProtection()
                    "RESTORE" -> { WorkScheduling.stop(this); controller.restoreProtection() }
                    else -> "No change."
                }
            } catch (_: Exception) { "Could not update jobs. Existing records remain saved; retry when storage is available." }
            main.post { if (!isDestroyed) { busy=false; message.text=result; refresh() } }
        }
    }
}
