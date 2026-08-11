# ECS Runtime 代码职责

这份文档约束核心 Runtime 的代码归属，避免 Prompt、HTTP transport、工作流编排和持久化状态机重新集中到单个文件。

## 依赖方向

```text
HTTP Handler
  → Application orchestration
    → Agent / Office / Network domain
      → Registry / Store / Platform ports
```

- Handler 负责请求大小、JSON 解码、鉴权入口、HTTP 状态码和响应编码。
- Application orchestration 负责调用顺序、超时、节点选择和跨模块结果组合。
- Domain 负责能力规则、状态转换、审批、Receipt 和补偿语义。
- Store/Platform 负责文件持久化与系统能力，不反向依赖 HTTP。

## Agent

| 文件 | 职责 |
|---|---|
| `internal/agent/agent.go` | 模型执行循环、Tool/Skill 调用、结果验证和汇总 |
| `internal/agent/prompt.go` | System Prompt、身份上下文、能力目录和 Manifest 问答渲染 |
| `internal/agent/openai.go` | OpenAI-compatible transport 与响应协议 |
| `internal/agent/tool_alias.go` | 传给模型的安全 Tool/Skill 别名 |

新增 Prompt 片段应优先进入 `prompt.go` 的命名构建函数，不在 Handler 或执行循环中继续拼接大段模板。

## HTTP Server

| 文件 | 职责 |
|---|---|
| `internal/server/server.go` | Server 依赖、构造、路由注册和轻量目录入口 |
| `internal/server/reply_handlers.go` | 单节点 Reply、Session 写入和 request 幂等入口 |
| `internal/server/group_handlers.go` | Group fan-out、竞速/全量调度和 peer 调用 |
| `internal/server/recursive_handlers.go` | Recursive 调度、深度/路径约束和嵌套 peer 调用 |
| `internal/server/agentteams_handlers.go` | AgentTeams Task 接入与 Office Stage 映射 |
| `internal/server/capability_handlers.go` | 节点身份、能力、Session 与健康查询 |
| `internal/server/http_helpers.go` | JSON、认证中间件、日志与 panic recovery |
| `internal/server/office.go` | Office HTTP 数据结构、WorkOrder CRUD 与审批 Handler |
| `internal/server/office_dispatch.go` | Controller 侧节点选择与 WorkOrder/Stage 调度 |
| `internal/server/office_worker.go` | Worker 执行、peer 调用与补偿执行 |
| `internal/server/office_contracts.go` | Connector/Skill 匹配、Receipt 校验与 Stage Prompt |

HTTP 文件可以调用 domain API，但不应自行实现新的审批策略或持久化格式。

## Office 状态与持久化

| 文件 | 职责 |
|---|---|
| `internal/office/registry.go` | Registry 生命周期、Manifest 快照、WorkOrder 编译和通用原子更新入口 |
| `internal/office/approval.go` | 审批状态、策略快照和决定流程 |
| `internal/office/execution_state.go` | Durable Attempt、Lease、Outbox、Reaper 和恢复状态 |
| `internal/office/agentteams_state.go` | AgentTeams Task ↔ Office Stage 状态映射 |
| `internal/office/store.go` | WorkOrder 查询、owner 过滤与文件保存 |
| `internal/office/manifest_validation.go` | Office Manifest、DAG 和执行契约校验 |
| `internal/office/types.go` | 稳定领域数据结构 |

状态迁移必须通过 `Registry.UpdateOrder` 或后续专用 transition API 完成。不要在 HTTP 层直接读写 WorkOrder 文件，也不要把持久化失败降级成成功响应。

## IoT Connector

| 文件 | 职责 |
|---|---|
| `internal/iot/connector.go` | 命名 HTTP/MQTT profile、目标白名单、环境凭据、网络调用和有界输出 |
| `tools.d/iot_*.json` | 模型可见的紧凑输入 Schema、风险和 Connector capability |
| `skills.d/iot.*/` | 使用条件、失败边界和 Skill → Tool 合同 |

IoT 目标主机、Broker 和凭据必须留在运行配置中。不得把任意 URL/Broker 参数重新加入 Tool
Schema，也不得把设备协议实现塞回通用 `toolbox`。

## 新代码放置检查

提交前确认：

1. 新字符串是在表达 Prompt，还是在表达确定性策略？Prompt 进入 `agent/prompt.go`；确定性策略进入对应 domain。
2. 新 Handler 是否只做 transport 映射？跨节点循环或状态推进应进入明确的 orchestration 文件。
3. 新 Office 状态是否有唯一 transition 入口、旧状态守卫和持久化错误处理？
4. 新 Connector 是否作为 adapter 注册，而不是继续加入通用 `toolbox` 内核？
5. 拆分后是否仍能通过 `go test ./...`、`go vet ./...` 和 `git diff --check`？
