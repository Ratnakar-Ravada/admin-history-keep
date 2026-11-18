package com.locationkeeper.app

import android.app.Application
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.IOException

@RunWith(RobolectricTestRunner::class)
@Config(application=Application::class,sdk=[28],manifest=Config.NONE)
class UploadRequestFlowTest {
    private val device="42533311-a2c3-4b1a-9649-d788754fcf3c"
    private val event="f61a379e-2780-405f-b4ef-f6393a7e4224"
    private val connection=UploadConnection(ConnectionSettings.DEFAULT_ENDPOINT,"a".repeat(43),true)
    private fun sample()=LocationSample(event,device,null,null,null,null,70,false,"none",null,
        recordedAt="2026-11-18T00:00:00Z",reason="screen_wake",accuracyMode="battery_saving")
    private fun ack()=JSONObject().put("saved",true).put("event_id",event)
        .put("received_at","2026-11-18T00:00:01Z").toString()
    private fun profile(): DeviceProfile {
        val context=ApplicationProvider.getApplicationContext<Context>()
        return DeviceProfile(context).apply { save("Test phone") }
    }

    @Test fun registeredReadingUsesOneRequestAndDoesNotResendTheProfile() {
        val bodies=mutableListOf<JSONObject>()
        val client=UploadClient(profile(),transport={ _,body,_,_ -> bodies.add(body); ack() })
        assertEquals("2026-11-18T00:00:01Z",client.send(connection,sample()))
        assertEquals(1,bodies.size)
        assertTrue(bodies.single().has("event_id"))
        assertFalse(bodies.single().has("type"))
    }

    @Test fun freshInstallationRegistersOn401AndRetriesTheSameImmutablePayloadImmediately() {
        val bodies=mutableListOf<String>()
        var inserts=0; var registrations=0
        val client=UploadClient(profile(),transport={ _,body,registration,_ ->
            if(registration) {
                registrations++
                assertEquals(device,body.getString("device_id"))
                assertEquals("Test phone",body.getString("device_name"))
                JSONObject().put("device_id",device).put("state","approved").toString()
            } else {
                bodies.add(body.toString())
                if(inserts++==0) throw UploadClient.UploadHttpException(401,"Not registered")
                ack()
            }
        })
        client.send(connection,sample())
        assertEquals(1,registrations)
        assertEquals(2,bodies.size)
        assertEquals(bodies[0],bodies[1])
    }

    @Test fun conflictsNeverRegisterOrAcknowledgeTheRecord() {
        var registrations=0
        val client=UploadClient(profile(),transport={ _,_,registration,_ ->
            if(registration) registrations++
            throw UploadClient.UploadHttpException(409,"Conflicting event")
        })
        try { client.send(connection,sample()); fail("Conflict acknowledged") } catch(_: IOException) {}
        assertEquals(0,registrations)
    }
}
