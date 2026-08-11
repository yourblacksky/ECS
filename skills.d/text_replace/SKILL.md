---
name: text_replace
description: Replace every exact occurrence of one string in supplied text.
---

# Text Replace

## When to Use

Use only when source text, old value, and new value are explicit.

## Do Not Use

Do not use for regex, fuzzy matching, empty old values, or direct file editing.

## Input, Output, and Workflow

Input is `{text, old, new}`. Invoke `text_replace` once. Output is `{"text": string}`.

## Dependencies and Failure

Requires `text_replace >=1.0.0`. On execution failure, return the original input as a degraded result.

## Risk and Compensation

Only the supplied string is modified in memory. No files are written. No external rollback is required.

## Compatibility and Reuse

ECS 0.3+, tool protocol v1, models from 0.5B. Reusable by worker and editor agents.

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `text_replace >=1.0.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 1 次，单次超时 `3s`；失败后返回原始输入并说明替换失败。
- 风险等级 `low`，审批策略 `none`；边界：只处理传入字符串；不使用正则；不读写文件。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为编辑类 Agent 提供安全的精确替换。
- 标签：`text`、`replace`、`local`。

### 版本演进
- Skill `text_replace v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
