import Foundation

struct EcsSettings: Codable, Equatable {
    var baseURL = "https://api.deepseek.com"
    var model = "deepseek-v4-flash"
    var distributed = true
    var autoStart = false

    static func load() -> EcsSettings {
        guard
            let data = UserDefaults.standard.data(forKey: storageKey),
            let value = try? JSONDecoder().decode(EcsSettings.self, from: data)
        else {
            return EcsSettings()
        }
        let migrated = value.migratingLegacyDeepSeekModel()
        if migrated != value {
            try? migrated.save()
        }
        return migrated
    }

    func migratingLegacyDeepSeekModel() -> EcsSettings {
        guard
            baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
                .trimmingCharacters(in: CharacterSet(charactersIn: "/"))
                .caseInsensitiveCompare("https://api.deepseek.com") == .orderedSame,
            model.trimmingCharacters(in: .whitespacesAndNewlines) == "deepseek-chat"
        else {
            return self
        }
        var migrated = self
        migrated.model = "deepseek-v4-flash"
        return migrated
    }

    func save() throws {
        let data = try JSONEncoder().encode(self)
        UserDefaults.standard.set(data, forKey: Self.storageKey)
    }

    private static let storageKey = "ecs.settings.v1"
}
