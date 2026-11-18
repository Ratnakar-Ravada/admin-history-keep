package com.locationkeeper.app

import org.junit.Assert.*
import org.junit.Test

class LiveStatusTest {
    @Test fun greenRequiresARecentServerConfirmationAndAnAvailableNetwork() {
        val now=10_000_000L
        val snapshot=SyncSnapshot(1,now-1_000,now-1_000,0,true)
        assertTrue(LiveStatus.isLive(snapshot,true,now))
        assertFalse(LiveStatus.isLive(snapshot,false,now))
        assertFalse(LiveStatus.isLive(snapshot.copy(confirmed=false),true,now))
        assertFalse(LiveStatus.isLive(snapshot,true,now+91*60_000))
        assertFalse(LiveStatus.isLive(snapshot,true,now-2_000))
    }
}
