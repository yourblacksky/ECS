package io.github.yourblacksky.ecs

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

object EcsSecrets {
    const val LLM_API_KEY = "llm.api_key"
    const val ADMIN_TOKEN = "admin.token"

    private const val KEY_ALIAS = "ecs.mobile.secrets"
    private const val PREFERENCES = "ecs.encrypted.secrets"

    fun apiKey(context: Context): String = get(context, LLM_API_KEY).orEmpty()

    fun setApiKey(context: Context, value: String) {
        if (value.isBlank()) delete(context, LLM_API_KEY) else set(context, LLM_API_KEY, value.trim())
    }

    fun adminToken(context: Context): String {
        get(context, ADMIN_TOKEN)?.let { return it }
        val bytes = ByteArray(32).also(SecureRandom()::nextBytes)
        val token = Base64.encodeToString(bytes, Base64.NO_WRAP or Base64.URL_SAFE or Base64.NO_PADDING)
        set(context, ADMIN_TOKEN, token)
        return token
    }

    fun importAdminTokenIfMissing(context: Context, value: String) {
        if (value.isNotBlank() && get(context, ADMIN_TOKEN).isNullOrBlank()) {
            set(context, ADMIN_TOKEN, value.trim())
        }
    }

    private fun set(context: Context, name: String, value: String) {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, secretKey())
        val encoded = cipher.doFinal(value.toByteArray(Charsets.UTF_8))
        val document = listOf(cipher.iv, encoded).joinToString(".") {
            Base64.encodeToString(it, Base64.NO_WRAP)
        }
        context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
            .edit()
            .putString(name, document)
            .apply()
    }

    private fun get(context: Context, name: String): String? {
        val document = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
            .getString(name, null) ?: return null
        return runCatching {
            val parts = document.split(".", limit = 2)
            require(parts.size == 2)
            val iv = Base64.decode(parts[0], Base64.NO_WRAP)
            val encoded = Base64.decode(parts[1], Base64.NO_WRAP)
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.DECRYPT_MODE, secretKey(), GCMParameterSpec(128, iv))
            String(cipher.doFinal(encoded), Charsets.UTF_8)
        }.getOrNull()
    }

    private fun delete(context: Context, name: String) {
        context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
            .edit()
            .remove(name)
            .apply()
    }

    private fun secretKey(): SecretKey {
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (keyStore.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(
                KeyGenParameterSpec.Builder(
                    KEY_ALIAS,
                    KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
                )
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .build(),
            )
            generateKey()
        }
    }
}
