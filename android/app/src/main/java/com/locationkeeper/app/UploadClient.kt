package com.locationkeeper.app

import android.net.Network
import org.json.JSONObject
import java.io.IOException
import java.net.HttpURLConnection
import java.net.SocketTimeoutException
import java.net.URL
import java.time.Instant

object UploadProtocol {
    fun payload(sample: LocationSample): JSONObject = JSONObject().apply {
        put("event_id", sample.eventId)
        put("device_id", sample.deviceId)
        put("captured_at", sample.capturedAt ?: JSONObject.NULL)
        put("latitude", sample.latitude ?: JSONObject.NULL)
        put("longitude", sample.longitude ?: JSONObject.NULL)
        put("accuracy_m", sample.accuracyM ?: JSONObject.NULL)
        put("battery_percent", sample.batteryPercent ?: JSONObject.NULL)
        put("is_mock", sample.isMock)
        put("permission_accuracy", sample.permissionAccuracy)
        put("fix_age_ms", sample.fixAgeMs ?: JSONObject.NULL)
        put("recorded_at", sample.recordedAt)
        put("reason", sample.reason)
        put("scheduled_for", sample.scheduledFor ?: JSONObject.NULL)
        put("accuracy_mode", sample.accuracyMode)
        put("location_status", sample.locationStatus)
        put("network_transport", sample.networkTransport)
        put("executed_at", sample.executedAt ?: JSONObject.NULL)
        put("execution_status", sample.executionStatus ?: JSONObject.NULL)
        // Omit absent metadata so retries of pre-upgrade stored events keep their exact projection.
        sample.airplaneModeEnabled?.let { put("airplane_mode_enabled", it) }
    }

    /** A 2xx response alone is not proof that this particular reading was stored. */
    fun acknowledgedAt(body: String, eventId: String): String {
        val json = JSONObject(body)
        require(json.getBoolean("saved") && json.getString("event_id") == eventId) {
            "The server did not confirm this reading."
        }
        return json.getString("received_at").also { Instant.parse(it) }
    }
}

class UploadClient(
    private val profile: DeviceProfile? = null,
    private val networkRetry: UploadNetworkRetry<Network>? = null,
    private val onConnectionResult: ((Boolean) -> Unit)? = null,
    private val onUploadTiming: ((String, Long, Long?) -> Unit)? = null,
    private val transport: ((UploadConnection, JSONObject, Boolean, Network?) -> String)? = null,
) {
    private val registeredDevices = java.util.concurrent.ConcurrentHashMap.newKeySet<Pair<UploadConnection,String>>()

    /** Registration uses this installation's credential and never needs an approval step. */
    fun registerDevice(connection: UploadConnection, deviceId: String) =
        withNetwork { registerOnNetwork(connection, deviceId, it) }

    @Synchronized
    private fun registerOnNetwork(connection: UploadConnection, deviceId: String, network: Network?) {
        if (!connection.automaticRegistration) return
        val identity = connection to deviceId
        if (identity in registeredDevices) return
        check(connection.endpoint == ConnectionSettings.DEFAULT_ENDPOINT) { "Unexpected automatic endpoint." }
        check(profile?.complete == true) { "Finish device naming first." }
        val registration = connection.copy(endpoint = URL(URL(connection.endpoint), "/request-device").toString())
        val response = JSONObject(post(registration,
            profile.addTo(JSONObject().put("device_id", deviceId)), registration = true, network = network))
        // "approved" is the wire value used by older APKs; the server now registers automatically.
        check(response.getString("device_id") == deviceId && response.getString("state") in
            setOf("approved", "registered")) { "Automatic registration could not be confirmed. Try again when online." }
        registeredDevices.add(identity)
        reportConnection(true)
    }

    fun syncProfile(connection: UploadConnection, deviceId: String) =
        withNetwork { syncProfileOnNetwork(connection, deviceId, it) }
            .also { if (profile?.complete == true) reportConnection(true) }

    private fun syncProfileOnNetwork(connection: UploadConnection, deviceId: String, network: Network?) {
        if (profile?.complete != true) return
        registerOnNetwork(connection, deviceId, network)
        val body = post(connection, profile.addTo(JSONObject().put("type", "device_profile").put("device_id", deviceId)),
            network = network)
        check(JSONObject(body).getBoolean("saved")) { "Device name upload was not confirmed." }
    }

    fun send(connection: UploadConnection, sample: LocationSample): String = withNetwork { network ->
        // The same immutable event ID is retried if Wi-Fi TLS fails partway through an operation.
        fun insert()=UploadProtocol.acknowledgedAt(post(connection,UploadProtocol.payload(sample),network=network),sample.eventId)
        try {
            insert().also { registeredDevices.add(connection to sample.deviceId) }
        } catch(error: UploadHttpException) {
            if(error.status!=401 || !connection.automaticRegistration) throw error
            // A fresh installation can register and retry the same immutable event immediately.
            registerOnNetwork(connection,sample.deviceId,network)
            insert()
        }
    }

    private fun reportConnection(success: Boolean) { runCatching { onConnectionResult?.invoke(success) } }

    private fun <T> withNetwork(request: (Network?) -> T): T = try {
        if (networkRetry != null) networkRetry.run(request) else request(null)
    } catch (error: Exception) {
        reportConnection(false)
        throw error
    }

    private fun post(connection: UploadConnection, payload: JSONObject, registration: Boolean = false,
        network: Network? = null): String {
        transport?.let { return it(connection,payload,registration,network) }
        val url = URL(connection.endpoint)
        val request = (network?.openConnection(url) ?: url.openConnection()) as HttpURLConnection
        val started=System.nanoTime()
        var serverMs: Long?=null
        try {
            request.requestMethod = "POST"
            request.instanceFollowRedirects = false
            request.connectTimeout = 12_000
            request.readTimeout = 15_000
            request.doOutput = true
            request.setRequestProperty("Content-Type", "application/json; charset=utf-8")
            request.setRequestProperty("Accept", "application/json")
            request.setRequestProperty("Authorization", "Bearer ${connection.token}")
            val bytes = payload.toString().toByteArray(Charsets.UTF_8)
            request.setFixedLengthStreamingMode(bytes.size)
            request.outputStream.use { it.write(bytes) }
            val status = request.responseCode
            serverMs=Regex("tidb;dur=([0-9.]+)").find(request.getHeaderField("Server-Timing").orEmpty())
                ?.groupValues?.get(1)?.toDoubleOrNull()?.toLong()
            if (status !in 200..299) {
                throw UploadHttpException(status,when (status) {
                    401, 403 -> "Upload token was rejected. Check the database connection."
                    409 -> if (registration) "This device ID is already in use by another installation. Your readings remain saved on this phone."
                        else "The server rejected a conflicting reading ID."
                    429 -> "The server is busy. Try sending pending readings later."
                    in 300..399 -> "Endpoint redirected the request. Use the final HTTPS endpoint."
                    else -> "Server returned HTTP $status."
                })
            }
            val body = request.inputStream.use { input ->
                val buffer = ByteArray(16_385)
                var count = 0
                while (count < buffer.size) {
                    val read = input.read(buffer, count, buffer.size - count)
                    if (read < 0) break
                    count += read
                }
                check(count <= 16_384) { "Server response was too large." }
                String(buffer, 0, count, Charsets.UTF_8)
            }
            return body
        } finally {
            if(payload.has("event_id")) runCatching { onUploadTiming?.invoke(payload.getString("event_id"),(System.nanoTime()-started)/1_000_000,serverMs) }
            request.disconnect()
        }
    }

    internal class UploadHttpException(val status: Int,message: String): IOException(message)

    companion object {
        fun friendlyError(error: Exception): String = when (error) {
            is SocketTimeoutException -> "Connection timed out. Your reading is still saved on this phone."
            is java.net.UnknownHostException -> "Cannot reach the server. Check the internet connection."
            is javax.net.ssl.SSLException -> "HTTPS connection failed on this network. Complete any Wi-Fi sign-in or try mobile data. Your readings remain saved on this phone."
            is IOException -> error.message ?: "Upload failed. Your readings remain saved on this phone."
            else -> "The server response could not be confirmed. Your reading is still pending."
        }
    }
}

