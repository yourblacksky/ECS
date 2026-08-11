package io.github.yourblacksky.ecs

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.graphics.drawable.Icon
import android.os.IBinder
import io.github.yourblacksky.ecs.mobile.Mobile
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors

class EcsService : Service() {
    private val executor = Executors.newSingleThreadExecutor()
    private var discovery: EcsDiscoveryManager? = null
    private lateinit var deviceCapabilities: AndroidDeviceCapabilityProvider

    override fun onCreate() {
        super.onCreate()
        deviceCapabilities = AndroidDeviceCapabilityProvider(this)
        Mobile.setDeviceCapabilityProvider(deviceCapabilities)
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            executor.execute {
                discovery?.stop()
                discovery = null
                deviceCapabilities.resetTransientCapabilities()
                runCatching { Mobile.stop() }
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf()
            }
            return START_NOT_STICKY
        }

        startForeground(NOTIFICATION_ID, notification(getString(R.string.service_starting)))
        executor.execute { startRuntime() }
        return START_STICKY
    }

    override fun onDestroy() {
        discovery?.stop()
        discovery = null
        deviceCapabilities.resetTransientCapabilities()
        runCatching { Mobile.stop() }
        deviceCapabilities.close()
        Mobile.clearDeviceCapabilityProvider()
        executor.shutdown()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun startRuntime() {
        val alreadyRunning = runCatching {
            JSONObject(Mobile.statusJSON()).optBoolean("running")
        }.getOrDefault(false)
        if (alreadyRunning) {
            updateNotification(getString(R.string.service_running))
            return
        }
        val result = runCatching {
            val root = EcsAssets.prepare(this)
            val runtimeConfig = EcsRuntimeConfig.create(
                this,
                File(root, "config.json"),
            )
            Mobile.startWithSecrets(
                runtimeConfig.configJSON,
                root.absolutePath,
                runtimeConfig.secretsJSON,
            )
            if (runtimeConfig.distributed) {
                discovery = EcsDiscoveryManager(
                    this,
                    runtimeConfig.nodeName,
                    runtimeConfig.port,
                    onAddressChanged = {
                        executor.execute {
                            discovery?.stop()
                            discovery = null
                            deviceCapabilities.resetTransientCapabilities()
                            runCatching { Mobile.stop() }
                            startRuntime()
                        }
                    },
                ).also { it.start() }
            }
        }
        if (result.isSuccess) {
            updateNotification(getString(R.string.service_running))
        } else {
            updateNotification(
                "${getString(R.string.service_failed)}: ${result.exceptionOrNull()?.message}",
            )
            stopSelf()
        }
    }

    private fun createNotificationChannel() {
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(
            NotificationChannel(
                CHANNEL_ID,
                getString(R.string.service_channel_name),
                NotificationManager.IMPORTANCE_LOW,
            ),
        )
    }

    private fun notification(text: String): Notification {
        val openApp = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val stopService = PendingIntent.getService(
            this,
            1,
            Intent(this, EcsService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return Notification.Builder(this, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(text)
            .setContentIntent(openApp)
            .setOngoing(true)
            .addAction(
                Notification.Action.Builder(
                    Icon.createWithResource(this, android.R.drawable.ic_media_pause),
                    "停止",
                    stopService,
                ).build(),
            )
            .build()
    }

    private fun updateNotification(text: String) {
        getSystemService(NotificationManager::class.java)
            .notify(NOTIFICATION_ID, notification(text))
    }

    companion object {
        const val ACTION_START = "io.github.yourblacksky.ecs.action.START"
        const val ACTION_STOP = "io.github.yourblacksky.ecs.action.STOP"
        private const val CHANNEL_ID = "ecs-runtime"
        private const val NOTIFICATION_ID = 18090
    }
}
