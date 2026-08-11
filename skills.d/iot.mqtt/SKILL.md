---
name: iot.mqtt
description: 通过预先配置和授权的 MQTT profile 发布或读取 IoT 消息。
---

# iot.mqtt

连接管理员在 `iot.mqtt` 中配置的 Broker。模型只能选择 profile、操作和允许前缀下的 topic，
不能提供 Broker 地址或凭据。

## 输入

发布：`{"profile":"lab","operation":"publish","topic":"ecs/device/light/set","payload":{"on":true},"qos":1}`。

等待一条消息：`{"profile":"lab","operation":"subscribe","topic":"ecs/device/sensor/state","wait_ms":5000}`。

## 边界

`publish` 必须提供 payload；QoS 只支持 0 或 1。`subscribe` 是有界的一次性读取，不启动
后台常驻订阅。所有调用都按高风险工具走 ECS 审批和授权，失败后不得改连其他 Broker。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `iot_mqtt >=1.0.0`
- 节点能力：`iot.mqtt.publish`、`iot.mqtt.subscribe`。

### 失败处理
- 最多尝试 1 次，单次超时 `35s`；失败后返回 profile、topic 白名单、凭据、连接或等待超时错误，不改连其他 Broker。
- 风险等级 `high`，审批策略 `required`；边界：只能选择管理员配置的 profile；topic 必须匹配允许前缀；凭据不进入模型上下文。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为 ESP32、传感器和执行器提供统一 MQTT 消息出口。
- 标签：`iot`、`mqtt`、`esp32`、`edge`。

### 版本演进
- Skill `iot.mqtt v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
