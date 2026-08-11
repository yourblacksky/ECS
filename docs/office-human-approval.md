# Office 真实人工审批门

ECS 把人工审批实现为持久化的 Office 工单状态，而不是一次请求里的布尔参数。
当 Stage 的 `approval.mode` 为 `human` 或 `policy` 时，首次调度只生成审批单，
不会调用 Skill 或 Connector。

## 状态流转

```text
pending
  │ 调度到受控 Stage
  ▼
pending_approval ── approve ──► pending/running ──► completed
        │
        ├── reject ───────────► rejected
        └── expire ───────────► 重新生成审批单，仍不执行
```

审批记录保存在工单 Task 的 `approval_record` 中，包括：

- 审批 ID、风险级别和所需权限；
- 本次获准的 Stage、Skill、Tool 与 Connector 快照；
- 申请人、申请时间和过期时间；
- 审批人、决定时间和审批意见；
- 对执行合同计算出的 `policy_version`。

Skill、Tool 或 Connector 集合发生变化后，旧审批不会被复用。执行节点还会在真正调用
Agent 和 Tool 前再次验证审批状态、有效期和策略快照。

## 发起审批

照常调度工单：

```http
POST /v1/office/work-orders/{work_order_id}/dispatch
Content-Type: application/json

{"group":"office"}
```

当运行到受控 Stage 时，服务返回 `202 Accepted`。工单和 Task 状态均为
`pending_approval`，Task 中包含待处理的 `approval_record.id`。

AgentTeams Task 映射到受控 Stage 时遵循同一规则：

- 返回 `202 Accepted` 和 `PENDING_APPROVAL`；
- AgentTeams 元数据记录 `pending_approval`；
- 不生成 `result.json`，因此审批后可以使用同一 Task 请求恢复；
- Matrix 协作消息使用 `TASK_PENDING_APPROVAL`，不会误报任务完成。

## 批准或拒绝

审批接口只在 Admin API 启用时注册，并要求 Admin Bearer Token：

```http
POST /v1/admin/office/work-orders/{work_order_id}/tasks/{stage_id}/approval
Authorization: Bearer <admin_token>
Content-Type: application/json

{
  "approval_id": "approval_...",
  "decision": "approve",
  "actor": "QA负责人",
  "opinion": "材料完整，同意执行"
}
```

`decision` 只能是 `approve` 或 `reject`。`actor` 是写入审计记录的人类身份标签，
真正的权限边界仍是 Admin Token。

批准后，重新发送原调度请求或原 AgentTeams Task 请求即可恢复执行。拒绝会把 Task 和
工单置为 `rejected`，后续调度返回冲突且不会调用 Connector。

## 配置

```json
{
  "admin_token": "replace-with-a-secret",
  "admin": {
    "enabled": true
  },
  "office": {
    "approval_ttl": "15m"
  }
}
```

`approval_ttl` 可配置为 1 分钟至 24 小时，默认 15 分钟。生产环境应通过安全的密钥注入
方式提供 Admin Token，并把审批接口放在受控管理网络中。
