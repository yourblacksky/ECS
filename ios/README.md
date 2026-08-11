# ECS iOS

iOS 宿主把共享 Go 内核打包为 XCFramework，并由 SwiftUI App 驱动。它可以在应用处于
前台时调用远程 OpenAI-compatible LLM、执行通用 Skill 和六个移动设备 Skill、接受
HTTP 请求、通过 Bonjour 发现和公告局域网 ECS，以及打开本机管理后台。

当前宿主已完成 iOS 模拟器与真机的编译、安装、启动、`/healthz` 和 Bonjour 公告验证。

iOS 普通应用进入后台后可能随时被系统挂起，因此当前节点固定声明为 `leaf`：

- `supports_inbound=true` 仅代表应用活跃时可以接收请求；
- `accepts_children=false`，不会被自动选为递归父节点；
- 进入后台时停止 ECS，回到前台后按之前的运行状态恢复；
- 不执行外部进程，Tool 使用 Go 内置实现。
- `DeviceCapabilityProvider` 统一提供手电筒、震动、蜂鸣、屏幕亮度、通知和一次性定位；
- 手电筒通过 AVFoundation 执行；模拟器没有闪光灯，只能在真机验证；
- ECS 停止或 App 进入后台时会自动尝试关闭手电筒。

## 环境准备

- macOS 与完整 Xcode，并通过 `xcode-select` 选中；
- Go，以及仓库固定版本的 gomobile 工具；
- XcodeGen：`brew install xcodegen`；
- 已登录 Xcode 的 Apple ID / 开发团队；
- 真机已开启开发者模式并信任当前 Mac。

## 构建与安装

在仓库根目录执行：

```bash
make mobile-xcframework
make ios-project
open ios/ECSApp.xcodeproj
```

然后在 Xcode 中：

1. 选择 `ECSApp` target；
2. 在 Signing & Capabilities 中选择自己的 Team；
3. 选择已连接的 iPhone；
4. 点击 Run 安装并启动。

首次启动需要允许“本地网络”权限，否则 Bonjour 和局域网访问不可用。相机、通知和定位
权限在对应 Skill 首次执行时由系统询问。模拟器适合验证界面与基本启动，手电筒、震动、
Bonjour、Local Network 权限和真实网络生命周期应以真机为准。

## 配置与凭证

`ECSApp/Resources/config.json` 是无密钥模板。LLM API Key 与管理 Token 在应用设置中
填写并保存到 iOS Keychain，运行时只通过内存传给 Go；不要把真实凭证写回资源文件。

App 内可以修改节点名称、监听地址和 LLM Endpoint。若需要让局域网其他设备访问，应使用
非回环监听地址，并遵循 ECS 的节点关系审批与 Scope 规则。

## 本地产物

以下内容由本机构建生成，已被 Git 忽略：

- `ios/Frameworks/Mobile.xcframework/`
- `ios/ECSApp.xcodeproj/`
- `ios/DerivedData*/`
- Apple 开发团队、证书与 Provisioning Profile

可移植的工程定义以 `ios/project.yml` 和 `ios/ECSApp/` 源码为准。
