package io.github.yourblacksky.ecs

import android.content.Context
import org.json.JSONObject
import java.io.File
import java.security.MessageDigest

object EcsAssets {
    private const val PREFERENCES = "ecs.assets"
    private const val HASHES = "managed_hashes"
    private val managedRoots = listOf(
        "prompts",
        "agents.d",
        "pipelines.d",
        "tools.d",
        "skills.d",
        "office.d",
    )

    fun prepare(context: Context): File {
        val root = File(context.filesDir, "ecs")
        root.mkdirs()
        copyConfigIfMissing(context, File(root, "config.json"))
        val preferences = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
        val previous = runCatching {
            JSONObject(preferences.getString(HASHES, "{}") ?: "{}")
        }.getOrDefault(JSONObject())
        val current = JSONObject()
        managedRoots.forEach { asset ->
            copyManaged(context, asset, File(root, asset), previous, current)
        }
        preferences.edit().putString(HASHES, current.toString()).apply()
        return root
    }

    private fun copyConfigIfMissing(context: Context, target: File) {
        if (target.exists()) return
        writeAtomic(target, context.assets.open("config.json").use { it.readBytes() })
    }

    private fun copyManaged(
        context: Context,
        assetPath: String,
        target: File,
        previous: JSONObject,
        current: JSONObject,
    ) {
        val children = context.assets.list(assetPath).orEmpty()
        if (children.isNotEmpty()) {
            target.mkdirs()
            children.forEach { child ->
                copyManaged(context, "$assetPath/$child", File(target, child), previous, current)
            }
            return
        }
        val packaged = context.assets.open(assetPath).use { it.readBytes() }
        val packagedHash = sha256(packaged)
        val previousHash = previous.optString(assetPath)
        val targetHash = target.takeIf(File::isFile)?.readBytes()?.let(::sha256)
        if (!target.exists() || (previousHash.isNotEmpty() && targetHash == previousHash)) {
            if (targetHash != packagedHash) writeAtomic(target, packaged)
        }
        current.put(assetPath, packagedHash)
    }

    private fun writeAtomic(target: File, bytes: ByteArray) {
        target.parentFile?.mkdirs()
        val temporary = File(target.parentFile, ".${target.name}.tmp")
        temporary.outputStream().use { it.write(bytes) }
        if (!temporary.renameTo(target)) {
            temporary.copyTo(target, overwrite = true)
            temporary.delete()
        }
    }

    private fun sha256(bytes: ByteArray): String =
        MessageDigest.getInstance("SHA-256")
            .digest(bytes)
            .joinToString("") { "%02x".format(it) }
}
