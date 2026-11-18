package com.locationkeeper.app

import android.app.admin.DevicePolicyManager
import android.content.ComponentName
import android.content.Context
import android.os.Build
import android.os.UserManager

/** All privileged operations stay in the owner package and target only this app. */
class ManagedDeviceController(private val context: Context) {
    private val dpm = context.getSystemService(DevicePolicyManager::class.java)
    private val admin = ComponentName(context, TrackerAdminReceiver::class.java)
    private val prefs = context.getSharedPreferences("managed_policy_backup", Context.MODE_PRIVATE)
    val isOwner: Boolean get() = dpm.isDeviceOwnerApp(context.packageName)
    val supported: Boolean get() = Build.VERSION.SDK_INT >= 30
    val hasBackup: Boolean get() = prefs.getBoolean("saved", false)

    fun applyProtection(): String {
        check(supported && isOwner) { "Android 11+ and device-owner enrollment are required." }
        if (Build.VERSION.SDK_INT < 30) return "Android 11 or later is required."
        if (!hasBackup) {
            val restrictions = dpm.getUserRestrictions(admin)
            check(prefs.edit()
                .putBoolean("location", context.getSystemService(android.location.LocationManager::class.java).isLocationEnabled)
                .putBoolean("restricted", restrictions.getBoolean(UserManager.DISALLOW_CONFIG_LOCATION))
                .putBoolean("uninstall", dpm.isUninstallBlocked(admin, context.packageName))
                .putBoolean("user_control", context.packageName in dpm.getUserControlDisabledPackages(admin))
                .putBoolean("saved", true).commit()) { "Could not save rollback settings." }
        }
        return perform(
            "Location enabled" to { dpm.setLocationEnabled(admin, true) },
            "Location changes blocked" to { dpm.addUserRestriction(admin, UserManager.DISALLOW_CONFIG_LOCATION) },
            "Force-stop and data clearing blocked" to {
                dpm.setUserControlDisabledPackages(admin,
                    (dpm.getUserControlDisabledPackages(admin) + context.packageName).distinct())
            },
            "Uninstall blocked" to { dpm.setUninstallBlocked(admin, context.packageName, true) },
        )
    }

    /** Attempts every restoration even if one policy fails; retains backup for retry. */
    fun restoreProtection(): String {
        check(supported && isOwner) { "Device-owner enrollment is required." }
        if (Build.VERSION.SDK_INT < 30 || !hasBackup) return "No policy backup to restore."
        var failed = false
        val result = perform(
            "Uninstall policy restored" to { dpm.setUninstallBlocked(admin, context.packageName, prefs.getBoolean("uninstall", false)) },
            "User control restored" to {
                val packages = dpm.getUserControlDisabledPackages(admin).toMutableSet()
                if (prefs.getBoolean("user_control", false)) packages.add(context.packageName)
                else packages.remove(context.packageName)
                dpm.setUserControlDisabledPackages(admin, packages.toList())
            },
            "Location restriction restored" to {
                if (prefs.getBoolean("restricted", false)) dpm.addUserRestriction(admin, UserManager.DISALLOW_CONFIG_LOCATION)
                else dpm.clearUserRestriction(admin, UserManager.DISALLOW_CONFIG_LOCATION)
            },
            "Location setting restored" to { dpm.setLocationEnabled(admin, prefs.getBoolean("location", true)) },
            onFailure = { failed = true },
        )
        if (!failed) check(prefs.edit().clear().commit())
        return result
    }

    fun status(): String {
        if (!isOwner) return "Standard mode: periodic saving is available. Device-owner protections are off."
        if (!supported) return "Device-owner protection requires Android 11 or later."
        return try {
            if (Build.VERSION.SDK_INT < 30) return "Android 11 or later is required."
            val location = context.getSystemService(android.location.LocationManager::class.java).isLocationEnabled
            "Device owner: enrolled\nLocation: ${if (location) "on" else "off"}\n" +
                "Location changes blocked: ${dpm.getUserRestrictions(admin).getBoolean(UserManager.DISALLOW_CONFIG_LOCATION)}\n" +
                "User control blocked: ${context.packageName in dpm.getUserControlDisabledPackages(admin)}\n" +
                "Uninstall blocked: ${dpm.isUninstallBlocked(admin, context.packageName)}"
        } catch (_: SecurityException) { "Policy access changed. Refresh enrollment status." }
    }

    private fun perform(vararg actions: Pair<String, () -> Unit>, onFailure: () -> Unit = {}): String =
        actions.joinToString("\n") { (label, action) ->
            try { action(); "$label: applied" }
            catch (_: RuntimeException) { onFailure(); "$label: failed; refresh status and retry or restore policies." }
        }
}
