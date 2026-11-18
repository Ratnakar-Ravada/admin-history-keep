package com.locationkeeper.app

import android.content.pm.ApplicationInfo
import android.os.Bundle
import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.net.URI
import java.security.MessageDigest
import java.security.SecureRandom

/** Owner-operated debug setup. Only public enrollment details are returned to the host. */
@RunWith(AndroidJUnit4::class)
class CloudConnectionSetupTest {
    @Test fun verifyCurrentConnection() {
        val args = InstrumentationRegistry.getArguments()
        assumeTrue("Network verification requires explicit owner setup arguments.", args.getString("setup_confirmed") == "true")
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val app = context.applicationContext as LocationKeeperApplication
        val result = Bundle()
        result.putString("endpoint", app.connectionSettings.endpoint())
        result.putString("tracking_status", TrackingSettings(context).status)
        result.putBoolean("profile_complete", DeviceProfile(context).complete)
        val counts = app.store.counts()
        result.putInt("saved_count", counts.first)
        result.putInt("pending_count", counts.second)
        try {
            val connection = checkNotNull(app.connectionSettings.load())
            app.uploadClient.syncProfile(connection, app.connectionSettings.deviceId)
            result.putString("profile_sync", "confirmed")
            if (args.getString("send_pending") == "true") {
                var sent = 0
                for (sample in app.store.pending(100)) {
                    val receipt = app.uploadClient.send(connection, sample)
                    app.store.markUploaded(sample.eventId, receipt)
                    sent++
                }
                result.putInt("uploaded_count", sent)
                result.putInt("pending_count", app.store.counts().second)
            }
        } catch (error: Exception) {
            result.putString("profile_sync", UploadClient.friendlyError(error))
        }
        InstrumentationRegistry.getInstrumentation().sendStatus(0, result)
    }

    @Test fun prepareConnection() {
        val arguments = InstrumentationRegistry.getArguments()
        val endpoint = arguments.getString("upload_endpoint")
        assumeTrue(
            "Cloud setup requires explicit instrumentation arguments.",
            (arguments.getString("setup_confirmed") == "true") && !endpoint.isNullOrBlank(),
        )
        checkNotNull(endpoint) { "An upload endpoint is required." }

        val context = InstrumentationRegistry.getInstrumentation().targetContext
        check((context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE) != 0) {
            "Cloud setup is available only for a debuggable target app."
        }
        val freshD1 = arguments.getString("start_fresh_d1") == "true"
        val freshTiDB = arguments.getString("start_fresh_tidb") == "true"
        val prepareOnly = arguments.getString("prepare_only") == "true"
        val tidbEndpoint = "https://location-keeper-tidb-api.ratnakarravada11.workers.dev/ingest-location"
        val d1Endpoint = "https://location-keeper-api.ratnakarravada11.workers.dev/ingest-location"
        val uri = runCatching { URI(endpoint) }.getOrNull()
        check((freshD1 && endpoint == d1Endpoint) || (freshTiDB && endpoint == tidbEndpoint) || (uri != null && uri.scheme == "https" && !uri.isOpaque &&
            uri.rawAuthority?.matches(Regex("[a-z0-9]{20}\\.supabase\\.co")) == true &&
            uri.port == -1 && uri.rawUserInfo == null && uri.rawQuery == null &&
            uri.rawFragment == null && uri.rawPath == "/functions/v1/ingest-location")) {
            "Use the exact configured HTTPS ingestion endpoint without extra URL components."
        }

        val settings = ConnectionSettings(context)
        val currentEndpoint = settings.endpoint()
        check(currentEndpoint.isBlank() || currentEndpoint == endpoint || freshD1 || freshTiDB) {
            "A different database connection already exists. Review it in the app first."
        }
        val existing = try { settings.load() } catch (_: Exception) {
            throw AssertionError("The existing database credential could not be read. Review it in the app.")
        }
        check(currentEndpoint.isBlank() || existing != null) {
            "The existing database connection is incomplete. Review it in the app first."
        }
        val connection = existing?.copy(endpoint = endpoint) ?: UploadConnection(endpoint,
            Base64.encodeToString(ByteArray(32).also { SecureRandom().nextBytes(it) },
                Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING))
        // Enrollment can inspect the credential hash before committing a backend switch.
        if (!prepareOnly || existing == null) settings.save(connection, when {
            freshTiDB -> "locations-tidb.db"
            freshD1 -> "locations-d1.db"
            else -> null
        })
        val digest = MessageDigest.getInstance("SHA-256")
            .digest(connection.token.toByteArray(Charsets.UTF_8))
        val alphabet = "0123456789abcdef"
        val hash = buildString(64) {
            digest.forEach { byte ->
                val value = byte.toInt() and 0xff
                append(alphabet[value ushr 4])
                append(alphabet[value and 0x0f])
            }
        }
        InstrumentationRegistry.getInstrumentation().sendStatus(0, Bundle().apply {
            putString("device_id", settings.deviceId)
            putString("upload_token_sha256", hash)
            putString("endpoint", connection.endpoint)
        })
    }
}
