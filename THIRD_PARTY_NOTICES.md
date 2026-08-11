# ECS 第三方依赖与许可证声明

本文件覆盖 ECS `v0.3.0` 源码、二进制和多平台交付包中的第三方组件。ECS 采用 Apache-2.0，
但不会改变第三方组件各自的许可证；使用者应同时遵守对应上游许可证。

## MQTT 许可证选择

`github.com/eclipse/paho.mqtt.golang v1.5.1` 采用双许可证发布：EPL-2.0 或 Eclipse
Distribution License 1.0。ECS 对该组件明确选择 **Eclipse Distribution License v1.0
（EDL-1.0，BSD-3-Clause 形式）**，不选择 EPL-2.0。随仓保留的许可文本见
[`third_party/paho-edl-v1.0.txt`](third_party/paho-edl-v1.0.txt)，项目和商标 Notice 见
[`third_party/paho-NOTICE.md`](third_party/paho-NOTICE.md)。

## Go 运行时与构建依赖

下列版本由 `go.mod` / `go.sum` 固定。`直接`表示 ECS 源码直接导入；`传递/工具`表示由直接
依赖、测试或构建工具引入。许可证名称用于交付审计，完整条款以链接的上游版本为准。

| 组件 | 版本 | 用途 | 许可证 |
|---|---|---|---|
| `github.com/eclipse/paho.mqtt.golang` | v1.5.1 | MQTT Connector | EDL-1.0（本项目选择，BSD-3-Clause 形式） |
| `github.com/gorilla/websocket` | v1.5.3 | Browser 节点 WebSocket | BSD-2-Clause |
| `github.com/grandcat/zeroconf` | v1.0.0 | Go mDNS/DNS-SD Provider | MIT |
| `github.com/santhosh-tekuri/jsonschema/v6` | v6.0.2 | JSON Schema Draft 2020-12 校验 | Apache-2.0 |
| `golang.org/x/mobile` | v0.0.0-20260803200217-62cee1672c8e | Android/iOS Go bridge 构建 | BSD-3-Clause |
| `golang.org/x/sys` | v0.47.0 | 系统调用适配 | BSD-3-Clause |
| `gopkg.in/yaml.v3` | v3.0.1 | AgentTeams runtime YAML | MIT 与 Apache-2.0（按文件） |
| `github.com/cenkalti/backoff` | v2.2.1+incompatible | 传递依赖 | MIT |
| `github.com/dlclark/regexp2` | v1.11.0 | 传递/测试依赖 | MIT |
| `github.com/google/go-cmp` | v0.6.0 | 测试依赖 | BSD-3-Clause |
| `github.com/miekg/dns` | v1.1.72 | zeroconf 传递依赖 | BSD-3-Clause |
| `github.com/pkg/errors` | v0.9.1 | 传递依赖 | BSD-2-Clause |
| `github.com/yuin/goldmark` | v1.4.13 | 工具传递依赖 | MIT |
| `golang.org/x/crypto` | v0.54.0 | 传递依赖 | BSD-3-Clause |
| `golang.org/x/exp/shiny` | v0.0.0-20260611194520-c48552f49976 | mobile 工具传递依赖 | BSD-3-Clause |
| `golang.org/x/image` | v0.44.0 | mobile 工具传递依赖 | BSD-3-Clause |
| `golang.org/x/mod` | v0.38.0 | Go 工具传递依赖 | BSD-3-Clause |
| `golang.org/x/net` | v0.57.0 | HTTP/WebSocket/DNS 传递依赖 | BSD-3-Clause |
| `golang.org/x/sync` | v0.22.0 | 传递依赖 | BSD-3-Clause |
| `golang.org/x/telemetry` | v0.0.0-20260708182218-49f421fb7959 | Go 工具传递依赖 | BSD-3-Clause |
| `golang.org/x/term` | v0.45.0 | 工具传递依赖 | BSD-3-Clause |
| `golang.org/x/text` | v0.40.0 | 传递依赖 | BSD-3-Clause |
| `golang.org/x/tools` | v0.48.0 | Go/mobile 构建工具 | BSD-3-Clause |
| `golang.org/x/tools/go/expect` | v0.1.1-deprecated | 工具测试依赖 | BSD-3-Clause |
| `golang.org/x/tools/go/packages/packagestest` | v0.1.1-deprecated | 工具测试依赖 | BSD-3-Clause |
| `golang.org/x/xerrors` | v0.0.0-20191011141410-1b5146add898 | 工具传递依赖 | BSD-3-Clause |
| `gopkg.in/check.v1` | v0.0.0-20161208181325-20d25e280405 | YAML 测试依赖 | BSD-2-Clause |

版本对应的源码与许可证可从 Go Module Proxy、`pkg.go.dev` 或各模块仓库的 `LICENSE` 文件
核对。构建评审包前运行 `make verify-third-party`，会检查当前 module graph 中每个外部模块
都在本表出现。

## Android、HarmonyOS 与系统 SDK

| 组件 | 范围 | 说明 |
|---|---|---|
| Android Gradle Plugin 9.2.0 | 仅构建 | Google Android 构建工具，Apache-2.0；不作为 ECS Runtime 源码再分发 |
| AndroidX Test Runner/Rules/JUnit/Espresso | 仅测试 | Apache-2.0；不进入发布 APK 的业务运行路径 |
| `@ohos/hypium 1.0.25`、`@ohos/hamock 1.0.0` | 仅 HarmonyOS 开发测试 | DevEco/OpenHarmony 开发依赖，不进入 Go Runtime |
| Android SDK、Apple SDK、HarmonyOS SDK | 平台工具链 | 遵循各平台 SDK 条款；仓库与源码评审包不再分发完整 SDK |
| `yourblacksky/ohos_golang_go` | HarmonyOS Go 构建工具链 | 基于 Go 1.25.12 与 OpenHarmony SIG 移植，BSD-3-Clause；仅构建，不随 ECS 源码包分发完整工具链 |
| Source Han Sans CN 2.005R | 演示文稿 PDF 字体 | SIL Open Font License 1.1；构建时按固定 URL 与 SHA-256 获取，许可文本见 `third_party/source-han-sans-OFL-1.1.txt` |

`integrations/agentteams` 针对 `agentscope-ai/AgentTeams` 固定提交提供补丁和兼容测试；上游
代码采用 Apache-2.0，ECS 交付包不内嵌完整 AgentTeams 仓库。DeepSeek、Ollama 与其他
OpenAI-compatible 模型服务是部署时选择的外部服务，不作为 ECS 软件组件再分发。

## 维护规则

- 修改 `go.mod`、Android Gradle 依赖或 HarmonyOS package 清单时同步更新本文件；
- 双许可证组件必须在这里明确 ECS 选择的许可证分支；
- 对外二进制或评审包必须携带本文件和 `third_party/` 下的必要 Notice；
- 本清单是工程合规记录，不替代具体部署组织的法律审查。
