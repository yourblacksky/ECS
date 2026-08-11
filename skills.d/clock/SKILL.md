---
name: clock
description: Read the current ECS node time and timezone.
---

# Clock

## When to Use

Use for the current node date, time, or timezone.

## Do Not Use

Do not use for another location, historical/future dates, time conversion, or changing the system clock.

## Input, Output, and Workflow

Input is `{}`. Invoke `clock` once. Output contains RFC3339 `time` and `timezone`.

## Dependencies and Failure

Requires `clock >=1.0.0`. Do not retry invalid output; report that node time is unavailable.

## Risk and Compensation

Read-only and local. It does not modify the clock and needs no rollback.

## Compatibility and Reuse

ECS 0.3+, tool protocol v1, models from 0.5B. Reusable by worker and observer agents.

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `clock >=1.0.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 1 次，单次超时 `3s`；失败后说明节点时间不可用。
- 风险等级 `low`，审批策略 `none`；边界：只读取节点时钟；不修改系统时间。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 提供统一的节点时间来源。
- 标签：`time`、`local`、`read-only`。

### 版本演进
- Skill `clock v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
