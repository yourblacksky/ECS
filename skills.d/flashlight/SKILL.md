---
name: flashlight
description: 打开、关闭或查询当前 Android/iOS ECS 节点的手电筒。
---

# flashlight

控制当前 Android 或 iOS ECS 节点的手电筒。

## 何时使用

用户明确要求打开、关闭或查询当前移动节点手电筒时使用。

## 输入 / 输出

- 输入：`{"action":"on"}`、`{"action":"off"}` 或 `{"action":"status"}`。
- 输出：实际 `state` 和硬件是否 `available`。

`action` 只能从三个枚举值中选择，不能创造 `open`、`close`、`toggle` 等操作。

## 边界与失败

只控制执行该 Skill 的节点，不拍照、不录像、不读取相机画面。设备没有闪光灯、权限被
拒绝或相机被占用时直接返回具体错误，不猜测执行成功。

## 补偿

打开属于可逆设备操作。ECS 或移动宿主停止时自动尝试关闭；也可显式调用
`{"action":"off"}`。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `flashlight >=1.0.0`
- 节点能力：`mobile.flashlight`。

### 失败处理
- 最多尝试 1 次，单次超时 `5s`；失败后返回权限、硬件不可用或相机占用的具体原因。
- 风险等级 `medium`，审批策略 `none`；边界：只控制本节点照明灯；不拍照录像；不读取相机画面。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为移动设备控制、现场照明和远程协助提供统一节点能力。
- 标签：`device`、`mobile`、`flashlight`。

### 版本演进
- Skill `flashlight v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
