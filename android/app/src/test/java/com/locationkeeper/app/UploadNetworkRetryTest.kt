package com.locationkeeper.app

import org.junit.Assert.*
import org.junit.Test
import java.io.IOException
import javax.net.ssl.SSLException

class UploadNetworkRetryTest {
    @Test fun successfulDefaultNetworkDoesNotRequestMobileData() {
        var acquired = 0
        val retry = UploadNetworkRetry({ true }) {
            acquired++
            UploadNetworkLease("cellular") {}
        }
        assertEquals("saved", retry.run { assertNull(it); "saved" })
        assertEquals(0, acquired)
    }

    @Test fun wifiTlsFailureRetriesSameOperationOnceAndReleasesMobileNetwork() {
        var released = 0
        val routes = mutableListOf<String?>()
        val eventId = "same-event"
        val sentIds = mutableListOf<String>()
        val retry = UploadNetworkRetry({ true }) { UploadNetworkLease("cellular") { released++ } }
        val result = retry.run { network ->
            routes += network
            sentIds += eventId
            if (network == null) throw SSLException("Wi-Fi TLS failed")
            "receipt"
        }
        assertEquals("receipt", result)
        assertEquals(listOf(null, "cellular"), routes)
        assertEquals(listOf(eventId, eventId), sentIds)
        assertEquals(1, released)
    }

    @Test fun deniedFallbackPreservesOriginalFailureWithoutAcquiringNetwork() {
        val failure = SSLException("TLS failed")
        val retry = UploadNetworkRetry<String>({ false }) { error("Must not request mobile data") }
        try {
            retry.run<String> { throw failure }
            fail("Expected the original failure")
        } catch (error: SSLException) { assertSame(failure, error) }
    }

    @Test fun httpOrOtherIoFailuresDoNotTriggerMobileFallback() {
        val failure = IOException("Upload token rejected")
        val retry = UploadNetworkRetry<String>({ true }) { error("Must not request mobile data") }
        try {
            retry.run<String> { throw failure }
            fail("Expected the original failure")
        } catch (error: IOException) { assertSame(failure, error) }
    }

    @Test fun failedMobileRetryStillReleasesNetworkAndDoesNotRetryAgain() {
        var calls = 0
        var released = 0
        val retry = UploadNetworkRetry({ true }) { UploadNetworkLease("cellular") { released++ } }
        try {
            retry.run<String> {
                calls++
                throw SSLException("TLS failed")
            }
            fail("Expected a failure")
        } catch (_: SSLException) {}
        assertEquals(2, calls)
        assertEquals(1, released)
    }

    @Test fun unavailableMobileNetworkIsReportedWithoutAnotherUploadAttempt() {
        var calls = 0
        val unavailable = IOException("Mobile data unavailable")
        val retry = UploadNetworkRetry<String>({ true }) { throw unavailable }
        try {
            retry.run<String> { calls++; throw SSLException("Wi-Fi TLS failed") }
            fail("Expected a failure")
        } catch (error: IOException) { assertSame(unavailable, error) }
        assertEquals(1, calls)
    }
}
