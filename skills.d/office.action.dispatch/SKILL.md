---
name: office.action.dispatch
description: 通过办公连接器创建会后行动并生成结构化执行回执。
---

# Office Action Dispatch

## 前置条件

行动项必须已经通过证据核验，并具有仍有效的人工批准。调用方必须提供稳定幂等键。

## 工作流

调用 `office_action_dispatch` 创建办公待办或等价外部动作。成功结果必须包含动作、目标、
执行时间、结果、外部引用和幂等键；模型文本不能代替连接器回执。

## 边界

- 缺少责任人、期限、批准或幂等键时不得调用。
- 只执行当前 Office Stage 授权的 Connector。
- 失败时按 Stage 合同进入补偿或人工接管，不重复制造外部动作。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `office_action_dispatch >=1.0.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 1 次，单次超时 `8s`；失败后返回未派发的行动项和失败原因。
- 风险等级 `medium`，审批策略 `required`；边界：必须使用稳定幂等键；必须保留连接器回执。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 把已批准行动项转换为带回执的办公动作。
- 标签：`office`、`dispatch`、`receipt`。

### 版本演进
- Skill `office.action.dispatch v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
