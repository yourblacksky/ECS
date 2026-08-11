---
name: make_id
description: Generate a random local identifier with an optional prefix.
---

# Make ID

## When to Use

Use for a random unique identifier or prefixed ID.

## Do Not Use

Do not use when the value must be predictable, repeatable, database-sequential, or a standard UUID.

## Input, Output, and Workflow

Input is optional `prefix`. Invoke `make_id` once. Output is `{"id": string}`.

## Dependencies and Failure

Requires `make_id >=1.0.0`. Report random-source failure without inventing an ID.

## Risk and Compensation

Never treat the result as a password or authentication credential. Generation has no external side effect and needs no rollback.

## Compatibility and Reuse

ECS 0.3+, tool protocol v1, models from 0.5B. Reusable by worker and builder agents.

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `make_id >=1.0.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 1 次，单次超时 `3s`；失败后说明安全随机源不可用。
- 风险等级 `low`，审批策略 `none`；边界：不保证标准UUID格式；不把ID作为密码或认证凭据。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为任务、资源和演示数据生成无中心依赖的标识。
- 标签：`id`、`random`、`local`。

### 版本演进
- Skill `make_id v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
