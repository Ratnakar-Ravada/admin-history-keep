package com.locationkeeper.app

import org.junit.Assert.*
import org.junit.Test

class WorkPolicyTest {
    @Test fun backoffIsBoundedAndClockRollbackCannotLeaveARetryStuck() {
        assertEquals(listOf(1L,2L,4L,8L,15L,15L), (1..6).map { WorkPolicy.backoff(it)/60_000 })
        assertFalse(WorkPolicy.due(120_000,60_000))
        assertTrue(WorkPolicy.due(60_000,60_000))
        assertTrue(WorkPolicy.due(10_000_000,60_000))
    }
}
