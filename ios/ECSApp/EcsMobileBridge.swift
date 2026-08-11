import Foundation

#if canImport(Mobile)
import Mobile
#endif

enum EcsMobileBridge {
    static func setDeviceCapabilityProvider(_ provider: IOSDeviceCapabilityProvider) {
        #if canImport(Mobile)
        MobileSetDeviceCapabilityProvider(provider)
        #endif
    }

    static func clearDeviceCapabilityProvider() {
        #if canImport(Mobile)
        MobileClearDeviceCapabilityProvider()
        #endif
    }

    static func start(configJSON: String, dataDirectory: URL, secretsJSON: String) throws {
        #if canImport(Mobile)
        var failure: NSError?
        guard MobileStartWithSecrets(
            configJSON,
            dataDirectory.path,
            secretsJSON,
            &failure
        ) else {
            throw failure ?? BridgeError.callFailed("启动 ECS")
        }
        #else
        throw BridgeError.frameworkUnavailable
        #endif
    }

    static func stop() throws {
        #if canImport(Mobile)
        var failure: NSError?
        guard MobileStop(&failure) else {
            throw failure ?? BridgeError.callFailed("停止 ECS")
        }
        #else
        throw BridgeError.frameworkUnavailable
        #endif
    }

    static func statusJSON() -> String {
        #if canImport(Mobile)
        return MobileStatusJSON()
        #else
        return #"{"running":false,"error":"Mobile.xcframework is unavailable"}"#
        #endif
    }

    static func pushDiscovery(_ payload: String) throws {
        #if canImport(Mobile)
        var failure: NSError?
        guard MobilePushDiscoveryAnnouncement(payload, &failure) else {
            throw failure ?? BridgeError.callFailed("注入发现公告")
        }
        #else
        throw BridgeError.frameworkUnavailable
        #endif
    }

    static func discoveryAnnouncement() -> String {
        #if canImport(Mobile)
        return MobileDiscoveryAnnouncement()
        #else
        return ""
        #endif
    }

    enum BridgeError: LocalizedError {
        case frameworkUnavailable
        case callFailed(String)

        var errorDescription: String? {
            switch self {
            case .frameworkUnavailable:
                return "Mobile.xcframework 尚未生成，请先运行 make mobile-xcframework"
            case .callFailed(let action):
                return "\(action)失败，原生绑定未返回错误详情"
            }
        }
    }
}
