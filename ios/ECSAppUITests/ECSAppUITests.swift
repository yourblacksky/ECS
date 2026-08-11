import XCTest

final class ECSAppUITests: XCTestCase {
    private let app = XCUIApplication()

    override func setUpWithError() throws {
        continueAfterFailure = false
        app.launch()
    }

    func testOpeningAdminKeepsRuntimeAlive() throws {
        try ensureRunning()

        let openAdmin = app.buttons["openAdmin"]
        XCTAssertTrue(openAdmin.waitForExistence(timeout: 5))
        XCTAssertTrue(openAdmin.isEnabled)
        openAdmin.tap()

        XCTAssertTrue(app.navigationBars["ECS 后台"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.webViews.firstMatch.waitForExistence(timeout: 5))

        let response = try healthResponse()
        XCTAssertEqual(response.statusCode, 200)

        app.buttons["关闭"].tap()
        XCTAssertTrue(app.navigationBars["ECS iOS"].waitForExistence(timeout: 3))
    }

    func testRuntimeControlsAndLogs() throws {
        try ensureRunning()
        XCTAssertTrue(app.staticTexts["runtimeStatus"].label.contains("运行中"))
        XCTAssertTrue(app.staticTexts.matching(
            NSPredicate(format: "label CONTAINS %@", "20 Tool / 19 Skill")
        ).firstMatch.waitForExistence(timeout: 3))

        app.buttons["runtimeToggle"].tap()
        XCTAssertTrue(app.buttons["runtimeToggle"].waitForLabel("启动 ECS", timeout: 5))
        XCTAssertTrue(app.staticTexts["runtimeStatus"].label.contains("已停止"))

        app.swipeUp()
        XCTAssertTrue(app.staticTexts["宿主日志"].waitForExistence(timeout: 3))
        XCTAssertTrue(app.staticTexts.matching(
            NSPredicate(format: "label CONTAINS %@", "manual")
        ).firstMatch.waitForExistence(timeout: 3))

        app.swipeDown()
        try ensureRunning()
    }

    func testCredentialCopyProducesToken() throws {
        try ensureRunning()
        app.buttons["copyAdminToken"].tap()
        XCTAssertTrue(app.buttons["copyAdminToken"].waitForLabel("已复制", timeout: 2))
    }

    func testSettingsControlsCanBeEditedAndSaved() throws {
        try ensureRunning()

        let baseURL = app.textFields["llmBaseURL"]
        let model = app.textFields["llmModel"]
        XCTAssertTrue(baseURL.waitForExistence(timeout: 3))
        let originalBaseURL = try XCTUnwrap(baseURL.value as? String)
        let originalModel = try XCTUnwrap(model.value as? String)
        replaceText(in: baseURL, with: "https://example.invalid/v1")
        replaceText(in: model, with: "ecs-ui-test-model")

        app.swipeUp()
        let distributed = app.switches["distributedToggle"]
        let autoStart = app.switches["autoStartToggle"]
        XCTAssertTrue(distributed.waitForExistence(timeout: 3))

        let originalDistributed = distributed.value as? String
        let originalAutoStart = autoStart.value as? String
        distributed.tap()
        autoStart.tap()
        app.buttons["saveSettings"].tap()

        XCTAssertTrue(app.staticTexts.matching(
            NSPredicate(format: "label == %@", "配置已保存")
        ).firstMatch.waitForExistence(timeout: 5))

        if distributed.value as? String != originalDistributed {
            distributed.tap()
        }
        if autoStart.value as? String != originalAutoStart {
            autoStart.tap()
        }
        app.swipeDown()
        replaceText(in: baseURL, with: originalBaseURL)
        replaceText(in: model, with: originalModel)
        app.swipeUp()
        app.buttons["saveSettings"].tap()
    }

    func testInstallLLMCredentialsFromEnvironment() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let apiKey = environment["ECS_TEST_LLM_API_KEY"], !apiKey.isEmpty else {
            throw XCTSkip("ECS_TEST_LLM_API_KEY is not set")
        }
        let baseURLValue = environment["ECS_TEST_LLM_BASE_URL"] ?? "https://api.deepseek.com"
        let modelValue = environment["ECS_TEST_LLM_MODEL"] ?? "deepseek-v4-flash"

        let baseURL = app.textFields["llmBaseURL"]
        let model = app.textFields["llmModel"]
        let key = app.secureTextFields["llmAPIKey"]
        XCTAssertTrue(baseURL.waitForExistence(timeout: 3))
        replaceText(in: baseURL, with: baseURLValue)
        replaceText(in: model, with: modelValue)
        replaceText(in: key, with: apiKey)

        app.swipeUp()
        app.buttons["saveSettings"].tap()
        XCTAssertTrue(app.staticTexts.matching(
            NSPredicate(format: "label == %@", "配置已保存")
        ).firstMatch.waitForExistence(timeout: 5))
    }

    private func healthResponse() throws -> HTTPURLResponse {
        let completed = expectation(description: "ECS health response")
        var receivedResponse: HTTPURLResponse?
        var receivedError: Error?
        URLSession.shared.dataTask(with: URL(string: "http://127.0.0.1:18090/healthz")!) {
            _, response, error in
            receivedResponse = response as? HTTPURLResponse
            receivedError = error
            completed.fulfill()
        }.resume()
        wait(for: [completed], timeout: 5)
        if let receivedError {
            throw receivedError
        }
        return try XCTUnwrap(receivedResponse)
    }

    private func ensureRunning() throws {
        let toggle = app.buttons["runtimeToggle"]
        XCTAssertTrue(toggle.waitForExistence(timeout: 5))
        if toggle.label == "启动 ECS" {
            toggle.tap()
        }
        XCTAssertTrue(toggle.waitForLabel("停止 ECS", timeout: 5))
        XCTAssertEqual(try healthResponse().statusCode, 200)
    }

    private func replaceText(in element: XCUIElement, with value: String) {
        element.tap()
        element.typeKey("a", modifierFlags: .command)
        if value.isEmpty {
            element.typeKey(.delete, modifierFlags: [])
        } else {
            element.typeText(value)
        }
    }
}

private extension XCUIElement {
    func waitForLabel(_ expected: String, timeout: TimeInterval) -> Bool {
        XCTWaiter.wait(
            for: [XCTNSPredicateExpectation(
                predicate: NSPredicate(format: "label == %@", expected),
                object: self
            )],
            timeout: timeout
        ) == .completed
    }
}
