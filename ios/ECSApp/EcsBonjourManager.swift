import Foundation
import Network

@MainActor
final class EcsBonjourManager: NSObject {
    var onNetworkAddressChanged: (() -> Void)?

    private var browser: NetServiceBrowser?
    private var publishedService: NetService?
    private var resolving: [NetService] = []
    private var resolvedServices: [String: NetService] = [:]
    private var discoveryRefreshTask: Task<Void, Never>?
    private var pathMonitor: NWPathMonitor?
    private var addressChangeTask: Task<Void, Never>?
    private var lastAddress: String?
    private var localNodeID = ""
    private var nodeName = ""
    private var port = 0

    func start(nodeName: String, port: Int, nodeID: String) {
        guard browser == nil else {
            return
        }
        self.nodeName = nodeName
        self.port = port
        self.localNodeID = nodeID
        self.lastAddress = LocalNetworkAddress.ipv4()

        publishLocalService()

        let browser = NetServiceBrowser()
        browser.delegate = self
        browser.includesPeerToPeer = true
        browser.searchForServices(ofType: "_ecs._tcp.", inDomain: "local.")
        self.browser = browser
        discoveryRefreshTask = Task { @MainActor [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                guard !Task.isCancelled, let self else {
                    return
                }
                self.publishLocalService()
                for service in self.resolvedServices.values {
                    self.inject(service)
                }
            }
        }

        let monitor = NWPathMonitor(requiredInterfaceType: .wifi)
        monitor.pathUpdateHandler = { [weak self] path in
            Task { @MainActor in
                guard let self else { return }
                self.handlePathUpdate(path)
            }
        }
        monitor.start(queue: DispatchQueue(label: "ecs.ios.network-path"))
        pathMonitor = monitor
    }

    func stop() {
        discoveryRefreshTask?.cancel()
        discoveryRefreshTask = nil
        addressChangeTask?.cancel()
        addressChangeTask = nil
        pathMonitor?.cancel()
        pathMonitor = nil
        browser?.stop()
        browser?.delegate = nil
        browser = nil
        for service in resolving {
            service.stop()
            service.delegate = nil
        }
        resolving.removeAll()
        for service in resolvedServices.values {
            service.stop()
            service.delegate = nil
        }
        resolvedServices.removeAll()
        publishedService?.stop()
        publishedService = nil
    }

    private func handlePathUpdate(_ path: NWPath) {
        // NWPathMonitor always emits an initial snapshot. It may also briefly
        // report no interface while WebKit creates its networking process.
        // Neither event means the ECS listener needs to be restarted.
        guard path.status == .satisfied, let current = LocalNetworkAddress.ipv4() else {
            return
        }
        guard let previous = lastAddress else {
            lastAddress = current
            return
        }
        guard current != previous else {
            return
        }

        lastAddress = current
        addressChangeTask?.cancel()
        addressChangeTask = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(1))
            guard
                !Task.isCancelled,
                let self,
                self.pathMonitor != nil,
                LocalNetworkAddress.ipv4() == current
            else {
                return
            }
            self.onNetworkAddressChanged?()
        }
    }

    private func txtRecord() -> [String: Data] {
        var record = [
            "protocol": Data("ecs1".utf8),
            "node": Data(nodeName.prefix(96).utf8),
            "node_id": Data(localNodeID.utf8),
            "platform": Data("ios".utf8),
            "runtime_mode": Data("foreground".utf8),
            "topology_role": Data("leaf".utf8),
            "network_auth": Data("1".utf8),
            "features": Data("network_auth".utf8),
            "accepts_children": Data("0".utf8),
            "supports_inbound": Data("1".utf8),
            "supports_background": Data("0".utf8),
            "supports_subprocess": Data("0".utf8),
            "supports_multicast": Data("0".utf8),
            "supports_push_wakeup": Data("0".utf8),
        ]
        // DNS-SD services commonly live much longer than ECS's discovery
        // expiry. A changing, protocol-opaque value makes each lease renewal
        // observable to browsers without changing the node's identity.
        record["lease"] = Data(String(Int(Date().timeIntervalSince1970)).utf8)
        return record
    }

    private func publishLocalService() {
        // NetService sends TXT updates, but some DNS-SD clients de-duplicate a
        // service instance until they observe a goodbye record. Re-publishing
        // emits that lifecycle transition and makes the ECS lease observable.
        publishedService?.stop()
        let service = NetService(
            domain: "local.",
            type: "_ecs._tcp.",
            name: nodeName,
            port: Int32(port)
        )
        service.includesPeerToPeer = true
        service.setTXTRecord(NetService.data(fromTXTRecord: txtRecord()))
        service.publish()
        publishedService = service
    }

    private func inject(_ service: NetService) {
        guard
            let address = service.addresses?.compactMap(Self.numericHost).first,
            address != lastAddress || service.port != port
        else {
            return
        }
        let record = service.txtRecordData().map(NetService.dictionary(fromTXTRecord:)) ?? [:]
        func field(_ name: String) -> String {
            record[name].flatMap { String(data: $0, encoding: .utf8) } ?? ""
        }
        guard field("protocol") == "ecs1" else {
            return
        }
        func list(_ name: String) -> [String] {
            field(name)
                .split(separator: ",")
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                .filter { !$0.isEmpty }
        }
        let peerNodeID = field("node_id")
        if !peerNodeID.isEmpty && peerNodeID == localNodeID {
            return
        }
        let host = address.contains(":") ? "[\(address)]" : address
        let payload: [String: Any] = [
            "protocol": "ecs/1",
            "node": field("node").isEmpty ? service.name : field("node"),
            "node_id": peerNodeID,
            "url": "http://\(host):\(service.port)",
            "model": "",
            "features": list("features"),
            "groups": list("groups"),
            "capability_hash": field("capability_hash"),
            "platform": [
                "name": field("platform").isEmpty ? "unknown" : field("platform"),
                "runtime_mode": field("runtime_mode").isEmpty ? "service" : field("runtime_mode"),
                "topology_role": field("topology_role").isEmpty
                    ? (field("accepts_children") == "1" ? "peer" : "leaf")
                    : field("topology_role"),
                "accepts_children": field("accepts_children") == "1",
                "supports_inbound": field("supports_inbound") == "1",
                "supports_background": field("supports_background") == "1",
                "supports_subprocess": field("supports_subprocess") == "1",
                "supports_multicast": field("supports_multicast") == "1",
                "supports_push_wakeup": field("supports_push_wakeup") == "1",
            ],
            "tools": [],
        ]
        guard
            let data = try? JSONSerialization.data(withJSONObject: payload),
            let json = String(data: data, encoding: .utf8)
        else {
            return
        }
        try? EcsMobileBridge.pushDiscovery(json)
    }

    private static func serviceKey(_ service: NetService) -> String {
        "\(service.domain)|\(service.type)|\(service.name)"
    }

    private static func numericHost(_ data: Data) -> String? {
        data.withUnsafeBytes { rawBuffer in
            guard let address = rawBuffer.baseAddress?.assumingMemoryBound(to: sockaddr.self) else {
                return nil
            }
            var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            guard getnameinfo(
                address,
                socklen_t(data.count),
                &host,
                socklen_t(host.count),
                nil,
                0,
                NI_NUMERICHOST
            ) == 0 else {
                return nil
            }
            return String(cString: host).components(separatedBy: "%").first
        }
    }
}

extension EcsBonjourManager: NetServiceBrowserDelegate {
    nonisolated func netServiceBrowser(
        _ browser: NetServiceBrowser,
        didFind service: NetService,
        moreComing: Bool
    ) {
        Task { @MainActor in
            service.delegate = self
            self.resolving.append(service)
            service.resolve(withTimeout: 5)
        }
    }

    nonisolated func netServiceBrowser(
        _ browser: NetServiceBrowser,
        didRemove service: NetService,
        moreComing: Bool
    ) {
        Task { @MainActor in
            self.resolving.removeAll { $0 === service }
            self.resolvedServices.removeValue(forKey: Self.serviceKey(service))
        }
    }
}

extension EcsBonjourManager: NetServiceDelegate {
    nonisolated func netServiceDidResolveAddress(_ sender: NetService) {
        Task { @MainActor in
            self.inject(sender)
            self.resolvedServices[Self.serviceKey(sender)] = sender
            self.resolving.removeAll { $0 === sender }
        }
    }

    nonisolated func netService(_ sender: NetService, didUpdateTXTRecord data: Data) {
        Task { @MainActor in
            self.inject(sender)
            self.resolvedServices[Self.serviceKey(sender)] = sender
        }
    }

    nonisolated func netService(
        _ sender: NetService,
        didNotResolve errorDict: [String: NSNumber]
    ) {
        Task { @MainActor in
            self.resolving.removeAll { $0 === sender }
        }
    }
}
