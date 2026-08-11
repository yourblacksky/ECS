# ECS 工程 Agent 参考（按需读取）

这里保存从根 `AGENTS.md` 下沉的详细工程事实。**不要在每个会话全量加载**；根入口按改动面
路由到本文件的相关章节。项目介绍、目录结构、API 列表见 [README](README.md)，提示词设计
依据见 [`docs/small-model-tuning.md`](docs/small-model-tuning.md)。

> 本文按当前 tree 核对。**正文只引符号名，不引行号**——行号会漂，符号名不会。发现对不上请顺手更正。
>
> **关键数字不用人工重数**：`python3 scripts/verify-agents-md.py` 会把下方已登记的数量断言、工具链版本、测试硬断言与几条关键的代码结论跟当前 tree 对撞，漂了就非零退出并列出哪一条。改完 `tools.d/`、`skills.d/`、`agents.d/executor.json`、workflow 或 Go 版本之后跑一次。它由 `make test` 执行；pre-commit 只检查 staged diff 格式。

ECS 的核心论点：**把路由、参数校验、任务状态、重试和结果验证留在确定性的 Go 内核里**，只让 0.5B–4B 的本地模型理解一句话、选一个能力、整理最终答案。凡是把复杂度推回给模型的改动，请先说明理由。

## 分享边界

本仓按 Apache-2.0 公开发布，约定见
[`docs/open-source-and-sharing-policy.md`](docs/open-source-and-sharing-policy.md)。公开授权不覆盖密钥、
个人数据、内部部署配置或未获授权的第三方素材；演示与运行证据必须脱敏。

`manual-package.yml` 不接受或嵌入模型 API Key；移动端模型凭证必须由使用者在设备安全存储中
配置。发布前必须扫描完整历史与当前差异，发现真实凭证时先撤销和轮换，再净化历史。

## 提交门禁

项目不再运行模型驱动的本地提交审核，也没有 staged-tree 审核凭证。pre-commit 只做
`git diff --cached --check`；pre-push 对实际推送 HEAD 运行 `make test`；远端代码审查由 GitHub
Review 和分支保护承担。递归树仅负责按路径提示应读取的工程上下文，不参与提交批准。

```bash
make test   # go test ./... + go vet ./...
```

**提交前必须跑，但它已经不是唯一门禁。** `.github/workflows/` 下**五个** workflow，其中三个是门禁——`ci.yml`、`docker.yml`、`agentteams-integration.yml` 的 `on:` 都带一个无过滤的 `pull_request:`：

| Workflow | 跑什么 | 本地等价 |
|---|---|---|
| `ci.yml`（ubuntu，Go **1.25.12**） | ① `make test` ② `./scripts/demo-meeting-followup.sh` | `make test` + `make demo-meeting` |
| `docker.yml` | `docker build` → 单容器等 HEALTHCHECK + `curl /healthz` → 三节点 compose → `go run ./cmd/ecs-network-smoke` → `go run ./cmd/ecs-office-network-demo`（三节点完整会议闭环） | `make demo-network`（需本机 Docker）；无 Docker 时 `make verify-three-node-meeting` 用真实进程本地等价验证。⚠️ 单容器 `/healthz` 那步**没有本地入口** |
| `agentteams-integration.yml` | `scripts/verify-agentteams-integration.sh`：固定上游补丁 + Controller → ECS Worker → Office Stage 的端到端，产出独立 Artifact | `./scripts/verify-agentteams-integration.sh` |
| `release.yml` | 打 tag 时跑，校验 `version/VERSION` 与 tag 一致（`scripts/verify-version.sh`） | `./scripts/verify-version.sh` |
| `manual-package.yml` | ⚠️ **不是门禁**：唯一触发器是 `workflow_dispatch`，且限 `main`、跑在 self-hosted macOS 上。它调 `scripts/build-multiplatform-package.sh`（`make test` → 会议双路径 + AgentTeams 四阶段证据 → Windows/Linux 交叉编译 → `assembleDebug` → `xcodebuild build` + 签名 → `assembleHap` → 打包），**不跑任何原生测试**，PR 上不会产出任何 check | `./scripts/build-multiplatform-package.sh --check` 可先验环境 |

⚠️ HarmonyOS 交付依赖固定的 OpenHarmony Go fork；项目构建脚本会对托管缓存应用
`harmonyos/toolchain/openharmony-go-cshared-argv.patch`，防止 c-shared 动态加载时把无效寄存当作
`argc/argv` 导致 Go 1.25 在 `getGodebugEarly` 崩溃。自定义 `OHOS_GO_ROOT` 必须自带等价修复，
否则构建会 fail closed。

**没有 lint job，`go vet` 就是全部静态检查。** 真正可能「本地绿 CI 红」的五处是：`Dockerfile`、`demo/three-node/`、`cmd/ecs-network-smoke`、`cmd/ecs-meeting-demo`、`cmd/ecs-office-network-demo`。`cmd/` 下六个二进制包中，`ecs-agentteams-testbed`、`ecs-mock-llm` 和 `ecs-office-network-demo` 有单元测试；`ecs`、`ecs-meeting-demo`、`ecs-network-smoke` 没有测试文件，`make test` 只保证后三者可以编译。`ecs-office-network-demo` 的完整闭环另外由 `make verify-three-node-meeting` 以真实进程验证，并由 `docker.yml` 在容器内复验。

⚠️ **CI 固定 Go 1.25.12，`go.mod` 是 `go 1.25.0`**（`adca383` 从 1.22 升上来的，同时升了依赖并加了 `govulncheck`）。本地工具链低于 1.25 会直接编不过。

⚠️ **两处不明显的磁盘耦合**：`meetingdemo.New` 读的是**仓库根目录磁盘上的** `tools.d/`、`skills.d/`（含 `_knowledge/`）、`office.d/`，不是测试 fixture；`internal/core` 的 `testConfig` 直接读 **`config.example.json`** 再按顶层键覆盖。改坏其中任一 manifest，红的会是 `internal/meetingdemo`；删掉 `config.example.json` 的任一顶层段，红的会是 `internal/core`——两处都与你改的东西看上去毫不相干。

### `make test` 不需要模型 / 设备 / 外部服务

全仓没有 `testing.Short()` 分支、没有 build-tag 隔离的测试文件，只有一个 `go.mod`。**`t.Skip` 只有一处**：`internal/iot/connector_test.go` 的 `TestMQTTIntegration`，未设 `ECS_TEST_MQTT_BROKER` 时跳过。⚠️ **这条小标题现在靠那个 skip 才成立**，不是「不存在这类测试」——删掉它、或在 CI 里设上该变量，`make test` 就会依赖外部 broker。

需要对端的地方一律 `httptest` 现搭：16 个测试文件用到 `httptest`，其中 12 个真正起服务，其余 4 个只用 `NewRequest` / `NewRecorder` 直接打 handler。

⚠️ **别去 `internal/agent/agent_test.go` 找跨节点的桩**：它那 10 个 `httptest.NewServer` 全是 LLM 桩（`.URL` 全部作为 `LLMConfig.BaseURL` 传入），peer 是直接构造 `protocol.PeerResult` 喂进 `Summarize` 的，根本不走 HTTP。跨节点桩在 `internal/server/{server,network,observability_handlers,office_approval,office_receipt_compensation}_test.go`、`internal/networkauth/client_test.go`、`internal/discovery/discovery_test.go` 等处（非穷举）。

`cmd/ecs-mock-llm` 的单元测试覆盖四阶段 deterministic decision，但不启动 HTTP 服务。会实际启动该进程的自动验证有两条：`make verify-three-node-meeting` 在本地拉起一个 mock LLM 和三个 ECS 进程；Docker 门禁通过 `Dockerfile` 的 `mock-llm` stage 与 compose 服务启动它，三个节点都 `depends_on: {condition: service_healthy}`。

### 不在门禁内的原生测试

| 入口 | 需要什么 |
|---|---|
| `make android-ui-test` | `ANDROID_HOME`、`JAVA_HOME`、已授权 adb 设备（**只有 adb 设备是硬检查**，另两个有兜底路径）。gradle 的 `preBuild` `dependsOn(buildEcsMobileAar)`，会**自动回调 `make mobile-aar`**，连带需要 gomobile 与 `MOBILE_GO_TOOLCHAIN`（Makefile 首行，当前 **go1.25.12**） |
| `ios/ECSAppTests`、`ios/ECSAppUITests` | **没有 Makefile target、没有任何脚本会跑它们**。先 `make ios-project`（需 `xcodegen`），再在 Xcode 里跑 |
| HarmonyOS ArkTS / native | **Makefile 里完全没有 harmony target**。两条构建路径都在门禁外：`scripts/build-harmony-runtime.sh`（需 DevEco native SDK + gitcode 上的 OpenHarmony Go fork），以及 `scripts/build-multiplatform-package.sh` 对它的调用 |

⚠️ 全仓有 **2 处 `xcodebuild`**，都在 `scripts/build-multiplatform-package.sh` 里（一处依赖检查、一处 `-scheme ECSApp … build`），**只出 Simulator app、不跑任何 XCTest**，且只由 `manual-package.yml` 手动触发。所以「有 `xcodebuild`」不等于「原生测试进了门禁」。

⚠️ `scripts/build-multiplatform-package.sh` **不是某个平台的单独构建入口**——同一次运行还跑 `make test`、Android、iOS、HarmonyOS 与交叉编译；`check_environment` 要求 macOS + 完整 Xcode + Android SDK + JDK17 + DevEco 工具链，且**工作区必须干净**（`git status --porcelain` 非空即拒）。

⚠️ **但 HarmonyOS 的 Go 侧在门禁内**：`harmonyos/bridge` 只有 `main.go` 带 `//go:build openharmony`，`migration.go` 与 `migration_test.go` **没有 build tag**，`go test ./...` 会真跑它们。

### 新 clone 起不来的两个坑

1. `make run` 需要 `config.json`，而 `.gitignore` 里有 `/config.json`——先 `cp config.example.json config.json`。
2. ⚠️ **`llm.model` 必须显式声明，缺了是进程起不来，不是降级。** `Config.Validate` 报 `llm.model is required and must be declared explicitly`。此前 `defaults()` 里的 `Model: "qwen2.5:1.5b"` 兜底已被移除。**照抄 `config.example.json` 不会撞上**——中招的是手写配置，或沿用那次改动之前、省略了 `model` 的旧 `config.json`。
   > ⚠️ 紧挨着的 `llm.base_url` 校验**与它不对称**：`defaults()` 里仍有 `http://127.0.0.1:11434/v1` 兜底，所以**省略这个键不会失败，只有显式置空才报** `llm.base_url is required`。
   > 找校验语句请看 `LoadWithSecrets` 的结尾 `return cfg, cfg.Validate()`；`Load` 本身只有 `return LoadWithSecrets(path, nil)` 一行。

启动时 `Runtime.Start` 调 `logResolvedLLM` 打一行 `LLM runtime resolved`，用它核对真正生效的模型。**启动阶段不探活 LLM**——模型没 `pull` 过进程照样起得来、健康检查照样绿，要到第一个 reply 才炸。

## 新增一个能力：完整清单

`name` 仍然是 manifest 与实现之间的连接，但**这条连接只有一半有门禁**：`@builtin` 那半已经补上，`@device` 那半仍然完全裸奔。

`@builtin` 的门禁在 `registerBuiltinTools`（`internal/core/builtins.go`）：遍历所有 `command == "@builtin"` 的 manifest，拿不到实现就返回 `tool %q declares @builtin but has no registered implementation`。`core.New` 与 `Runtime.Reload` 都调它——进程起不来、`SIGHUP` 失败、`make test` 变红。

| 加什么 | 除 manifest 外必须同步改 | 漏了会怎样 |
|---|---|---|
| `@builtin`（纯计算） | `internal/toolbox/toolbox.go` 的 `builtinHandlers` map（当前 **15** 条），它同时喂进程内调用与 `ecs tool <name>` CLI | 硬失败 |
| `@builtin` 且要读注册表 | `internal/core/builtins.go` 的 `builtinToolHandler`。`capabilities` 是走这条路的唯一一个 | 硬失败 |
| `@builtin` 且要注入运行时依赖 | `registerBuiltinTools` 的 `extra ...map[string]builtinRegistration` 注入路径，命中时**优先于** toolbox map。`iotBuiltinRegistrations`（`iot_http_request` / `iot_mqtt`）是现存唯一例子，可另带 `availability` 回调。⚠️ `Runtime` 构造与 `Reload` **两处**都要传 | 硬失败 |
| `@device` | `AndroidDeviceCapabilityProvider.kt` 与 `IOSDeviceCapabilityProvider.swift` 各自的 `availability` / `execute`；manifest 必须带 `"args": ["<与 name 相同>"]` 与 `"platforms": ["android","ios"]` | ⚠️ **全绿**，见下 |
| Skill | `skills.d/<目录>/skill.json` **和** `SKILL.md`，缺一不可 | ⚠️ 静默消失或整体 reload 失败 |
| 两者 | `agents.d/executor.json` 的 `allowed_tools`；若同时是 Skill 还要加 `allowed_skills` | ⚠️ 静默 |

> **18 个 `@builtin` manifest = toolbox map 的 15 条 + `capabilities` + 走 `iotBuiltinRegistrations` 的 2 个 IoT 工具。** 注册路径有三条且有优先级，加新 `@builtin` 前先决定属于哪一条。（`0969f7f` 起 `meeting_scope_plan` 进了 toolbox map，14 → 15。）
>
> 不在 toolbox map、因而跑不了 `ecs tool <name>` 的现在有 **3 个**：`capabilities`、`iot_http_request`、`iot_mqtt`。
>
> `BuiltinNames()` 还在（从同一个 map 派生），但**全仓已无调用点**，改它没有任何效果。
>
> 例外：`internal/meetingdemo/demo.go` 自建注册表消费同一批 manifest，用的仍是旧的宽容写法（拿不到实现就静默跳过），且 `SkillsConfig` 是硬编码的、不读 `config.json`。

**⚠️ `@device` 那一半完全没有门禁。** 节点按 manifest 的 `platforms` 过滤（`supportsPlatform`，空列表放行），6 个 `@device` 工具在桌面 / CI 上根本不会被加载，`registerBuiltinTools` 又只管 `@builtin`。所以「manifest 加了、Kotlin/Swift 没加」是 100% 全绿的，只在真机上以 `unsupported_capability` 现形。

> ⚠️ `@device` 那半的连接键**实际是 `manifest.Args[0]`**——`deviceCapabilityBridge` 只读 `Args[0]`、从不读 `Name`，两处都要求 `len(Args)==1`。仓内约定写成与 name 相同，但**没有任何校验强制**。

HarmonyOS 至今**没有** device capability provider（`mobile.DeviceCapabilityProvider` 只有 Android 与 iOS 实现），所以别往 `platforms` 里写 `harmonyos`——写了会被加载，然后宣告一个永远不可用的能力。

**⚠️ 计数类安全网分成 Runtime 与移动端两层。** `TestRuntimeLifecycleAndPlatformStatus` 现在断言 `Tools >= 10`、`Skills >= 10`、`Agents == 15`、`Pipelines == 1`，加 Tool/Skill 不会让它变红，但增删 Agent 会。`mobile/mobile_test.go` 的 `TestMobileLifecycle` 把目录指向仓库根真实路径并**硬断言 `Tools == 16 && Skills == 15`**（写法是 `status.Tools != 16 || status.Skills != 15` 即 Fatal）。所以：

- 加一个在桌面**可用**的 Tool 或 Skill → `TestMobileLifecycle` 变红，必须同步改那两个数
- 加 `@device` Tool，或加平台/依赖不可用的（IoT 那两组即是）→ 计数不动、**全绿**
- `@builtin` 写了没实现 → 全红；`command` 写成非 `@builtin` → `TestAllShippedToolsAreInProcess` Fatal

`TestAllShippedToolsAreInProcess` 对每个 manifest 断言 `Command == "@builtin"`、handler 非 nil；**可用性断言分两支**——普通 `@builtin` 必须 `Availability().Available`，经 `iotBuiltinRegistrations` 注册的两个 IoT 工具反过来**必须不可用**且 `Reason == "connector_unconfigured"`。它遍历 `node.tools.All()`，桌面平台上 `@device` 已被过滤——**覆盖不到 `@device`**。

原生 UI 测试都不在 Go 门禁内：`ios/ECSAppUITests` 仍断言当前 iOS 模拟器的
`20 Tool / 19 Skill`；Android 不再写死设备动态计数，而是从 `Mobile.statusJSON()` 取当次
Runtime 值并校验界面一致。⚠️ **这些数不是磁盘上的 manifest 数**——界面读
`core.Status`，取的是 `len(tools.Available())`，已按当前设备的权限与硬件过滤。

**当前清单**（`ls` 数出来的）：`tools.d/` **24** 个 = **18** 个 `@builtin` + 6 个 `@device`；`skills.d/` **23** 个 Skill 目录，外加 `_knowledge/` 2 篇知识文档；`agents.d/` **15** 个；`pipelines.d/` 1；`office.d/` 3。

⚠️ **「装载」与「可用」不再是同一个数，引用时务必说清是哪个口径**：桌面节点先按 `platforms` 滤掉 6 个 `@device` Tool 与 6 个移动端 Skill → **装载 18 Tool / 17 Skill**；而 `core.Status` 读的是 `len(Available())`，默认配置下两个 IoT Tool 判 `connector_unconfigured`，`manifestAvailability` 连带判掉依赖它们的 2 个 Skill → **上报 16 Tool / 15 Skill**（即 `TestMobileLifecycle` 守的那两个数）。

⚠️ 也别再把「`@builtin` vs `@device`」当成平台过滤的判据：`iot_http_request` / `iot_mqtt` 是 `@builtin` 但 `platforms` 限 `["linux","darwin","windows"]`，**在移动端反而被过滤掉**。

## Skill 可见性与注册表

**⚠️ 忘记加 `executor.json` 白名单大多数时候是静默的。** `Registry.Route`（`internal/roles/roles.go`）分四条路，**只有第一条会响**：

| 请求形态 | 结果 |
|---|---|
| 请求体显式写了 `skills` / `tools` | 名字不被任何 executor 覆盖时硬失败：`pipeline "default" has no executor covering requested skills [...] and tools [...]` |
| 提示词里出现**已在某个白名单里**的能力名（`explicitCapabilitiesForPrompt`，**先判**） | 目录收窄到被点名的那几个，不会失败 |
| 提示词里**没有**任何白名单能力名，且命中 18 条能力问答短语之一（`有什么工具` / `能做什么` / `what tools` …） | 请求被强制收窄成 `capabilities` 一个能力 |
| 以上都不成立 | ⚠️ **静默**：`constrain()` 原样返回 executor 白名单全量，新能力从不进入模型目录，无报错无日志 |

⚠️ **第 2、3 条的先后顺序容易读反**：`requiredSkillForPrompt`（短语）是 `explicitCapabilitiesForPrompt`（能力名）的 `else if` 分支。名字匹配按「非 `[a-z0-9_.-]`」切分，**中文字符算分隔符**，token 长度 ≥2 且等于白名单里的名字才命中——所以 `clock 能做什么` 得到的是 `[clock]` 而不是 `[capabilities]`。

第 2 条有个自指陷阱：`explicitCapabilitiesForPrompt` 只在白名单**之内**做名字匹配——**恰恰是「忘了加白名单」这个场景匹配不到**。所以「在提示词里点名新工具」并不能把这个错误变响。

⚠️ 现成的坑：当前有 **8 个**名字只在 `allowed_tools`、不在 `allowed_skills`（`office_action_compensate`、`iot_http_request`、`iot_mqtt`、`meeting_fact_extract`、`meeting_scope_plan`、`office_action_dispatch`、`execution_receipt_verify`、`meeting_evidence_verify`）。其中 7 个只是命名风格差异（skill 侧点号、tool 侧下划线），**只有 `office_action_compensate` 是真没有对应 Skill**：`{"tools":["office_action_compensate"]}`（未置 `restrict_skills`）会被镜像成同名 skill 请求然后硬失败——报错指向 skill，问题出在镜像。

**⚠️ 新增 Skill 不等于模型能看见它。** 从磁盘到提示词有五道闸，**每一道都不报错**：平台（`supportsPlatform`）→ 依赖可用性（`manifestAvailability`）→ Agent 兼容（`supportsAgent`）→ 负关键词（`never_keywords`）→ 名额截断。

⚠️ 第二道闸自 IoT 起第一次有了**非磁盘**触发源：`readyHTTPProfiles` 会逐 profile 检查 `headers_env` 指向的环境变量是否非空（MQTT 侧看 `username_env` / `password_env`），任一为空即该 profile 不 ready → 工具 unavailable → 依赖它的 `skills.d/iot.*` 从模型目录消失。**改 manifest、改配置都查不出来，只有 `/v1/tools` 的 reason 能看见**（`connector_unconfigured` / `connector_credentials_unavailable`）。

打分（`score`）只是 `when.keywords` 命中 +3、`when.intents` 命中 +2，`ToLower` 后纯子串匹配，无分词无向量。两个容易读错的地方：

- **名额 N = `min(agent.max_prompt_tools, skills.max_prompt_skills)`**，默认 `min(12, 5) = 5`。只调大一个不会变宽。
- 🔴 **0 分候选不被淘汰。** `List` 只排序后截断，不过滤零分。所以一句关键词全不命中的提示，进目录的就是**名字排序最靠前的 5 个**。关键词写不好不是「进不去」，是「被别人的名字排序挤掉」——现象一样，但你 grep 不到任何抱怨。

**Agent 兼容三条**（`supportsAgent`，全部大小写不敏感）：`agents.exclude_roles` 命中 → 排除；`agents.names` 非空且不含当前 agent → 排除；`agents.roles` **非空却不含 `executor`** → 对默认 pipeline 不可见，**留空反而是全放行**。当前有 **2 个**这样的 Skill：`meeting.evidence.verify`（`["verifier","worker"]`）与 `meeting.scope.plan`（`["planner","worker"]`）。

> 🔴 **但「对默认 executor 不可见」不等于「没人能用」——这条自 `a4a0ba4` 起变了，是本节最容易读错的地方。**
>
> `supportsAgent` 收的是 `context.AgentRole`，它来自 `PromptContext.Role`。`executorPromptContext`（`internal/agent/agent.go`）**过去无条件把它覆盖成 executor 名**，现在是：
>
> ```go
> // The pipeline executor defines the security policy, while an Office stage
> // role defines the callable Skill profile. Preserve an explicit stage role
> // so verifier/planner-only Skills remain routable.
> if promptContext.Role == "" {
>     promptContext.Role = executor.Name
> }
> ```
>
> 于是走 **Office Stage** 进来的请求会带着 stage 自己的角色：`office.d/meeting_followup.json` 的四个 team 成员映射到三个 role（`planner` / `executor` / `verifier`），`decision_reviewer` 真的能看见 `meeting.evidence.verify`，而 `minutes_clerk` 看不见。**这两个 Skill 不是死的，是只对特定 stage 活的。**
>
> ⚠️ **现在 Office stage 是独立可路由 principal，与默认 executor 是两回事。** `agents.d/` 里
> `meeting_planner` / `minutes_clerk` / `decision_reviewer` / `followup_coordinator` 各自有
> 独立 manifest；`research_brief` 与 `approval_memo` 的 8 个 team identity 也各有最小权限
> manifest。Office reload 会把所有 team/stage 与 Agent 注册表交叉校验，并检查 Stage Skill
> 及其 Tool 依赖没有越出 Agent allowlist。Office 与 AgentTeams
> 执行路径通过 `protocol.ReplyRequest.Agent` 显式直选（`roles.routeForAgent`，fail closed：
> 未注册、非 llm、能力越权一律拒绝），不再由 pipeline 的单个 executor 代跑。`pipelines.d/default.json`
> 仍是 `"executors": ["executor"]`，**普通对话路径不变**；`Agent == ""` 时 `Route` 走原有
> pipeline 选择，四个 Office 角色只被 Office/AgentTeams 显式寻址（见 #19 验收项）。

⚠️ **`never_when` 是死字段。** `Manifest` 里声明了、JSON 也解析了、十几份 `skill.json` 写了它，但全仓**没有任何读取点**。真正生效的是 `never_keywords`。只写前者等于负向路由完全没生效，且无任何报错。

> 同型的还有一个：`skills.Dependencies.Capabilities`。字段在、JSON 解析了、**8 份 `skill.json` 正在用**（beep / flashlight / iot.http.request / iot.mqtt / location / notify / screen_brightness / vibrate），但 `validate` 只遍历 `Dependencies.Tools`，全仓没有任何代码读过它——写错、写成不存在的能力名都不会有提示。

🔴 **默认路径下模型目录里只有 Skill，没有 Tool。** 但**判据不是 `restrict_skills`**：真正打开 Tool 分支的是 **`Route` 之后 `request.Skills` 为空**，且 `!restrict_tools || len(request.Tools) > 0`。

- **`restrict_skills: true` 不充分**：`authz.Narrow` 在调用方未自选能力时会把 `Skills` 回填成 relationship 的 `allowed_skills`，`/v1/agentteams/tasks` 与 Office worker 的 `replyRequest` 也都带 Skills——这些情况下目录里仍然只有 Skill。
- **也不必要**：第 2 条路一旦命中纯 Tool 名（如 `office_action_compensate:`），skill 侧 `constrain` 返回空切片，默认 `/v1/reply` 未置 `restrict_skills` 也会得到纯 Tool 目录。
- **进去了也不是「全量」**：只取 `registry.List(request.Tools, a.cfg.MaxPromptTools)`，受 `max_prompt_tools` 截断（默认 12）。

后果不变：`office_action_compensate` 这种没有同名 Skill 的纯 Tool，默认路径下**模型根本看不到**（仍可经 `direct_tool` 确定性直调）。

> ⚠️ 顺带纠正一条容易被抄走的说法：该分支里「同名 Tool 被同名 Skill 遮蔽」（`managedTools`）**目前确实一次都不会发生，但不是因为两个条件互斥**。`Route` 只把命中的能力名传给 `constrain` 的 `restricted` 形参，**从不回写 `RestrictSkills` 字段**——点中纯 Tool 时 `Skills` 为空而 `RestrictSkills` 仍是 `false`，两个条件同时成立、`managedTools` 被灌满。它没造成遮蔽只靠一件事：**磁盘上 23 个 Skill 名与 `allowed_skills` 完全一致**。这是配置层的巧合，**没有任何测试守它**——把某个已装 Skill 从 `allowed_skills` 摘掉而同名 Tool 留着，点名它的提示词就会真的被吃掉。

**⚠️ 任何一个条目校验失败 = 这次 reload 整体作废，整份注册表一起没了。** 不是跳过坏的那个：`Reload` 把所有 manifest 收进局部 `next`，中途任一 `return err` 直接中止，只有全部成功才 `r.items = next`。触发条件：

| 触发 | 检查点 |
|---|---|
| 依赖 Tool 未安装或版本不满足约束 | `Registry.validate` |
| schema_version / name / version / workflow 步数等任一不合法 | `Registry.validate` |
| 缺 `SKILL.md`（`skills.documentation_required` 默认 **true**） | `loadDocumentation` |
| `SKILL.md` 为空、无 frontmatter、frontmatter 未闭合、缺 `name` | `loadDocumentation` |
| frontmatter `name` 与 manifest `name` 不一致（**恒检查**） | `loadDocumentation` |
| 正文超 `skills.max_document_chars`（默认 12000，按 rune） | `loadDocumentation` |
| 两个目录声明了同名 Skill | `Reload` |
| `_knowledge/*.md` 任一缺 frontmatter / 缺 `title` / 既无 `keywords` 也无 `intents` / 重名 / 超长 / `name` 不匹配 `^[a-zA-Z][a-zA-Z0-9_.-]{0,63}$` | `loadKnowledge` |

最后一行值得单独记住：**一篇知识文档写坏，打爆的是整个 Skill 注册表。**

⚠️ 作废的是**本节点平台过滤后的那一批**——平台过滤跑在 `validate` **之前**，磁盘上 23 个从来不会同时在一份注册表里。推论：**在桌面上写坏一个 android/ios 专属 Skill，根本拦不住 reload**。失败形态也分两路：启动时 `core.New` 返回 `load skills: %w`，`cmd/ecs` 打印后 `os.Exit(1)`；`SIGHUP` 时旧的整份 Skill 注册表留在内存、只往 stderr 打一行继续服务——但 tools 排在 skills 之前且**已经提交**，所以不是「什么都没发生」。

🔴 **`enabled: false` 救不了你。** `Reload` 的顺序是 目录遍历 → 读 `skill.json`（不存在就 `continue`）→ **JSON 解码** → 平台过滤 → `validate` → `loadDocumentation` → **最后才判 `enabled`**。让坏 Skill 不再打爆 reload 只有两招稳妥：删掉/改名该目录的 `skill.json`（目录可留），或把整个目录移出 `skills.d/`——这两招连**语法坏掉的 JSON** 也能覆盖。改 `platforms` 让它匹配不上本机也能躲过 `validate`，但**躲不过 JSON 解码失败**（解码排在平台过滤之前）。

⚠️ **目录布局放错就是静默消失。** `Reload` 只遍历 `skills.d/` 下的**目录**，读 `<目录>/skill.json`，文件不存在就 `continue`——零日志、零报错。旧的平铺 `skills.d/<name>.json` 不会被读，文件名拼错也不会被读（`_knowledge/` 正是靠这条被跳过的）。目录名不参与识别，注册用的是 manifest 里的 `name`。

**SKILL.md 已不是死文件。** 它参与上表的硬校验，并经 `Registry.Grounding` 把正文注入系统提示词，每条引用带内容哈希写进 trace。设计说明见 [`docs/skill-documentation-grounding.md`](docs/skill-documentation-grounding.md)。

⚠️ grounding 有两个反直觉点：

- **真正的天花板不是 `max_grounding_chars`（默认 1800 rune）。** `buildSystemPrompt` 在有 grounding 的分支里还有一道 `truncateRunes(grounding, max_system_prompt_chars/5)`，默认 8000/5 = **1600**，比 1800 更紧。**只调大 `max_grounding_chars` 一个字都多不进来**，要调必须连 `max_system_prompt_chars` 一起调。现有 23 篇 SKILL.md 正文在 **638–1570 rune** 之间（`0969f7f` 的契约同步脚本给每篇都补齐了结构，已无短文档），预算通常 **1–2 篇**就耗尽——比改动前更紧。
- **`_knowledge/` 的语料在默认配置下极难挤进提示词。** 结论没变，但机制不是「Skill 恒排在知识文档之前」——`Grounding` 的比较器是 `explicit` → `score` 降序 → `kind` **升序**（同分时 knowledge 反而在前）→ `name`。真正把 Skill 顶到前面的是 **explicit**：默认路径下 `Route` 把整份 `allowed_skills` 写进 `request.Skills`，被选中的 Skill 全是 explicit，**无条件靠前，连 0 分的也照样占预算**。一旦这条链路被改动，优先级会当场反转。

## 其他硬约束

| 约束 | 校验点 | 失败方式 |
|---|---|---|
| Pipeline 的每个 executor 必须 `execution_mode: "llm"`（`planner`/`verifier` 是 `deterministic`，不能放进 `executors`） | `validatePipeline` | 硬失败，`executor role %q must use llm execution_mode` |
| `agent_roles.default_pipeline` 指定的 Pipeline 必须存在 | `roles.Registry.Reload` | 硬失败，`core.New` 直接退出 |
| Skill 的 `workflow` **和** `compensation.steps` 步数都受 `skills.max_steps` 限制（默认 4，范围 1..10） | `skills.Registry.validate` / `config.Validate` | 硬失败 |
| Office 组件的 `stages` 数量受 `office.max_stages` 限制（默认 16，范围 1..64） | `office.validateManifest` | 硬失败 |
| `schema_version` 精确匹配：Skill `ecs.skill/v1`、Agent `ecs.agent/v1`、Pipeline `ecs.pipeline/v1`、**Office `ecs.office/v1`**（Tool manifest 无此字段） | 各注册表 validate | 硬失败 |
| manifest 里的每个 schema 必须是**本地自包含、可编译**的 JSON Schema（`$ref` 只能是 `#` 开头的本地片段）；`input_schema` 与 `canonical_input_schema` 还要求根 `type` 为 `object` | `ecsschema.ValidateDefinition`，被 tools / skills / roles / office 四个注册表共 10 处调用 | 硬失败，reload 不交换注册表 |
| Office stage 一旦声明 `skills`：每个 Skill 必须已安装且 available，其 tool 依赖必须已安装，且 stage 的每个 `connectors` 必须被某个依赖 tool 的 `capabilities` 覆盖 | `validateExecutionContracts` | 硬失败，`stage %q connector %q is not implemented by a declared skill dependency` |
| **config.json 里**的相对路径相对 `config.json` 所在目录解析，不是 cwd | `config.LoadWithSecrets`（内联，**没有** `resolvePaths` 这类函数可 grep） | ⚠️ 静默，见下 |

⚠️ **Office stage 不声明 `skills` 时，它的 `connectors` 不做实现校验。** 两层别混：名字一致性（`stage.connectors` 必须在顶层 `connectors` 里声明过）**总是**校验；有没有人实现**只在** stage 声明了 `skills` 时才校验（`validateExecutionContracts` 循环体第一行就是 `if len(stage.Skills) == 0 { continue }`）。

结果是**一个组件可以宣称一整套 connector 而节点上零实现，reload 照样通过**。出厂三份组件有两份正是这一类：`approval_memo.json` 与 `research_brief.json` 的全部 stage 都没声明 `skills`，合计 7 处 connector 引用在 `tools.d/` 里没有任何 tool 提供。

> ⚠️ 这里的 connector 指 **Office connector**（`office.*` 能力契约）。仓里另有一个同名不同物的 **IoT Connector**（`internal/iot` 的 Go 类型），两者不要互相套用——`internal/office`、`internal/server`、`internal/toolbox` 对 `iot` 零引用。

⚠️ **config.json 放错目录的失败形态**：`defaults()` 提供的十几个相对路径全部由 `LoadWithSecrets` 的 `base := filepath.Dir(path)` 重挂到配置文件所在目录。`tools` / `skills` / `office` 三个注册表的 `Reload` 遇到「目录不存在」都是 `return os.MkdirAll(...)` ——**早退且不报错**，于是旁边被静默创建出空目录；`roles` 侧没有这个早退分支（是 `loadRoles` / `loadPipelines` 各自建目录后返回空表），进程一路走到默认 pipeline 检查才死：

```
initialize ECS: load agent roles and pipelines: default agent pipeline "default" is missing
```

⚠️ 一个反直觉点：目录缺失时早退**并不交换注册表**。启动时注册表本就是空的，所以上面那条链成立；但**热重载时语义相反**——删掉 `tools.d/` 再 `SIGHUP`，工具表会**保持原样**，不会清空。想撤掉工具就删文件、别删目录。

Tool manifest 的 `command` 走**另一套**：相对该 manifest 所在目录解析。⚠️ **但只在 command 含路径分隔符时才解析**——裸命令名（如 `python3`）原样交给 PATH，写成 `./x.sh` 才会被重写。另有三个哨兵值：`@builtin`、`@device`、`@ecs`（每次 `Reload` 都替换成 `os.Executable()`；出厂 23 个 manifest 用了 0 次）。

`Dockerfile` 与 `.dockerignore` 是**可用**的部署参考（`docker.yml` 每个 PR 都构建 + 探活 + 起三节点 demo）。构建阶段 `COPY go.mod go.sum`，运行阶段 COPY 了 `prompts` 与全部五个 `.d` 目录及 `config.docker.json`，带非 root 用户、`/app/data` volume 和 `/healthz` HEALTHCHECK。

⚠️ 一个非显然点：`config.docker.json` 的 `listen` 是 `0.0.0.0:8090` 而 `auth_token` 是空串，`config.Validate` 要求非 loopback 监听必须有 token——**不传 `ECS_AUTH_TOKEN` 容器根本起不来**。它唯一写到的目录是 `network_auth.directory`，而该值与 `defaults()` 逐字相同（写了等于没写）；真正兜住其余十几个相对路径的是 `CMD ["-config", "/app/config.json"]` 这个**绝对路径**（解析基准 `/app`），**不是 `WORKDIR`**。

## 热重载语义

| 改了什么 | 生效方式 |
|---|---|
| `tools.d/` `skills.d/` `agents.d/` `pipelines.d/` `office.d/` | `SIGHUP` → `Runtime.Reload`。移动端宿主收不到信号，用 `mobile.Reload()`，它直接转调同一个 `Runtime.Reload` |
| `prompts/*.txt` | **每次请求自动重读，改完立即生效**，不需要 SIGHUP 也不需要重启 |
| `config.json` 其余部分 | 必须重启 |

⚠️ **管理 API 不是全量 reload。** `PUT`/`DELETE /v1/admin/tools/{tool}` 只跑 `reloadToolsAndSkills`——**只有 tools 与 skills 两个注册表**，不重跑 `registerBuiltinTools`，也不碰 roles 与 office。它能改的只是 tool 文件，但能撬动的不止 tool：

- 新增一个 `@builtin` manifest 会**静默成功**，该工具不出现在 `/v1/tools`（判为 `handler_unavailable`）。**真正的代价在下一次 `SIGHUP`**：`registerBuiltinTools` 硬失败，从此 roles 与 office 也一起停止重载。
- 用 `PUT` 改掉某个 tool 的 `capabilities`，只要新名字合法且不重复就**静默成功**（tools 侧的 `validate` 会查 `validName` 与重复，返 400 `invalid or duplicate capability`；但**不查**这个 capability 是否仍被 Office stage 依赖）。Office 照旧持有旧 manifest，**直到下一次 `SIGHUP` 才炸**。
- 反过来**删除**一个被 Skill 依赖的 tool 是安全的：`skills.Registry.validate` 硬要求依赖已安装（没有「可选依赖」这回事），reload 失败即回滚。

它确实是**唯一**在失败时真回滚的路径（还原原文件并重新 reload，返回 `tool change rolled back: ...`，全仓仅此一处）。

**`Runtime.Reload` 不是事务性的。** 当前顺序是 **tools → skills → builtins → roles → office**，前面的**已经提交**后面才校验：

| 步 | 典型失败 | 失败时已经换新的 |
|---|---|---|
| 1 tools | manifest JSON 写坏、工具重名 | 无 |
| 2 skills | 删掉某个 Skill 依赖的 tool | tools |
| 3 builtins | 写了 `@builtin` 但没有对应实现 | tools、skills |
| 4 roles | 删掉 `default` pipeline、executor 不是 `llm` | + builtins |
| 5 office | stage 依赖的 Skill/tool 没了、connector 失去覆盖 | 以上全部 |

**Office 排在最后而它的校验最严**——它是唯一同时查 skills、tools 与 connector 覆盖的一步。任何影响 Office 执行契约的改动，失败时留下的是「Tool/Skill/角色全新、Office 停在旧集合、且下次 reload 仍然失败」，比 reload 前更坏。而且 **`SIGHUP` reload 失败不会终止进程**（只往 stderr 打一行 `reload ECS: ...` 就继续跑），节点会带着这个错位状态一直服务下去。

提示词那一行是实测结论：`ReplyWithContext` 内部每个请求都 `os.ReadFile` 一次 `system_prompt_file`，开了会话引导再读一次。⚠️ **但那两次读发生在四条短路分支之后**：`runLiteralProbe`（`request.ExpectedText != ""` 时进）、`manifestExplanation`、`tryDirectInvocation`、`tryConstrainedCapabilityDecision`——四条都在 `ReadFile` 之前 return，其中前两条与 `tryDirectInvocation` 根本不调模型。所以删除或移动提示词文件的表现是「一部分请求照常成功、走完整提示词的请求失败」，**不是全线失败**——排查时别因为「随手试一句还能用」就排除掉提示词文件。失败时返回 `read system prompt` / `read conversation guidance`。两处都是裸 `os.ReadFile`，编辑到一半的内容会被静默拼进系统提示词。

## Schema 校验：Go 侧是完整的 Draft 2020-12，浏览器叶子仍是手写子集

> ⚠️ **这一节自 `9a7c662` 起整体反转。** 旧版说的「手写子集、只认 14 个关键字、`pattern` 是零校验」已经不成立——`validateValue` / `matchesType` 全仓删除。看到别处仍在按旧结论推理的，那是过期的。

Go 侧真源是 `internal/schema/schema.go` 的 `Validate` → `compile`，用 `github.com/santhosh-tekuri/jsonschema/v6`（`go.mod` 锁 v6.0.2），`DefaultDraft(Draft2020)` + **`AssertFormat()`**，编译结果按 schema 的 sha256 缓存。`internal/tools/registry.go` 的 `validateArguments` 只是加前缀 `arguments do not match schema: ` 的薄封装。

**所以 `pattern`、`format`、`const`、`allOf`、`anyOf`、`not`、`multipleOf`、`exclusiveMinimum/Maximum`、`$ref`、`contains`、`propertyNames`、`patternProperties`、`if`/`then`/`else` 现在全部真的执行。** 仍不生效的只有三类，原因各不相同：

- **annotation 类**：`default` 认识但永不断言、也不注入（`tools.d/iot_mqtt.json` 有 3 处真实使用，**不会补值**）
- **被 2020-12 废弃的旧 draft 词**：`additionalItems`（改用 `prefixItems` + `items`）
- **非 JSON Schema 词汇**：`nullable`（OpenAPI 方言；要可空写 `"type":["string","null"]`），以及拼错/自造的未知词（`minLenght` 仍是零校验）

⚠️ **边界是库实现而不是规范条文**——draft-07 的 `dependencies` 在 v6 里仍被执行。拿不准就实测，别按规范推。

三个旧陷阱现在**变成加载期硬失败**（`ValidateDefinition` 四处调用，返回 `invalid input_schema: ...`，工具/技能根本装不上）：tuple 形式 `"items":[{...}]` → 编译报 `got array, want boolean or object`（tuple 用 `prefixItems`）；`oneOf` 写成对象而非数组 → 编译失败；`type` 拼错（`"str"`）→ 编译失败。**`oneOf` 也不再短路**，同层并列的关键字照常生效（合取），约束不必重复写进每个分支。

仍然成立的两条：`"type":["string","null"]` 联合类型支持，任一命中即通过；`minLength` / `maxLength` 按 **rune**（code point）计不是字节——「中文中文」算 4 不算 12。

> ⚠️ 别被 grep 骗了，但方向和以前相反：现在**真的有** manifest 在用这些关键字。`tools.d/iot_http_request.json` 与 `skills.d/iot.http.request/skill.json` 的 `path` 写了 `"pattern": "^/"`（与 `type`/`maxLength` 平级，**是一条起作用的路径穿越防护**），两处 `query` 的 `additionalProperties` 是 schema 对象而非 bool。而 `tools.d/vibrate.json`、`text_extract.json` 里的 `"pattern"` 仍是**属性名**，`skill.json` 里的 `"dependencies"` 仍是 Skill 自己的依赖字段。判据不变：看它是不是与 `type`/`enum`/`properties` 平级。

**校验入口不止一个**，三类调用者里有两类不经过 `ValidateArguments`：

| 入口 | 时机 | 干什么 |
|---|---|---|
| `ecsschema.ValidateDefinition` | manifest 加载期 | 校验 schema 文档自身，并经 `rejectRemoteReferences` 拒绝非本地 `$ref`——目的是**加载 manifest 绝不发网络请求**。⚠️ 它按 key 名递归，字面叫 `$ref` 的**属性名**会被误伤 |
| `ecsschema.Adapt` | 执行前 | model 参数 → canonical 参数，内部两次调 `Validate`。经 `tools.PrepareArguments` |
| `tools.ValidateArguments` | 运行期 | `Registry.Execute`（Tool 入参）、`Runner.Execute`（Skill 入/出参）、`Registry.Compile`（Office 工单入参，编译期）与直调路径 |

⚠️ `Adapt` 不是绕过 `ValidateArguments`，是**前置于**它。真正的不对称在于：声明了 `canonical_input_schema` 时 `RuntimeInputSchema()` 返回 canonical，所以 **model schema 上的约束只被 `Adapt` 校验那一次**。另：`MissingRequired` / `collectMissing` **仍是手写遍历**，只认 `required` + `properties`，对 `$ref` / `allOf` / `oneOf` / `if-then` **全盲**——组合关键字下的必填会漏报。

> 这套三层输入契约（`input_schema` / `canonical_input_schema` / `input_adapter`）已经实现，但 `tools.d` 与 `skills.d` 共 45 份 manifest 里**声明了后两者的是 0 份**。细节见 [`docs/json-schema-contracts.md`](docs/json-schema-contracts.md)。

三处覆盖范围上的不对称，改动前先确认：

| | 行为 |
|---|---|
| **Tool 出参** | **只在 manifest 声明了 `output_schema` 时才检查**。出厂 23 个全部声明了，所以今天摸不到这个洞；**新增工具漏写就等于出参零检查**。另：出参走未导出的 `validateArguments`，失败不是 `*ArgumentValidationError`，因此拿不到下一节第 1 条那次纠错机会 |
| ⚠️ **Skill 出参非 JSON** | `Runner.Execute` 的门控直接跳过整个 `output_schema` 校验，`run.Status` 照样置 `success` |
| 🔴 **浏览器叶子节点** | **这条路径上 Go 侧一次 schema 校验都不做**。全链路唯一的校验在浏览器里（`internal/browserui/assets/app.js` 的 `validateArguments`），只认 `required` / `additionalProperties: false` / `type`（**仅 number 与 string**）/ `enum` 四项。**Go 侧升到全量 Draft 2020-12 之后这条不对称是变严重了** |

关于最后一条再补一句：浏览器叶子**从不读 `tools.d/`**，而是把 `app.js` 里 JS 内联声明的 `toolDefinitions` 在注册时上报给 hub。同名工具在两边是**两份各自独立的 schema**——改 `tools.d/calculate.json` 不会传导到浏览器节点，两边漂移不会有任何报错。

## 内核侧的兜底防线（改动时别削弱）

这三条是这个项目对「小模型不可靠」的核心回答。**三条的覆盖面互不相同，动手前先确认你落在哪条路径上。**

### 1. 参数校验失败只给一次纠错机会——但缺必填字段走的是另一条道

⚠️ **先分岔再谈纠错：缺必填字段根本不进纠错循环。** 执行前先过 `tools.PrepareArguments` → `ecsschema.Adapt`，其中 `MissingRequired` 在 `Validate` **之前**短路，缺字段时返回 missing 而非 error，agent 立刻 `return missingInputResponse(...), nil`：**nil error、HTTP 200**，带 `input_request` 与 `Verification{Verifier:"schema-slot-gate", Status:"needs_input", Validation:"incomplete"}`。**零纠错、零报错，也不写 ToolRun。**

三条容易忽略的顺序与覆盖：① 这道 gate 在**审批门之前**——高风险工具缺参时返回 `needs_input` 而不是 `approval_required`；② 它**直接 return、不走 `finalizeResponse`**，所以下面第 2 条的 `VerifyWithRequest` 在这条路径上根本不执行；③ 判据用的是**模型面** schema（`manifest.InputSchema`），显式 `null` 也算缺失，嵌套必填产出 `a.b` 点号路径。

（旧基线上缺字段由 `validateArguments` 报 `missing required property` 并包成 `ArgumentValidationError`，**当时是能拿到那一次修正机会的**。）

参数**值**不合法才进纠错循环：`ReplyWithContext` 主循环里的 `correctionTool` / `correctionUsed`，回灌给模型的结果被追加「你只有一次修正机会」，随后三条都是**硬失败**——不再调工具直接回答、换了工具或一轮调多个、重调但参数又没过。

⚠️ **但这个循环只是 `ReplyWithContext` 的最后一档。** 分派顺序是 `runLiteralProbe` → `manifestExplanation` → `tryDirectInvocation` → `tryConstrainedCapabilityDecision` → LLM 循环，前面任何一个返回就直接 return。带参数的能力调用有三条路绕开它：

| 路径 | 参数校验失败时 |
|---|---|
| `Server.runDirectTool`（请求体带 `direct_tool`，进 agent **之前**就分流） | **零纠错、零模型**，`/v1/reply` 返回 502 |
| `tryDirectInvocation`（能力已锁成 1 个，**且 `bindManifestInput` 能确定性绑出参数**） | **零纠错**，`registry.Execute` 一报错就 return，连 `finalizeResponse` 都不走 |
| `tryConstrainedCapabilityDecision`（能力已锁成 1 个，**但 `bindManifestInput` 没绑出参数**） | 自带 2 次 attempt，但校验的是执行**前**的参数 |

> 上表是**分派级**绕过。另有一条不分档位的**循环内短路**：上面那道 slot gate。它同时存在于 LLM 主循环与 `executeResolvedCapability`（后者是表中第 2、3 行的共同尾部），**三条循环侧路径全部命中**；只有第 1 行 `Server.runDirectTool` 不受影响——它不调 `PrepareArguments`，裸参数进 `registry.Execute` 被拦下并回 502。

🔴 **「能力被锁成 1 个」的判据是反直觉的：传了 `tools` 反而不锁。** `roles.Registry.Route` 只在请求体**没有** `skills`/`tools`、且两个 `restrict_*` 都为假时才去看 prompt。所以**显式传 `tools: ["calculate"]` 走的是 LLM 循环**。

⚠️ 但第 2 行的判据**不是 prompt 长得像 `name: value`**，而是 `bindManifestInput` 能不能绑出参数——三种形状：① 裸能力名且 schema 无必填；② `<能力名>: <合法 JSON 对象>`；③ `<能力名>: <标量>` 且 schema 恰好 1 个必填、该属性 `type: string`。能力名还必须是 prompt 的**起始 token**（「请调用 text_stats: …」不绑）。所以 **`calculate: 1+1` 并不走零纠错直调**——`calculate` 的 `required` 是 `["a","op","b"]`，`1+1` 既不是 JSON 也绑不进单字段，它落到第三行（带模型）；`text_stats: 一段文字` 才是真正的零纠错直调，`calculate: {"a":2,"op":"add","b":3}` 也是。

⚠️ 第三行还有一处**语义反转**：受约束判定路径里模型输出 `{"execute":false}` 是**合法分支**，会清空 `RequiredSkills`/`RequiredTools` 再走 `finalizeResponse`——所以下面第 2 条那侧的「必备能力未成功执行」也查不出来。主循环那条「直接回答 = 失败」在这里不成立。

另有一个**独立**的一次性重试 `requiredRetryUsed`（必备能力一次都没调时补一轮），它和参数纠错各计各的次数，不共用配额。

### 2. 最终答案里的裸 `<tool_call>` 一律拦下

`Registry.Verify` 现在只是薄封装，**全仓非测试调用点为 0**；生产入口是 `Agent.finalizeResponse` 里的 `a.roles.VerifyWithRequest`。**两侧是两套代码，行为不同**：

| | `VerifyWithRequest` | `validateSynthesis`（汇总侧） |
|---|---|---|
| 覆盖 | 所有走 `finalizeResponse` 的路径：`/v1/reply`、`/v1/office/tasks/execute`、`/v1/agentteams/tasks`、`/v1/recursive/reply` 的本地叶子 | `/v1/groups/{group}/reply` 与 `/v1/recursive/reply` 的 summary |
| `<tool_call` 匹配 | **区分大小写** | **不区分** |
| 失败后果 | `rejected` → `/v1/reply` 回 **502** | 追加提示重试**一次**；仍不合格则拼接各 peer 原文降级，`Status: "degraded"`，**HTTP 依然 200** |
| 还查什么 | 空内容、`ExpectedText` 探针、skill/tool run 报错、必备能力未执行、`capabilityContradictions`，以及下面第 3 条 | 只查空内容 + `tool_call` |

⚠️ **换 pipeline 时最容易撞的静默失效**：`plan.Verifier` 为空或该 agent 没装上时，`VerifyWithRequest` 立刻返回 `Validation: "skipped"`，`finalizeResponse` 原样放行——**整条防线消失且不报错**。

⚠️ 另有**四条**自造 Verification、不进上表任一侧的路径：`finalizeDeterministicManifestResponse`（能力清单问答）与 `a.roles == nil` 分支只调 `validateSynthesis`；`Server.runDirectTool` **连 `validateSynthesis` 都不调**，工具输出原样当最终答案、并凭空产出一条 `tool:<name>=success` evidence；以及上一节那个 `missingInputResponse`（`schema-slot-gate` / `needs_input`），它从主循环与 `executeResolvedCapability` 两处直接 return，两侧校验都不过、HTTP 仍 200。「所有过 agent 的请求都过了 roles verifier」不是事实。

### 3. 工具输出的标量必须字面出现在最终答案里

`outputEvidenceAppears`，被 `contains(verifier.AllowedTools, run.Name)` 门控。

**这条限定仍然成立，而且比原来更差**：`agents.d/verifier.json` 的 `allowed_tools` 自旧基线起一个字没改，还是 `calculate` + `text_stats`；而 `tools.d/` 已从 16 涨到 **24**（`executor.json` 倒是随 IoT 更新了）。

**24 个工具里只有 2 个受这条约束。** 名单外的工具，模型把结果说错、说反、干脆不提，这条检查一律不触发；evidence 里也只有 `tool:<name>=success`，不会出现 `=verified`——**静默**。新增工具默认不进名单。⚠️ 新增的两个 IoT 工具是 `risk: high` + `requires_approval: true` 的**外发**工具，同样不受约束。

匹配规则本身有三处反直觉，其中两处自 `9a7c662` 起变了：

- output `TrimSpace` 后为空 → **直接判不通过**（未变）
- `null` / `{}` / `[]` **不再是自动失败**：`scalarValues` 给它们各返回一个字面量 token（`"null"` / `"{}"` / `"[]"`），于是要求变成「**必须把这个符号原样抄进自然语言答复里**」——output 是 `{"items":[]}` 时答案里得真的出现 `[]`
- 数字标量经 `json.Decoder.UseNumber()` **原样保留 JSON 里的字面量**：`1000000` 就是 `1000000`，旧的 `%g` → `1e+06` 陷阱已不存在。**但匹配是字面量严格相等**——工具输出写成 `1e6` / `1.50` / `1.0` 时，答案里必须原样出现该字面量

第 1、3 条的「限定」不是笔误，是当前的真实状态。依赖任何一条做设计判断前，先确认你在哪条路径上。

## 当前未决

先跑 `gh issue list --state all`（不带 `--state all` 只列 OPEN，看不到已关闭的那些）。当前 OPEN 的是 **#19 / #20 / #22**（#21 已关闭，其内容并入本节；#24 已完成高清运行证据并关闭）。

- ✅ **已闭合：admin 端点在默认配置下无鉴权。** `f767ccd` 让 `withAdminAuth` 独立于节点凭证并 **fail closed**——`tokenMatches` 的第一个条件就是 `expected != ""`，所以 `admin_token` 为空时任何请求都拿 401，`auth_token` 也不再能换到审批 / 关系 / 运行时配置权限。这条原先记在 #22。

  🔴 **但别把它读成「鉴权整体收紧了」——`withAuthScope` 一字未变**：它开头仍是 `if s.cfg.AuthToken == "" && s.network == nil { next(...) }`，命中 `AuthToken` / `AdminToken` 时也仍是直接放行且**不注入 relationship**。两个函数管的是两批端点，下面四条 P1 的残留根因在后者，**没有被 `f767ccd` 触及**。

- **可信执行内核的四个 P1：原评审范围已 PASS，但用户粒度仍未闭合。** 旧的内部评审快照已从公开工作树移除；原评审只确认 relationship/节点粒度的整改，不要把 PASS 扩读为普通调用路径已经具备用户粒度信任。更细的残留风险仍以本节与 #21 为准。

  `6ec2f48` 按评审处方收紧了四条，但只收紧到**按 relationship / 节点隔离**，不是按用户隔离。四条共享同一个残留根因：`withAuthScope` 只在 `network.Authenticate` 成功时注入 relationship，命中 `cfg.AuthToken` / `cfg.AdminToken`、或「无 token 且无 network」时都直接放行且不写 ctx；而 `Config.Validate` 允许 loopback 监听时 `auth_token` 为空——**默认 loopback 部署下四条防护全部退化**。

  | | 已关闭的部分 | 仍然成立的缺口 |
  |---|---|---|
  | `ApprovedTools` | `authz.Narrow` 无条件清空，批准只能经 `NarrowApproved` / `boundApprovedRequest` 挂回 | `protocol.ReplyRequest.ApprovedTools` 字段仍在，trusted 路径原样保留；`/v1/agentteams/tasks` 的清零只在 `task.Office != nil` 分支；清除只在**接收侧**，`callPeerOnce` / `runRecursive` 发送侧整包转发 |
  | Office `ApprovalRecord` | `office_grant.go` 的 HMAC grant 覆盖整个请求，**审批与任务内容的绑定成立** | **不是独立信任根**：签名密钥就是调用方同一请求里发出的 bearer token（`signOfficeExecutionGrant(peer.Token, …)`），grant 只重复证明「持有凭证」；无有效期、无 nonce、非一次性；`/v1/office/tasks/compensate` 完全没有审批门 |
  | `SessionID` | `Conversation.Owner` 持久化，路径按 `sha256(owner + id)` 分命名空间，错 owner 报 `session owner mismatch`，服务端 7 个调用点全传 `requestOwner` | principal 粒度是 **token / 节点，不是用户**；`SessionID` 本身仍是调用方自选、无签发、无熵要求；`loadLegacyLocked` 会认领任意升级前的旧文件且不删除 |
  | `TraceID` | `StartOwned` 用 `O_CREATE\|O_EXCL` 预留文件，**既有审计证据不会被后来的请求覆盖** | 「调用方可写」本身没关：7 个入口里仍有 **5 个**只在 `!trace.IsValidID(...)` 时才改用服务端 ID，派生出**存在性探针**与 **ID 预占**两条侧信道；owner 过滤不覆盖共享 token / 匿名调用方 |

  **在这四条收紧到「按用户」之前，不要把它们当作可信输入来设计新功能。**

- ⚠️ 两个与之相连的静默失效，排查时先想到它们：`Store.StartOwned` 占位遇到非 `IsExist` 的文件系统错误会 `return nil`，`Recorder` 各方法全部 `r == nil` 早退——**整个请求零 span、零日志、HTTP 照样 200**；撞名时循环换新 ID 并由 handler 回写响应，按原 ID 去捞会捞到别人的。session 侧同型：升级前由 peer 建立的会话在升级后 `Load` 得 `ErrNotExist`，`Append` 直接建新 `Conversation`，**历史静默清零、请求照常 200**。

- **#21** 保留为历史证据链（四个 P1 在 `797a793` 上的完整攻击路径）。引用时注意它**锚在 `797a793`**，其中 `traces.Start` 在当前 tree 应读作 `traces.StartOwned`。

- **#20** 已按 ECS 规模落地精简 Harness：`AGENTS.md` 继续作为内容真源，`CLAUDE.md` 只做转引与 Claude-only 边界；判定逻辑只在 `scripts/harness/core.py`，`.codex/hooks/*.py` 是小于 3 KiB 的薄 adapter，`.codex/hooks.json` 由 `scripts/render-codex-hooks.py` 机械生成。生成命令绑定共享 bundle SHA-256，核心变化会先 exit 2，再通过新 command hash 触发每位成员 `/hooks` 重新授信；`.githooks/` 与 CI 是不依赖 Agent 的最终兜底。完整契约见 `docs/harness-governance.md`。**`skills.d/` 是 ECS Runtime Skill，不能迁入或链接为 `.agents/skills/`。**

- **#19** 是当前的场景主线（可信会议督办闭环），其验收清单决定哪些交付算完成。
