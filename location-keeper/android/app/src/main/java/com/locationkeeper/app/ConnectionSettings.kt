package com.locationkeeper.app

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.net.URI
import java.security.KeyStore
import java.security.SecureRandom
import java.util.UUID
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

data class UploadConnection(val endpoint: String, val token: String, val automaticRegistration: Boolean = false) {
    init {
        val uri = URI(endpoint)
        require(uri.scheme.equals("https", ignoreCase = true) && !uri.host.isNullOrBlank()) {
            "Use a full HTTPS endpoint URL."
        }
        require(uri.userInfo == null && uri.fragment == null && uri.rawQuery == null) {
            "The endpoint must not contain a username, query string, or fragment."
        }
        require(token.matches(Regex("[A-Za-z0-9_-]{32,256}"))) {
            "Use the device upload token generated during backend setup."
        }
    }
}

class ConnectionSettings(context: Context) {
    private val prefs = context.getSharedPreferences("connection", Context.MODE_PRIVATE)

    @get:Synchronized
    val deviceId: String
        get() = prefs.getString("device_id", null) ?: UUID.randomUUID().toString().also {
            check(prefs.edit().putString("device_id", it).commit()) { "Could not save the device ID." }
        }

    @Synchronized
    fun load(): UploadConnection? {
        val endpoint = prefs.getString("endpoint", null) ?: run {
            if (prefs.getBoolean("phone_only", false)) return null
            val token = Base64.encodeToString(ByteArray(32).also { SecureRandom().nextBytes(it) },
                Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING)
            return UploadConnection(DEFAULT_ENDPOINT, token, automaticRegistration = true).also { save(it) }
        }
        val encoded = prefs.getString("token_encrypted", null) ?: return null
        val parts = encoded.split(":")
        check(parts.size == 2) { "Upload token needs to be entered again." }
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(
            Cipher.DECRYPT_MODE,
            secretKey(),
            GCMParameterSpec(128, Base64.decode(parts[0], Base64.NO_WRAP)),
        )
        val token = String(cipher.doFinal(Base64.decode(parts[1], Base64.NO_WRAP)), Charsets.UTF_8)
        return UploadConnection(endpoint, token, prefs.getBoolean("automatic_registration",
            prefs.getBoolean("requires_approval", false)))
    }

    val storeName: String get() = prefs.getString("store_name", "locations.db") ?: "locations.db"

    @Synchronized
    fun save(connection: UploadConnection, freshStoreName: String? = null) {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, secretKey())
        val encoded = Base64.encodeToString(cipher.iv, Base64.NO_WRAP) + ":" +
            Base64.encodeToString(cipher.doFinal(connection.token.toByteArray(Charsets.UTF_8)), Base64.NO_WRAP)
        check(prefs.edit().putString("endpoint", connection.endpoint)
            .putString("token_encrypted", encoded)
            .putBoolean("automatic_registration", connection.automaticRegistration)
            .remove("requires_approval").putBoolean("phone_only", false).apply {
                if (freshStoreName != null) putString("store_name", freshStoreName)
            }.commit()) { "Could not save the connection." }
    }

    fun endpoint(): String = prefs.getString("endpoint", null)
        ?: if (prefs.getBoolean("phone_only", false)) "" else DEFAULT_ENDPOINT

    @Synchronized
    fun clear() {
        check(prefs.edit().remove("endpoint").remove("token_encrypted")
            .remove("requires_approval").remove("automatic_registration").putBoolean("phone_only", true).commit())
    }

    @Synchronized
    private fun secretKey(): SecretKey {
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (keyStore.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            ).setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
        }.generateKey()
    }

    companion object {
        const val DEFAULT_ENDPOINT = "https://location-keeper-tidb-api.ratnakarravada11.workers.dev/ingest-location"
        private const val KEY_ALIAS = "location_keeper_upload_token_v1"
    }
}

