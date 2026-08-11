---
name: meeting.evidence.verify
description: 核验会议行动项的责任人、期限和原文依据是否完整。
---

# Meeting Evidence Verify

## 适用场景

在任何外部派发动作之前，对结构化行动项做确定性完整性检查。

## 工作流

调用 `meeting_evidence_verify`，逐项检查责任人、期限和原文依据。任一必需字段缺失时必须
返回阻断结果和待补字段，不能把模型推断当作会议证据。

## 边界

- 只判断结构完整性，不裁决有争议的会议事实。
- 验证通过不等于人工批准；外部动作仍必须经过 Office 审批合同。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `meeting_evidence_verify >=1.0.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 0 次，单次超时 `5s`；失败后列出缺失的责任人、期限或原文依据。
- 风险等级 `low`，审批策略 `none`；边界：只做确定性完整性检查；不代替人工确认争议事实。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 行动项证据完整性的确定性阻断器。
- 标签：`meeting`、`verify`、`governance`。

### 版本演进
- Skill `meeting.evidence.verify v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
