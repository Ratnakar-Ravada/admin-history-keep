package com.locationkeeper.app

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.text.InputFilter
import android.text.InputType
import android.view.Gravity
import android.view.View
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputMethodManager
import android.widget.*
import java.util.concurrent.atomic.AtomicReference

/** Three permission/name steps, then a finite success transition. No idle animation or network wait. */
class OnboardingActivity : Activity() {
    private val profile by lazy { DeviceProfile(this) }
    private val main=Handler(Looper.getMainLooper())
    private var step=0
    private var nameDraft=""
    private var waitingForSettings=false
    private var visible=false
    private var task: SetupTask?=null
    private lateinit var content: LinearLayout
    private lateinit var message: TextView
    private var nameInput: EditText?=null
    private var primary: Button?=null
    private var back: Button?=null
    private val observeSetup=object : Runnable {
        override fun run() {
            if (!visible) return
            val current=task ?: return
            val result=current.result.get()
            if (result==null) main.postDelayed(this,200)
            else if (result=="done") { task=null; step=3; render() }
            else {
                task=null; primary?.isEnabled=true; nameInput?.isEnabled=true; back?.isEnabled=true
                message.text="Could not finish setup. Your saved details are safe. Please try again."
            }
        }
    }
    private val openRecords=Runnable {
        if (visible && !isFinishing) {
            startActivity(Intent(this,MainActivity::class.java))
            finish()
        }
    }
    private var backCallback: android.window.OnBackInvokedCallback?=null

    override fun onCreate(state: Bundle?) {
        super.onCreate(state)
        step=state?.getInt("step") ?: 0
        nameDraft=state?.getString("draft") ?: profile.name.ifBlank { profile.detectedName.take(100) }
        waitingForSettings=state?.getBoolean("waiting") ?: false
        @Suppress("DEPRECATION")
        task=lastNonConfigurationInstance as? SetupTask
        if (state?.getBoolean("finishing_setup")==true && task==null && profile.complete) step=3
        content=KeeperUi.page(this)
        if (Build.VERSION.SDK_INT>=33) {
            backCallback=android.window.OnBackInvokedCallback { goBack() }
            onBackInvokedDispatcher.registerOnBackInvokedCallback(0,backCallback!!)
        }
        render()
    }
    override fun onSaveInstanceState(state: Bundle) {
        state.putInt("step",step); state.putString("draft",nameDraft)
        state.putBoolean("waiting",waitingForSettings); state.putBoolean("finishing_setup",task!=null)
        super.onSaveInstanceState(state)
    }
    @Deprecated("Retains only completion state, never an Activity or View")
    override fun onRetainNonConfigurationInstance(): Any?=task
    override fun onResume() {
        super.onResume(); visible=true
        if (waitingForSettings) {
            waitingForSettings=false
            if (step==0 && foreground()) step=1
            else if (step==1 && foreground() && background()) step=2
            render()
        }
        if (task!=null) main.post(observeSetup)
        if (step==3) main.postDelayed(openRecords,1_300)
    }
    override fun onPause() {
        visible=false; main.removeCallbacksAndMessages(null); content.animate().cancel()
        content.alpha=1f; content.translationY=0f
        super.onPause()
    }
    override fun onDestroy() {
        main.removeCallbacksAndMessages(null)
        if (Build.VERSION.SDK_INT>=33) backCallback?.let { onBackInvokedDispatcher.unregisterOnBackInvokedCallback(it) }
        super.onDestroy()
    }
    // API 33+ uses the registered OnBackInvokedCallback above; this is only the legacy fallback.
    @android.annotation.SuppressLint("GestureBackNavigation")
    @Deprecated("Used on Android 12 and earlier")
    override fun onBackPressed() { goBack() }
    private fun goBack() {
        if (task!=null) return
        if (step==3) { main.removeCallbacks(openRecords); openRecords.run() }
        else if (step>0) { step--; render() } else finish()
    }

    private fun granted(permission: String)=checkSelfPermission(permission)==PackageManager.PERMISSION_GRANTED
    private fun foreground()=granted(Manifest.permission.ACCESS_FINE_LOCATION)||granted(Manifest.permission.ACCESS_COARSE_LOCATION)
    private fun background()=Build.VERSION.SDK_INT<29||granted(Manifest.permission.ACCESS_BACKGROUND_LOCATION)
    private fun appSettings() {
        waitingForSettings=true
        startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS,Uri.parse("package:$packageName")))
    }

    private fun render() {
        content.animate().cancel(); content.removeAllViews(); nameInput=null; primary=null; back=null
        val brand=KeeperUi.text(this,"LOCATION KEEPER",12f,KeeperUi.green,true)
        content.addView(brand.top(8))
        if (step==3) { renderSuccess(); return }
        val progress=LinearLayout(this).apply { orientation=LinearLayout.HORIZONTAL }
        repeat(3) { index ->
            progress.addView(View(this).apply {
                background=KeeperUi.shape(this@OnboardingActivity,if (index<=step) KeeperUi.green else KeeperUi.line,4)
                importantForAccessibility=View.IMPORTANT_FOR_ACCESSIBILITY_NO
            },LinearLayout.LayoutParams(0,dp(4),1f).apply { if(index<2) marginEnd=dp(6) })
        }
        content.addView(progress.top(28))
        content.addView(KeeperUi.text(this,"STEP ${step+1} OF 3",11f,KeeperUi.muted,true).top(20))
        content.addView(KeeperMark(this).apply {
            layoutParams=LinearLayout.LayoutParams(dp(72),dp(72)).apply { topMargin=dp(16) }
            importantForAccessibility=View.IMPORTANT_FOR_ACCESSIBILITY_NO
        })
        val title=when(step) { 0->"Start with location"; 1->"Keep saving in the background"; else->"Name this phone" }
        content.addView(KeeperUi.text(this,title,30f,KeeperUi.ink,true).top(20))
        val description=when(step) {
            0->"Allow location so this phone can save its whereabouts. Choose Precise for the best available accuracy."
            1->if(Build.VERSION.SDK_INT<29) "Once location access is allowed, this Android version can also use it in the background."
                else "Let Location Keeper save a reading after you leave the app. Android calls this “Allow all the time”."
            else->"Choose a name you will recognise when you look for this device in your location history."
        }
        content.addView(KeeperUi.text(this,description,16f).top(14))
        val detail=KeeperUi.card(this)
        content.addView(detail.top(26))
        when(step) {
            0-> {
                detail.addView(KeeperUi.text(this,"A lighter way to keep track",16f,KeeperUi.ink,true))
                detail.addView(KeeperUi.text(this,"A reading about every 30 minutes. Saved on the phone while offline, then uploaded when connected.",14f).top(8))
            }
            1-> {
                detail.addView(KeeperUi.text(this,"In Android settings",16f,KeeperUi.ink,true))
                val option=if (Build.VERSION.SDK_INT>=30) packageManager.backgroundPermissionOptionLabel.toString() else "Allow all the time"
                detail.addView(KeeperUi.text(this,if(Build.VERSION.SDK_INT<29)
                    "Location access is ready. No additional background permission is needed on this Android version."
                    else "1   Open Permissions → Location\n2   Choose “$option”\n3   Return here to continue",15f,KeeperUi.ink).top(12))
                detail.addView(KeeperUi.text(this,"You can change this later in the phone’s app settings. Android may delay background readings.",13f).top(12))
            }
            else-> {
                detail.addView(KeeperUi.text(this,"DEVICE NAME",11f,KeeperUi.green,true))
                nameInput=EditText(this).apply {
                    hint="e.g. My everyday phone"; textSize=19f; setTextColor(KeeperUi.ink)
                    setSingleLine(true); filters=arrayOf(InputFilter.LengthFilter(100))
                    inputType=InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_WORDS
                    imeOptions=EditorInfo.IME_ACTION_DONE
                    setText(nameDraft); setSelectAllOnFocus(true)
                    addTextChangedListener(object: android.text.TextWatcher {
                        override fun beforeTextChanged(s: CharSequence?,start: Int,count: Int,after: Int) {}
                        override fun onTextChanged(s: CharSequence?,start: Int,before: Int,count: Int) { nameDraft=s.toString() }
                        override fun afterTextChanged(s: android.text.Editable?) {}
                    })
                    setOnEditorActionListener { _,action,_ -> if (action==EditorInfo.IME_ACTION_DONE) { finishSetup(); true } else false }
                }
                detail.addView(nameInput!!.top(8))
                detail.addView(KeeperUi.text(this,"Detected: ${profile.detectedName}",12f).top(10))
                detail.addView(KeeperUi.text(this,"Remember this name. You’ll use it to identify the right phone in your tracking records.",14f,KeeperUi.ink,true).top(18))
            }
        }
        message=KeeperUi.text(this,"",13f,KeeperUi.red).apply {
            accessibilityLiveRegion=View.ACCESSIBILITY_LIVE_REGION_POLITE
        }
        content.addView(message.top(12))
        val action=when(step) {
            0->if(foreground()) "Continue" else "Allow location"
            1->if(foreground()&&background()) "Continue" else if(!foreground()) "Allow location first" else "Open location settings"
            else->"Start saving"
        }
        primary=KeeperUi.button(this,action) {
            when(step) {
                0->if(foreground()) advance() else requestForeground()
                1->when {
                    !foreground()->requestForeground()
                    background()->advance()
                    Build.VERSION.SDK_INT==29->requestPermissions(arrayOf(Manifest.permission.ACCESS_BACKGROUND_LOCATION),2)
                    else->appSettings()
                }
                else->finishSetup()
            }
        }
        content.addView(primary!!.top(12))
        if (step<2) content.addView(KeeperUi.link(this,"Set this permission later") { advance() }.top(4))
        else content.addView(KeeperUi.text(this,"Starts 30-minute saving immediately. No internet connection needed to get started.",12f).top(14))
        if(step>0) {
            back=KeeperUi.link(this,"← Back") { goBack() }
            content.addView(back!!.top(8))
        }
        if (task!=null) { primary?.isEnabled=false; back?.isEnabled=false; nameInput?.isEnabled=false; message.text="Starting local saving…" }
        KeeperUi.enter(content)
    }
    private fun advance() { step=(step+1).coerceAtMost(2); render() }
    private fun requestForeground() {
        val prefs=getPreferences(MODE_PRIVATE)
        if (prefs.getBoolean("location_asked",false)&&
            !shouldShowRequestPermissionRationale(Manifest.permission.ACCESS_COARSE_LOCATION)&&
            !shouldShowRequestPermissionRationale(Manifest.permission.ACCESS_FINE_LOCATION)) { appSettings(); return }
        prefs.edit().putBoolean("location_asked",true).apply()
        requestPermissions(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION,Manifest.permission.ACCESS_COARSE_LOCATION),1)
    }
    override fun onRequestPermissionsResult(code: Int,permissions: Array<out String>,results: IntArray) {
        super.onRequestPermissionsResult(code,permissions,results)
        if(code==1 && foreground()) { if(step==0) step=1; render() }
        else if(code==2 && background()) { step=2; render() }
        else { render(); message.text="You can allow this in app settings later. Background readings need location access." }
    }

    private fun finishSetup() {
        if (task!=null) return
        val chosenName=nameDraft.trim()
        if(chosenName.isBlank()) { nameInput?.error="Enter a name for this phone"; nameInput?.requestFocus(); return }
        getSystemService(InputMethodManager::class.java).hideSoftInputFromWindow(nameInput?.windowToken,0)
        val state=SetupTask(); task=state
        primary?.isEnabled=false; back?.isEnabled=false; nameInput?.isEnabled=false
        message.setTextColor(KeeperUi.muted); message.text="Starting local saving…"
        val app=application as LocationKeeperApplication
        // Only application context and immutable input are retained by the task.
        app.io.execute {
            try {
                val savedProfile=DeviceProfile(app)
                val firstSetup=!savedProfile.complete
                if(firstSetup) TrackingSettings(app).enableSetupDefaults()
                savedProfile.save(chosenName)
                if(firstSetup) WorkScheduling.start(app) else WorkScheduling.reconcile(app)
                state.result.set("done")
                app.uploads.execute { runCatching { app.connectionSettings.load()?.let {
                    app.uploadClient.syncProfile(it,app.connectionSettings.deviceId)
                } } }
            } catch (_: Exception) { state.result.set("failed") }
        }
        main.post(observeSetup)
    }
    private fun renderSuccess() {
        content.gravity=Gravity.CENTER_HORIZONTAL
        content.addView(KeeperMark(this).apply { layoutParams=LinearLayout.LayoutParams(dp(120),dp(120)).apply { topMargin=dp(80) } })
        content.addView(KeeperUi.text(this,"You’re all set",13f,KeeperUi.green,true).top(30))
        val allowed=foreground()&&background()
        content.addView(KeeperUi.text(this,if(allowed) "Saving has started" else "Saving is scheduled",32f,KeeperUi.ink,true).top(12))
        content.addView(KeeperUi.text(this,"$nameDraft · Every 30 minutes",17f,KeeperUi.ink).top(16))
        content.addView(KeeperUi.text(this,if(allowed)
            "Your first reading is queued. We’ll upload it when connected."
            else "Allow location all the time to collect readings while the app is closed.",15f).top(12))
        content.addView(KeeperUi.button(this,"View records") { main.removeCallbacks(openRecords); openRecords.run() }.top(32))
        KeeperUi.enter(content)
        if(visible) main.postDelayed(openRecords,1_300)
    }
    private fun dp(value: Int)=KeeperUi.dp(this,value)
    private class SetupTask { val result=AtomicReference<String?>(null) }
}
