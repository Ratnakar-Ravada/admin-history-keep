package com.locationkeeper.app

import android.content.ComponentName
import android.content.Context
import android.content.pm.PackageManager
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Assume.assumeFalse
import org.junit.Test
import org.junit.runner.RunWith
import java.util.UUID

@RunWith(AndroidJUnit4::class)
class ManagedModeTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()

    @Test fun trackingIsOptInAndSettingsSurviveReopening() {
        val name = "tracking_test_" + UUID.randomUUID()
        try {
            val settings = TrackingSettings(context, name)
            assertFalse(settings.enabled)
            assertEquals(30, settings.intervalMinutes)
            assertEquals("battery_saving", settings.accuracyMode)
            assertTrue(settings.autoUpload)
            settings.intervalMinutes = 15
            settings.accuracyMode = "high_precision"
            settings.autoUpload = false
            settings.enabled = true
            val reopened = TrackingSettings(context, name)
            assertTrue(reopened.enabled)
            assertEquals(15, reopened.intervalMinutes)
            assertEquals("high_precision", reopened.accuracyMode)
            assertFalse(reopened.autoUpload)
            try { reopened.intervalMinutes = 0; fail("Invalid interval accepted") }
            catch (_: IllegalArgumentException) { /* expected */ }
            try { reopened.accuracyMode = "unknown"; fail("Invalid accuracy mode accepted") }
            catch (_: IllegalArgumentException) { /* expected */ }
            assertEquals("high_precision", TrackingSettings(context, name).accuracyMode)
        } finally { context.deleteSharedPreferences(name) }
    }

    @Suppress("DEPRECATION")
    @Test fun managementComponentsArePrivateAndAdminIsSystemProtected() {
        val pm = context.packageManager
        assertFalse(pm.getActivityInfo(ComponentName(context, ManagedDeviceActivity::class.java), 0).exported)
        try {
            pm.getServiceInfo(ComponentName(context,context.packageName+".ManagedTrackingService"),0)
            fail("Retired foreground service is still declared")
        } catch (_: PackageManager.NameNotFoundException) { /* expected */ }
        val admin = pm.getReceiverInfo(ComponentName(context, TrackerAdminReceiver::class.java), PackageManager.GET_META_DATA)
        assertTrue(admin.exported)
        assertEquals("android.permission.BIND_DEVICE_ADMIN", admin.permission)
        assertNotEquals(0, admin.metaData.getInt("android.app.device_admin"))
        assertFalse(pm.getReceiverInfo(ComponentName(context, TrackingRestartReceiver::class.java), 0).exported)
    }

    @Test fun normalInstallationCannotApplyOwnerPolicies() {
        val controller = ManagedDeviceController(context)
        assumeFalse(controller.isOwner)
        try { controller.applyProtection(); fail("Non-owner applied policies") }
        catch (_: IllegalStateException) { /* expected, before any mutation */ }
        assertFalse(controller.hasBackup)
    }
}
