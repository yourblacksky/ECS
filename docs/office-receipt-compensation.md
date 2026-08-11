# Office Receipt 验证与补偿闭环

人工审批回答“是否允许执行”，Receipt 回答“外部动作是否真实发生”，补偿回答“失败后如何
恢复到可控状态”。ECS 对 Office Dispatch 和 AgentTeams → Office 两条入口使用同一套
Receipt 与补偿合同。

## Receipt 强验证

当 Stage 声明名称包含 `receipt` 的必需产物时，ECS 不接受模型文本作为完成证明。执行必须：

1. 使用 ECS 生成的稳定幂等键 `office:{work_order_id}:{task_id}`；
2. 通过声明的 Skill 调用 Connector；
3. 获得包含动作、目标、结果、外部引用、时间和幂等键的结构化 Receipt；
4. 调用 `execution.receipt.verify` 进行确定性校验；
5. 由控制节点再次核验 Receipt 与工单、Task、Connector、Tool 和审批策略版本的绑定。

通过验证的证据持久化到 Task：

- `receipts`
- `receipt_verification.status = verified`
- `receipt_verification.expected_prefix`
- `receipt_verification.verified_at`

跨工单 Receipt、重复 Receipt ID、重复幂等键、错误时间、缺少外部引用或没有确定性验证结果
都会阻止 Task 进入 `completed`。

## Stage 级真实补偿

当 Stage 配置 `on_failure: compensate` 和 `compensation` 时，ECS 会寻找同时满足以下条件的
补偿 Tool：

- 提供声明的 Compensation Connector capability；
- Tool 名明确标识为 compensation Tool；
- 不在正常 Agent/Skill 的执行白名单内。

补偿由 ECS 执行内核直接调用，不交给模型自由规划。补偿请求使用独立稳定幂等键：

```text
office:{work_order_id}:{task_id}:compensate
```

成功状态为 `compensated`，并保存补偿 Receipt；补偿 Tool 缺失、调用失败或返回无效 Receipt
时状态为 `compensation_failed`，保留错误和审计记录供人工接管。

如果 Worker 请求超时、连接在响应前关闭，ECS 无法判断 Connector 是否已经产生副作用。
这种情况不会直接补偿，而会把 Attempt、Outbox、Task 和 WorkOrder 标为
`recovery_required`。随后使用原业务幂等键恢复执行。只有已经取得响应且 Receipt 验证明确
失败时，ECS 才进入本节的确定性补偿路径。

默认的 `office_action_compensate` 是本地演示 Connector，与
`office_action_dispatch` 一样返回可验证的本地回执。生产集成应由真实业务 Connector 提供
同一能力合同。

## 双路径回归

回归套件对 `Office Dispatch` 与 `AgentTeams → Office` 使用同一个 Receipt 验证函数，并覆盖：

- 正确 Receipt 被接受并绑定审批策略版本；
- 另一工单的幂等键被拒绝；
- 正常执行 Tool 与补偿 Tool 权限隔离；
- 补偿实际调用 Tool 并返回 `compensated` Receipt；
- Receipt 或补偿失败时不产生虚假的 `completed` 状态。

因此两条入口只负责不同的任务来源，可信执行语义保持一致：

```text
审批授权 → Connector 执行 → Receipt 验证 → 完成
          ├─响应未知→ recovery_required → 原幂等键恢复
          └─Receipt 确定失败→ 补偿 Connector → 补偿 Receipt / 人工接管
```
