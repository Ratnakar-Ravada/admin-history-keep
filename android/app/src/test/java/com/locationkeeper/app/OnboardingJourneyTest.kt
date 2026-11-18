package com.locationkeeper.app

import android.Manifest
import android.app.Application
import android.content.Context
import android.os.Bundle
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import androidx.test.core.app.ApplicationProvider
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(application=Application::class,sdk=[28],manifest=Config.NONE)
class OnboardingJourneyTest {
    private val context=ApplicationProvider.getApplicationContext<Context>()
    @Before fun before() { context.getSharedPreferences("device_profile",0).edit().clear().commit() }
    @After fun after() { context.getSharedPreferences("device_profile",0).edit().clear().commit() }
    private fun views(view: View): List<View> = listOf(view) +
        if(view is ViewGroup) (0 until view.childCount).flatMap { views(view.getChildAt(it)) } else emptyList()
    private fun content(activity: android.app.Activity)=views(activity.findViewById(android.R.id.content))
    private fun click(activity: android.app.Activity,title: String) {
        content(activity).filterIsInstance<Button>().single { it.text.toString()==title }.performClick()
    }
    private fun has(activity: android.app.Activity,text: String)=
        content(activity).filterIsInstance<TextView>().any { it.text.toString().contains(text) }

    @Test fun setupHasOnePrimaryPermissionStepAtATimeAndRetainsTheDeviceName() {
        var controller=Robolectric.buildActivity(OnboardingActivity::class.java).create().start().resume().visible()
        var activity=controller.get()
        assertTrue(has(activity,"STEP 1 OF 3"))
        assertTrue(has(activity,"Allow location"))
        assertFalse(has(activity,"Open location settings"))
        shadowOf(context as Application).grantPermissions(Manifest.permission.ACCESS_FINE_LOCATION,Manifest.permission.ACCESS_COARSE_LOCATION)
        activity.onRequestPermissionsResult(1,arrayOf(Manifest.permission.ACCESS_FINE_LOCATION),intArrayOf(0))
        assertTrue(has(activity,"STEP 2 OF 3"))
        assertFalse(has(activity,"DEVICE NAME"))
        click(activity,"Continue")
        assertTrue(has(activity,"STEP 3 OF 3"))
        assertTrue(has(activity,"Remember this name"))
        val input=content(activity).filterIsInstance<EditText>().single()
        input.setText("Everyday phone")
        val saved=Bundle()
        controller.saveInstanceState(saved).pause().stop().destroy()
        controller=Robolectric.buildActivity(OnboardingActivity::class.java).create(saved).start().resume().visible()
        activity=controller.get()
        assertEquals("Everyday phone",content(activity).filterIsInstance<EditText>().single().text.toString())
        assertTrue(has(activity,"Start saving"))
        controller.pause().stop().destroy()
    }

    @Test fun denyingOrDeferringPermissionDoesNotClaimThatAccessWasGranted() {
        val controller=Robolectric.buildActivity(OnboardingActivity::class.java).create().start().resume().visible()
        val activity=controller.get()
        activity.onRequestPermissionsResult(1,arrayOf(Manifest.permission.ACCESS_FINE_LOCATION),intArrayOf(-1))
        assertTrue(has(activity,"STEP 1 OF 3"))
        assertTrue(has(activity,"You can allow this"))
        click(activity,"Set this permission later")
        assertTrue(has(activity,"STEP 2 OF 3"))
        assertTrue(has(activity,"Allow location first"))
        controller.pause().stop().destroy()
    }

    @Test fun recordsScreenUsesACompactLiveIndicatorAndHasNoDatabaseCard() {
        DeviceProfile(context).save("Everyday phone")
        // onCreate builds the static screen; no onResume, workers, GPS or network are started.
        val controller=Robolectric.buildActivity(MainActivity::class.java).create()
        val activity=controller.get()
        assertTrue(has(activity,"Everyday phone"))
        assertTrue(has(activity,"LIVE"))
        assertTrue(has(activity,"On this phone"))
        assertFalse(has(activity,"Your database"))
        assertFalse(has(activity,"Connection details"))
        controller.destroy()
    }
}
