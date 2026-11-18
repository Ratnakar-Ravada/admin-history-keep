package com.locationkeeper.app

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.UUID

@RunWith(RobolectricTestRunner::class)
@Config(sdk=[34])
class AirplaneStoreTest {
    @Test fun newObservationsRoundTripAndOldRowsRemainUnknown() {
        val context=ApplicationProvider.getApplicationContext<Context>()
        val name="airplane-${UUID.randomUUID()}.db"
        var store=LocationStore(context,name)
        try {
            val old=LocationSampleTest.sample()
            store.save(old)
            recreateLegacyLocationTable(store.writableDatabase)
            store.writableDatabase.version=4
            store.close();store=LocationStore(context,name)
            assertNull(store.pending().single().airplaneModeEnabled)
            val observed=old.copy(eventId=UUID.randomUUID().toString(),reason="airplane_mode_on",airplaneModeEnabled=true)
            store.save(observed)
            store.close();store=LocationStore(context,name)
            assertTrue(store.pending().first {it.eventId==observed.eventId}.airplaneModeEnabled!!)
            assertFalse(UploadProtocol.payload(store.pending().first {it.eventId==old.eventId}).has("airplane_mode_enabled"))
        } finally {store.close();context.deleteDatabase(name)}
    }
}
