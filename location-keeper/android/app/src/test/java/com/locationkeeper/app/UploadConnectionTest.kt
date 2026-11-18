package com.locationkeeper.app

import org.junit.Assert.assertEquals
import org.junit.Test

class UploadConnectionTest {
    private val token = "a".repeat(43)

    @Test fun acceptsHttpsEndpointAndDeviceToken() {
        val connection = UploadConnection("https://project.supabase.co/functions/v1/ingest-location", token)
        assertEquals(token, connection.token)
    }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsCleartextEndpoint() { UploadConnection("http://example.com/location", token) }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsCredentialsInsideUrl() { UploadConnection("https://user:password@example.com/location", token) }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsCredentialsInQueryString() { UploadConnection("https://example.com/location?token=secret", token) }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsHeaderInjectionInToken() { UploadConnection("https://example.com/location", "$token\r\nother: header") }
}

