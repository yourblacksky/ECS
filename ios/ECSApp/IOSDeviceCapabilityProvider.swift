import AVFoundation
import AudioToolbox
import CoreLocation
import Foundation
import UIKit
import UserNotifications

#if canImport(Mobile)
import Mobile

final class IOSDeviceCapabilityProvider: NSObject,
    MobileDeviceCapabilityProviderProtocol,
    UNUserNotificationCenterDelegate
{
    private let torchLock = NSLock()
    private let location = IOSLocationRequest()

    override init() {
        super.init()
        UNUserNotificationCenter.current().delegate = self
    }

    func availability(_ name: String?) -> String {
        switch name {
        case "flashlight":
            guard torchDevice() != nil else {
                return "hardware_unavailable"
            }
            switch AVCaptureDevice.authorizationStatus(for: .video) {
            case .authorized, .notDetermined:
                return ""
            case .denied, .restricted:
                return "permission_denied"
            @unknown default:
                return "status_unavailable"
            }
        case "vibrate":
            return UIDevice.current.userInterfaceIdiom == .phone
                ? "" : "hardware_unavailable"
        case "beep", "screen_brightness":
            return ""
        case "notify":
            return notificationAvailability()
        case "location":
            guard CLLocationManager.locationServicesEnabled() else {
                return "location_services_disabled"
            }
            switch CLLocationManager.authorizationStatus() {
            case .authorizedAlways, .authorizedWhenInUse, .notDetermined:
                return ""
            case .denied, .restricted:
                return "permission_denied"
            @unknown default:
                return "status_unavailable"
            }
        default:
            return "unsupported_capability"
        }
    }

    func available(_ name: String?) -> Bool {
        availability(name).isEmpty
    }

    func execute(
        _ name: String?,
        argumentsJSON: String?,
        error errorPointer: NSErrorPointer
    ) -> String {
        do {
            let arguments = try decode(argumentsJSON)
            let output: [String: Any]
            switch name {
            case "flashlight":
                output = try executeFlashlight(arguments)
            case "vibrate":
                output = try executeVibration(arguments)
            case "beep":
                output = try executeBeep(arguments)
            case "screen_brightness":
                output = try executeBrightness(arguments)
            case "notify":
                output = try executeNotification(arguments)
            case "location":
                output = try executeLocation(arguments)
            default:
                throw DeviceCapabilityError.unsupported(name ?? "")
            }
            let encoded = try JSONSerialization.data(withJSONObject: output)
            return String(decoding: encoded, as: UTF8.self)
        } catch {
            errorPointer?.pointee = error as NSError
            return ""
        }
    }

    func resetTransientCapabilities() {
        turnOffTorch()
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler:
            @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }

    private func executeFlashlight(_ arguments: [String: Any]) throws -> [String: Any] {
        try requireCameraAuthorization()
        guard let device = torchDevice() else {
            throw DeviceCapabilityError.unavailable("当前设备没有可用的闪光灯")
        }
        guard let action = arguments["action"] as? String else {
            throw DeviceCapabilityError.invalidArguments("action 必须是 on、off 或 status")
        }
        torchLock.lock()
        defer { torchLock.unlock() }
        switch action {
        case "on":
            try setTorch(device, enabled: true)
        case "off":
            try setTorch(device, enabled: false)
        case "status":
            break
        default:
            throw DeviceCapabilityError.invalidArguments("action 必须是 on、off 或 status")
        }
        return [
            "state": device.torchMode == .on ? "on" : "off",
            "available": true
        ]
    }

    private func executeVibration(_ arguments: [String: Any]) throws -> [String: Any] {
        guard let pattern = arguments["pattern"] as? String else {
            throw DeviceCapabilityError.invalidArguments(
                "pattern 必须是 short、long 或 double"
            )
        }
        let repetitions: Int
        switch pattern {
        case "short":
            repetitions = 1
        case "long":
            repetitions = 3
        case "double":
            repetitions = 2
        default:
            throw DeviceCapabilityError.invalidArguments(
                "pattern 必须是 short、long 或 double"
            )
        }
        for index in 0..<repetitions {
            AudioServicesPlaySystemSound(kSystemSoundID_Vibrate)
            if index + 1 < repetitions {
                Thread.sleep(forTimeInterval: pattern == "long" ? 0.35 : 0.22)
            }
        }
        return ["pattern": pattern, "performed": true]
    }

    private func executeBeep(_ arguments: [String: Any]) throws -> [String: Any] {
        guard let count = arguments["count"] as? Int, (1...3).contains(count) else {
            throw DeviceCapabilityError.invalidArguments("count 必须是 1 到 3 的整数")
        }
        let engine = AVAudioEngine()
        let player = AVAudioPlayerNode()
        let format = AVAudioFormat(standardFormatWithSampleRate: 44_100, channels: 1)!
        let frames = AVAudioFrameCount(0.16 * format.sampleRate)
        guard let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frames),
              let samples = buffer.floatChannelData?[0]
        else {
            throw DeviceCapabilityError.unavailable("无法创建蜂鸣音频")
        }
        buffer.frameLength = frames
        for frame in 0..<Int(frames) {
            samples[frame] = Float(sin(2 * .pi * 880 * Double(frame) / format.sampleRate) * 0.22)
        }
        engine.attach(player)
        engine.connect(player, to: engine.mainMixerNode, format: format)
        try AVAudioSession.sharedInstance().setCategory(.ambient)
        try AVAudioSession.sharedInstance().setActive(true)
        try engine.start()
        defer {
            player.stop()
            engine.stop()
            try? AVAudioSession.sharedInstance().setActive(
                false,
                options: .notifyOthersOnDeactivation
            )
        }
        for index in 0..<count {
            player.scheduleBuffer(buffer)
            player.play()
            Thread.sleep(forTimeInterval: index + 1 < count ? 0.28 : 0.18)
        }
        return ["count": count, "performed": true]
    }

    private func executeBrightness(_ arguments: [String: Any]) throws -> [String: Any] {
        guard let action = arguments["action"] as? String else {
            throw DeviceCapabilityError.invalidArguments("action 必须是 get 或 set")
        }
        return try onMain {
            let previous = Double(UIScreen.main.brightness)
            var value = previous
            switch action {
            case "get":
                break
            case "set":
                guard let requested = arguments["value"] as? Double,
                      requested.isFinite,
                      (0...1).contains(requested)
                else {
                    throw DeviceCapabilityError.invalidArguments(
                        "set 操作的 value 必须是 0 到 1"
                    )
                }
                UIScreen.main.brightness = CGFloat(requested)
                value = Double(UIScreen.main.brightness)
            default:
                throw DeviceCapabilityError.invalidArguments("action 必须是 get 或 set")
            }
            return [
                "value": value,
                "previous_value": previous,
                "writable": true
            ]
        }
    }

    private func executeNotification(_ arguments: [String: Any]) throws -> [String: Any] {
        guard let title = (arguments["title"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines),
              let body = (arguments["body"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines),
              !title.isEmpty, title.count <= 80,
              !body.isEmpty, body.count <= 240
        else {
            throw DeviceCapabilityError.invalidArguments(
                "title 必须是 1 到 80 个字符，body 必须是 1 到 240 个字符"
            )
        }
        try requireNotificationAuthorization()
        let identifier = UUID().uuidString
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default
        let semaphore = DispatchSemaphore(value: 0)
        var deliveryError: Error?
        UNUserNotificationCenter.current().add(
            UNNotificationRequest(identifier: identifier, content: content, trigger: nil)
        ) { error in
            deliveryError = error
            semaphore.signal()
        }
        guard semaphore.wait(timeout: .now() + 5) == .success else {
            throw DeviceCapabilityError.timedOut("发送通知超时")
        }
        if let deliveryError {
            throw deliveryError
        }
        return ["delivered": true, "id": identifier]
    }

    private func executeLocation(_ arguments: [String: Any]) throws -> [String: Any] {
        guard let accuracy = arguments["accuracy"] as? String,
              accuracy == "coarse" || accuracy == "precise"
        else {
            throw DeviceCapabilityError.invalidArguments(
                "accuracy 必须是 coarse 或 precise"
            )
        }
        let value = try location.current(precise: accuracy == "precise")
        return [
            "latitude": value.coordinate.latitude,
            "longitude": value.coordinate.longitude,
            "accuracy_m": value.horizontalAccuracy,
            "timestamp_ms": value.timestamp.timeIntervalSince1970 * 1_000,
            "provider": "core-location",
            "requested_accuracy": accuracy
        ]
    }

    private func decode(_ argumentsJSON: String?) throws -> [String: Any] {
        let data = Data((argumentsJSON ?? "{}").utf8)
        guard let value = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw DeviceCapabilityError.invalidArguments("参数必须是 JSON 对象")
        }
        return value
    }

    private func requireCameraAuthorization() throws {
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized:
            return
        case .notDetermined:
            let semaphore = DispatchSemaphore(value: 0)
            var granted = false
            AVCaptureDevice.requestAccess(for: .video) {
                granted = $0
                semaphore.signal()
            }
            guard semaphore.wait(timeout: .now() + 15) == .success, granted else {
                throw DeviceCapabilityError.permissionDenied(
                    "相机权限未授权，无法控制手电筒"
                )
            }
        default:
            throw DeviceCapabilityError.permissionDenied(
                "相机权限未授权，无法控制手电筒"
            )
        }
    }

    private func requireNotificationAuthorization() throws {
        let center = UNUserNotificationCenter.current()
        let settingsSemaphore = DispatchSemaphore(value: 0)
        var status: UNAuthorizationStatus = .notDetermined
        center.getNotificationSettings {
            status = $0.authorizationStatus
            settingsSemaphore.signal()
        }
        guard settingsSemaphore.wait(timeout: .now() + 5) == .success else {
            throw DeviceCapabilityError.timedOut("读取通知权限超时")
        }
        if status == .authorized || status == .provisional {
            return
        }
        guard status == .notDetermined else {
            throw DeviceCapabilityError.permissionDenied("通知权限未授权")
        }
        let requestSemaphore = DispatchSemaphore(value: 0)
        var granted = false
        center.requestAuthorization(options: [.alert, .sound]) { allowed, _ in
            granted = allowed
            requestSemaphore.signal()
        }
        guard requestSemaphore.wait(timeout: .now() + 15) == .success, granted else {
            throw DeviceCapabilityError.permissionDenied("通知权限未授权")
        }
    }

    private func notificationAvailability() -> String {
        let semaphore = DispatchSemaphore(value: 0)
        var status: UNAuthorizationStatus?
        UNUserNotificationCenter.current().getNotificationSettings {
            status = $0.authorizationStatus
            semaphore.signal()
        }
        guard semaphore.wait(timeout: .now() + 2) == .success,
              let status
        else {
            return "status_unavailable"
        }
        switch status {
        case .authorized, .provisional, .ephemeral, .notDetermined:
            return ""
        case .denied:
            return "permission_denied"
        @unknown default:
            return "status_unavailable"
        }
    }

    private func turnOffTorch() {
        guard let device = torchDevice() else {
            return
        }
        torchLock.lock()
        defer { torchLock.unlock() }
        try? setTorch(device, enabled: false)
    }

    private func setTorch(_ device: AVCaptureDevice, enabled: Bool) throws {
        try device.lockForConfiguration()
        defer { device.unlockForConfiguration() }
        if enabled {
            try device.setTorchModeOn(level: AVCaptureDevice.maxAvailableTorchLevel)
        } else {
            device.torchMode = .off
        }
    }

    private func torchDevice() -> AVCaptureDevice? {
        AVCaptureDevice.DiscoverySession(
            deviceTypes: [.builtInWideAngleCamera],
            mediaType: .video,
            position: .back
        ).devices.first(where: { $0.hasTorch && $0.isTorchAvailable })
    }

    private func onMain<T>(_ operation: @escaping () throws -> T) throws -> T {
        if Thread.isMainThread {
            return try operation()
        }
        return try DispatchQueue.main.sync(execute: operation)
    }

    enum DeviceCapabilityError: LocalizedError {
        case unsupported(String)
        case permissionDenied(String)
        case unavailable(String)
        case invalidArguments(String)
        case timedOut(String)

        var errorDescription: String? {
            switch self {
            case .unsupported(let name):
                return "不支持的设备能力：\(name)"
            case .permissionDenied(let message),
                 .unavailable(let message),
                 .invalidArguments(let message),
                 .timedOut(let message):
                return message
            }
        }
    }
}

private final class IOSLocationRequest: NSObject, CLLocationManagerDelegate {
    private let stateLock = NSLock()
    private var manager: CLLocationManager?
    private var semaphore: DispatchSemaphore?
    private var result: Result<CLLocation, Error>?

    func current(precise: Bool) throws -> CLLocation {
        stateLock.lock()
        guard semaphore == nil else {
            stateLock.unlock()
            throw LocationError.inProgress
        }
        let pending = DispatchSemaphore(value: 0)
        semaphore = pending
        result = nil
        stateLock.unlock()

        DispatchQueue.main.async {
            let manager = CLLocationManager()
            self.manager = manager
            manager.delegate = self
            manager.desiredAccuracy = precise ? kCLLocationAccuracyBest : kCLLocationAccuracyKilometer
            switch manager.authorizationStatus {
            case .authorizedAlways, .authorizedWhenInUse:
                manager.requestLocation()
            case .notDetermined:
                manager.requestWhenInUseAuthorization()
            default:
                self.finish(.failure(LocationError.permissionDenied))
            }
        }

        if pending.wait(timeout: .now() + 12) != .success {
            finish(.failure(LocationError.timedOut))
        }
        stateLock.lock()
        let completed = result ?? .failure(LocationError.timedOut)
        stateLock.unlock()

        let cleanup = {
            self.manager?.delegate = nil
            self.manager?.stopUpdatingLocation()
            self.manager = nil
        }
        if Thread.isMainThread {
            cleanup()
        } else {
            DispatchQueue.main.sync(execute: cleanup)
        }

        stateLock.lock()
        semaphore = nil
        result = nil
        stateLock.unlock()
        return try completed.get()
    }

    func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
        guard manager === self.manager else {
            return
        }
        switch manager.authorizationStatus {
        case .authorizedAlways, .authorizedWhenInUse:
            manager.requestLocation()
        case .denied, .restricted:
            finish(.failure(LocationError.permissionDenied))
        default:
            break
        }
    }

    func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
        guard manager === self.manager else {
            return
        }
        guard let location = locations.last else {
            finish(.failure(LocationError.unavailable))
            return
        }
        finish(.success(location))
    }

    func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) {
        guard manager === self.manager else {
            return
        }
        finish(.failure(error))
    }

    private func finish(_ value: Result<CLLocation, Error>) {
        stateLock.lock()
        guard result == nil else {
            stateLock.unlock()
            return
        }
        result = value
        let pending = semaphore
        stateLock.unlock()
        pending?.signal()
    }

    enum LocationError: LocalizedError {
        case inProgress
        case permissionDenied
        case timedOut
        case unavailable

        var errorDescription: String? {
            switch self {
            case .inProgress:
                return "已有定位请求正在执行"
            case .permissionDenied:
                return "定位权限未授权"
            case .timedOut:
                return "12 秒内未获得定位结果"
            case .unavailable:
                return "系统未返回定位结果"
            }
        }
    }
}
#else
final class IOSDeviceCapabilityProvider {
    func resetTransientCapabilities() {}
}
#endif
