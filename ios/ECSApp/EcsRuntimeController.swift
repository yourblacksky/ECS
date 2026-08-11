import Foundation
import SwiftUI

struct EcsRuntimeStatus {
    var running = false
    var node = ""
    var nodeID = ""
    var tools = 0
    var skills = 0
    var address = ""
    var error = ""

    init(json: String = #"{"running":false}"#) {
        guard
            let data = json.data(using: .utf8),
            let value = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else {
            error = "状态数据无效"
            return
        }
        running = value["running"] as? Bool ?? false
        node = value["node"] as? String ?? ""
        nodeID = value["node_id"] as? String ?? ""
        tools = value["tools"] as? Int ?? 0
        skills = value["skills"] as? Int ?? 0
        address = value["address"] as? String ?? ""
        error = value["error"] as? String ?? ""
    }
}

struct EcsHostLog: Identifiable {
    let id = UUID()
    let date: Date
    let message: String
    let isError: Bool
}

@MainActor
final class EcsRuntimeController: ObservableObject {
    @Published private(set) var status = EcsRuntimeStatus()
    @Published private(set) var logs: [EcsHostLog] = []
    @Published private(set) var busy = false
    @Published var settings = EcsSettings.load()
    @Published var presentedError: String?

    let adminURL = URL(string: "http://127.0.0.1:18090/admin/")!

    private let secrets = KeychainSecretStore()
    private let discovery = EcsBonjourManager()
    private let deviceCapabilities = IOSDeviceCapabilityProvider()
    private var statusTask: Task<Void, Never>?
    private var resumeAfterForeground = false
    private var isForeground = true

    init() {
        EcsMobileBridge.setDeviceCapabilityProvider(deviceCapabilities)
        discovery.onNetworkAddressChanged = { [weak self] in
            Task { @MainActor in
                await self?.restartAfterNetworkChange()
            }
        }
    }

    func startIfConfigured() async {
        if settings.autoStart {
            await start()
        }
    }

    func start() async {
        guard !busy, !status.running else {
            return
        }
        busy = true
        defer { busy = false }
        do {
            let root = try EcsAssets.prepare()
            let configuration = try EcsRuntimeConfig.create(
                root: root,
                settings: settings,
                secrets: secrets
            )
            try EcsMobileBridge.start(
                configJSON: configuration.configJSON,
                dataDirectory: root,
                secretsJSON: configuration.secretsJSON
            )
            refreshStatus()
            if configuration.distributed {
                discovery.start(
                    nodeName: configuration.nodeName,
                    port: configuration.port,
                    nodeID: status.nodeID
                )
            }
            resumeAfterForeground = true
            appendLog("ECS 已启动：\(status.node.isEmpty ? configuration.nodeName : status.node)")
            beginStatusRefresh()
        } catch {
            appendLog("启动失败：\(error.localizedDescription)", error: true)
            presentedError = error.localizedDescription
            try? EcsMobileBridge.stop()
            refreshStatus()
        }
    }

    func stop(
        rememberForForeground: Bool = false,
        reason: String = "manual"
    ) async {
        guard !busy else {
            return
        }
        if !rememberForForeground {
            resumeAfterForeground = false
        }
        busy = true
        statusTask?.cancel()
        statusTask = nil
        discovery.stop()
        deviceCapabilities.resetTransientCapabilities()
        defer { busy = false }
        do {
            try EcsMobileBridge.stop()
            appendLog("ECS 已停止（\(reason)）")
        } catch {
            appendLog("停止失败：\(error.localizedDescription)", error: true)
            presentedError = error.localizedDescription
        }
        refreshStatus()
    }

    func save(settings: EcsSettings, apiKey: String, parentPairingCode: String) async {
        do {
            try settings.save()
            try secrets.set(apiKey.trimmingCharacters(in: .whitespacesAndNewlines), for: EcsSecretName.llmAPIKey)
            try secrets.set(
                parentPairingCode.trimmingCharacters(in: .whitespacesAndNewlines),
                for: EcsSecretName.parentPairingCode
            )
            self.settings = settings
            let wasRunning = status.running
            if wasRunning {
                await stop(reason: "configuration")
                await start()
            }
            appendLog("配置已保存")
        } catch {
            appendLog("保存失败：\(error.localizedDescription)", error: true)
            presentedError = error.localizedDescription
        }
    }

    func storedAPIKey() -> String {
        (try? secrets.value(for: EcsSecretName.llmAPIKey)) ?? ""
    }

    func storedParentPairingCode() -> String {
        (try? secrets.value(for: EcsSecretName.parentPairingCode)) ?? ""
    }

    func adminToken() -> String {
        (try? secrets.adminToken()) ?? ""
    }

    func handleScenePhase(_ phase: ScenePhase) async {
        appendLog("Scene 状态：\(String(describing: phase))")
        switch phase {
        case .active:
            isForeground = true
            if resumeAfterForeground || settings.autoStart {
                await start()
            }
        case .background:
            isForeground = false
            if status.running {
                resumeAfterForeground = true
                await stop(rememberForForeground: true, reason: "background")
            }
        case .inactive:
            break
        @unknown default:
            break
        }
    }

    private func restartAfterNetworkChange() async {
        guard isForeground, status.running, settings.distributed, !busy else {
            return
        }
        appendLog("局域网地址变化，正在安全重启节点")
        await stop(rememberForForeground: true, reason: "network-change")
        guard isForeground else {
            return
        }
        await start()
    }

    private func refreshStatus() {
        status = EcsRuntimeStatus(json: EcsMobileBridge.statusJSON())
    }

    private func beginStatusRefresh() {
        statusTask?.cancel()
        statusTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                guard !Task.isCancelled else { return }
                self?.refreshStatus()
            }
        }
    }

    private func appendLog(_ message: String, error: Bool = false) {
        print("[ECSHost] \(message)")
        logs.insert(EcsHostLog(date: Date(), message: message, isError: error), at: 0)
        if logs.count > 100 {
            logs.removeLast(logs.count - 100)
        }
    }
}
