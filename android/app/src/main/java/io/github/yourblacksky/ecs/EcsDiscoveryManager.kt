package io.github.yourblacksky.ecs

import android.content.Context
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Handler
import android.os.Looper
import io.github.yourblacksky.ecs.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject
import java.net.Inet4Address
import java.util.concurrent.Executors

class EcsDiscoveryManager(
    context: Context,
    private val nodeName: String,
    private val port: Int,
    private val onAddressChanged: () -> Unit,
) {
    private val application = context.applicationContext
    private val nsd = application.getSystemService(NsdManager::class.java)
    private val connectivity = application.getSystemService(ConnectivityManager::class.java)
    private val main = Handler(Looper.getMainLooper())
    private val callbacks = Executors.newCachedThreadPool()
    private var registration: NsdManager.RegistrationListener? = null
    private var discovery: NsdManager.DiscoveryListener? = null
    private var networkCallback: ConnectivityManager.NetworkCallback? = null
    @Volatile private var running = false
    private var localAddress = EcsRuntimeConfig.localIPv4(application)
    private var restartScheduled = false

    fun start() {
        if (running) return
        running = true
        runCatching { startNsd() }
        networkCallback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                main.post {
                    if (running && registration == null) runCatching { startNsd() }
                    handleAddressChange()
                }
            }

            override fun onLinkPropertiesChanged(network: Network, linkProperties: LinkProperties) {
                main.post(::handleAddressChange)
            }

            override fun onLost(network: Network) {
                main.post(::handleAddressChange)
            }
        }.also { callback ->
            runCatching { connectivity.registerDefaultNetworkCallback(callback) }
        }
    }

    fun stop() {
        if (!running) return
        running = false
        networkCallback?.let { runCatching { connectivity.unregisterNetworkCallback(it) } }
        networkCallback = null
        discovery?.let { runCatching { nsd.stopServiceDiscovery(it) } }
        discovery = null
        registration?.let { runCatching { nsd.unregisterService(it) } }
        registration = null
        callbacks.shutdownNow()
    }

    private fun handleAddressChange() {
        if (!running || restartScheduled) return
        val current = EcsRuntimeConfig.localIPv4(application)
        if (current == localAddress) return
        localAddress = current
        restartScheduled = true
        onAddressChanged()
    }

    private fun startNsd() {
        if (registration != null || discovery != null) return
        val info = NsdServiceInfo().apply {
            serviceName = nodeName.take(48)
            serviceType = SERVICE_TYPE
            port = this@EcsDiscoveryManager.port
            setAttribute("protocol", "ecs1")
            setAttribute("node", nodeName.take(96))
            statusNodeID().takeIf(String::isNotBlank)?.let { setAttribute("node_id", it) }
            setAttribute("platform", "android")
            setAttribute("runtime_mode", "foreground")
            setAttribute("topology_role", "peer")
            setAttribute("network_auth", "1")
            setAttribute("features", "network_auth")
            setAttribute("accepts_children", "1")
            setAttribute("supports_inbound", "1")
            setAttribute("supports_background", "0")
            setAttribute("supports_subprocess", "0")
            setAttribute("supports_multicast", "1")
            setAttribute("supports_push_wakeup", "0")
        }
        registration = object : NsdManager.RegistrationListener {
            override fun onServiceRegistered(serviceInfo: NsdServiceInfo) = Unit
            override fun onRegistrationFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                registration = null
            }
            override fun onServiceUnregistered(serviceInfo: NsdServiceInfo) {
                registration = null
            }
            override fun onUnregistrationFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                registration = null
            }
        }.also { nsd.registerService(info, NsdManager.PROTOCOL_DNS_SD, it) }

        discovery = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) = Unit
            override fun onDiscoveryStopped(serviceType: String) = Unit
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                discovery = null
            }
            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {
                discovery = null
            }
            override fun onServiceLost(serviceInfo: NsdServiceInfo) = Unit
            override fun onServiceFound(serviceInfo: NsdServiceInfo) {
                if (serviceInfo.serviceName == nodeName) return
                @Suppress("DEPRECATION")
                nsd.resolveService(serviceInfo, object : NsdManager.ResolveListener {
                    override fun onResolveFailed(serviceInfo: NsdServiceInfo, errorCode: Int) = Unit
                    override fun onServiceResolved(resolved: NsdServiceInfo) {
                        injectResolvedService(resolved)
                    }
                })
            }
        }.also { nsd.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, it) }
    }

    private fun injectResolvedService(service: NsdServiceInfo) {
        @Suppress("DEPRECATION")
        val host = service.host as? Inet4Address ?: return
        val attributes = service.attributes
        if (attributes["protocol"]?.toString(Charsets.UTF_8) != "ecs1") return
        val peerNode = attributes["node"]?.toString(Charsets.UTF_8)
            ?.takeIf(String::isNotBlank) ?: service.serviceName
        val peerNodeID = attributes["node_id"]?.toString(Charsets.UTF_8).orEmpty()
        if (peerNodeID.isNotEmpty() && peerNodeID == statusNodeID()) return
        val features = JSONArray()
        attributes["features"]?.toString(Charsets.UTF_8)
            ?.split(",")
            ?.map(String::trim)
            ?.filter(String::isNotEmpty)
            ?.forEach(features::put)
        val groups = JSONArray()
        attributes["groups"]?.toString(Charsets.UTF_8)
            ?.split(",")
            ?.map(String::trim)
            ?.filter(String::isNotEmpty)
            ?.forEach(groups::put)
        val payload = JSONObject()
            .put("protocol", "ecs/1")
            .put("node", peerNode)
            .put("node_id", peerNodeID)
            .put("url", "http://${host.hostAddress}:${service.port}")
            .put("model", "")
            .put("features", features)
            .put("groups", groups)
            .put(
                "capability_hash",
                attributes["capability_hash"]?.toString(Charsets.UTF_8).orEmpty(),
            )
            .put(
                "platform",
                JSONObject()
                    .put(
                        "name",
                        attributes["platform"]?.toString(Charsets.UTF_8) ?: "unknown",
                    )
                    .put(
                        "runtime_mode",
                        attributes["runtime_mode"]?.toString(Charsets.UTF_8) ?: "foreground",
                    )
                    .put(
                        "topology_role",
                        attributes["topology_role"]?.toString(Charsets.UTF_8) ?: "peer",
                    )
                    .put("accepts_children", attributes["accepts_children"]?.toString(Charsets.UTF_8) == "1")
                    .put("supports_inbound", attributes["supports_inbound"]?.toString(Charsets.UTF_8) == "1")
                    .put("supports_background", attributes["supports_background"]?.toString(Charsets.UTF_8) == "1")
                    .put("supports_subprocess", attributes["supports_subprocess"]?.toString(Charsets.UTF_8) == "1")
                    .put("supports_multicast", attributes["supports_multicast"]?.toString(Charsets.UTF_8) == "1")
                    .put("supports_push_wakeup", attributes["supports_push_wakeup"]?.toString(Charsets.UTF_8) == "1"),
            )
            .put("tools", JSONArray())
        inject(payload.toString())
    }

    private fun inject(payload: String) {
        if (!running) return
        runCatching {
            callbacks.execute { runCatching { Mobile.pushDiscoveryAnnouncement(payload) } }
        }
    }

    private fun statusNodeID(): String = runCatching {
        JSONObject(Mobile.statusJSON()).optString("node_id")
    }.getOrDefault("")

    companion object {
        private const val SERVICE_TYPE = "_ecs._tcp."
    }
}
