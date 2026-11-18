package com.locationkeeper.app

import android.content.Context
import android.os.Build
import android.provider.Settings
import org.json.JSONObject

class DeviceProfile(private val context: Context) {
    private val prefs = context.getSharedPreferences("device_profile", Context.MODE_PRIVATE)
    val complete: Boolean get() = prefs.getBoolean("complete", false)
    val name: String get() = prefs.getString("name", "") ?: ""
    val detectedName: String get() = runCatching {
        Settings.Global.getString(context.contentResolver, Settings.Global.DEVICE_NAME)
    }.getOrNull()?.trim()?.takeIf { it.isNotEmpty() }?.take(200)
        ?: listOf(Build.MANUFACTURER, Build.MODEL).filter { it.isNotBlank() }
            .joinToString(" ").take(200)

    fun save(name: String) {
        require(name.trim().length in 1..100)
        check(prefs.edit().putString("name", name.trim()).putBoolean("complete", true).commit())
    }

    fun addTo(json: JSONObject): JSONObject = json.apply {
        if (complete) {
            put("device_name", name)
            put("detected_device_name", detectedName)
        }
    }
}
