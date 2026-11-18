package com.locationkeeper.app

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/** Tests the installed manifest; website certificate verification is tested separately. */
@RunWith(AndroidJUnit4::class)
class AppLinksTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()

    @Suppress("DEPRECATION")
    private fun handlers(url: String) = context.packageManager.queryIntentActivities(
        Intent(Intent.ACTION_VIEW, Uri.parse(url))
            .addCategory(Intent.CATEGORY_BROWSABLE)
            .setPackage(context.packageName), 0).map { it.activityInfo.name }

    @Test fun ownedWebLinksOpenTheNormalActivity() {
        for (url in listOf("https://location-keep.rockyworks.in/",
                "https://location-keep.rockyworks.in",
                "http://location-keep.rockyworks.in/",
                "https://location-keep.rockyworks.in/open?tracking=on")) {
            assertEquals(listOf(MainActivity::class.java.name), handlers(url))
        }
    }

    @Test fun unrelatedDomainsAndSchemesAreNotClaimed() {
        for (url in listOf("https://rockyworks.in/",
                "https://location-keep.rockyworks.in.example.com/",
                "https://other.rockyworks.in/",
                "ftp://location-keep.rockyworks.in/",
                "locationkeeper://open")) {
            assertTrue(url, handlers(url).isEmpty())
        }
    }
}
