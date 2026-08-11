# HTTP / MQTT IoT Connector

ECS 的 IoT Connector 面向 Raspberry Pi、ESP32、传感器、执行器和局域网网关。完整 ECS
节点运行在 Linux/macOS/Windows 上；ESP32 等微控制器通过 HTTP 或 MQTT 接入，不需要承载
Go Runtime。

## 安全模型

模型不能传入任意 URL、Broker 或认证信息，只能选择管理员配置的命名 profile：

```text
Agent → Skill → iot_http_request / iot_mqtt
                    ↓
        profile、method/path、topic 白名单
                    ↓
             HTTP 设备 / MQTT Broker
```

两个 Tool 都是高风险能力并要求审批，还会继续受到 Agent 角色白名单、节点关系授权、JSON
Schema、超时、Trace 和输出大小限制。配置了 Manifest 但没有任何可用 profile 时，后台会显示
“已注册”，同时标记为 `connector_unconfigured` 或 `connector_credentials_unavailable`，不会
把不可执行能力交给模型。

## 配置

在节点 `config.json` 中加入：

```json
{
  "iot": {
    "max_response_bytes": 65536,
    "http": {
      "lab-sensor": {
        "base_url": "http://192.168.1.50:8080",
        "allowed_methods": ["GET", "POST"],
        "allowed_path_prefixes": ["/api/device/"],
        "headers_env": {
          "Authorization": "ECS_IOT_LAB_HTTP_AUTH"
        },
        "timeout": "8s"
      }
    },
    "mqtt": {
      "lab": {
        "broker_url": "mqtts://mqtt.example.com:8883",
        "client_id": "ecs-pi-zero",
        "username_env": "ECS_IOT_LAB_MQTT_USER",
        "password_env": "ECS_IOT_LAB_MQTT_PASSWORD",
        "allowed_topic_prefixes": ["ecs/device/"],
        "timeout": "8s"
      }
    }
  }
}
```

认证值只从运行 ECS 的环境变量读取。环境变量不存在时，相关协议只有一个 profile 也不会
被标记为可调用。Broker URL 支持 `mqtt://`、`mqtts://`、`tcp://`、`ssl://`、`ws://`
和 `wss://`；禁止把用户名或密码嵌入 URL。修改 profile 或凭据后应重启 ECS 节点。

## HTTP 调用

读取设备：

```json
{"profile":"lab-sensor","method":"GET","path":"/api/device/status"}
```

控制设备：

```json
{
  "profile":"lab-sensor",
  "method":"POST",
  "path":"/api/device/power",
  "body":{"on":true}
}
```

`path` 必须是相对 profile 主机的绝对路径，不能包含 query、fragment 或 `..`；query 必须通过
独立的字符串 map 提供。Connector 不跟随 HTTP Redirect，防止授权目标把请求转向其他主机。

## MQTT 调用

向 ESP32 发布控制消息：

```json
{
  "profile":"lab",
  "operation":"publish",
  "topic":"ecs/device/light-1/set",
  "payload":{"on":true},
  "qos":1,
  "retain":false
}
```

等待一条状态消息：

```json
{
  "profile":"lab",
  "operation":"subscribe",
  "topic":"ecs/device/light-1/state",
  "wait_ms":5000
}
```

QoS 支持 0 和 1。订阅是最长 30 秒的一次性有界读取，不会在后台保持长期消费者；长期遥测
应由专用采集服务订阅并通过受控 HTTP/知识 Connector 向 ECS 暴露查询能力。

## Raspberry Pi

手动多平台打包 ZIP 的 `builds/linux/armv6/ecs` 可运行于 Pi Zero/Zero W，
`builds/linux/arm64/ecs` 面向 Pi Zero 2 W 等 64 位设备。把对应二进制、`runtime/` 目录和自行
填写的 `config.json` 放到设备上即可；模型建议使用 DeepSeek 等远程 OpenAI-compatible 服务。
