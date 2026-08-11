import SwiftUI
import WebKit

struct WebAdminView: UIViewRepresentable {
    let url: URL
    let adminToken: String

    func makeCoordinator() -> Coordinator {
        Coordinator(adminToken: adminToken)
    }

    func makeUIView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .default()
        let view = WKWebView(frame: .zero, configuration: configuration)
        view.navigationDelegate = context.coordinator
        view.load(URLRequest(url: url))
        return view
    }

    func updateUIView(_ uiView: WKWebView, context: Context) {
        context.coordinator.adminToken = adminToken
    }

    final class Coordinator: NSObject, WKNavigationDelegate {
        var adminToken: String

        init(adminToken: String) {
            self.adminToken = adminToken
        }

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation?) {
            guard
                let tokenData = try? JSONSerialization.data(
                    withJSONObject: adminToken,
                    options: .fragmentsAllowed
                ),
                let token = String(data: tokenData, encoding: .utf8)
            else {
                return
            }
            webView.evaluateJavaScript(
                """
                if (sessionStorage.getItem('ecs-admin-token') !== \(token)) {
                  sessionStorage.setItem('ecs-admin-token', \(token));
                  location.reload();
                }
                """
            )
        }
    }
}
