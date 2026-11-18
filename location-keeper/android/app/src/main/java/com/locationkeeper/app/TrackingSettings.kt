package com.locationkeeper.app

import android.content.Context

class TrackingSettings(context: Context, name: String = "managed_tracking") {
    private val prefs = context.getSharedPreferences(name, Context.MODE_PRIVATE)
    var enabled: Boolean
        get() = prefs.getBoolean("enabled", false)
        set(value) {
            val edit = prefs.edit().putBoolean("enabled", value)
            if (!value) edit.putLong("next_slot_ms", 0)
            check(edit.commit())
        }
    var intervalMinutes: Int
        get() = prefs.getInt("interval_minutes", 30).takeIf { it in INTERVALS } ?: 30
        set(value) {
            require(value in INTERVALS)
            check(prefs.edit().putInt("interval_minutes", value).commit())
        }
    var accuracyMode: String
        get() = prefs.getString("accuracy_mode", "battery_saving")
            ?.takeIf { it in ACCURACY_MODES } ?: "battery_saving"
        set(value) {
            require(value in ACCURACY_MODES)
            check(prefs.edit().putString("accuracy_mode", value).commit())
        }
    var autoUpload: Boolean
        get() = prefs.getBoolean("auto_upload", true)
        set(value) { check(prefs.edit().putBoolean("auto_upload", value).commit()) }
    var recoveryStatus: String
        get() = prefs.getString("recovery_status", "No boot or update recovery has been received yet.")!!
        set(value) { check(prefs.edit().putString("recovery_status", value).commit()) }
    var status: String
        get() = prefs.getString("status", "Tracking has not been started.")!!
        set(value) { prefs.edit().putString("status", value).apply() }

    /** One local commit; a completed profile can never precede the default saving preference. */
    fun enableSetupDefaults() {
        check(prefs.edit().putBoolean("enabled", true).putInt("interval_minutes", 30)
            .putBoolean("auto_upload", true).commit())
    }

    companion object {
        val INTERVALS = listOf(15, 30, 60)
        val ACCURACY_MODES = listOf("battery_saving", "high_precision")
    }
}

