# Office Stage → Skill → Connector 执行合同

Office Stage 使用 `skills` 声明完成当前阶段必须执行的业务能力，使用 `connectors`
声明这些能力需要触达的外部系统能力。

```json
{
  "id": "dispatch",
  "skills": ["office.action.dispatch", "execution.receipt.verify"],
  "connectors": ["office.task.write"]
}
```

链路中的职责如下：

```text
Office Stage
  → Skill：业务步骤、输入输出合同、失败策略
    → Tool：确定性执行单元
      → Connector capability：可替换的业务系统能力
```

## 加载时校验

Office Component 加载时会拒绝以下配置：

- Team 引用了未安装或非 LLM execution mode 的 Agent principal；
- Stage Skill 或其 Tool 依赖超出对应 Agent manifest 的 allowlist；
- Stage 引用了不存在或当前不可用的 Skill；
- Stage 中重复或非法的 Skill 名称；
- Skill 的 Tool 依赖不存在；
- Stage 声明了 Connector，但所有 Skill 依赖都没有 Tool 提供该 capability。

因此 Connector 不能再仅作为说明性元数据存在。

## 节点选择与执行

调度 Office 工单时，控制节点只选择同时满足以下条件的节点：

- 提供工单要求的全部 Connector capability；
- 提供工单全部 Stage 要求的 Skill；
- Skill 依赖的 Tool 可以在该节点执行。

执行请求同时携带 `skills`、`tools` 和 `connectors`，并设置
`restrict_skills=true`、`restrict_tools=true`。模型只能看到当前 Stage 声明的 Skill；
Skill Runner 再调用其固定 Tool 工作流。

执行成功还必须满足：

- 每个 Stage Skill 都产生状态为 `success` 的 `SkillRun`；
- 每个 Stage Connector 都由某个成功 SkillRun 内的 ToolRun 实际覆盖；
- SkillRun 和 ToolRun 均进入 ECS trace。

只返回模型文本、跳过 Skill，或者声明 Connector 但没有实际 ToolRun，都会令 Stage 失败。

## 会议闭环的当前合同

`meeting_followup` 当前使用：

| Stage | Skill | Connector Tool / capability |
|---|---|---|
| `extract` | `meeting.fact.extract` | `meeting_fact_extract` / `office.document.read` |
| `verify` | `meeting.evidence.verify` | `meeting_evidence_verify` / 纯确定性校验 |
| `dispatch` | `office.action.dispatch` | `office_action_dispatch` / `office.task.write` |
| `dispatch` | `execution.receipt.verify` | `execution_receipt_verify` / 纯确定性校验 |

需要执行回执的 Stage 还会由控制节点把 Receipt 绑定到工单、Task、幂等键、Connector 与
审批策略版本；失败时按 Stage 合同调用补偿 Connector。详见
[`office-receipt-compensation.md`](office-receipt-compensation.md)。

`office_action_dispatch` 当前是可运行的本地演示 Connector，返回带幂等键和
`local-task://` 引用的结构化回执。接入真实任务、日历或消息系统时，可以替换对应 Tool
实现并保留 capability 与 Skill 合同；当前实现不应描述为已经写入第三方业务系统。
