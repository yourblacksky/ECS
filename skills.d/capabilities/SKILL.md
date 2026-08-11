---
name: capabilities
description: 准确回答本 ECS 节点有哪些 Tool 与 Skill 可用。
---

# capabilities

读取本 ECS 节点的 `/v1/capabilities`，分别返回当前可用的全部 Tool 与 Skill。

## 何时使用

用户问"你有什么工具 / Skill / 能力 / 你能做什么"等元问题时必须使用。

## 输入 / 输出

- 输入：无参数 `{}`
- 输出：`tool_count`、`skill_count`、`tools`、`skills` 和 `summary`。

## 风险

低。只读操作，不修改节点配置。

## 失败降级

无法连接本节点 API 时，说明失败原因，不要编造 Tool 或 Skill 列表。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `capabilities >=1.1.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 1 次，单次超时 `8s`；失败后说明无法读取节点能力目录的原因。
- 风险等级 `low`，审批策略 `none`；边界：只读本节点能力目录；不修改任何配置。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为任意 Agent 提供准确且分组的 Tool/Skill 节点能力自述。
- 标签：`meta`、`local`、`readonly`。

### 版本演进
- Skill `capabilities v1.1.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
