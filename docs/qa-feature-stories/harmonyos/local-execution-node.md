# QA 功能说明：HarmonyOS 本地执行节点

## 功能标识

- 功能域：`harmonyos`
- 功能点：`local-execution-node`
- 最近更新：HarmonyOS 原生节点与模型密钥安全注入

## 背景

ECS 需要在 HarmonyOS 设备上作为真实执行节点运行，而不是只连接桌面或服务器节点
的控制客户端。应用还需要在不把模型密钥写入普通配置的前提下支持本机对话调试。

## 用户故事

作为 HarmonyOS 设备用户，我希望应用启动后直接成为一个 ECS 节点，并能安全配置
DeepSeek API Key、查看本机能力和打开管理后台，以便独立完成对话与工具调试。

## 变更范围

- 新增 HarmonyOS Stage 应用、内嵌 Go Runtime、N-API 桥和本机后台。
- 新增 HarmonyOS 平台身份与前台 peer 能力。
- 新增 Asset Store 模型密钥保存及运行中内存注入。
- 不包含后台常驻、多播发现、shell、子进程和 HarmonyOS 设备 Tool。

## 当前确认行为

- 应用启动后监听 `127.0.0.1:18090`，节点名为 `ecs-harmony`。
- 首页显示平台实际可用的 Tool 与 Skill，以及 15 个 Agent、1 条 Pipeline 和 3 个 Office Stage。
- `/healthz` 返回节点健康状态，`/v1/capabilities` 返回完整 Manifest。
- 管理后台通过 ArkWeb 打开 `http://127.0.0.1:18090/admin/`。
- API Key 仅保存在 HarmonyOS Asset Store，并以内存 Secret 传入 Go Runtime。
- 完全退出并重新启动应用后，API Key 可恢复，对话仍可用。

## 触发入口

- 应用首页：自动启动本机 ECS 节点。
- DeepSeek API Key 输入框：仅在模型密钥未配置时显示。
- “打开后台”：节点运行后进入管理后台。
- 管理后台“对话调试”：向本机 `/v1/reply` 发送请求。

## 主流程

1. 用户启动 ECS 应用。
2. 应用从 Asset Store 读取模型密钥，并启动内嵌 Go Runtime。
3. 首页展示节点状态和能力数量。
4. 首次使用时，用户输入 DeepSeek API Key 并点击“保存”。
5. 应用将 Key 保存到 Asset Store，并立即更新运行中 LLM Client。
6. 用户打开后台，在“对话调试”中发送请求并看到模型回复。

## 分支与异常流程

- Asset Store 没有密钥：首页显示“需要配置 DeepSeek API Key”，节点能力仍可查看。
- 输入为空：显示“请输入 DeepSeek API Key”，不写入 Asset Store。
- Key 无效或模型服务异常：节点保持运行，`/v1/reply` 返回明确的模型错误。
- Go 动态库或 Runtime 启动失败：首页显示“启动失败”和错误信息。
- 应用退出：不承诺节点继续在后台运行。

## 验收标准

- 安装并启动 HAP 后，应用进程持续存活且 `/healthz` 返回 200。
- 首页能力计数与 `/v1/capabilities` 返回数量一致。
- 未配置 Key 时页面有明确引导，不错误显示模型已就绪。
- 保存有效 Key 后，无需重启即可完成一次 `/v1/reply` 对话。
- 强制停止并重新启动应用后，无需再次输入 Key，仍可完成对话。
- 仓库、HAP 配置和管理接口中不出现明文 API Key。
- 应用不声明未实现的后台常驻、子进程或多播能力。

## 数据与接口影响

- 新增本地秘密项别名：`ecs.llm.api_key`。
- `core.Status` 新增 `model_ready: boolean`，只表示内存中是否存在非空凭证。
- 新增应用内 C ABI/N-API：`ECSHarmonySetLLMAPIKey` / `setLLMAPIKey`。
- 既有 `/v1/reply`、会话和 Trace 数据结构不变。

## 前端 / App 细节

- 页面：`pages/Index`、`pages/Admin`。
- 用户可见状态：正在启动、本地节点运行中、启动失败、需要配置 Key、模型对话已就绪。
- 本地状态：节点配置/会话/Trace 位于应用沙箱；Key 位于 Asset Store。
- 权限：仅申请 `ohos.permission.INTERNET`。
- 本次未新增埋点。

## 后端 / Runtime 细节

- `mobile.StartWithSecrets` 加载应用沙箱配置和内存 Secrets。
- `core.Runtime.SetLLMAPIKey` 将 Key 更新到线程安全的 `agent.Client`。
- 模型请求开始时读取一份配置快照，避免与密钥更新产生数据竞争。
- 节点限制为回环监听，HTTP 并发上限为 8，模型超时为 90 秒。

## 测试关注点

- 正向：首次配置、即时对话、重启恢复、能力清单、后台加载。
- 边界：空 Key、重复保存、应用快速重开、缺省 `node_id` 字段。
- 异常：错误 Key、DeepSeek 401/超时、动态库加载失败、端口占用、Asset Store 异常。
- 回归：Android peer、iOS leaf、桌面 ECS 对话和 Tool/Skill Manifest。

## 变更历史

- 2026-07-30：新增 HarmonyOS 本地 ECS 节点及 Asset Store 模型密钥链路。
