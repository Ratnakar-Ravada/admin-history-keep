package com.locationkeeper.app

import android.app.Application
import android.content.Context
import android.content.Intent
import android.os.Looper
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(application=Application::class,sdk=[28],manifest=Config.NONE)
class EventSchedulingTest {
    @Test fun screenWakeAndBothAirplaneTransitionsProduceIndependentObservedEvents() {
        val context=ApplicationProvider.getApplicationContext<Context>()
        val events=mutableListOf<ObservedTrigger>()
        val observer=TrackingEvents(context,onTrigger={ events.add(it) },onInternet={})
        observer.register()
        try {
            for(intent in listOf(Intent(Intent.ACTION_SCREEN_ON),
                Intent(Intent.ACTION_AIRPLANE_MODE_CHANGED).putExtra("state",true),
                Intent(Intent.ACTION_AIRPLANE_MODE_CHANGED).putExtra("state",false))) {
                context.sendBroadcast(intent)
                shadowOf(Looper.getMainLooper()).idle()
            }
            assertEquals(listOf("screen_wake","airplane_mode_on","airplane_mode_off"),events.map { it.reason })
            assertTrue(events.all { it.at>0 })
        } finally { observer.close() }
    }

    @Test fun readyExpeditedWorkFallsBackNormallyAndDelayedRetriesNeverStayExpedited() {
        val fast=WorkScheduling.recordRequest("event",0,true,false).build()
        assertTrue(fast.workSpec.expedited)
        assertEquals(androidx.work.OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST,fast.workSpec.outOfQuotaPolicy)
        assertEquals(androidx.work.NetworkType.NOT_REQUIRED,fast.workSpec.constraints.requiredNetworkType)
        val retry=WorkScheduling.recordRequest("event",60_000,true,true).build()
        assertFalse(retry.workSpec.expedited)
        assertEquals(androidx.work.NetworkType.CONNECTED,retry.workSpec.constraints.requiredNetworkType)
    }
}
