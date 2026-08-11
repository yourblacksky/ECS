import Foundation
import Security

enum EcsSecretName {
    static let llmAPIKey = "llm.api_key"
    static let adminToken = "admin.token"
    static let parentPairingCode = "network.parent_pairing_code"
}

struct KeychainSecretStore {
    private let service = Bundle.main.bundleIdentifier ?? "io.github.yourblacksky.ecs"

    func value(for account: String) throws -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess, let data = item as? Data else {
            throw KeychainError(status)
        }
        return String(data: data, encoding: .utf8)
    }

    func set(_ value: String, for account: String) throws {
        if value.isEmpty {
            try delete(account)
            return
        }
        let identity: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        let attributes: [String: Any] = [
            kSecValueData as String: Data(value.utf8),
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        ]
        let status: OSStatus
        if try self.value(for: account) == nil {
            status = SecItemAdd(identity.merging(attributes) { _, new in new } as CFDictionary, nil)
        } else {
            status = SecItemUpdate(identity as CFDictionary, attributes as CFDictionary)
        }
        guard status == errSecSuccess else {
            throw KeychainError(status)
        }
    }

    func delete(_ account: String) throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError(status)
        }
    }

    func adminToken() throws -> String {
        if let token = try value(for: EcsSecretName.adminToken), !token.isEmpty {
            return token
        }
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
            throw KeychainError(errSecInternalError)
        }
        let token = Data(bytes).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
        try set(token, for: EcsSecretName.adminToken)
        return token
    }
}

struct KeychainError: LocalizedError {
    let status: OSStatus

    init(_ status: OSStatus) {
        self.status = status
    }

    var errorDescription: String? {
        (SecCopyErrorMessageString(status, nil) as String?) ?? "Keychain error \(status)"
    }
}
