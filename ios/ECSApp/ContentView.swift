import SwiftUI
import UIKit

struct ContentView: View {
    @EnvironmentObject private var runtime: EcsRuntimeController
    @State private var draft = EcsSettings.load()
    @State private var apiKey = ""
    @State private var pairingCode = ""
    @State private var showAdmin = false
    @State private var tokenCopied = false

    var body: some View {
        NavigationStack {
            ZStack {
                EcsPalette.background
                    .ignoresSafeArea()
                ScrollView {
                    LazyVStack(spacing: 16) {
                        statusHero
                        actionsPanel
                        modelPanel
                        networkPanel
                        logsPanel
                    }
                    .padding(.horizontal, 18)
                    .padding(.vertical, 16)
                }
            }
            .navigationTitle("ECS iOS")
            .navigationBarTitleDisplayMode(.inline)
            .toolbarBackground(EcsPalette.rail.opacity(0.96), for: .navigationBar)
            .toolbarBackground(.visible, for: .navigationBar)
            .onAppear {
                draft = runtime.settings
                apiKey = runtime.storedAPIKey()
                pairingCode = runtime.storedParentPairingCode()
            }
            .alert(
                "ECS 错误",
                isPresented: Binding(
                    get: { runtime.presentedError != nil },
                    set: { if !$0 { runtime.presentedError = nil } }
                )
            ) {
                Button("好") {
                    runtime.presentedError = nil
                }
            } message: {
                Text(runtime.presentedError ?? "")
            }
            .sheet(isPresented: $showAdmin) {
                NavigationStack {
                    WebAdminView(url: runtime.adminURL, adminToken: runtime.adminToken())
                        .navigationTitle("ECS 后台")
                        .navigationBarTitleDisplayMode(.inline)
                        .toolbar {
                            ToolbarItem(placement: .confirmationAction) {
                                Button("关闭") {
                                    showAdmin = false
                                }
                            }
                        }
                }
            }
        }
        .tint(EcsPalette.flow)
        .preferredColorScheme(.dark)
    }

    private var statusHero: some View {
        VStack(alignment: .leading, spacing: 17) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("FOREGROUND LEAF NODE")
                        .font(.system(size: 10, weight: .semibold, design: .monospaced))
                        .tracking(1.4)
                        .foregroundStyle(EcsPalette.muted)
                    Text(runtime.status.node.isEmpty ? "iPhone ECS" : runtime.status.node)
                        .font(.system(size: 28, weight: .bold, design: .rounded))
                        .foregroundStyle(EcsPalette.text)
                }
                Spacer()
                HStack(spacing: 7) {
                    Circle()
                        .fill(runtime.status.running ? EcsPalette.flow : EcsPalette.wait)
                        .frame(width: 8, height: 8)
                    Text(runtime.status.running ? "运行中" : "已停止")
                        .accessibilityIdentifier("runtimeStatus")
                }
                .font(.system(size: 12, weight: .semibold, design: .rounded))
                .foregroundStyle(runtime.status.running ? EcsPalette.flow : EcsPalette.wait)
                .padding(.horizontal, 11)
                .padding(.vertical, 8)
                .background(
                    Capsule()
                        .fill((runtime.status.running ? EcsPalette.flow : EcsPalette.wait).opacity(0.1))
                )
                .overlay(
                    Capsule()
                        .stroke((runtime.status.running ? EcsPalette.flow : EcsPalette.wait).opacity(0.35))
                )
            }

            FlowRoute()

            if !runtime.status.node.isEmpty {
                HStack(spacing: 10) {
                    metric("能力", "\(runtime.status.tools) Tool / \(runtime.status.skills) Skill")
                    metric("地址", runtime.status.address)
                }
            } else {
                Text("节点尚未监听。启动后，本机设备能力会进入 ECS 网络。")
                    .font(.footnote)
                    .foregroundStyle(EcsPalette.muted)
            }
        }
        .padding(18)
        .ecsPanel(emphasis: true)
    }

    private var actionsPanel: some View {
        VStack(spacing: 11) {
            HStack(spacing: 10) {
                Button {
                    Task {
                        if runtime.status.running {
                            await runtime.stop()
                        } else {
                            await runtime.start()
                        }
                    }
                } label: {
                    Label(
                        runtime.status.running ? "停止 ECS" : "启动 ECS",
                        systemImage: runtime.status.running ? "stop.fill" : "play.fill"
                    )
                    .frame(maxWidth: .infinity)
                }
                .accessibilityIdentifier("runtimeToggle")
                .buttonStyle(EcsActionButtonStyle(primary: true))
                .disabled(runtime.busy)

                Button {
                    showAdmin = true
                } label: {
                    Label("打开后台", systemImage: "rectangle.connected.to.line.below")
                        .frame(maxWidth: .infinity)
                }
                .accessibilityIdentifier("openAdmin")
                .buttonStyle(EcsActionButtonStyle())
                .disabled(!runtime.status.running)
            }

            Button {
                UIPasteboard.general.string = runtime.adminToken()
                tokenCopied = true
                Task { @MainActor in
                    try? await Task.sleep(for: .seconds(1.5))
                    tokenCopied = false
                }
            } label: {
                Label(
                    tokenCopied ? "已复制" : "复制管理凭证",
                    systemImage: tokenCopied ? "checkmark" : "key"
                )
                .frame(maxWidth: .infinity)
            }
            .accessibilityIdentifier("copyAdminToken")
            .buttonStyle(EcsActionButtonStyle())
        }
        .padding(14)
        .ecsPanel()
    }

    private var modelPanel: some View {
        EcsSection(
            eyebrow: "MODEL ENDPOINT",
            title: "模型连接",
            detail: "模型配置保留在本机；节点可使用本地模型，也可连接远程兼容接口。"
        ) {
            VStack(spacing: 10) {
                TextField("LLM Base URL", text: $draft.baseURL)
                    .textInputAutocapitalization(.never)
                    .keyboardType(.URL)
                    .accessibilityIdentifier("llmBaseURL")
                    .ecsField()
                TextField("模型名称", text: $draft.model)
                    .textInputAutocapitalization(.never)
                    .accessibilityIdentifier("llmModel")
                    .ecsField()
                SecureField("API Key（保存在 Keychain）", text: $apiKey)
                    .textInputAutocapitalization(.never)
                    .accessibilityIdentifier("llmAPIKey")
                    .ecsField()
            }
        }
    }

    private var networkPanel: some View {
        EcsSection(
            eyebrow: "LOCAL FABRIC",
            title: "局域网关系",
            detail: "自动发现周围 ECS，并以 leaf 身份提交加入申请。"
        ) {
            VStack(spacing: 13) {
                Toggle("自动发现并申请加入", isOn: $draft.distributed)
                    .accessibilityIdentifier("distributedToggle")
                Divider().overlay(EcsPalette.line)
                Toggle("应用进入前台时自动启动", isOn: $draft.autoStart)
                    .accessibilityIdentifier("autoStartToggle")
                SecureField("父节点配对码（可选）", text: $pairingCode)
                    .textInputAutocapitalization(.never)
                    .accessibilityIdentifier("parentPairingCode")
                    .ecsField()
                Button("保存配置") {
                    Task {
                        await runtime.save(
                            settings: draft,
                            apiKey: apiKey,
                            parentPairingCode: pairingCode
                        )
                    }
                }
                .accessibilityIdentifier("saveSettings")
                .buttonStyle(EcsActionButtonStyle(primary: true))
                .disabled(
                    draft.baseURL.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ||
                    draft.model.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                )
            }
        }
    }

    private var logsPanel: some View {
        EcsSection(
            eyebrow: "HOST EVENT STREAM",
            title: "宿主日志",
            detail: "最近 20 条移动宿主事件。"
        ) {
            VStack(alignment: .leading, spacing: 0) {
                if runtime.logs.isEmpty {
                    Text("暂无日志。启动节点后，这里会记录宿主事件。")
                        .font(.footnote)
                        .foregroundStyle(EcsPalette.muted)
                        .padding(.vertical, 8)
                } else {
                    ForEach(runtime.logs.prefix(20)) { entry in
                        VStack(alignment: .leading, spacing: 5) {
                            Text(entry.message)
                                .font(.system(size: 13, design: .rounded))
                                .foregroundStyle(entry.isError ? EcsPalette.fault : EcsPalette.text)
                            Text(entry.date.formatted(date: .omitted, time: .standard))
                                .font(.system(size: 10, design: .monospaced))
                                .foregroundStyle(EcsPalette.muted)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.vertical, 10)
                        Divider().overlay(EcsPalette.line)
                    }
                }
            }
        }
        .accessibilityIdentifier("hostLogs")
    }

    private func metric(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(label)
                .font(.caption2)
                .foregroundStyle(EcsPalette.muted)
            Text(value)
                .font(.system(size: 11, weight: .medium, design: .monospaced))
                .lineLimit(2)
                .minimumScaleFactor(0.75)
                .foregroundStyle(EcsPalette.text)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(11)
        .background(EcsPalette.field, in: RoundedRectangle(cornerRadius: 12))
    }
}

private struct FlowRoute: View {
    var body: some View {
        HStack(spacing: 0) {
            routeNode("设备", color: EcsPalette.flow)
            routeLine
            routeNode("节点", color: EcsPalette.flow)
            routeLine
            routeNode("网络", color: EcsPalette.blue)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("设备能力通过本机节点接入 ECS 网络")
    }

    private func routeNode(_ title: String, color: Color) -> some View {
        VStack(spacing: 6) {
            Circle()
                .fill(EcsPalette.panel)
                .frame(width: 13, height: 13)
                .overlay(Circle().stroke(color, lineWidth: 2))
                .shadow(color: color.opacity(0.4), radius: 6)
            Text(title)
                .font(.system(size: 9, weight: .medium, design: .monospaced))
                .foregroundStyle(EcsPalette.muted)
        }
    }

    private var routeLine: some View {
        Rectangle()
            .fill(
                LinearGradient(
                    colors: [EcsPalette.flow, EcsPalette.blue],
                    startPoint: .leading,
                    endPoint: .trailing
                )
            )
            .frame(height: 1)
            .padding(.horizontal, 7)
            .padding(.bottom, 17)
    }
}

private struct EcsSection<Content: View>: View {
    let eyebrow: String
    let title: String
    let detail: String
    let content: Content

    init(
        eyebrow: String,
        title: String,
        detail: String,
        @ViewBuilder content: () -> Content
    ) {
        self.eyebrow = eyebrow
        self.title = title
        self.detail = detail
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 15) {
            VStack(alignment: .leading, spacing: 5) {
                Text(eyebrow)
                    .font(.system(size: 10, weight: .semibold, design: .monospaced))
                    .tracking(1.2)
                    .foregroundStyle(EcsPalette.muted)
                Text(title)
                    .font(.system(size: 20, weight: .bold, design: .rounded))
                    .foregroundStyle(EcsPalette.text)
                Text(detail)
                    .font(.footnote)
                    .foregroundStyle(EcsPalette.muted)
            }
            content
        }
        .padding(16)
        .ecsPanel()
    }
}

private struct EcsActionButtonStyle: ButtonStyle {
    var primary = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 13, weight: .semibold, design: .rounded))
            .foregroundStyle(primary ? EcsPalette.background : EcsPalette.text)
            .padding(.horizontal, 12)
            .frame(minHeight: 46)
            .background(
                RoundedRectangle(cornerRadius: 14)
                    .fill(primary ? EcsPalette.flow : EcsPalette.panel2)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 14)
                    .stroke(primary ? EcsPalette.flow : EcsPalette.line)
            )
            .opacity(configuration.isPressed ? 0.72 : 1)
            .scaleEffect(configuration.isPressed ? 0.985 : 1)
    }
}

private struct EcsFieldModifier: ViewModifier {
    func body(content: Content) -> some View {
        content
            .font(.system(size: 14, design: .rounded))
            .padding(.horizontal, 13)
            .frame(minHeight: 46)
            .background(EcsPalette.field, in: RoundedRectangle(cornerRadius: 12))
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .stroke(EcsPalette.line)
            )
    }
}

private struct EcsPanelModifier: ViewModifier {
    let emphasis: Bool

    func body(content: Content) -> some View {
        content
            .background(
                LinearGradient(
                    colors: emphasis
                        ? [EcsPalette.panel2, EcsPalette.panel]
                        : [EcsPalette.panel, EcsPalette.rail],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                ),
                in: RoundedRectangle(cornerRadius: 20)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 20)
                    .stroke(emphasis ? EcsPalette.flow.opacity(0.28) : EcsPalette.line)
            )
    }
}

private extension View {
    func ecsField() -> some View {
        modifier(EcsFieldModifier())
    }

    func ecsPanel(emphasis: Bool = false) -> some View {
        modifier(EcsPanelModifier(emphasis: emphasis))
    }
}

private enum EcsPalette {
    static let background = Color(red: 7 / 255, green: 18 / 255, blue: 37 / 255)
    static let rail = Color(red: 9 / 255, green: 26 / 255, blue: 49 / 255)
    static let panel = Color(red: 16 / 255, green: 39 / 255, blue: 67 / 255)
    static let panel2 = Color(red: 21 / 255, green: 51 / 255, blue: 83 / 255)
    static let field = Color(red: 9 / 255, green: 27 / 255, blue: 51 / 255)
    static let line = Color(red: 49 / 255, green: 81 / 255, blue: 110 / 255)
    static let text = Color(red: 244 / 255, green: 248 / 255, blue: 247 / 255)
    static let muted = Color(red: 155 / 255, green: 176 / 255, blue: 194 / 255)
    static let flow = Color(red: 71 / 255, green: 226 / 255, blue: 206 / 255)
    static let wait = Color(red: 243 / 255, green: 201 / 255, blue: 105 / 255)
    static let fault = Color(red: 255 / 255, green: 122 / 255, blue: 110 / 255)
    static let blue = Color(red: 123 / 255, green: 183 / 255, blue: 255 / 255)
}
