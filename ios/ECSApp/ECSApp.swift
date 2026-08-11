import SwiftUI

@main
struct ECSApp: App {
    @Environment(\.scenePhase) private var scenePhase
    @StateObject private var runtime = EcsRuntimeController()

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(runtime)
                .task {
                    await runtime.startIfConfigured()
                }
        }
        .onChange(of: scenePhase) { phase in
            Task {
                await runtime.handleScenePhase(phase)
            }
        }
    }
}
