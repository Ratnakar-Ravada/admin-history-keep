package com.locationkeeper.app

import android.app.admin.DeviceAdminReceiver
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

class TrackerAdminReceiver : DeviceAdminReceiver() {
    override fun onDisabled(context: Context, intent: Intent) {
        TrackingSettings(context).enabled=false
        (context.applicationContext as LocationKeeperApplication).io.execute {
            runCatching { WorkScheduling.stop(context) }
        }
    }
}

/** WorkManager restores its own jobs. This receiver reconciles the SQLite ledger after system recovery. */
class TrackingRestartReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action !in setOf(Intent.ACTION_BOOT_COMPLETED,Intent.ACTION_MY_PACKAGE_REPLACED)) return
        val triggeredAt=System.currentTimeMillis()
        val pending=goAsync()
        val app=context.applicationContext as LocationKeeperApplication
        app.io.execute {
            try {
                if (DeviceProfile(context).complete) {
                    WorkScheduling.retireLegacyExecution(context)
                    WorkScheduling.reconcile(context)
                    WorkScheduling.enqueueEvent(context,"boot_recovery",triggeredAt=triggeredAt)
                }
            } finally { pending.finish() }
        }
    }
}
