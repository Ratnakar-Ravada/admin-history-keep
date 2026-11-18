package com.locationkeeper.app

import org.junit.Assert.*
import org.junit.Test

class TrackingSignalsTest {
    @Test fun internetCapableNetworkCanUploadWithoutAndroidValidation() {
        val state = UploadNetworkState<String>()
        state.available("open-wifi")
        assertFalse(state.ready)
        assertTrue(state.capabilitiesChanged("open-wifi", hasInternet = true, validated = false))
        assertTrue(state.ready)
        assertFalse(state.capabilitiesChanged("open-wifi", hasInternet = true, validated = false))
        assertFalse(state.capabilitiesChanged("open-wifi", hasInternet = false, validated = false))
        assertFalse(state.ready)
        assertTrue(state.capabilitiesChanged("open-wifi", hasInternet = true, validated = false))
    }

    @Test fun validationLossDoesNotBlockUploadsAndRecoveryRequestsOneRetry() {
        val state = UploadNetworkState<String>()
        state.available("wifi")
        assertTrue(state.capabilitiesChanged("wifi", true, true))
        assertFalse(state.capabilitiesChanged("wifi", true, false))
        assertTrue(state.ready)
        assertTrue(state.capabilitiesChanged("wifi", true, true))
        assertFalse(state.capabilitiesChanged("wifi", true, true))
        assertTrue(state.ready)
    }

    @Test fun staleCallbacksCannotChangeTheNewDefaultNetwork() {
        val state = UploadNetworkState<String>()
        state.available("wifi")
        state.capabilitiesChanged("wifi", true, true)
        state.available("mobile")
        assertFalse(state.ready)
        assertFalse(state.capabilitiesChanged("wifi", true, true))
        assertTrue(state.capabilitiesChanged("mobile", true, false))
        state.lost("wifi")
        assertTrue(state.ready)
        state.lost("mobile")
        assertFalse(state.ready)
        assertFalse(state.capabilitiesChanged("mobile", true, true))
    }
}
