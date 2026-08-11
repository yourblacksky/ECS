# ECS 配置参考

ECS 从 JSON 文件加载配置；示例位于 [`config.example.json`](../config.example.json)。运行时先
应用代码默认值，再覆盖 JSON、Secret Provider 和环境变量，最后执行完整校验。`llm.model`
没有隐式默认值，必须显式声明。

## 启动、路径与优先级

```bash
cp config.example.json config.json
go run ./cmd/ecs -config ./config.json
```

所有相对路径都相对于 **配置文件所在目录**，而不是当前工作目录。该规则适用于：

- `agent.system_prompt_file`、`agent.conversation_guidance_file`；
- `agent_roles.directory`、`agent_roles.pipeline_directory`；
- `agentteams.runtime_config_file`、`agentteams.task_directory`、`agentteams.matrix_since_file`；
- `office.directory`、`office.order_directory`；
- `tools.directory`、`skills.directory`、`skills.knowledge_directory`；
- `trace.directory`、`sessions.directory`、`network_auth.directory`。

值的优先级从低到高为：代码默认值 → JSON → `*_secret` 对应的宿主 Secret Provider → 固定
环境变量 → 配置中指定的 `*_env`。移动端 Secret Store 通过 Secret Provider 注入，不要求把
密钥写入 JSON。

## 根配置与模型

| 字段 | 默认/示例 | 作用与约束 |
|---|---|---|
| `node_name` | `ecs-node` | 节点显示名。 |
| `listen` | `127.0.0.1:8090` | HTTP 监听地址；非 loopback 必须配置节点鉴权。 |
| `auth_token` | 空 | 本节点共享 API Token；优先使用环境变量或 Secret Provider。 |
| `auth_token_secret` | 空 | 宿主 Secret Provider 中的节点 Token 引用。 |
| `admin_token` | 空 | Admin API 独立 Token；不能由 `auth_token` 代替。CLI loopback 首次启动会安全生成，非 loopback 必须显式提供。 |
| `admin_token_secret` | 空 | Admin Token 的 Secret Provider 引用。 |
| `log_level` | `info` | `debug`、`info`、`warn` 或 `error`。 |
| `llm.base_url` | `http://127.0.0.1:11434/v1` | OpenAI-compatible API 根地址。 |
| `llm.api_key` | 空 | 模型密钥；不应提交到仓库或评审包。 |
| `llm.api_key_secret` | 空 | 模型密钥的 Secret Provider 引用。 |
| `llm.model` | 无 | **必填**，例如 `qwen3:4b-q4_K_M` 或 `deepseek-v4-flash`。 |
| `llm.timeout` | `90s` | 单次模型请求超时，使用 Go duration 格式。 |
| `llm.native_tools` | `false` | 是否使用模型 API 原生 Tool Calling；关闭时使用 ECS 文本协议。 |
| `llm.reasoning_effort` | `none` | `none`、`low`、`medium`、`high`。 |

固定环境变量：`ECS_AUTH_TOKEN` 覆盖 `auth_token`，`ECS_ADMIN_TOKEN` 覆盖
`admin_token`，`ECS_LLM_API_KEY` 覆盖 `llm.api_key`。启动日志只记录最终 Base URL、模型、
Tool 协议、reasoning 配置和 Admin Token 指纹，不记录任何完整凭证。

## Agent、角色与 Pipeline

| 字段 | 默认 | 作用与约束 |
|---|---|---|
| `agent.system_prompt_file` | `prompts/system.small.zh.txt` | 基础 System Prompt 文件。 |
| `agent.conversation_guidance_file` | `prompts/conversation.default.zh.txt` | 会话引导文件；可为空。 |
| `agent.conversation_guidance` | `true` | 是否启用会话引导。 |
| `agent.max_system_prompt_chars` | `8000` | System Prompt 字符预算，至少 1000。 |
| `agent.max_agent_context_chars` | `2200` | Agent 身份/任务上下文预算，至少 200。 |
| `agent.max_conversation_guide_chars` | `900` | 会话引导预算，可为 0。 |
| `agent.max_tool_catalog_chars` | `3200` | 发给模型的能力目录预算，至少 200。 |
| `agent.max_tool_rounds` | `3` | 最大 Tool/Skill 循环轮数，0–10。 |
| `agent.max_prompt_tools` | `12` | 单轮最多暴露的候选能力，必须为正数。 |
| `agent.approval_mode` | `high_risk` | `off`、`high_risk` 或 `all`。 |
| `agent.group_synthesis` | `true` | 多节点结果是否进入汇总。 |
| `agent.group_dispatch_mode` | `single` | `single`、`broadcast` 或 `race`。 |
| `agent.group_race_fanout` | `2` | race 并发节点数，1–32。 |
| `agent_roles.directory` | `agents.d` | Agent Manifest 目录。 |
| `agent_roles.pipeline_directory` | `pipelines.d` | Pipeline Manifest 目录。 |
| `agent_roles.default_pipeline` | `default` | 默认 Pipeline 名称。 |

## AgentTeams

| 字段 | 默认 | 作用与约束 |
|---|---|---|
| `agentteams.enabled` | `false` | 是否启用 AgentTeams Worker 接入。 |
| `agentteams.controller_url` | 空 | Controller lifecycle HTTP 地址。 |
| `agentteams.worker_name` | 空/示例 `ecs-worker-1` | 启用时必填的 Worker 身份。 |
| `agentteams.runtime_config_file` | 空 | Controller 生成的 `runtime.yaml`；Matrix 模式必填。 |
| `agentteams.task_directory` | `data/agentteams/shared` | TeamHarness 共享任务目录。 |
| `agentteams.max_task_prompt_chars` | `3000` | Task Prompt 上限；启用时至少 500。 |
| `agentteams.heartbeat_interval` | `30s` | 向 Controller 报告心跳的周期。 |
| `agentteams.matrix_url` | 空 | Matrix Client-Server API 地址。 |
| `agentteams.matrix_token_env` | `AGENTTEAMS_WORKER_MATRIX_TOKEN` | Matrix Token 所在环境变量名。 |
| `agentteams.matrix_sync_timeout` | `30s` | Matrix `/sync` 超时，至少 1 秒。 |
| `agentteams.matrix_since_file` | `data/agentteams/matrix-since` | Matrix since token 持久化文件。 |

固定覆盖变量包括 `ECS_AGENTTEAMS_CONTROLLER_URL`、`ECS_AGENTTEAMS_WORKER_NAME` 和
`AGENTTEAMS_MATRIX_URL`；Matrix 凭证使用 `agentteams.matrix_token_env` 指向的变量。

## Office durable execution

| 字段 | 默认 | 作用与约束 |
|---|---|---|
| `office.directory` | `office.d` | Office Component Manifest 目录。 |
| `office.order_directory` | `data/office/orders` | WorkOrder、Attempt、Outbox 和审批状态目录。 |
| `office.max_stages` | `16` | 单个组件最大 Stage 数，1–64。 |
| `office.max_context_entries` | `64` | 工单上下文条目上限，1–256。 |
| `office.approval_ttl` | `15m` | 审批有效期，1 分钟至 24 小时。 |
| `office.attempt_lease` | `2m` | durable Attempt 租约，10 秒至 24 小时。 |
| `office.reaper_interval` | `30s` | 过期 Attempt 扫描周期，1 秒至 attempt lease。 |

## Tool、Skill 与 grounding

| 字段 | 默认 | 作用与约束 |
|---|---|---|
| `tools.directory` | `tools.d` | Tool Manifest 目录。 |
| `tools.default_timeout` | `10s` | Tool 未声明时的执行超时。 |
| `tools.max_output_bytes` | `65536` | 单次 Tool 输出字节上限。 |
| `skills.directory` | `skills.d` | Skill Manifest 和 `SKILL.md` 目录。 |
| `skills.knowledge_directory` | `skills.d/_knowledge` | 本地受控知识片段目录。 |
| `skills.max_prompt_skills` | `5` | 单轮最多提供给模型的 Skill，1–20。 |
| `skills.max_steps` | `4` | 单个 Skill 最大工作流步骤，1–10。 |
| `skills.documentation_required` | `true` | 是否要求启用 Skill 必须有合法 `SKILL.md`。 |
| `skills.grounding_enabled` | `true` | 是否把授权后检索到的文档写入 Prompt。 |
| `skills.max_document_chars` | `12000` | 单文档上限，1000–100000。 |
| `skills.max_grounding_chars` | `1800` | 单请求 grounding 总预算，200 至文档上限。 |
| `skills.max_knowledge_documents` | `3` | 单请求知识片段数，0–20。 |

## Session、Trace 与 Admin

| 字段 | 默认 | 作用与约束 |
|---|---|---|
| `sessions.directory` | `data/sessions` | 按 principal/owner 隔离的会话目录。 |
| `sessions.max_messages` | `0` | 持久消息上限；0 表示不按条数裁剪。 |
| `sessions.context_messages` | `20` | 送入模型的最近消息数，必须为正数。 |
| `sessions.max_context_chars` | `4000` | 会话上下文字符预算。 |
| `trace.enabled` | `true` | 是否持久化执行 Trace。 |
| `trace.directory` | `data/traces` | Trace 目录。 |
| `trace.max_value_chars` | `4000` | 单个 Trace 值预算，200–100000。 |
| `trace.max_traces` | `1000` | 本地最大 Trace 数，1–100000。 |
| `trace.retention` | `168h` | 保留期，至少 1 小时。 |
| `admin.enabled` | `true` | 是否启用 `/admin/` 与 `/v1/admin/*`。 |
| `admin.log_capacity` | `1000` | 内存日志环容量，100–10000。 |

CLI 在 loopback 且未显式配置时，把随机生成的独立凭证保存为配置目录下权限为 `0600` 的
`data/admin.token`。多人工作站、CI Runner 和共享开发机仍应显式注入 `ECS_ADMIN_TOKEN`；
loopback 只限制网络来源，不隔离同一主机上的其他进程。Admin API 缺少该凭证时始终返回
`401`，不会回退到 `auth_token`。

## Discovery 与 Registry

| 字段 | 默认 | 作用与约束 |
|---|---|---|
| `discovery.enabled` | `true` | 启用当前平台支持的本地发现 Provider。 |
| `discovery.service_type` | `_ecs._tcp` | DNS-SD TCP service type。 |
| `discovery.advertise_url` | 从 listen 推导 | 节点对外公告的 HTTP(S) 地址。 |
| `discovery.groups` | 空/示例 `lab` | 本节点公告所属组。 |
| `discovery.interval` | `5s` | 浏览/刷新周期，必须为正。 |
| `discovery.expiry` | `20s` | 节点过期时间，必须大于 interval。 |
| `discovery.registry.enabled` | `false` | 是否向 Controller Registry 注册/查询。 |
| `discovery.registry.serve` | `false` | 是否在本节点提供 Registry API。 |
| `discovery.registry.controller_urls` | 空 | 一个或多个 HTTP(S) Registry 地址；启用 client 时必填。 |
| `discovery.registry.token` | 空 | Registry Token 明文值，不建议写入文件。 |
| `discovery.registry.token_env` | `ECS_DISCOVERY_REGISTRY_TOKEN` | Registry Token 环境变量名。 |
| `discovery.registry.token_secret` | 空 | Registry Token Secret Provider 引用。 |
| `discovery.registry.heartbeat_interval` | `15s` | Registry 心跳周期。 |
| `discovery.registry.lease_timeout` | `45s` | Registry 租约，必须大于 heartbeat。 |
| `discovery.registry.request_timeout` | `5s` | Registry HTTP 请求超时。 |

Registry 只解决“节点在哪里”，不授予关系或 Tool/Skill 权限。`ECS_DISCOVERY_REGISTRY_TOKEN`
是示例默认变量名；若修改 `token_env`，以配置的新变量名为准。

## Network authentication 与嵌套调用

| 字段 | 默认 | 作用与约束 |
|---|---|---|
| `network_auth.enabled` | `false` | 启用节点身份、关系授权和 capability narrowing。 |
| `network_auth.directory` | `data/network` | 身份、关系和策略持久目录。 |
| `network_auth.route_overrides` | `{}` | `node_id → http(s) URL` 的受控路由覆盖。 |
| `network_auth.heartbeat_interval` | `30s` | 关系心跳周期。 |
| `network_auth.lease_timeout` | `90s` | 关系租约，必须大于 heartbeat。 |
| `network_auth.enrollment_ttl` | `10m` | 加网请求有效期，至少 1 分钟。 |
| `network_auth.parent.url` | 空 | 父节点 HTTP(S) 地址。 |
| `network_auth.parent.node_id` | 空 | 预期父节点稳定 ID。 |
| `network_auth.parent.auto_discover` | `false` | 通过本地/Registry 发现父节点。 |
| `network_auth.parent.discovery_groups` | 空 | 自动发现父节点时允许的组。 |
| `network_auth.parent.pairing_code` | 空 | 配对码明文值，不建议持久化。 |
| `network_auth.parent.pairing_code_env` | 示例 `ECS_PARENT_PAIRING_CODE` | 配对码环境变量名。 |
| `network_auth.parent.pairing_code_secret` | 空 | 配对码 Secret Provider 引用。 |
| `nested.enabled` | `true` | 是否允许受深度限制的递归节点调用。 |
| `nested.default_max_depth` | `8` | 默认最大递归深度，不能为负。 |
| `nested.discovery_groups` | 空 | 可作为递归子节点的发现组。 |
| `nested.children[].node_id` | 空 | 静态子节点稳定 ID。 |
| `nested.children[].name` | 必填 | 静态子节点名称。 |
| `nested.children[].url` | 必填 | 静态子节点 HTTP(S) URL。 |
| `nested.children[].token` | 空 | 静态子节点 Token 明文值。 |
| `nested.children[].token_env` | 空 | Token 环境变量名。 |
| `nested.children[].token_secret` | 空 | Token Secret Provider 引用。 |

启用 `network_auth` 时必须启用 Admin，并提供独立 `admin_token`。启用父节点自动
发现时，本地发现或 Registry 至少一个必须启用。

## 静态 Group

`groups` 是 `组名 → Peer[]` 映射；下列 `<name>` 表示任意组名：

| 字段 | 作用 |
|---|---|
| `groups.<name>[].node_id` | 目标节点稳定 ID，可选。 |
| `groups.<name>[].name` | Peer 名称，必填。 |
| `groups.<name>[].url` | Peer HTTP(S) URL，必填。 |
| `groups.<name>[].token` | 明文关系 Token，不建议提交。 |
| `groups.<name>[].token_env` | 关系 Token 环境变量名，推荐。 |
| `groups.<name>[].token_secret` | 关系 Token Secret Provider 引用。 |

## HTTP 资源边界与重试

| 字段 | 默认 | 作用与约束 |
|---|---|---|
| `http.read_timeout` | `15s` | HTTP 请求头/Body 读取超时。 |
| `http.write_timeout` | `120s` | 响应写入超时。 |
| `http.max_body_bytes` | `1048576` | 请求 Body 上限，必须为正数。 |
| `http.max_concurrent` | `16` | 并发请求门限，必须为正数。 |
| `http.peer_timeout` | `120s` | 调用其他 ECS 节点的单次超时。 |
| `http.peer_retries` | `2` | Peer 请求重试次数，0–10。 |
| `http.retry_backoff` | `500ms` | 重试退避，不能为负数。 |

## IoT Connector profiles

IoT Tool 只能选择管理员预先定义的 profile，不能让模型提交任意 Host/Broker。`<name>` 表示
profile 名称，名称需匹配字母开头、最长 64 字符的受限标识符。

| 字段 | 作用与约束 |
|---|---|
| `iot.max_response_bytes` | 所有 IoT Connector 响应上限；默认 65536，范围 1024–1048576。 |
| `iot.http.<name>.base_url` | HTTP(S) origin/base path；禁止内嵌凭证、query 和 fragment。 |
| `iot.http.<name>.allowed_methods` | 允许 `GET/POST/PUT/PATCH/DELETE` 的非空集合。 |
| `iot.http.<name>.allowed_path_prefixes` | 允许路径前缀；必须以 `/` 开头并禁止 `..`、query、fragment。 |
| `iot.http.<name>.headers_env` | `Header → 环境变量名` 映射；模型看不到密钥值。 |
| `iot.http.<name>.timeout` | 正数且不超过 2 分钟。 |
| `iot.mqtt.<name>.broker_url` | `mqtt/mqtts/tcp/ssl/ws/wss` Broker，禁止内嵌凭证。 |
| `iot.mqtt.<name>.client_id` | 可选固定 Client ID。 |
| `iot.mqtt.<name>.username_env` | MQTT 用户名环境变量名。 |
| `iot.mqtt.<name>.password_env` | MQTT 密码环境变量名。 |
| `iot.mqtt.<name>.allowed_topic_prefixes` | 非空 Topic 前缀集合，禁止通配符 `+`、`#`。 |
| `iot.mqtt.<name>.timeout` | 正数且不超过 2 分钟。 |

## 校验与排错

```bash
make verify-config-reference
go run ./cmd/ecs -config ./config.json
```

配置变更需要重启；Tool/Skill 文档和 Manifest 可以按运行时 Reload 规则重载。启动失败时应先
检查：模型是否显式声明、相对路径是否以配置文件目录为基准、非 loopback 是否有 Token、
Discovery URL 是否有效，以及 lease/expiry 是否大于对应 heartbeat/interval。
