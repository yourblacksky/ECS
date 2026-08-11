import XCTest
@testable import ECS

final class EcsAssetsTests: XCTestCase {
    func testConfigUpgradeAddsNestedDefaultsAndPreservesUserValues() throws {
        let current: [String: Any] = [
            "trace": [
                "enabled": false,
                "max_value_chars": 999,
            ],
            "custom": "preserved",
        ]
        let defaults: [String: Any] = [
            "trace": [
                "enabled": true,
                "max_value_chars": 4_000,
                "max_traces": 300,
                "retention": "72h",
            ],
            "discovery": [
                "enabled": true,
            ],
        ]

        let merged = EcsAssets.mergeMissingDefaults(
            current: current,
            defaults: defaults
        )
        XCTAssertTrue(merged.changed)
        XCTAssertEqual(merged.document["custom"] as? String, "preserved")

        let trace = try XCTUnwrap(merged.document["trace"] as? [String: Any])
        XCTAssertEqual(trace["enabled"] as? Bool, false)
        XCTAssertEqual(trace["max_value_chars"] as? Int, 999)
        XCTAssertEqual(trace["max_traces"] as? Int, 300)
        XCTAssertEqual(trace["retention"] as? String, "72h")
        XCTAssertNotNil(merged.document["discovery"])
    }

    func testConfigUpgradeIsStableAfterDefaultsArePresent() {
        let current: [String: Any] = [
            "trace": [
                "max_traces": 42,
                "retention": "24h",
            ],
        ]
        let merged = EcsAssets.mergeMissingDefaults(
            current: current,
            defaults: current
        )
        XCTAssertFalse(merged.changed)
    }
}
