---
name: text_extract
description: 从文本中确定性提取邮箱、手机号、链接或日期。
---

# text_extract

从一段文本中提取邮箱、手机号、链接或日期，一次调用可提取一种或多种类型。

## 何时使用

用户要求"找出 / 提取 / 抓取"文本中的邮箱、电话、链接或日期时使用。

## 输入 / 输出

- 单类型输入：`{"text": "原文", "pattern": "email"}`
- 多类型输入：`{"text": "原文", "pattern": ["email", "date"]}`
- 输出：`{"matches": [...], "count": n, "by_pattern": {...}, "summary": "..."}`

## 风险

低。只读输入文本，不访问网络或文件。

## 失败降级

说明无法提取的具体原因；pattern 中的值只能逐字选
`email` / `phone` / `url` / `date`。

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `text_extract >=1.1.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 1 次，单次超时 `5s`；失败后说明无法提取的具体原因。
- 风险等级 `low`，审批策略 `none`；边界：只读输入文本；不访问网络或文件。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 从任意文本中一次确定性抓取一种或多种联系方式、链接或日期。
- 标签：`text`、`extract`、`office`。

### 版本演进
- Skill `text_extract v1.1.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
