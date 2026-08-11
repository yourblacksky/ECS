---
name: screen_brightness
description: 读取或设置当前 Android/iOS ECS 节点的屏幕亮度。
---

# screen_brightness

读取或设置执行节点的屏幕亮度。

## 输入

- 查询：`{"action":"get"}`。
- 设置一半亮度：`{"action":"set","value":0.5}`。

`value` 是 0 到 1 的数字，不是百分号字符串。Android 修改系统亮度前需要用户在节点配置
中授予“修改系统设置”权限；iOS 仅在应用前台运行时可靠。

## 输出与边界

返回实际 `value`、修改前的 `previous_value` 和 `writable`。只控制当前执行节点。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `screen_brightness >=1.0.0`
- 节点能力：`mobile.screen.brightness`。

### 失败处理
- 最多尝试 1 次，单次超时 `5s`；失败后返回系统设置权限或设备状态导致的具体错误。
- 风险等级 `medium`，审批策略 `none`；边界：value 限制为 0 到 1；只修改执行节点屏幕亮度。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为展示终端、夜间场景和远程协助提供亮度控制。
- 标签：`device`、`mobile`、`display`。

### 版本演进
- Skill `screen_brightness v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
