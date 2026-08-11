---
name: iot.http.request
description: 通过预先配置和授权的 HTTP profile 查询或控制 IoT 设备。
---

# iot.http.request

只向管理员在 `iot.http` 中配置的设备发送请求。模型提供 profile、HTTP 方法、相对路径和
可选 JSON body；目标主机、路径范围和认证 Header 都由运行配置决定。

## 输入

`{"profile":"lab-sensor","method":"GET","path":"/api/status"}`。

写操作示例：

`{"profile":"lab-light","method":"POST","path":"/api/power","body":{"on":true}}`。

## 边界

不得猜测 profile、路径或请求内容，不接受完整 URL，不会把环境变量中的认证信息返回给
模型。所有调用都按高风险工具走 ECS 审批和授权；失败后不改用 shell 或其他网络工具绕过。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `iot_http_request >=1.0.0`
- 节点能力：`iot.http.request`。

### 失败处理
- 最多尝试 1 次，单次超时 `35s`；失败后返回 profile、方法、路径白名单、凭据或设备响应错误，不改用任意网络请求。
- 风险等级 `high`，审批策略 `required`；边界：只能选择管理员配置的 profile；只能使用允许的方法和路径前缀；凭据不进入模型上下文。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为局域网设备的 HTTP 状态与控制接口提供统一受控出口。
- 标签：`iot`、`http`、`edge`。

### 版本演进
- Skill `iot.http.request v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
