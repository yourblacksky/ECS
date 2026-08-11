---
name: location
description: 读取当前 Android/iOS ECS 节点的一次性地理位置。
---

# location

读取执行该 Skill 的移动 ECS 节点的一次性位置。

## 输入

- `{"accuracy":"coarse"}`：普通位置需求，优先低精度。
- `{"accuracy":"precise"}`：用户明确需要精确经纬度时使用。

只能选择两个枚举。首次执行会由系统询问定位权限。

## 隐私与失败

只读取一次，不后台跟踪，也不定位其他人。坐标会进入 ECS Tool Run，并可能作为上下文发送
给当前配置的 LLM。权限被拒、定位关闭或超时必须原样报错，绝不猜测位置。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `location >=1.0.0`
- 节点能力：`mobile.location.once`。

### 失败处理
- 最多尝试 1 次，单次超时 `15s`；失败后返回权限、定位服务关闭或超时的具体原因，不猜测位置。
- 风险等级 `medium`，审批策略 `none`；边界：只读取当前执行节点一次；不后台跟踪；结果可能发送给已配置的 LLM。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为现场服务、设备自报位置和边缘任务提供一次性坐标。
- 标签：`device`、`mobile`、`location`。

### 版本演进
- Skill `location v1.0.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
