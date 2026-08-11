# ECS HarmonyOS

这是 ECS 的 HarmonyOS 原生节点，使用 ArkTS、ArkUI、Stage 模型和内嵌 Go Runtime
实现。应用启动时会在自己的进程内启动 `ecs-harmony`，加载随 HAP 打包的 Manifest、
Tool、Skill、Agent、Pipeline 与 Office Stage，并在前台监听 `0.0.0.0:18090`。

当前节点提供：

- ECS `/healthz` 与 `/v1/capabilities` 协议接口；
- 随当前仓库打包的 Tool、Skill、Agent、Pipeline 与 Office Component；
- 会议提取、证据核验、人工审批、执行回执、补偿与恢复等 Office 能力；
- 原生节点状态页，以及在 ArkWeb 中打开的本机管理后台；
- 通过 NetworkKit 注册和浏览 `_ecs._tcp` mDNS / DNS-SD 服务；
- `harmonyos` 平台身份，可作为支持关系鉴权与入站连接的前台 peer 参与 ECS 网络。

节点的生命周期跟随应用：应用在前台运行时提供服务，退出应用后不承诺后台常驻。
首次进行模型对话前，在节点首页填写 DeepSeek API Key。密钥由 HarmonyOS Asset Store
保存，并仅以内存 Secret 传给 Go Runtime；不会写入 `config.json`、日志或管理接口。
应用还会在 Asset Store 中生成独立的后台管理口令；内嵌 ArkWeb 会自动注入口令，局域网
节点只能通过 ECS 加入、配对和关系凭证访问获准能力。

## 环境

- DevEco Studio 6.0.2 或更新版本；
- HarmonyOS SDK 6.0.2（API 22）；
- macOS、Windows 或鸿蒙电脑上的 DevEco Studio；
- 构建内嵌 Runtime 时需要 Git，以及 ECS 固定的 Go 1.25.12 OpenHarmony 私有 fork
  访问权限。

当前仓库采用 DevEco CLI 1.2.0-stable 的 Empty Ability 工程结构。

## 构建

先在仓库根目录构建 HarmonyOS ARM64 Go Runtime：

```bash
./scripts/build-harmony-runtime.sh
```

脚本默认从 `yourblacksky/ohos_golang_go` 的固定提交获取 Go 1.25.12 工具链并生成
`entry/src/main/libs/arm64-v8a/libecs_go.so`。该 fork 将 OpenHarmony SIG 的 ARM64
目标和动态 TLS 支持移植到 Go 1.25.12，避免 Go 根据 `go.mod` 自动回退到不支持
`openharmony/arm64` 的官方工具链。可通过 `OHOS_GO_ROOT` 复用兼容工具链，通过
`HARMONY_NATIVE_SDK` 指定 Native SDK；自定义工具链同样必须报告 `go1.25.12` 并支持
`openharmony/arm64`，且需要包含 `toolchain/openharmony-go-cshared-argv.patch` 所示的
c-shared 启动修复。脚本会自动修补托管缓存，并对未修复的自定义工具链拒绝构建。

随后在 DevEco Studio 中打开 `harmonyos/`，等待 SDK 与 ohpm 同步后运行 `entry` 模块；
也可以在 DevEco Studio Terminal 中执行：

```bash
hvigorw --mode module \
  -p product=default \
  -p module=entry@default \
  -p buildMode=debug \
  assembleHap
```

HAP 位于 `entry/build/default/outputs/default/`。生成的 Go 动态库、头文件、Runtime
资源和 DevEco 构建目录都不提交到仓库。

## 运行边界

- 当前是前台 ECS peer，支持 NetworkKit mDNS 发现和入站协议，但不承诺后台常驻；
- 局域网节点只能发现公开身份；跨设备执行仍需按 ECS 加入、配对、Scope 和关系凭证授权；
- 除 NetworkKit mDNS 发现外，不向 Tool/Skill 提供任意多播、shell、子进程、推送唤醒或
  其他平台未授权能力；
- DeepSeek 等模型凭证必须通过运行环境安全注入，不能写入仓库；
- DevEco 自动签名、模拟器镜像和真机证书属于本机状态，不提交到仓库。
