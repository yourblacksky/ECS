package io.github.yourblacksky.ecs

import android.content.Context
import android.net.ConnectivityManager
import org.json.JSONObject
import java.io.File
import java.net.Inet4Address

data class EcsSettings(
    val baseUrl: String,
    val model: String,
    val distributed: Boolean,
    val autoStart: Boolean,
) {
    companion object {
        private const val PREFERENCES = "ecs.settings"
        private const val DEEPSEEK_BASE_URL = "https://api.deepseek.com"
        private const val LEGACY_DEEPSEEK_MODEL = "deepseek-chat"
        private const val DEFAULT_DEEPSEEK_MODEL = "deepseek-v4-flash"

        fun load(context: Context): EcsSettings {
            val preferences = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
            val baseUrl = preferences.getString("base_url", DEEPSEEK_BASE_URL)
                ?: DEEPSEEK_BASE_URL
            val storedModel = preferences.getString("model", DEFAULT_DEEPSEEK_MODEL)
                ?: DEFAULT_DEEPSEEK_MODEL
            val model = migrateLegacyDeepSeekModel(baseUrl, storedModel)
            if (model != storedModel) {
                preferences.edit().putString("model", model).apply()
            }
            return EcsSettings(
                baseUrl = baseUrl,
                model = model,
                distributed = preferences.getBoolean("distributed", true),
                autoStart = preferences.getBoolean("auto_start", false),
            )
        }

        internal fun migrateLegacyDeepSeekModel(baseUrl: String, model: String): String {
            val officialProvider = baseUrl.trim().trimEnd('/').equals(
                DEEPSEEK_BASE_URL,
                ignoreCase = true,
            )
            return if (officialProvider && model.trim() == LEGACY_DEEPSEEK_MODEL) {
                DEFAULT_DEEPSEEK_MODEL
            } else {
                model
            }
        }

        fun save(context: Context, settings: EcsSettings) {
            context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
                .edit()
                .putString("base_url", settings.baseUrl.trim())
                .putString("model", settings.model.trim())
                .putBoolean("distributed", settings.distributed)
                .putBoolean("auto_start", settings.autoStart)
                .apply()
        }
    }
}

data class EcsRuntimeConfiguration(
    val configJSON: String,
    val secretsJSON: String,
    val nodeName: String,
    val port: Int,
    val distributed: Boolean,
)

object EcsRuntimeConfig {
    fun create(context: Context, sourceFile: File): EcsRuntimeConfiguration {
        val settings = EcsSettings.load(context)
        val storedDocument = JSONObject(sourceFile.readText())
        val storedLlm = storedDocument.getJSONObject("llm")

        // Move credentials from test builds into Keystore and remove the old plaintext.
        val legacyKey = storedLlm.optString("api_key")
        val legacyAdminToken = storedDocument.optString("admin_token")
        if (legacyKey.isNotBlank() && EcsSecrets.apiKey(context).isBlank()) {
            EcsSecrets.setApiKey(context, legacyKey)
        }
        EcsSecrets.importAdminTokenIfMissing(context, legacyAdminToken)
        if (legacyKey.isNotBlank() || legacyAdminToken.isNotBlank()) {
            storedLlm.put("api_key", "")
            storedLlm.put("api_key_secret", EcsSecrets.LLM_API_KEY)
            storedDocument.put("admin_token", "")
            storedDocument.put("admin_token_secret", EcsSecrets.ADMIN_TOKEN)
            writeAtomic(sourceFile, storedDocument.toString(2))
        }

        // Runtime settings are overlaid in memory; the user's base config remains editable.
        val document = JSONObject(storedDocument.toString())
        val llm = document.getJSONObject("llm")
        llm.put("base_url", settings.baseUrl)
        llm.put("model", settings.model)
        llm.put("api_key", "")
        llm.put("api_key_secret", EcsSecrets.LLM_API_KEY)
        document.put("admin_token", "")
        document.put("admin_token_secret", EcsSecrets.ADMIN_TOKEN)

        val port = 18090
        val discovery = document.getJSONObject("discovery")
        val network = document.getJSONObject("network_auth")
        val parent = network.getJSONObject("parent")
        if (settings.distributed) {
            val address = localIPv4(context) ?: "127.0.0.1"
            document.put("listen", "0.0.0.0:$port")
            discovery.put("enabled", true)
            discovery.put("advertise_url", "http://$address:$port")
            network.put("enabled", true)
            parent.put("auto_discover", true)
        } else {
            document.put("listen", "127.0.0.1:$port")
            discovery.put("enabled", false)
            discovery.put("advertise_url", "http://127.0.0.1:$port")
            network.put("enabled", false)
            parent.put("auto_discover", false)
        }

        val secrets = JSONObject()
            .put(EcsSecrets.ADMIN_TOKEN, EcsSecrets.adminToken(context))
        EcsSecrets.apiKey(context).takeIf(String::isNotBlank)?.let {
            secrets.put(EcsSecrets.LLM_API_KEY, it)
        }
        return EcsRuntimeConfiguration(
            configJSON = document.toString(),
            secretsJSON = secrets.toString(),
            nodeName = document.optString("node_name", "ecs-android"),
            port = port,
            distributed = settings.distributed,
        )
    }

    private fun writeAtomic(target: File, content: String) {
        val temporary = File(target.parentFile, ".${target.name}.tmp")
        temporary.writeText(content)
        if (!temporary.renameTo(target)) {
            temporary.copyTo(target, overwrite = true)
            temporary.delete()
        }
    }

    fun localIPv4(context: Context): String? {
        val manager = context.getSystemService(ConnectivityManager::class.java)
        val network = manager.activeNetwork ?: return null
        return manager.getLinkProperties(network)?.linkAddresses
            ?.map { it.address }
            ?.filterIsInstance<Inet4Address>()
            ?.firstOrNull { !it.isLoopbackAddress }
            ?.hostAddress
    }
}
