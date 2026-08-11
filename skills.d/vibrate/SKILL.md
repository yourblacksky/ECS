---
name: vibrate
description: 让当前 Android/iOS ECS 节点按简单模式震动。
---

# vibrate

让执行该 Skill 的 Android 或 iOS ECS 节点震动。

## 输入

- `{"pattern":"short"}`：一次短震动。
- `{"pattern":"long"}`：较长提醒。
- `{"pattern":"double"}`：两次短震动。

只能选择这三个枚举，不生成时长、循环次数或其他字段。

## 边界

只在用户明确要求物理震动提醒时使用。所有模式都会自行结束；设备不支持震动时返回错误，
不能把口头回答当作已经执行。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `vibrate >=1.0.0`
- 节点能力：`mobile.vibration`。

### 失败处理
- 最多尝试 1 次，单次超时 `5s`；失败后返回设备不支持震动或系统拒绝执行的具体原因。
- 风险等级 `medium`，审批策略 `none`；边界：最多执行一个有限模式；不支持循环或后台持续震动。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为提醒、查找设备和无声反馈提供统一能力。
- 标签：`device`、`mobile`、`vibration`。

### 版本演进
- Skill `vibrate v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
