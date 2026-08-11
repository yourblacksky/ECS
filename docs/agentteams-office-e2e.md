# AgentTeams Controller → ECS Worker → Office Stage 端到端回归

这项回归补足“补丁可应用”与“系统真实连通”之间的证据缺口，并把完整会议督办
闭环（scope → extract → verify → dispatch）固化为真实多节点产物。运行：

```bash
make demo-agentteams-office
```

回归固定检出 `agentscope-ai/AgentTeams@2540c968a642845c4b9382afd75d8c80ed861137`，
应用 ECS Runtime 补丁，并在该上游源码内部调用真实 Controller `Deployer` 生成
`MemberRuntimeConfig/runtime.yaml`。随后启动真实 ECS Worker：

```text
AgentTeams Controller Deployer
  → runtime.yaml（runtime=ecs、身份、模型、Skill 白名单）
  → ECS Worker 启动并加载 Runtime Profile
  → POST /api/v1/workers/ecs-worker-1/ready（首次 + 周期心跳）
  → Matrix TASK_ASSIGNED × 4（scope → extract → verify → dispatch 顺序派发）
  → ECS /v1/agentteams/tasks（每个 task 绑定一个 Office Stage）
  → AgentTeams Task ↔ Office Stage（meeting_planner / minutes_clerk / decision_reviewer / followup_coordinator）
  → dispatch 停在人工审批门（PENDING_APPROVAL）
  → admin approve → 同一 task 经 Matrix 重派发恢复执行
  → ECS execution Trace + result.json × 4
  → Matrix TASK_COMPLETED
```

## 真实实现与测试替身

| 边界 | 回归使用内容 |
|---|---|
| AgentTeams Runtime 投影 | 固定上游提交中补丁后的真实 `service.Deployer` |
| ECS Worker | 当前提交构建的真实 `cmd/ecs` 进程 |
| Task / Office / Trace | ECS 真实持久化 Store、Office Registry 和 Trace Recorder |
| Controller lifecycle HTTP | 确定性测试替身，遵循上游 `/ready` 契约 |
| Matrix Client-Server API | 确定性测试替身：按序派发 4 个 Assignment，审批后重发未完成任务并接收 Completion |
| LLM | OpenAI-compatible 确定性响应，实现 Office 必选能力 decision 协议；本回归验证编排契约，不衡量模型质量 |
| 对象存储 | AgentTeams 自带 `ossfake.Memory`；生成内容来自真实 Deployer |

因此这项测试证明 Controller 投影、ECS Runtime 消费、Matrix Assignment、四个 Office
Stage 的顺序执行与人工审批门、receipt 验证、证据回传能够连成一条链；它不宣称已经
部署生产 Matrix、MinIO 或模型网关。

## 验收证据

默认输出到 `data/demo/agentteams-office-e2e/`，也可指定目录：

```bash
./scripts/demo-agentteams-office-e2e.sh --out /tmp/agentteams-office-e2e
```

输出包含：

- `controller-runtime.yaml`：真实 AgentTeams Deployer 投影；
- `controller-state.json`：ready/heartbeat 次数、已派发 Assignment 数与 Completion；
- `<task>-meta.json`、`<task>-result.json`、`<task>-result.md`：四个 stage
  （scope / extract / verify / dispatch）各自的共享任务状态；
- `work-order.json`：完成后的 Office Work Order（四个 Stage、审批记录、receipt、outbox）；
- `execution-trace-<stage>.json`：四个 stage 各自的 ECS 服务端执行 Trace；
- `summary.json`：机器可读断言，覆盖 Controller 投影、心跳、四任务顺序分派与回报、
  Task ↔ Stage 映射、四个 Skill（`meeting.scope.plan` / `meeting.fact.extract` /
  `meeting.evidence.verify` / `office.action.dispatch`）、`execution.receipt.verify`
  回执校验、人工审批门（approve 后同 task 恢复）、持久化 Trace、成功的 Attempt / Outbox；
- `SHA256SUMS`：关键证据摘要；
- `ecs.log`、`testbed.log`：诊断日志。

GitHub Actions 的 `AgentTeams Integration` 工作流运行同一命令，并上传保留 30 天的
`agentteams-office-e2e-<commit>` Artifact。AgentTeams 集成文件以及 ECS 的 Agent、Office、
Server、Skill、Tool、Prompt 和 Manifest 发生变化时都会触发回归；上传前还会重新校验
`SHA256SUMS`，避免损坏的证据进入 Artifact。
