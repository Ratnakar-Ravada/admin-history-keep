package com.locationkeeper.app

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.Typeface
import android.location.LocationManager
import android.net.*
import android.os.*
import android.provider.Settings
import android.text.InputType
import android.view.Gravity
import android.view.View
import android.widget.*
import com.google.android.gms.common.ConnectionResult
import com.google.android.gms.common.GoogleApiAvailability
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

class MainActivity : Activity() {
    private val app get()=application as LocationKeeperApplication
    private val main=Handler(Looper.getMainLooper())
    private val uiPrefs by lazy { getSharedPreferences("ui",MODE_PRIVATE) }
    private lateinit var status: TextView
    private lateinit var permissions: TextView
    private lateinit var counts: TextView
    private lateinit var history: LinearLayout
    private lateinit var capture: Button
    private lateinit var send: Button
    private lateinit var refreshReadings: Button
    private lateinit var refreshInfo: TextView
    private lateinit var periodicState: TextView
    private lateinit var autoRefresh: CheckBox
    private lateinit var deviceName: TextView
    private lateinit var savingTitle: TextView
    private lateinit var uploadedCount: TextView
    private lateinit var syncTime: TextView
    private lateinit var liveDot: View
    private lateinit var liveLabel: TextView
    private var snapshot=SyncSnapshot(0,0,0,0)
    private var visible=false
    private var refreshInFlight=false
    private var refreshAgain=false
    private var lastRefreshed: String?=null
    private var renderedRows: List<LocationSample>?=null
    private var renderedEmptyKey=""
    private var busy=false
    private var pendingCount=0
    private var cloudConfigured=false
    private var permissionDialogOpen=false
    private var waitingForLocationSettings=false
    private var waitingForAppSettings=false
    private var networkRegistered=false
    private val refreshTicker=Runnable { if(visible && autoRefresh.isChecked) refresh() }
    private val queueRefresh=Runnable { if(visible) refresh() }
    private val queueObserver: () -> Unit = {
        main.post { if(visible) { main.removeCallbacks(queueRefresh); main.postDelayed(queueRefresh,250) } }
    }
    private val networkCallback=object: ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) { if(visible) renderLive() }
        override fun onLost(network: Network) { if(visible) renderLive() }
        override fun onCapabilitiesChanged(network: Network,capabilities: NetworkCapabilities) { if(visible) renderLive() }
    }

    override fun onCreate(state: Bundle?) {
        super.onCreate(state)
        if(!DeviceProfile(this).complete) {
            startActivity(Intent(this,OnboardingActivity::class.java)); finish(); return
        }
        buildScreen()
        showStatus("Readings are saved here before upload. You can also save one now.")
    }
    override fun onResume() {
        super.onResume()
        if(!::status.isInitialized) return
        visible=true
        app.addQueueObserver(queueObserver)
        val at=System.currentTimeMillis()
        app.io.execute { runCatching {
            WorkScheduling.reconcile(this)
            WorkScheduling.enqueueEvent(this,"app_launch",triggeredAt=at)
        } }
        refresh()
        try {
            getSystemService(ConnectivityManager::class.java).registerDefaultNetworkCallback(networkCallback,main)
            networkRegistered=true
        } catch (_: SecurityException) { /* Existing refresh still updates the indicator. */ }
        if(waitingForLocationSettings) {
            waitingForLocationSettings=false
            if(locationEnabled()) beginCapture() else showStatus("Location is still off. Turn it on to save a reading.",true)
        } else if(waitingForAppSettings) {
            waitingForAppSettings=false
            if(hasLocationPermission()) beginCapture() else showStatus("Location permission is still needed.",true)
        }
    }
    override fun onPause() {
        visible=false; main.removeCallbacks(refreshTicker); main.removeCallbacks(queueRefresh)
        app.removeQueueObserver(queueObserver)
        if(networkRegistered) {
            runCatching { getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(networkCallback) }
            networkRegistered=false
        }
        super.onPause()
    }
    override fun onDestroy() { main.removeCallbacksAndMessages(null); super.onDestroy() }

    private fun buildScreen() {
        val content=KeeperUi.page(this)
        val header=LinearLayout(this).apply { orientation=LinearLayout.HORIZONTAL; gravity=Gravity.CENTER_VERTICAL }
        header.addView(KeeperUi.text(this,"LOCATION KEEPER",12f,GREEN,true),LinearLayout.LayoutParams(0,-2,1f))
        header.addView(KeeperUi.link(this,"•••") { showMore() }.apply { contentDescription="More options" },
            LinearLayout.LayoutParams(dp(54),dp(48)))
        content.addView(header)

        val hero=KeeperUi.card(this,GREEN)
        content.addView(hero.top(16))
        val heroHeader=LinearLayout(this).apply { orientation=LinearLayout.HORIZONTAL; gravity=Gravity.CENTER_VERTICAL }
        heroHeader.addView(label("THIS DEVICE",11f,0xFFD9EADF.toInt(),true),LinearLayout.LayoutParams(0,-2,1f))
        val live=LinearLayout(this).apply {
            orientation=LinearLayout.HORIZONTAL; gravity=Gravity.CENTER_VERTICAL
            setPadding(dp(12),dp(8),dp(12),dp(8))
            background=KeeperUi.shape(this@MainActivity,Color.WHITE,20)
            isClickable=true; isFocusable=true; setOnClickListener { showSyncStatus() }
        }
        liveDot=View(this).apply { importantForAccessibility=View.IMPORTANT_FOR_ACCESSIBILITY_NO }
        live.addView(liveDot,LinearLayout.LayoutParams(dp(8),dp(8)).apply { marginEnd=dp(7) })
        liveLabel=label("LIVE",11f,GREEN,true)
        live.addView(liveLabel)
        heroHeader.addView(live)
        hero.addView(heroHeader)
        deviceName=label(DeviceProfile(this).name,30f,Color.WHITE,true).apply { setTextIsSelectable(true) }
        hero.addView(deviceName.top(18))
        val initial=TrackingSettings(this)
        savingTitle=label(if(initial.enabled) "Saving every ${initial.intervalMinutes} minutes" else "Automatic saving is paused",16f,Color.WHITE,true)
        hero.addView(savingTitle.top(10))
        periodicState=label(if(initial.enabled) "Your next reading is scheduled in the background." else "Resume at any time in Saving settings.",13f,0xFFD9EADF.toInt())
        hero.addView(periodicState.top(6))
        hero.addView(View(this).apply { setBackgroundColor(0x4473AC88) },
            LinearLayout.LayoutParams(-1,dp(1)).apply { topMargin=dp(22); bottomMargin=dp(18) })
        val stats=LinearLayout(this).apply { orientation=LinearLayout.HORIZONTAL }
        val total=LinearLayout(this).apply { orientation=LinearLayout.VERTICAL }
        uploadedCount=label("0",26f,Color.WHITE,true); total.addView(uploadedCount)
        total.addView(label("uploaded from this phone",11f,0xFFD9EADF.toInt()).top(4))
        val last=LinearLayout(this).apply { orientation=LinearLayout.VERTICAL }
        syncTime=label("Not yet",19f,Color.WHITE,true); last.addView(syncTime)
        last.addView(label("last upload",11f,0xFFD9EADF.toInt()).top(7))
        stats.addView(total,LinearLayout.LayoutParams(0,-2,1f))
        stats.addView(last,LinearLayout.LayoutParams(0,-2,1f).apply { marginStart=dp(16) })
        hero.addView(stats)

        val actions=KeeperUi.card(this)
        content.addView(actions.top(16))
        permissions=label("Saved locally · Uploaded when connected",12f)
        actions.addView(permissions)
        capture=button("Save a location now",true) { beginCapture() }
        actions.addView(capture.top(14))
        actions.addView(KeeperUi.link(this,"Saving settings") {
            startActivity(Intent(this,ManagedDeviceActivity::class.java))
        }.top(4))
        status=label("",13f).apply { accessibilityLiveRegion=View.ACCESSIBILITY_LIVE_REGION_POLITE }
        actions.addView(status.top(10))

        content.addView(label("On this phone",23f,TEXT,true).top(30))
        content.addView(label("Readings waiting to upload. Confirmed records are removed from this phone and stay in your cloud history.",14f).top(8))
        counts=label("Checking the local queue…",13f,GREEN,true).apply { id=R.id.readings_count }
        content.addView(counts.top(18))
        val refreshBar=LinearLayout(this).apply { orientation=LinearLayout.HORIZONTAL; gravity=Gravity.CENTER_VERTICAL }
        autoRefresh=CheckBox(this).apply {
            id=R.id.auto_refresh_readings; text="Auto update · 30s"; textSize=13f; setTextColor(MUTED)
            isChecked=uiPrefs.getBoolean("auto_refresh",true)
            setOnCheckedChangeListener { _,checked ->
                uiPrefs.edit().putBoolean("auto_refresh",checked).apply()
                scheduleRefresh(); renderRefreshInfo()
            }
        }
        refreshBar.addView(autoRefresh,LinearLayout.LayoutParams(0,-2,1f))
        refreshReadings=KeeperUi.link(this,"Refresh") { refresh() }.apply { id=R.id.refresh_readings }
        refreshBar.addView(refreshReadings)
        content.addView(refreshBar.top(4))
        refreshInfo=label("",11f).apply { id=R.id.readings_refresh_status }
        content.addView(refreshInfo.top(4))
        history=LinearLayout(this).apply { orientation=LinearLayout.VERTICAL }
        content.addView(history.top(16))
        send=button("Sync queued readings",false) { uploadPending() }
        content.addView(send.top(14))
        content.addView(label("Saving runs about every 30 minutes by default. Android controls background timing.",12f).top(22))
        renderLive()
    }

    private fun scheduleRefresh() {
        main.removeCallbacks(refreshTicker)
        if(visible && autoRefresh.isChecked) main.postDelayed(refreshTicker,30_000)
    }
    private fun renderRefreshInfo() {
        if(!::refreshInfo.isInitialized) return
        refreshReadings.isEnabled=!refreshInFlight
        refreshReadings.text=if(refreshInFlight) "Updating…" else "Refresh"
        val time=lastRefreshed ?: "not yet"
        refreshInfo.text=when {
            refreshInFlight->"Updating readings · Last updated $time"
            autoRefresh.isChecked->"Last updated $time · Auto update every 30s"
            else->"Last updated $time · Auto update paused"
        }
    }
    private fun refresh() {
        if(refreshInFlight) { refreshAgain=true; return }
        refreshInFlight=true; renderRefreshInfo()
        permissions.text=if(!hasLocationPermission()) "Location access needed · tap Save to enable"
            else if(!locationEnabled()) "Phone location is off"
            else if(LocationCapture.hasBackgroundPermission(this)) "Location ready · background access allowed"
            else "Location ready · allow all the time for background saving"
        app.io.execute {
            try {
                val rows=app.store.recent()
                val (_,pending)=app.store.counts()
                val captures=app.store.captureCount()
                val sync=app.store.syncSnapshot()
                val configured=app.connectionSettings.endpoint().isNotBlank()
                val settings=TrackingSettings(this)
                onUi {
                    refreshInFlight=false; snapshot=sync; pendingCount=pending; cloudConfigured=configured
                    lastRefreshed=DateTimeFormatter.ofPattern("HH:mm:ss").withZone(ZoneId.systemDefault()).format(Instant.now())
                    savingTitle.text=if(settings.enabled) "Saving every ${settings.intervalMinutes} minutes" else "Automatic saving is paused"
                    periodicState.text=if(settings.enabled) "Your next reading is scheduled in the background." else "You can resume in Saving settings."
                    uploadedCount.text=sync.uploaded.toString()
                    syncTime.text=if(sync.receivedMs>0) DateTimeFormatter.ofPattern("HH:mm · d MMM").withZone(ZoneId.systemDefault())
                        .format(Instant.ofEpochMilli(sync.receivedMs)) else "Not yet"
                    counts.text="$pending waiting to upload · $captures captures queued"
                    val emptyKey="$captures:${sync.uploaded}"
                    if(renderedRows!=rows || (rows.isEmpty() && renderedEmptyKey!=emptyKey)) {
                        renderedEmptyKey=emptyKey
                        renderedRows=rows; history.removeAllViews()
                        if(rows.isEmpty()) {
                            val empty=KeeperUi.card(this)
                            empty.addView(label(if(captures>0) "Your first reading is on its way" else if(sync.uploaded>0) "All caught up" else "Ready for your first reading",19f,TEXT,true))
                            empty.addView(label(if(captures>0) "A capture is queued. Its trigger time is already saved."
                                else if(sync.uploaded>0) "Uploaded readings are safely stored in your cloud history. This phone keeps only readings awaiting confirmation."
                                else "Automatic saving is scheduled. You can also tap Save a location now.",14f).top(10))
                            history.addView(empty)
                        } else rows.forEach { history.addView(recordCard(it).top(10)) }
                    }
                    renderRefreshInfo(); renderLive(); updateButtons(); scheduleRefresh()
                    if(refreshAgain) { refreshAgain=false; refresh() }
                }
            } catch (_: Exception) {
                onUi {
                    refreshInFlight=false; refreshAgain=false
                    renderRefreshInfo(); scheduleRefresh()
                    showStatus("Could not read the local queue. Check the phone’s free storage.",true)
                }
            }
        }
    }
    private fun hasInternet(): Boolean {
        val cm=getSystemService(ConnectivityManager::class.java)
        return cm.getNetworkCapabilities(cm.activeNetwork)?.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)==true
    }
    private fun renderLive() {
        if(!::liveDot.isInitialized) return
        val live=LiveStatus.isLive(snapshot,cloudConfigured&&hasInternet(),System.currentTimeMillis())
        val color=if(live) GREEN else ERROR
        liveDot.background=KeeperUi.shape(this,color,8)
        liveLabel.setTextColor(color)
        val description=if(live) "Live: last connection confirmed. Tap for sync status."
            else "Live: waiting for a confirmed connection. Tap for sync status."
        (liveDot.parent as View).contentDescription=description
    }
    private fun showSyncStatus() {
        val detail=when {
            !cloudConfigured->"Uploads are off. Readings remain saved on this phone."
            !hasInternet()->"Waiting for internet. Your queued readings will upload automatically."
            snapshot.confirmed->"Last confirmed connection: ${formatTime(Instant.ofEpochMilli(snapshot.successMs).toString())}.\n$pendingCount readings waiting to upload."
            else->"Waiting for a confirmed upload. Your readings remain queued and will retry automatically."
        }
        val timing=if(snapshot.httpMs>0) "\n\nLast record’s timing" +
            (if(snapshot.queueWaitMs>=0) "\nWaiting to run: ${snapshot.queueWaitMs / 1_000} s" else "") +
            (if(snapshot.captureMs>=0) "\nGetting location: ${snapshot.captureMs} ms" else "") +
            "\nUpload request: ${snapshot.httpMs} ms" +
            (if(snapshot.serverMs>=0) "\nDatabase requests: ${snapshot.serverMs} ms" else "") else ""
        AlertDialog.Builder(this).setTitle("Live sync").setMessage(detail+timing)
            .setPositiveButton("Sync now") { _,_ ->
                uploadPending()
                if(pendingCount==0) app.uploads.execute {
                    val result=runCatching { app.connectionSettings.load()?.let {
                        app.uploadClient.syncProfile(it,app.connectionSettings.deviceId)
                    } }
                    onUi { if(result.isFailure) showStatus("Could not connect yet. Saved readings will retry.",true); refresh() }
                }
            }.setNegativeButton("Close",null).show()
    }
    private fun showMore() {
        AlertDialog.Builder(this).setTitle("Location Keeper")
            .setItems(arrayOf("Saving settings","Copy device name","Advanced connection")) { _,which ->
                when(which) {
                    0->startActivity(Intent(this,ManagedDeviceActivity::class.java))
                    1->{
                        getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Device name",DeviceProfile(this).name))
                        Toast.makeText(this,"Device name copied",Toast.LENGTH_SHORT).show()
                    }
                    2->connectionDialog()
                }
            }.show()
    }

    private fun beginCapture() {
        if (busy || permissionDialogOpen) return
        if (!hasLocationPermission()) {
            val requested = uiPrefs.getBoolean("location_requested", false)
            if (requested && !shouldShowRequestPermissionRationale(Manifest.permission.ACCESS_COARSE_LOCATION)) {
                AlertDialog.Builder(this)
                    .setTitle("Allow location access")
                    .setMessage("Open Permissions → Location and allow access while using the app. Choose Precise for the best available accuracy.")
                    .setPositiveButton("Open app settings") { _, _ ->
                        waitingForAppSettings = true
                        startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$packageName")))
                    }.setNegativeButton("Cancel", null).show()
            } else {
                permissionDialogOpen = true
                updateButtons()
                uiPrefs.edit().putBoolean("location_requested", true).apply()
                requestPermissions(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION,
                    Manifest.permission.ACCESS_COARSE_LOCATION), REQUEST_LOCATION)
            }
            return
        }
        if (!locationEnabled()) {
            AlertDialog.Builder(this).setTitle("Turn on phone Location")
                .setMessage("Location Keeper needs the phone's Location setting enabled to capture a fresh reading.")
                .setPositiveButton("Open Location settings") { _, _ ->
                    waitingForLocationSettings = true
                    startActivity(Intent(Settings.ACTION_LOCATION_SOURCE_SETTINGS))
                }.setNegativeButton("Cancel", null).show()
            return
        }
        val availability = GoogleApiAvailability.getInstance()
        val playStatus = availability.isGooglePlayServicesAvailable(this)
        if (playStatus != ConnectionResult.SUCCESS) {
            showStatus("Google Play services is needed. Update it and try again.", true)
            availability.getErrorDialog(this, playStatus, REQUEST_PLAY_SERVICES)?.show()
            return
        }
        acquireFreshFix()
    }

    override fun onRequestPermissionsResult(requestCode: Int, requested: Array<out String>, results: IntArray) {
        super.onRequestPermissionsResult(requestCode, requested, results)
        if (requestCode != REQUEST_LOCATION) return
        permissionDialogOpen = false
        updateButtons()
        if (hasLocationPermission()) {
            if (visible) app.io.execute { runCatching { WorkScheduling.reconcile(this) } }
            beginCapture()
        } else showStatus("Permission was not granted. Tap again when you are ready to allow location.", true)
    }

    private fun acquireFreshFix() {
        val triggeredAt=System.currentTimeMillis()
        val transport=currentNetworkTransport()
        val enabled=locationEnabled()
        val sendAfterCapture=cloudConfigured
        setBusy(true)
        showStatus("Saving the capture request on this phone…")
        app.io.execute {
            val message=try {
                if (sendAfterCapture) app.store.setJobValue("manual_upload",1)
                WorkScheduling.enqueueEvent(this,"manual",manual=true,triggeredAt=triggeredAt,
                    observedTransport=transport,observedLocationEnabled=enabled)
                "Capture queued. Trigger time is saved; Android will run the job when eligible."
            } catch (_: Exception) { "Could not queue capture. Check free storage and try again." }
            onUi { setBusy(false); showStatus(message); refresh() }
        }
    }

    private fun uploadPending() {
        if (busy) return
        setBusy(true)
        app.io.execute {
            val message=try {
                WorkScheduling.upload(this,explicit=true)
                "Upload queued. Records remain pending until the database confirms them."
            } catch (_: Exception) { "Could not schedule upload. Saved readings remain on this phone." }
            onUi { setBusy(false); showStatus(message); refresh() }
        }
    }



    private fun recordCard(sample: LocationSample): LinearLayout=KeeperUi.card(this).apply {
        addView(label(reasonLabel(sample.reason),16f,TEXT,true))
        addView(label("QUEUED FOR UPLOAD",10f,GREEN,true).top(6))
        if(sample.latitude!=null && sample.longitude!=null) {
            addView(label(String.format(Locale.US,"%.6f, %.6f",sample.latitude,sample.longitude),21f,TEXT,true)
                .apply { setTextIsSelectable(true) }.top(16))
            addView(label("Accuracy ${String.format(Locale.US,"%.1f",sample.accuracyM)} m · ${sample.permissionAccuracy}",12f).top(4))
        } else {
            addView(label(if(sample.locationStatus=="disabled") "Location was off" else "No fresh position available",18f,TEXT,true).top(16))
        }
        addView(label("Triggered   ${formatTime(sample.recordedAt)}",12f).top(14))
        sample.executedAt?.let { addView(label("Executed   ${formatTime(it)}",12f).top(4)) }
        sample.capturedAt?.let { addView(label("Measured   ${formatTime(it)}",12f).top(4)) }
        val details=LinearLayout(this@MainActivity).apply { orientation=LinearLayout.VERTICAL; visibility=View.GONE }
        sample.scheduledFor?.let { details.addView(label("Scheduled   ${formatTime(it)}",12f).top(4)) }
        details.addView(label("${accuracyLabel(sample.accuracyMode)} · ${networkLabel(sample.networkTransport)}",12f).top(6))
        details.addView(label("Outcome: ${sample.executionStatus?.replace('_',' ') ?: sample.locationStatus}\nBattery ${sample.batteryPercent?.let { "$it%" } ?: "unknown"}${if(sample.isMock) " · Simulated location" else ""}",12f).top(6))
        details.addView(label("Event ${sample.eventId}",11f).apply { setTextIsSelectable(true) }.top(8))
        addView(details)
        val toggle=KeeperUi.link(this@MainActivity,"Details ↓") {}
        toggle.setOnClickListener {
            val expanded=details.visibility==View.VISIBLE
            details.visibility=if(expanded) View.GONE else View.VISIBLE
            toggle.text=if(expanded) "Details ↓" else "Fewer details ↑"
        }
        addView(toggle.top(8))
    }
    private fun currentNetworkTransport()=LocationCapture.transport(this)
    private fun accuracyLabel(mode: String): String = getString(when (mode) {
        "battery_saving" -> R.string.tracking_accuracy_battery
        "high_precision" -> R.string.tracking_accuracy_high
        else -> R.string.tracking_accuracy_legacy
    })

    private fun networkLabel(transport: String): String = getString(when (transport) {
        "wifi" -> R.string.network_wifi
        "cellular" -> R.string.network_cellular
        "none" -> R.string.network_none
        "other" -> R.string.network_other
        else -> R.string.network_unknown
    })

    private fun reasonLabel(reason: String): String = getString(when (reason) {
        "app_launch" -> R.string.reason_app_launch
        "manual" -> R.string.reason_manual
        "periodic" -> R.string.reason_periodic
        "screen_wake" -> R.string.reason_screen_wake
        "airplane_mode_on" -> R.string.reason_airplane_mode_on
        "airplane_mode_off" -> R.string.reason_airplane_mode_off
        "location_on" -> R.string.reason_location_on
        "location_off" -> R.string.reason_location_off
        "internet_on" -> R.string.reason_internet_on
        "internet_off" -> R.string.reason_internet_off
        "network_changed" -> R.string.reason_network_changed
        "tracking_start" -> R.string.reason_tracking_start
        "boot_recovery" -> R.string.reason_boot_recovery
        "location_available" -> R.string.reason_location_available
        else -> R.string.reason_legacy
    })


    private fun connectionDialog() {
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(24), dp(8), dp(24), dp(8))
        }
        content.addView(label("This installation connects to the shared database automatically. These advanced fields are only for changing an existing connection.", 14f))
        val copy = button("Copy device ID") {
            val id = app.connectionSettings.deviceId
            getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Location Keeper device ID", id))
            android.widget.Toast.makeText(this, "Device ID copied", android.widget.Toast.LENGTH_SHORT).show()
        }
        content.addView(copy.withTopMargin(8))
        val endpoint = EditText(this).apply {
            hint = "HTTPS upload endpoint"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            setSingleLine(true)
            setText(app.connectionSettings.endpoint())
        }
        content.addView(endpoint.withTopMargin(8))
        val token = EditText(this).apply {
            hint = "Device upload token"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            setSingleLine(true)
            importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
        }
        content.addView(token.withTopMargin(8))
        content.addView(label("Leave the token blank to keep the existing one. Saving a connection does not send earlier readings until you tap Send pending readings or save another location.", 12f).withTopMargin(8))
        val dialog = AlertDialog.Builder(this).setTitle("Connection details")
            .setView(ScrollView(this).apply { addView(content) })
            .setPositiveButton("Save", null).setNegativeButton("Cancel", null)
            .setNeutralButton("Use phone storage only") { _, _ ->
                app.io.execute {
                    try {
                        app.connectionSettings.clear()
                        onUi { showStatus("Connection removed. Readings remain saved on this phone."); refresh() }
                    } catch (_: Exception) { onUi { showStatus("Could not remove the connection. Please try again.", true) } }
                }
            }.create()
        dialog.setOnShowListener {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val endpointText = endpoint.text.toString().trim()
                val tokenText = token.text.toString().trim()
                try {
                    val existing = if (tokenText.isBlank()) app.connectionSettings.load() else null
                    require(tokenText.isNotBlank() || existing?.endpoint == endpointText) {
                        "Enter a new device token when adding or changing the endpoint."
                    }
                    val value = UploadConnection(endpointText, tokenText.ifBlank { existing?.token.orEmpty() },
                        automaticRegistration = existing?.automaticRegistration == true)
                    app.connectionSettings.save(value)
                    dialog.dismiss()
                    showStatus("Database connection saved. Tap Save & send location or Send pending readings to test it.")
                    refresh()
                } catch (error: Exception) {
                    endpoint.error = if (error is IllegalArgumentException) error.message else "Could not save the connection. Re-enter the token and try again."
                }
            }
        }
        dialog.show()
    }


    private fun hasLocationPermission()=LocationCapture.hasForegroundPermission(this)
    private fun locationEnabled()=LocationCapture.locationEnabled(this)
    private fun setBusy(value: Boolean) { busy=value; updateButtons() }
    private fun updateButtons() {
        capture.isEnabled=!busy && !permissionDialogOpen
        capture.text=if(busy) "Queuing…" else "Save a location now"
        send.isEnabled=!busy && cloudConfigured && pendingCount>0
        send.visibility=if(pendingCount>0) View.VISIBLE else View.GONE
    }
    private fun showStatus(message: String,error: Boolean=false) {
        status.text=message; status.setTextColor(if(error) ERROR else MUTED)
    }
    private fun onUi(action: ()->Unit) { main.post { if(!isFinishing && !isDestroyed) action() } }
    private fun formatTime(iso: String)=DateTimeFormatter.ofPattern("d MMM, HH:mm:ss")
        .withZone(ZoneId.systemDefault()).format(Instant.parse(iso))
    private fun label(value: String,size: Float,color: Int=MUTED,bold: Boolean=false)=KeeperUi.text(this,value,size,color,bold)
    private fun button(value: String,primary: Boolean=false,click: ()->Unit)=KeeperUi.button(this,value,primary,click)
    private fun <T: View> T.withTopMargin(value: Int): T=top(value)
    private fun dp(value: Int)=KeeperUi.dp(this,value)
    companion object {
        private const val REQUEST_LOCATION=100
        private const val REQUEST_PLAY_SERVICES=101
        private val TEXT=KeeperUi.ink
        private val MUTED=KeeperUi.muted
        private val GREEN=KeeperUi.green
        private val ERROR=KeeperUi.red
    }
}
