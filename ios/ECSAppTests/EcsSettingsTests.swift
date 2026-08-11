import XCTest
@testable import ECS

final class EcsSettingsTests: XCTestCase {
    func testMigratesLegacyOfficialDeepSeekModel() {
        let settings = EcsSettings(
            baseURL: "https://api.deepseek.com/",
            model: "deepseek-chat",
            distributed: true,
            autoStart: false
        )

        XCTAssertEqual(settings.migratingLegacyDeepSeekModel().model, "deepseek-v4-flash")
    }

    func testPreservesCustomProviderAndModel() {
        let customProvider = EcsSettings(
            baseURL: "https://provider.example/v1",
            model: "deepseek-chat",
            distributed: true,
            autoStart: false
        )
        let customModel = EcsSettings(
            baseURL: "https://api.deepseek.com",
            model: "custom-model",
            distributed: true,
            autoStart: false
        )

        XCTAssertEqual(customProvider.migratingLegacyDeepSeekModel(), customProvider)
        XCTAssertEqual(customModel.migratingLegacyDeepSeekModel(), customModel)
    }
}
