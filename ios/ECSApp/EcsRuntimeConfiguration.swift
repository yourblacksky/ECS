import Foundation
import Darwin

struct EcsRuntimeConfiguration {
    let configJSON: String
    let secretsJSON: String
    let nodeName: String
    let port: Int
    let distributed: Bool
}

enum EcsRuntimeConfig {
    static func create(
        root: URL,
        settings: EcsSettings,
        secrets: KeychainSecretStore
    ) throws -> EcsRuntimeConfiguration {
        let configURL = root.appendingPathComponent("config.json")
        let data = try Data(contentsOf: configURL)
        guard var document = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              var llm = document["llm"] as? [String: Any],
              var discovery = document["discovery"] as? [String: Any],
              var network = document["network_auth"] as? [String: Any],
              var parent = network["parent"] as? [String: Any]
        else {
            throw ConfigurationError.invalidDocument
        }

        llm["base_url"] = settings.baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        llm["model"] = settings.model.trimmingCharacters(in: .whitespacesAndNewlines)
        llm["api_key"] = ""
        llm["api_key_secret"] = EcsSecretName.llmAPIKey
        document["llm"] = llm
        document["admin_token"] = ""
        document["admin_token_secret"] = EcsSecretName.adminToken

        let port = 18_090
        if settings.distributed {
            let address = LocalNetworkAddress.ipv4() ?? "127.0.0.1"
            document["listen"] = "0.0.0.0:\(port)"
            discovery["enabled"] = true
            discovery["advertise_url"] = "http://\(address):\(port)"
            network["enabled"] = true
            parent["auto_discover"] = true
        } else {
            document["listen"] = "127.0.0.1:\(port)"
            discovery["enabled"] = false
            discovery["advertise_url"] = "http://127.0.0.1:\(port)"
            network["enabled"] = false
            parent["auto_discover"] = false
        }
        network["parent"] = parent
        document["discovery"] = discovery
        document["network_auth"] = network

        var secretValues = [
            EcsSecretName.adminToken: try secrets.adminToken(),
        ]
        if let value = try secrets.value(for: EcsSecretName.llmAPIKey), !value.isEmpty {
            secretValues[EcsSecretName.llmAPIKey] = value
        }
        if let value = try secrets.value(for: EcsSecretName.parentPairingCode), !value.isEmpty {
            secretValues[EcsSecretName.parentPairingCode] = value
        }
        let encodedConfig = try JSONSerialization.data(withJSONObject: document)
        let encodedSecrets = try JSONSerialization.data(withJSONObject: secretValues)
        return EcsRuntimeConfiguration(
            configJSON: String(decoding: encodedConfig, as: UTF8.self),
            secretsJSON: String(decoding: encodedSecrets, as: UTF8.self),
            nodeName: document["node_name"] as? String ?? "ecs-ios",
            port: port,
            distributed: settings.distributed
        )
    }

    enum ConfigurationError: LocalizedError {
        case invalidDocument

        var errorDescription: String? {
            "config.json 结构无效"
        }
    }
}

enum LocalNetworkAddress {
    static func ipv4() -> String? {
        var pointer: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&pointer) == 0, let first = pointer else {
            return nil
        }
        defer { freeifaddrs(pointer) }
        for item in sequence(first: first, next: { $0.pointee.ifa_next }) {
            let interface = item.pointee
            guard
                let socketAddress = interface.ifa_addr,
                socketAddress.pointee.sa_family == UInt8(AF_INET)
            else {
                continue
            }
            let name = String(cString: interface.ifa_name)
            guard name == "en0" || name == "en1" else {
                continue
            }
            var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            let length = socklen_t(socketAddress.pointee.sa_len)
            if getnameinfo(
                socketAddress,
                length,
                &host,
                socklen_t(host.count),
                nil,
                0,
                NI_NUMERICHOST
            ) == 0 {
                return String(cString: host)
            }
        }
        return nil
    }
}
