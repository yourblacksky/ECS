# 手动多平台打包 CI

仓库的 `Manual Multiplatform Package` 工作流只通过 GitHub Actions 的
`workflow_dispatch` 手动触发。它执行完整 Go 测试和交付内容检查，然后生成一个经过筛选的 ZIP：

```text
ECS-package/
├── ECS-source/                 # 当前提交中适合评审的源码与文档
├── demo/                       # 打开 ZIP 后可直接查看的路演材料
│   ├── README.md               # 推荐查看顺序与证据索引
│   ├── *.pptx / *.pdf / *.mp4  # 正式 PPT/PDF 与 58 秒 Demo 视频
│   ├── images/                 # 五张独立运行证据图片
│   └── reports/                # 会议闭环、AgentTeams 与小模型报告
├── builds/
│   ├── android/                # ABI APK、universal APK、ecs-mobile.aar
│   ├── ios/                    # 未签名模拟器 App、Mobile.xcframework
│   ├── harmonyos/              # 未签名 debug HAP、ARM64 Go Runtime
│   ├── windows/                # amd64/arm64 ecs.exe 与运行时资源
│   └── linux/                  # ARMv6/ARM64 ecs 与共享运行时资源
├── evidence/
│   ├── meeting-followup/       # 同一提交生成的成功/失败运行证据
│   ├── agentteams-office/      # 同一提交现场生成的四阶段 AgentTeams E2E
│   ├── qwen3-1.7b-live/        # 1.7B 双节点真实模型证据
│   └── qwen3-0.6b-live/        # 0.6B 双节点真实模型证据
├── BUILD-MANIFEST.txt
└── SHA256SUMS
```

最终 ZIP 和它的 SHA-256 文件上传为 Actions Artifact，默认保留 7 天；触发时可以选择
3、7、14 或 30 天。工作流不会创建 GitHub Release。

## 重建初赛 PPT 与 PDF

PPTX 和优先阅读的 PDF 由同一入口生成。先在临时虚拟环境安装固定依赖，再使用仓库固定的
LibreOfficeDev 版本转换；脚本会机械检查两份产物均为 19 页：

```bash
python3 -m venv /tmp/ecs-goai-deck-venv
/tmp/ecs-goai-deck-venv/bin/pip install -r scripts/requirements-goai-deck.txt
/tmp/ecs-goai-deck-venv/bin/python scripts/build-goai-preliminary-deck.py
```

锁文件固定 `Pillow 12.2.0`、`python-pptx 1.0.2`、`pypdf 6.15.0` 及其完整传递依赖闭包
（`lxml 6.1.1`、`XlsxWriter 3.2.9`、`typing_extensions 4.16.0`），并固定
`LibreOfficeDev 26.8.0.0.alpha0`；显式迁移 LibreOffice 时可用
`ECS_GOAI_SOFFICE_VERSION` 指定完整 `soffice --version` 输出，并同步更新生成物。
定版 PDF 还要求 macOS 系统字体 `Hiragino Sans GB` 的固定文件摘要；脚本同时写入 latin/eastAsia
字体声明，并在导出后拒绝已知替代字体，避免中文换行随 Runner 字体环境漂移。

## 为什么使用自托管 macOS Runner

Android、Windows 和 iOS 可以使用 GitHub 托管 Runner 构建，但 HarmonyOS HAP 需要
DevEco Studio、HarmonyOS SDK 和 Native SDK。GitHub 托管的 macOS Runner 不提供这些
工具。为了保证四个平台来自同一个提交，并且 HarmonyOS 产物是真实 HAP，工作流要求：

```text
self-hosted, macOS, ecs-release
```

Runner 需要安装并配置：

- Xcode，并通过 `xcode-select` 选中完整 Xcode；
- XcodeGen；
- Android SDK 36、NDK 与 JDK 17；
- DevEco Studio 6.0.2+ 与 HarmonyOS SDK API 22；
- Go 工具链；工作流会固定使用 Go 1.25.12。

DevEco Studio 不在默认位置时，给 Runner 设置 `DEVECO_STUDIO_HOME`，值应指向应用的
`Contents` 目录。Android SDK 不在默认位置时设置 `ANDROID_HOME` 或
`ANDROID_SDK_ROOT`。

在仓库 `Settings → Actions → Runners` 注册 macOS Runner，并添加 `ecs-release` 标签。
之后进入 `Actions → Manual Multiplatform Package → Run workflow`，在 `main` 分支选择
保留天数即可。
工作流拒绝从其他分支执行，避免未经合并的工作流代码进入自托管发布机器。

## 产物边界

- Android 使用 debug 签名，适合安装评审，不是商店发布包；
- Android APK 不嵌入模型 API Key；首次使用时由使用者在设备安全存储中自行配置；
- iOS 产物是 ad-hoc 签名的 Simulator App，真机 IPA 仍需要 Apple Team、证书和描述文件；
- HarmonyOS HAP 不签名，适合后续在 DevEco 环境签名或安装到允许调试包的设备；
- Linux ARMv6 面向 Raspberry Pi Zero/Zero W，ARM64 面向 Pi Zero 2 W 等 64 位设备；
- 源码快照来自 `git archive HEAD`，不会包含 `.git`、被忽略的 `config.json`、本地 API Key
  或其他未跟踪文件；
- 内部评审记录、答辩备忘和备注版 PPT 不进入仓库或交付 ZIP；正式 PPT、README、集成说明和
  运行文档仍会保留；
- 源码快照必须包含 `LICENSE`、`NOTICE`、`THIRD_PARTY_NOTICES.md`、Paho EDL-1.0 Notice、
  Source Han Sans OFL-1.1 和完整配置参考；
- `make verify-delivery-archive` 会实际生成临时源码归档，检查内部材料已排除且必要材料没有误删；
- `evidence/meeting-followup/` 由打包提交现场运行生成，包含成功/失败各 5 次的 JSON、持久化
  WorkOrder、`summary.json` 和独立 `SHA256SUMS`；
- `demo/` 把正式 PPT、视频、独立运行图片和报告提升到 ZIP 顶层，评审者无需先翻源码树；
- AgentTeams 四阶段证据在打包时现场生成；Qwen3 参考证据复制到顶层 `evidence/`；
- ZIP 按 [Apache License 2.0](../LICENSE) 发布，并包含 `NOTICE` 与第三方许可证声明。

同一脚本也可以在满足上述依赖的 Mac 上直接运行：

```bash
./scripts/build-multiplatform-package.sh --check
./scripts/build-multiplatform-package.sh --output /absolute/path/ECS-package.zip
```

本地打包要求工作区干净，确保构建产物与 `ECS-source/` 中记录的提交完全一致。
