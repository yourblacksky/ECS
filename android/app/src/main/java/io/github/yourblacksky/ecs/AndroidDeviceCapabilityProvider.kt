package io.github.yourblacksky.ecs

import android.Manifest
import android.annotation.SuppressLint
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.hardware.camera2.CameraCharacteristics
import android.hardware.camera2.CameraManager
import android.location.Location
import android.location.LocationListener
import android.location.LocationManager
import android.media.AudioManager
import android.media.ToneGenerator
import android.os.Build
import android.os.Bundle
import android.os.CancellationSignal
import android.os.Handler
import android.os.Looper
import android.os.VibrationEffect
import android.os.Vibrator
import android.os.VibratorManager
import android.provider.Settings
import io.github.yourblacksky.ecs.mobile.DeviceCapabilityProvider
import org.json.JSONObject
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

class AndroidDeviceCapabilityProvider(
    context: Context,
) : DeviceCapabilityProvider, AutoCloseable {
    private val appContext = context.applicationContext
    private val cameraManager = appContext.getSystemService(CameraManager::class.java)
    private val locationManager = appContext.getSystemService(LocationManager::class.java)
    private val notificationManager = appContext.getSystemService(NotificationManager::class.java)
    private val vibrator = if (Build.VERSION.SDK_INT >= 31) {
        appContext.getSystemService(VibratorManager::class.java).defaultVibrator
    } else {
        @Suppress("DEPRECATION")
        appContext.getSystemService(Vibrator::class.java)
    }
    private val cameraID = findFlashCamera()
    private val notificationIDs = AtomicInteger(DEVICE_NOTIFICATION_ID)
    @Volatile private var torchEnabled = false

    private val torchCallback = object : CameraManager.TorchCallback() {
        override fun onTorchModeChanged(cameraId: String, enabled: Boolean) {
            if (cameraId == cameraID) torchEnabled = enabled
        }

        override fun onTorchModeUnavailable(cameraId: String) {
            if (cameraId == cameraID) torchEnabled = false
        }
    }

    init {
        cameraManager.registerTorchCallback(torchCallback, Handler(Looper.getMainLooper()))
        notificationManager.createNotificationChannel(
            NotificationChannel(
                DEVICE_NOTIFICATION_CHANNEL,
                "ECS 设备通知",
                NotificationManager.IMPORTANCE_DEFAULT,
            ),
        )
    }

    override fun availability(name: String): String = when (name) {
        FLASHLIGHT -> when {
            cameraID == null -> HARDWARE_UNAVAILABLE
            !hasPermission(Manifest.permission.CAMERA) -> PERMISSION_DENIED
            else -> AVAILABLE
        }
        VIBRATE -> if (vibrator.hasVibrator()) AVAILABLE else HARDWARE_UNAVAILABLE
        BEEP -> AVAILABLE
        SCREEN_BRIGHTNESS ->
            if (Settings.System.canWrite(appContext)) AVAILABLE else PERMISSION_DENIED
        NOTIFY -> when {
            Build.VERSION.SDK_INT >= 33 &&
                !hasPermission(Manifest.permission.POST_NOTIFICATIONS) -> PERMISSION_DENIED
            !notificationManager.areNotificationsEnabled() -> PERMISSION_DENIED
            notificationManager.getNotificationChannel(DEVICE_NOTIFICATION_CHANNEL)?.importance ==
                NotificationManager.IMPORTANCE_NONE -> PERMISSION_DENIED
            else -> AVAILABLE
        }
        LOCATION -> when {
            locationManager.allProviders.isEmpty() -> HARDWARE_UNAVAILABLE
            !hasPermission(Manifest.permission.ACCESS_COARSE_LOCATION) &&
                !hasPermission(Manifest.permission.ACCESS_FINE_LOCATION) -> PERMISSION_DENIED
            Build.VERSION.SDK_INT >= 28 && !locationManager.isLocationEnabled ->
                LOCATION_SERVICES_DISABLED
            else -> AVAILABLE
        }
        else -> UNSUPPORTED_CAPABILITY
    }

    fun available(name: String): Boolean = availability(name).isEmpty()

    override fun execute(name: String, argumentsJSON: String): String {
        val arguments = JSONObject(argumentsJSON)
        return when (name) {
            FLASHLIGHT -> executeFlashlight(arguments)
            VIBRATE -> executeVibration(arguments)
            BEEP -> executeBeep(arguments)
            SCREEN_BRIGHTNESS -> executeBrightness(arguments)
            NOTIFY -> executeNotification(arguments)
            LOCATION -> executeLocation(arguments)
            else -> error("不支持的设备能力：$name")
        }
    }

    fun resetTransientCapabilities() {
        cameraID?.let { id -> runCatching { setTorch(id, false) } }
        vibrator.cancel()
    }

    override fun close() {
        resetTransientCapabilities()
        cameraManager.unregisterTorchCallback(torchCallback)
    }

    private fun executeFlashlight(arguments: JSONObject): String {
        val id = cameraID ?: error("当前设备没有可用的闪光灯")
        requirePermission(Manifest.permission.CAMERA, "相机权限未授权，无法控制手电筒")
        when (arguments.getString("action")) {
            "on" -> setTorch(id, true)
            "off" -> setTorch(id, false)
            "status" -> Unit
            else -> error("action 必须是 on、off 或 status")
        }
        return JSONObject()
            .put("state", if (torchEnabled) "on" else "off")
            .put("available", true)
            .toString()
    }

    private fun executeVibration(arguments: JSONObject): String {
        if (!vibrator.hasVibrator()) error("当前设备不支持震动")
        val pattern = arguments.getString("pattern")
        val timings = when (pattern) {
            "short" -> longArrayOf(0, 180)
            "long" -> longArrayOf(0, 700)
            "double" -> longArrayOf(0, 180, 140, 180)
            else -> error("pattern 必须是 short、long 或 double")
        }
        vibrator.vibrate(VibrationEffect.createWaveform(timings, -1))
        return JSONObject().put("pattern", pattern).put("performed", true).toString()
    }

    private fun executeBeep(arguments: JSONObject): String {
        val count = arguments.getInt("count")
        require(count in 1..3) { "count 必须是 1 到 3 的整数" }
        val tone = ToneGenerator(AudioManager.STREAM_ALARM, 80)
        try {
            repeat(count) { index ->
                tone.startTone(ToneGenerator.TONE_PROP_BEEP, 160)
                Thread.sleep(if (index == count - 1) 180 else 280)
            }
        } finally {
            tone.release()
        }
        return JSONObject().put("count", count).put("performed", true).toString()
    }

    private fun executeBrightness(arguments: JSONObject): String {
        val resolver = appContext.contentResolver
        val previous = Settings.System.getInt(
            resolver,
            Settings.System.SCREEN_BRIGHTNESS,
            DEFAULT_BRIGHTNESS,
        ) / 255.0
        val action = arguments.getString("action")
        var value = previous
        when (action) {
            "get" -> Unit
            "set" -> {
                if (!Settings.System.canWrite(appContext)) {
                    error("未授权修改系统设置，请在 ECS 节点配置中授权屏幕亮度控制")
                }
                value = arguments.optDouble("value", Double.NaN)
                require(value.isFinite() && value in 0.0..1.0) {
                    "set 操作的 value 必须是 0 到 1"
                }
                Settings.System.putInt(
                    resolver,
                    Settings.System.SCREEN_BRIGHTNESS_MODE,
                    Settings.System.SCREEN_BRIGHTNESS_MODE_MANUAL,
                )
                check(
                    Settings.System.putInt(
                        resolver,
                        Settings.System.SCREEN_BRIGHTNESS,
                        (value * 255).toInt().coerceIn(0, 255),
                    ),
                ) { "系统拒绝修改屏幕亮度" }
            }
            else -> error("action 必须是 get 或 set")
        }
        return JSONObject()
            .put("value", value)
            .put("previous_value", previous)
            .put("writable", Settings.System.canWrite(appContext))
            .toString()
    }

    private fun executeNotification(arguments: JSONObject): String {
        if (Build.VERSION.SDK_INT >= 33) {
            requirePermission(
                Manifest.permission.POST_NOTIFICATIONS,
                "通知权限未授权，无法发送通知",
            )
        }
        val title = arguments.getString("title").trim()
        val body = arguments.getString("body").trim()
        require(title.isNotEmpty() && title.length <= 80) { "title 必须是 1 到 80 个字符" }
        require(body.isNotEmpty() && body.length <= 240) { "body 必须是 1 到 240 个字符" }
        val id = notificationIDs.incrementAndGet()
        val openApp = PendingIntent.getActivity(
            appContext,
            id,
            Intent(appContext, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        notificationManager.notify(
            id,
            Notification.Builder(appContext, DEVICE_NOTIFICATION_CHANNEL)
                .setSmallIcon(android.R.drawable.stat_notify_chat)
                .setContentTitle(title)
                .setContentText(body)
                .setStyle(Notification.BigTextStyle().bigText(body))
                .setContentIntent(openApp)
                .setAutoCancel(true)
                .build(),
        )
        return JSONObject().put("delivered", true).put("id", id).toString()
    }

    private fun executeLocation(arguments: JSONObject): String {
        val accuracy = arguments.getString("accuracy")
        require(accuracy == "coarse" || accuracy == "precise") {
            "accuracy 必须是 coarse 或 precise"
        }
        val fine = hasPermission(Manifest.permission.ACCESS_FINE_LOCATION)
        val coarse = hasPermission(Manifest.permission.ACCESS_COARSE_LOCATION)
        if (!coarse && !fine) error("定位权限未授权")
        if (accuracy == "precise" && !fine) error("精确定位权限未授权")
        val providers = when (accuracy) {
            "precise" -> listOf(LocationManager.GPS_PROVIDER, LocationManager.NETWORK_PROVIDER)
            else -> listOf(LocationManager.NETWORK_PROVIDER, LocationManager.GPS_PROVIDER)
        }
        val enabledProviders = providers.filter {
            runCatching { locationManager.isProviderEnabled(it) }.getOrDefault(false)
        }
        if (enabledProviders.isEmpty()) error("系统定位服务未开启")
        val timeoutSeconds = (LOCATION_TIMEOUT_SECONDS / enabledProviders.size).coerceAtLeast(1)
        val location = enabledProviders.firstNotNullOfOrNull { provider ->
            currentLocation(provider, timeoutSeconds)
        } ?: error("${LOCATION_TIMEOUT_SECONDS} 秒内未获得定位结果")
        return locationJSON(location, accuracy).toString()
    }

    @SuppressLint("MissingPermission")
    private fun currentLocation(provider: String, timeoutSeconds: Long): Location? {
        val result = AtomicReference<Location?>()
        val latch = CountDownLatch(1)
        if (Build.VERSION.SDK_INT >= 30) {
            val cancellation = CancellationSignal()
            locationManager.getCurrentLocation(provider, cancellation, appContext.mainExecutor) {
                result.set(it)
                latch.countDown()
            }
            if (!latch.await(timeoutSeconds, TimeUnit.SECONDS)) cancellation.cancel()
            return result.get()
        }
        val listener = object : LocationListener {
            override fun onLocationChanged(location: Location) {
                result.set(location)
                latch.countDown()
            }

            @Deprecated("Deprecated by Android")
            override fun onStatusChanged(provider: String?, status: Int, extras: Bundle?) = Unit
            override fun onProviderEnabled(provider: String) = Unit
            override fun onProviderDisabled(provider: String) = Unit
        }
        val main = Handler(Looper.getMainLooper())
        main.post { locationManager.requestSingleUpdate(provider, listener, Looper.getMainLooper()) }
        latch.await(timeoutSeconds, TimeUnit.SECONDS)
        main.post { locationManager.removeUpdates(listener) }
        return result.get()
    }

    private fun locationJSON(location: Location, requestedAccuracy: String) = JSONObject()
        .put("latitude", location.latitude)
        .put("longitude", location.longitude)
        .put("accuracy_m", location.accuracy.toDouble())
        .put("timestamp_ms", location.time)
        .put("provider", location.provider)
        .put("requested_accuracy", requestedAccuracy)

    private fun setTorch(cameraId: String, enabled: Boolean) {
        cameraManager.setTorchMode(cameraId, enabled)
        torchEnabled = enabled
    }

    private fun findFlashCamera(): String? {
        val candidates = runCatching { cameraManager.cameraIdList.toList() }.getOrDefault(emptyList())
        return candidates
            .filter { id ->
                cameraManager.getCameraCharacteristics(id)
                    .get(CameraCharacteristics.FLASH_INFO_AVAILABLE) == true
            }
            .sortedBy { id ->
                val facing = cameraManager.getCameraCharacteristics(id)
                    .get(CameraCharacteristics.LENS_FACING)
                if (facing == CameraCharacteristics.LENS_FACING_BACK) 0 else 1
            }
            .firstOrNull()
    }

    private fun requirePermission(permission: String, message: String) {
        if (!hasPermission(permission)) error(message)
    }

    private fun hasPermission(permission: String): Boolean =
        appContext.checkSelfPermission(permission) == PackageManager.PERMISSION_GRANTED

    companion object {
        private const val FLASHLIGHT = "flashlight"
        private const val VIBRATE = "vibrate"
        private const val BEEP = "beep"
        private const val SCREEN_BRIGHTNESS = "screen_brightness"
        private const val NOTIFY = "notify"
        private const val LOCATION = "location"
        private const val DEVICE_NOTIFICATION_CHANNEL = "ecs-device-capabilities"
        private const val DEVICE_NOTIFICATION_ID = 18100
        private const val DEFAULT_BRIGHTNESS = 128
        private const val LOCATION_TIMEOUT_SECONDS = 12L
        private const val AVAILABLE = ""
        private const val HARDWARE_UNAVAILABLE = "hardware_unavailable"
        private const val PERMISSION_DENIED = "permission_denied"
        private const val LOCATION_SERVICES_DISABLED = "location_services_disabled"
        private const val UNSUPPORTED_CAPABILITY = "unsupported_capability"
    }
}
