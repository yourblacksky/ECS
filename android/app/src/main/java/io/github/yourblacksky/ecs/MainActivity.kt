package io.github.yourblacksky.ecs

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.text.InputType
import android.text.method.PasswordTransformationMethod
import android.view.Gravity
import android.view.View
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.Switch
import android.widget.TextView
import android.widget.Toast
import io.github.yourblacksky.ecs.mobile.Mobile
import org.json.JSONObject

class MainActivity : Activity() {
    private lateinit var status: TextView
    private lateinit var webView: WebView
    private val handler = Handler(Looper.getMainLooper())
    private val refreshStatus = object : Runnable {
        override fun run() {
            renderStatus()
            handler.postDelayed(this, 1_000)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        requestRuntimePermissions()
        setContentView(buildContent())
        if (EcsSettings.load(this).autoStart) startEcs()
    }

    override fun onResume() {
        super.onResume()
        handler.post(refreshStatus)
    }

    override fun onPause() {
        handler.removeCallbacks(refreshStatus)
        super.onPause()
    }

    override fun onDestroy() {
        webView.destroy()
        super.onDestroy()
    }

    private fun buildContent(): View {
        val padding = dp(20)
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(padding, padding, padding, padding)
            setBackgroundColor(COLOR_ABYSS)
        }
        val eyebrow = TextView(this).apply {
            text = "DEVICE EXECUTION NODE"
            textSize = 10f
            letterSpacing = .16f
            setTextColor(COLOR_MUTED)
            typeface = Typeface.MONOSPACE
        }
        val title = TextView(this).apply {
            text = "ECS · Android"
            textSize = 30f
            setTextColor(COLOR_TEXT)
            typeface = Typeface.create("sans-serif-rounded", Typeface.BOLD)
            setPadding(0, dp(6), 0, dp(4))
        }
        val subtitle = TextView(this).apply {
            text = "设备能力通过本机节点接入 ECS 网络"
            textSize = 13f
            setTextColor(COLOR_MUTED)
            setPadding(0, 0, 0, dp(18))
        }
        status = TextView(this).apply {
            id = R.id.ecs_status
            textSize = 14f
            setTextColor(COLOR_WARN)
            typeface = Typeface.create("sans-serif-medium", Typeface.NORMAL)
            setPadding(dp(16), dp(15), dp(16), dp(15))
            background = panelBackground(COLOR_PANEL)
        }
        val flow = TextView(this).apply {
            text = "DEVICE  ●━━━━●  ECS  ●━━━━●  NETWORK"
            textSize = 10f
            letterSpacing = .05f
            typeface = Typeface.MONOSPACE
            setTextColor(COLOR_FLOW)
            gravity = Gravity.CENTER
            setPadding(0, dp(15), 0, dp(15))
        }
        val actions = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER
        }
        listOf(
            button(R.id.ecs_start, "启动节点", true) { startEcs() },
            button(R.id.ecs_stop, "停止节点") { stopEcs() },
            button(R.id.ecs_open_admin, "进入控制台") { openAdmin() },
        ).forEachIndexed { index, action ->
            actions.addView(
                action,
                LinearLayout.LayoutParams(0, dp(48), 1f).apply {
                    if (index > 0) marginStart = dp(8)
                },
            )
        }
        val settingsAction = button(R.id.ecs_node_settings, "节点与模型配置") { showSettings() }
        webView = WebView(this).apply {
            id = R.id.ecs_admin_webview
            visibility = View.GONE
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            webViewClient = object : WebViewClient() {
                override fun onPageFinished(view: WebView, url: String) {
                    val token = JSONObject.quote(EcsSecrets.adminToken(this@MainActivity))
                    view.evaluateJavascript(
                        """
                        if (sessionStorage.getItem('ecs-admin-token') !== $token) {
                          sessionStorage.setItem('ecs-admin-token', $token);
                          location.reload();
                        }
                        """.trimIndent(),
                        null,
                    )
                }
            }
        }
        root.addView(eyebrow)
        root.addView(title)
        root.addView(subtitle)
        root.addView(status)
        root.addView(flow)
        root.addView(actions)
        root.addView(
            settingsAction,
            LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                dp(48),
            ).apply { topMargin = dp(9) },
        )
        root.addView(
            webView,
            LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                0,
                1f,
            ),
        )
        return root
    }

    private fun button(
        id: Int,
        label: String,
        primary: Boolean = false,
        action: () -> Unit,
    ): Button =
        Button(this).apply {
            this.id = id
            text = label
            isAllCaps = false
            textSize = 13f
            typeface = Typeface.create("sans-serif-medium", Typeface.NORMAL)
            setTextColor(if (primary) COLOR_ABYSS else COLOR_TEXT)
            background = buttonBackground(primary)
            stateListAnimator = null
            minHeight = 0
            minWidth = 0
            setPadding(dp(10), 0, dp(10), 0)
            setOnClickListener { action() }
        }

    private fun panelBackground(color: Int) = GradientDrawable().apply {
        shape = GradientDrawable.RECTANGLE
        cornerRadius = dp(18).toFloat()
        setColor(color)
        setStroke(dp(1), COLOR_LINE)
    }

    private fun buttonBackground(primary: Boolean) = GradientDrawable().apply {
        shape = GradientDrawable.RECTANGLE
        cornerRadius = dp(14).toFloat()
        setColor(if (primary) COLOR_FLOW else COLOR_PANEL_2)
        setStroke(dp(1), if (primary) COLOR_FLOW else COLOR_LINE)
    }

    private fun dp(value: Int) = (value * resources.displayMetrics.density).toInt()

    private fun startEcs() {
        val intent = Intent(this, EcsService::class.java).setAction(EcsService.ACTION_START)
        startForegroundService(intent)
    }

    private fun stopEcs() {
        startService(Intent(this, EcsService::class.java).setAction(EcsService.ACTION_STOP))
    }

    private fun openAdmin() {
        startEcs()
        webView.visibility = View.VISIBLE
        handler.postDelayed({ webView.loadUrl(ADMIN_URL) }, 800)
    }

    private fun showSettings() {
        val current = EcsSettings.load(this)
        val density = resources.displayMetrics.density
        val padding = (18 * density).toInt()
        val form = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(padding, padding / 2, padding, 0)
        }
        val baseUrl = EditText(this).apply {
            id = R.id.settings_base_url
            hint = "LLM Base URL"
            setText(current.baseUrl)
            setSingleLine()
        }
        val model = EditText(this).apply {
            id = R.id.settings_model
            hint = "模型名称"
            setText(current.model)
            setSingleLine()
        }
        val apiKey = EditText(this).apply {
            id = R.id.settings_api_key
            hint = if (EcsSecrets.apiKey(this@MainActivity).isBlank()) {
                "API Key"
            } else {
                "API Key（留空保持不变）"
            }
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            transformationMethod = PasswordTransformationMethod.getInstance()
            importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
            setSingleLine()
        }
        val distributed = Switch(this).apply {
            id = R.id.settings_distributed
            text = "局域网 ECS：自动发现并申请加入"
            isChecked = current.distributed
        }
        val autoStart = Switch(this).apply {
            id = R.id.settings_auto_start
            text = "应用启动时自动启动 ECS"
            isChecked = current.autoStart
        }
        val copyAdminToken = Button(this).apply {
            id = R.id.settings_copy_admin_token
            text = "复制管理凭证"
            setOnClickListener {
                getSystemService(ClipboardManager::class.java).setPrimaryClip(
                    ClipData.newPlainText(
                        "ECS Admin Token",
                        EcsSecrets.adminToken(this@MainActivity),
                    ),
                )
                text = "已复制"
                handler.postDelayed({ text = "复制管理凭证" }, 1_500)
                Toast.makeText(this@MainActivity, "管理凭证已复制", Toast.LENGTH_SHORT).show()
            }
        }
        val brightnessPermission = Button(this).apply {
            id = R.id.settings_brightness_permission
            text = if (Settings.System.canWrite(this@MainActivity)) {
                "屏幕亮度控制已授权"
            } else {
                "授权屏幕亮度控制"
            }
            setOnClickListener {
                startActivity(
                    Intent(
                        Settings.ACTION_MANAGE_WRITE_SETTINGS,
                        Uri.parse("package:$packageName"),
                    ),
                )
            }
        }
        listOf(
            baseUrl,
            model,
            apiKey,
            distributed,
            autoStart,
            copyAdminToken,
            brightnessPermission,
        ).forEach(form::addView)
        AlertDialog.Builder(this)
            .setTitle("ECS 节点配置")
            .setView(form)
            .setNegativeButton("取消", null)
            .setNeutralButton("清除 API Key") { _, _ ->
                EcsSecrets.setApiKey(this, "")
                Toast.makeText(this, "API Key 已清除", Toast.LENGTH_SHORT).show()
                restartEcs()
            }
            .setPositiveButton("保存") { _, _ ->
                EcsSettings.save(
                    this,
                    EcsSettings(
                        baseUrl = baseUrl.text.toString(),
                        model = model.text.toString(),
                        distributed = distributed.isChecked,
                        autoStart = autoStart.isChecked,
                    ),
                )
                apiKey.text.toString().takeIf(String::isNotBlank)?.let {
                    EcsSecrets.setApiKey(this, it)
                }
                Toast.makeText(this, "配置已保存，正在重启 ECS", Toast.LENGTH_SHORT).show()
                restartEcs()
            }
            .show()
    }

    private fun restartEcs() {
        stopEcs()
        handler.postDelayed({ startEcs() }, 700)
    }

    private fun renderStatus() {
        val state = runCatching { JSONObject(Mobile.statusJSON()) }.getOrNull()
        val running = state?.optBoolean("running") == true
        val node = state?.optString("node").orEmpty()
        val tools = state?.optInt("tools") ?: 0
        val skills = state?.optInt("skills") ?: 0
        status.text = if (running) {
            "● 运行中\n$node  ·  $tools Tool / $skills Skill\n$ADMIN_URL"
        } else {
            "○ 已停止\n节点服务尚未监听，点击“启动节点”开始工作"
        }
        status.setTextColor(if (running) COLOR_FLOW else COLOR_WARN)
    }

    private fun requestRuntimePermissions() {
        val requested = mutableListOf(
            Manifest.permission.CAMERA,
            Manifest.permission.ACCESS_COARSE_LOCATION,
            Manifest.permission.ACCESS_FINE_LOCATION,
        )
        if (Build.VERSION.SDK_INT >= 33) {
            requested += listOf(
                Manifest.permission.POST_NOTIFICATIONS,
                Manifest.permission.NEARBY_WIFI_DEVICES,
            )
        }
        val missing = requested.filter {
            checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED
        }
        if (missing.isNotEmpty()) {
            requestPermissions(missing.toTypedArray(), 100)
        }
    }

    companion object {
        private const val ADMIN_URL = "http://127.0.0.1:18090/admin/"
        private val COLOR_ABYSS = Color.rgb(7, 18, 37)
        private val COLOR_PANEL = Color.rgb(16, 39, 67)
        private val COLOR_PANEL_2 = Color.rgb(21, 51, 83)
        private val COLOR_LINE = Color.rgb(49, 81, 110)
        private val COLOR_TEXT = Color.rgb(244, 248, 247)
        private val COLOR_MUTED = Color.rgb(155, 176, 194)
        private val COLOR_FLOW = Color.rgb(71, 226, 206)
        private val COLOR_WARN = Color.rgb(243, 201, 105)
    }
}
