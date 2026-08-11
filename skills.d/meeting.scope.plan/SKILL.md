---
name: meeting.scope.plan
description: 把会议标题和参与人收敛成确定的会议类型与后续交付范围。
---

# Meeting Scope Plan

## 适用场景

在会议工单开始时使用。输入只包含当前会议标题和参与人，输出固定的会议类型与后续交付范围。

## 工作流

调用 `meeting_scope_plan` 一次。不得把 Office Component 名称当成 Tool，也不得提前执行事实
提取、审批或派发。

## 边界

- 只定义当前 Stage 的范围。
- 不推测会议正文中没有的参与人。
- 不产生任何外部副作用。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `meeting_scope_plan >=1.0.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 1 次，单次超时 `5s`；失败后返回缺失的会议标题或参与人。
- 风险等级 `low`，审批策略 `none`；边界：只规划当前会议的交付类型；不执行后续行动。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 小模型友好的会议范围确定性入口。
- 标签：`meeting`、`scope`、`planning`。

### 版本演进
- Skill `meeting.scope.plan v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
