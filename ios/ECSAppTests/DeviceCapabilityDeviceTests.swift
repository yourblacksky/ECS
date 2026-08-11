import XCTest
@testable import ECS

final class DeviceCapabilityDeviceTests: XCTestCase {
    private var provider: IOSDeviceCapabilityProvider!

    override func setUp() {
        super.setUp()
        provider = IOSDeviceCapabilityProvider()
    }

    override func tearDown() {
        provider.resetTransientCapabilities()
        provider = nil
        super.tearDown()
    }

    func testAllAvailableDeviceCapabilitiesExecute() throws {
        if provider.available("flashlight") {
            XCTAssertEqual(
                try execute("flashlight", #"{"action":"on"}"#)["state"] as? String,
                "on"
            )
            XCTAssertEqual(
                try execute("flashlight", #"{"action":"off"}"#)["state"] as? String,
                "off"
            )
        }

        XCTAssertEqual(
            try execute("vibrate", #"{"pattern":"double"}"#)["performed"] as? Bool,
            true
        )
        XCTAssertEqual(try execute("beep", #"{"count":2}"#)["count"] as? Int, 2)

        let brightness = try execute("screen_brightness", #"{"action":"get"}"#)
        let brightnessValue = try XCTUnwrap(brightness["value"] as? Double)
        XCTAssertTrue((0...1).contains(brightnessValue))

        XCTAssertEqual(
            try execute(
                "notify",
                #"{"title":"ECS 真机测试","body":"设备能力调用成功"}"#
            )["delivered"] as? Bool,
            true
        )

        let location = try execute("location", #"{"accuracy":"coarse"}"#)
        let latitude = try XCTUnwrap(location["latitude"] as? Double)
        let longitude = try XCTUnwrap(location["longitude"] as? Double)
        XCTAssertTrue((-90...90).contains(latitude))
        XCTAssertTrue((-180...180).contains(longitude))
    }

    private func execute(_ name: String, _ arguments: String) throws -> [String: Any] {
        let completed = expectation(description: name)
        var output = ""
        var executionError: NSError?
        DispatchQueue.global(qos: .userInitiated).async {
            var error: NSError?
            output = self.provider.execute(name, argumentsJSON: arguments, error: &error)
            executionError = error
            completed.fulfill()
        }
        wait(for: [completed], timeout: 25)
        if let executionError {
            throw executionError
        }
        let data = Data(output.utf8)
        return try XCTUnwrap(
            JSONSerialization.jsonObject(with: data) as? [String: Any]
        )
    }
}
