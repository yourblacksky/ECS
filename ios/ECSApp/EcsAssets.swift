import Foundation
import CryptoKit

enum EcsAssets {
    private static let managedHashesKey = "ecs.assets.managed-hashes"
    private static let managedRoots = [
        "prompts",
        "agents.d",
        "pipelines.d",
        "tools.d",
        "skills.d",
        "office.d",
    ]

    static func prepare() throws -> URL {
        let support = try FileManager.default.url(
            for: .applicationSupportDirectory,
            in: .userDomainMask,
            appropriateFor: nil,
            create: true
        )
        let root = support.appendingPathComponent("ECS", isDirectory: true)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try mergeConfigDefaults(to: root.appendingPathComponent("config.json"))
        let previousHashes = UserDefaults.standard.dictionary(
            forKey: managedHashesKey
        ) as? [String: String] ?? [:]
        var currentHashes: [String: String] = [:]
        for name in managedRoots {
            guard let source = Bundle.main.resourceURL?.appendingPathComponent(name),
                  FileManager.default.fileExists(atPath: source.path)
            else {
                throw AssetError.missing(name)
            }
            try mergeManagedDirectory(
                source: source,
                destination: root.appendingPathComponent(name),
                assetPath: name,
                previousHashes: previousHashes,
                currentHashes: &currentHashes
            )
        }
        UserDefaults.standard.set(currentHashes, forKey: managedHashesKey)
        return root
    }

    private static func mergeConfigDefaults(to destination: URL) throws {
        guard let source = Bundle.main.url(forResource: "config", withExtension: "json") else {
            throw AssetError.missing("config.json")
        }
        guard FileManager.default.fileExists(atPath: destination.path) else {
            try FileManager.default.copyItem(at: source, to: destination)
            return
        }

        let defaultsData = try Data(contentsOf: source)
        let currentData = try Data(contentsOf: destination)
        guard
            let defaults = try JSONSerialization.jsonObject(with: defaultsData) as? [String: Any],
            let current = try JSONSerialization.jsonObject(with: currentData) as? [String: Any]
        else {
            throw AssetError.invalid("config.json")
        }
        let merged = mergeMissingDefaults(current: current, defaults: defaults)
        guard merged.changed else {
            return
        }
        let data = try JSONSerialization.data(
            withJSONObject: merged.document,
            options: [.prettyPrinted, .sortedKeys]
        )
        try writeAtomically(data, to: destination)
    }

    // Upgrade migrations add newly packaged defaults recursively. Existing
    // values and user-defined keys always win, including nested sections.
    static func mergeMissingDefaults(
        current: [String: Any],
        defaults: [String: Any]
    ) -> (document: [String: Any], changed: Bool) {
        var document = current
        var changed = false
        for (key, defaultValue) in defaults {
            guard let currentValue = document[key] else {
                document[key] = defaultValue
                changed = true
                continue
            }
            guard
                let currentObject = currentValue as? [String: Any],
                let defaultObject = defaultValue as? [String: Any]
            else {
                continue
            }
            let nested = mergeMissingDefaults(
                current: currentObject,
                defaults: defaultObject
            )
            if nested.changed {
                document[key] = nested.document
                changed = true
            }
        }
        return (document, changed)
    }

    // Packaged defaults are upgraded, while files edited after installation win.
    private static func mergeManagedDirectory(
        source: URL,
        destination: URL,
        assetPath: String,
        previousHashes: [String: String],
        currentHashes: inout [String: String]
    ) throws {
        try FileManager.default.createDirectory(at: destination, withIntermediateDirectories: true)
        let keys: [URLResourceKey] = [.isDirectoryKey]
        let children = try FileManager.default.contentsOfDirectory(
            at: source,
            includingPropertiesForKeys: keys
        )
        for child in children {
            let target = destination.appendingPathComponent(child.lastPathComponent)
            let childAssetPath = "\(assetPath)/\(child.lastPathComponent)"
            if try child.resourceValues(forKeys: Set(keys)).isDirectory == true {
                try mergeManagedDirectory(
                    source: child,
                    destination: target,
                    assetPath: childAssetPath,
                    previousHashes: previousHashes,
                    currentHashes: &currentHashes
                )
                continue
            }

            let packaged = try Data(contentsOf: child)
            let packagedHash = sha256(packaged)
            let targetData = try? Data(contentsOf: target)
            let targetHash = targetData.map(sha256)
            let previousHash = previousHashes[childAssetPath]
            let isKnownPackagedVersion =
                (previousHash != nil && previousHash == targetHash) ||
                legacyPackagedHashes[childAssetPath]?.contains(targetHash ?? "") == true
            if targetData == nil || isKnownPackagedVersion {
                if targetHash != packagedHash {
                    try writeAtomically(packaged, to: target)
                }
            }
            currentHashes[childAssetPath] = packagedHash
        }
    }

    private static func writeAtomically(_ data: Data, to target: URL) throws {
        try FileManager.default.createDirectory(
            at: target.deletingLastPathComponent(),
            withIntermediateDirectories: true
        )
        let temporary = target.deletingLastPathComponent()
            .appendingPathComponent(".\(target.lastPathComponent).tmp")
        try data.write(to: temporary, options: .atomic)
        if FileManager.default.fileExists(atPath: target.path) {
            _ = try FileManager.default.replaceItemAt(target, withItemAt: temporary)
        } else {
            try FileManager.default.moveItem(at: temporary, to: target)
        }
    }

    private static func sha256(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    // Versions shipped before managed hashes were introduced. This lets the
    // first hash-aware release upgrade untouched defaults without replacing
    // a user's customized executor manifest.
    private static let legacyPackagedHashes: [String: Set<String>] = [
        "agents.d/executor.json": [
            "8ed731f2ffb70510e20f6294d26f6c60b65e9937ef0e6fb933f834bddd7e1e0e",
        ],
    ]

    enum AssetError: LocalizedError {
        case missing(String)
        case invalid(String)

        var errorDescription: String? {
            switch self {
            case .missing(let name):
                return "应用资源缺失：\(name)"
            case .invalid(let name):
                return "应用资源格式无效：\(name)"
            }
        }
    }
}
