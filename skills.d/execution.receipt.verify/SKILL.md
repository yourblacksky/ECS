---
name: execution.receipt.verify
description: 确定性校验办公动作产生的结构化执行回执。
---

# Execution Receipt Verify

## 适用场景

连接器完成外部动作并返回结构化回执后使用。没有回执时不得把任务标记为已闭环。

## 工作流

调用 `execution_receipt_verify`，检查动作、目标、执行时间、结果、外部引用和幂等键，并确认
回执与当前工单、Stage、批准策略版本相互绑定。

## 边界

- 只认可结构化 Connector 回执，不认可模型自述。
- 无效或不匹配的回执必须触发失败合同；需要补偿时，补偿动作本身也必须产生回执。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `execution_receipt_verify >=1.0.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 0 次，单次超时 `5s`；失败后列出无效或缺字段的回执。
- 风险等级 `low`，审批策略 `none`；边界：只校验结构化回执；不把模型文本当作执行成功证明。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 外部动作结果的确定性闭环校验。
- 标签：`receipt`、`verify`、`audit`。

### 版本演进
- Skill `execution.receipt.verify v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
