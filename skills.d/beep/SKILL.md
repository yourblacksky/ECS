---
name: beep
description: 让当前 Android/iOS ECS 节点发出一到三声短蜂鸣。
---

# beep

让执行该 Skill 的移动 ECS 节点发出短蜂鸣。

## 输入

`{"count":1}`、`{"count":2}` 或 `{"count":3}`。

`count` 只能是 1 到 3 的整数，不支持音频文件、语音、音量或无限循环。

## 边界

iOS 会遵守设备的静音设置，Android 使用系统提醒音频通道。只有 Tool Run 成功才能回答
“已蜂鸣”。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `beep >=1.0.0`
- 节点能力：`mobile.audio.beep`。

### 失败处理
- 最多尝试 1 次，单次超时 `5s`；失败后返回静音模式、音频设备或系统执行失败原因。
- 风险等级 `medium`，审批策略 `none`；边界：每次最多三声；不播放任意音频文件或语音。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为查找设备、计时结束和现场反馈提供简单声音能力。
- 标签：`device`、`mobile`、`audio`。

### 版本演进
- Skill `beep v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
