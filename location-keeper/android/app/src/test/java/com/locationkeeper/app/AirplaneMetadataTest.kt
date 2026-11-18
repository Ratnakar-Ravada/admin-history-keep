package com.locationkeeper.app

import org.junit.Assert.*
import org.junit.Test

class AirplaneMetadataTest {
    @Test fun explicitTransitionsCarryTheirObservedState() {
        assertEquals(true, WorkPolicy.observedAirplaneState("airplane_mode_on"))
        assertEquals(false, WorkPolicy.observedAirplaneState("airplane_mode_off"))
        assertNull(WorkPolicy.observedAirplaneState("internet_off"))
    }
    @Test fun oldRowsKeepTheirOriginalUploadProjection() {
        val old = LocationSampleTest.sample()
        assertFalse(UploadProtocol.payload(old).has("airplane_mode_enabled"))
    }
    @Test fun anObservedDisabledSettingIsNotLostAsFalse() {
        val row = LocationSampleTest.sample().copy(reason="airplane_mode_off",airplaneModeEnabled=false)
        assertFalse(UploadProtocol.payload(row).getBoolean("airplane_mode_enabled"))
    }
    @Test(expected=IllegalArgumentException::class) fun contradictoryMetadataIsRejected() {
        LocationSampleTest.sample().copy(reason="airplane_mode_on",airplaneModeEnabled=false)
    }
}
