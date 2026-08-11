---
name: text_stats
description: Deterministically count characters, words, and lines in supplied text.
---

# Text Stats

## When to Use

Use when text is supplied and exact character, word, or line counts are requested.

## Do Not Use

Do not use for summaries, semantic analysis, sentiment, or missing text.

## Input, Output, and Workflow

Input is `{"text": string}`. Invoke `text_stats` once. Return numeric `characters`, `words`, and `lines`.

## Dependencies and Failure

Requires `text_stats >=1.0.0`. On invalid input or output, return the single validation failure without guessing.

## Risk and Compensation

Only supplied text is read. No files are accessed, no text is modified, and no rollback is needed.

## Compatibility and Reuse

ECS 0.3+, tool protocol v1, models from 0.5B. Reusable by worker and analyst agents.

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `text_stats >=1.0.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 1 次，单次超时 `3s`；失败后要求提供可读取的文本。
- 风险等级 `low`，审批策略 `none`；边界：只统计传入文本；不读取文件；不修改内容。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 提供与模型无关的可复现文本计数。
- 标签：`text`、`count`、`deterministic`。

### 版本演进
- Skill `text_stats v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
