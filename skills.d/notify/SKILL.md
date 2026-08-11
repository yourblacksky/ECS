---
name: notify
description: 在当前 Android/iOS ECS 节点投递一条本地系统通知。
---

# notify

在执行该 Skill 的 Android 或 iOS 节点投递本地系统通知。

## 输入

`{"title":"任务完成","body":"本地处理已经结束"}`。

标题最多 80 字符，正文最多 240 字符。首次使用由系统询问通知权限。

## 边界

它不是短信、邮件或即时通讯工具，不会联系任何外部人员。系统返回投递成功后才可回答
“通知已发送”；通知一旦投递不提供撤回。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `notify >=1.0.0`
- 节点能力：`mobile.notification.local`。

### 失败处理
- 最多尝试 1 次，单次超时 `8s`；失败后返回通知权限被拒绝或系统投递失败的具体原因。
- 风险等级 `medium`，审批策略 `none`；边界：只投递本节点本地通知；不向联系人或外部平台发送消息。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为任务完成、设备状态和现场提醒提供统一通知出口。
- 标签：`device`、`mobile`、`notification`。

### 版本演进
- Skill `notify v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
