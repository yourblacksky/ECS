# AgentTeams Task 与 Office Stage 映射

AgentTeams 负责任务规划、拆解和分配，ECS Office 负责校验并执行已经定义好的业务阶段。
两者通过 AgentTeams Task 请求中的 `office` 字段建立显式的一对一关系：

```json
{
  "project_id": "meeting-followup-2026-07-29",
  "task_id": "extract-minutes",
  "objective": "从会议材料中提取决定、行动项、责任人和期限",
  "trace_id": "meeting-trace-001",
  "office": {
    "work_order_id": "office_0123456789abcdef01234567",
    "stage_id": "extract"
  }
}
```

请求前应先通过 `POST /v1/office/components/{component}/work-orders` 创建工单。
`work_order_id` 指向该工单，`stage_id` 指向组件清单中的阶段 ID。

## 状态关系

| AgentTeams | Office Stage | Office Work Order |
|---|---|---|
| `PENDING_APPROVAL` | `pending_approval` | `pending_approval` |
| 接收并开始执行 | `running` | `running` |
| `SUCCESS` | `completed` | 还有未完成阶段时为 `running`，全部完成后为 `completed` |
| `BLOCKED` | `failed` | 按 Stage 的 `on_failure` 映射为 `blocked`、`compensation_required` 或 `failed` |

ECS 在开始执行前检查：

- AgentTeams Task 与 Office 工单属于同一调用方；
- Stage 存在且尚未绑定给其他 AgentTeams Task；
- Stage 的所有依赖已经完成；
- 同一 Office 工单没有被映射到不同的 AgentTeams Project。

受控 Stage 还会忽略 AgentTeams 请求自带的 `approved_tools`，根据 Office Stage 合同
重新绑定 Skill、Tool 和 Connector，并使用持久化审批记录授权。审批前不会执行模型、
Skill 或 Connector；批准后重发同一 Task 即可继续。详见
[`office-human-approval.md`](office-human-approval.md)。

映射会同时写入 AgentTeams `meta.json`、`result.json` 和 Office 工单：

- `project_id`
- `agentteams_task_id`
- `work_order_id`
- `stage_id`
- `office_task_id`
- AgentTeams trace ID 与 ECS execution trace ID

因此可以从 AgentTeams 协作任务追踪到 Office 阶段、模型执行和后续 Tool/Skill 运行记录。
