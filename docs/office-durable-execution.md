# Office Durable Execution

Office DAG 的每个 Stage 都使用落盘的 Attempt、Lease 和 Outbox 状态执行。目标不是承诺网络调用严格 exactly-once，而是在进程退出、网络中断或结果写盘失败后明确回答：

- 系统准备执行什么；
- 哪一次执行可能已经发出；
- 是否取得并验证了 Receipt；
- 当前是否可以安全结束，还是必须恢复。

## 持久化顺序

```text
pending / recovery_required
  → ClaimTask
  → Attempt(claimed) + Outbox(pending) 原子落盘
  → MarkAttemptDispatched
  → Attempt(dispatched) + Outbox(dispatched) 落盘
  → 调用 Worker / Connector
  → Receipt 验证
  → Attempt(succeeded|failed) + Outbox(confirmed|failed)
```

执行前的 Intent 与 Attempt 写在同一个 WorkOrder JSON 更新中。只有这次更新成功后，Controller 才能调用远端 Worker。

## Attempt

每个 Task 保存：

- 单调递增的 `attempt_count`；
- 当前 `active_attempt_id`；
- 历史 `attempts`；
- Worker、Trace、业务幂等键；
- claim、dispatch、lease expiry 和 completion 时间。

恢复执行会创建新的 Attempt，但沿用相同的业务幂等键：

```text
office:<work-order-id>:<task-id>
```

因此 Connector 必须把该键作为副作用去重合同。Attempt ID 只区分 ECS 内部的执行轮次，不能代替业务幂等键。

## Lease 与 Reaper

配置项：

```json
{
  "office": {
    "attempt_lease": "2m",
    "reaper_interval": "30s"
  }
}
```

执行期间 Controller 每隔约三分之一 Lease 时长续租。Runtime 启动时会立即扫描过期 Attempt，运行期间按 `reaper_interval` 持续扫描。

过期的 `claimed` 或 `dispatched` Attempt 不会直接标记失败，也不会盲目补偿，而会变成：

```text
Attempt: outcome_unknown
Outbox: recovery_required
Task: recovery_required
WorkOrder: recovery_required
```

原因是请求可能已经到达 Connector，只是结果尚未被 Controller 持久化。

## Outbox

Outbox Event 与 WorkOrder 保存在同一原子文件更新中，状态包括：

| 状态 | 含义 |
|---|---|
| `pending` | Intent 已落盘，尚未记录网络 hand-off |
| `dispatched` | Controller 即将或已经调用 Worker |
| `confirmed` | 结果和 Receipt 已持久化 |
| `failed` | 已取得确定失败结果，失败状态已持久化 |
| `recovery_required` | 是否产生副作用未知，需要使用原幂等键恢复 |
| `superseded` | 后续 Attempt 使用同一幂等键成功，旧未知事件已被恢复结果取代 |

当前 Outbox 是 local-first 的事务 Intent 与审计账本，发送仍由 Controller 同步完成，不依赖外部 MQ。它不保存 peer token，也不会在后台未经操作员/上层调度重新发送未知副作用。

## 恢复

`recovery_required` WorkOrder 可以再次调用原 dispatch API。Registry 会：

1. 保留旧 Attempt 和旧 Outbox 证据；
2. 创建新的 Attempt 和 Outbox Event；
3. 沿用相同业务幂等键；
4. 重新执行 Receipt 验证；
5. 仅在结果成功落盘后确认新 Outbox。

如果远端调用没有返回，ECS 不自动调用补偿 Connector。只有已经收到响应、但 Receipt 验证确定失败时，才按 Stage 的 `on_failure` 合同执行补偿。

## 可见性

WorkOrder 详情直接包含 `tasks[].attempts` 与 `outbox`。列表摘要额外提供：

- `active_attempts`
- `pending_outbox`
- `recovery_tasks`

Audit 中会记录 `task.attempt.claimed`、`outbox.enqueued`、`outbox.dispatched`、`task.completed` 和 `task.recovery_required`。
