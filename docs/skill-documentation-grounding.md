# Skill 文档检索与受控 Grounding

ECS 将 Skill 拆成两层：

- `skill.json` 是运行时合同，决定依赖、工作流、风险、审批、Agent 兼容性和选择条件；
- `SKILL.md` 是可读说明，补充输入解释、工作习惯、边界和失败处理。

运行时加载 `skill.json` 后读取同目录的 `SKILL.md`，校验 YAML frontmatter 的 `name`
与 Manifest 名称一致，并记录内容摘要哈希。文档缺失、frontmatter 非法、名称不一致或
超过大小限制时，整个 Skill 重载失败，注册表继续保留上一次有效状态。

## 工程契约回填

每份 `SKILL.md` 的“工程契约”区块从同目录 `skill.json` 生成，统一覆盖依赖工具与节点能力、
失败/审批/补偿策略、复用价值，以及 Skill、ECS、Tool Protocol 和最低模型版本。Manifest 是
机器可校验的唯一来源，文档区块使用标记包围，不应手工维护：

```bash
make sync-skill-docs    # Manifest 变更后回填全部文档
make verify-skill-docs  # CI 只校验，不修改文件
```

输入输出、依赖、工作流或失败语义变化时，需要同步更新 Manifest，并按语义化版本提升 Skill
版本。`make test` 会阻止文档与 Manifest 不一致的改动进入主分支。

## 权限优先于检索

文档不会单独取得执行资格。进入模型提示词前，Skill 必须依次通过：

1. 当前平台及依赖 Tool 可用；
2. Agent 名称与角色兼容；
3. 节点关系的 `allowed_skills` 授权；
4. 请求中的 `skills` / `restrict_skills` 约束；
5. `never_keywords` 排除；
6. 显式指定，或由 `when.keywords` / `when.intents` 得到正相关分数。

因此，一个未授权或当前不可调用的 Skill，即使磁盘上存在 `SKILL.md`，其内容也不会进入
grounding。文档只能帮助模型正确使用已经获准的能力，不能新增 Tool、跳过人工审批、
改变 Office Stage 合同或覆盖系统安全规则。

## 检索策略

当前实现包含两类轻量词法检索：

- 对已经通过权限与可用性筛选的 Skill，复用 Manifest 的关键词和意图得分，检索同目录
  `SKILL.md`；显式指定的 Skill 优先；
- 对 `skills.knowledge_directory` 中的受管 Markdown 语料，按 frontmatter 的 `keywords`
  和 `intents` 召回正相关片段，并限制单次文档数量和总字符预算。

第二类是面向本地受管语料的最小 RAG：无需向量服务，适合离线边缘节点和小模型，并且每条
引用都带来源和内容哈希。它不把任意用户文件自动加入索引。外部制度、业务材料或用户文档
仍应通过带 ACL、版本、引用和有效期的知识 Connector 接入，再写入对应授权域的语料目录。
知识片段只提供事实依据，不授予 Tool 或 Skill 权限。

知识文档格式：

```markdown
---
name: meeting-action-completeness
title: 会议行动项完整性
summary: 用于核对会议督办事项的最小证据。
keywords: [会议纪要, 责任人, 截止时间]
intents: [会议督办]
---

正文……
```

## 配置

```json
{
  "skills": {
    "directory": "skills.d",
    "knowledge_directory": "skills.d/_knowledge",
    "max_prompt_skills": 5,
    "max_steps": 4,
    "documentation_required": true,
    "grounding_enabled": true,
    "max_document_chars": 12000,
    "max_grounding_chars": 1800,
    "max_knowledge_documents": 3
  }
}
```

- `documentation_required`：要求每个已启用 Skill 都提供合法 `SKILL.md`；
- `grounding_enabled`：允许为已授权候选检索文档；
- `knowledge_directory`：本节点管理员维护的知识 Markdown 目录；
- `max_document_chars`：单份文档的加载上限；
- `max_grounding_chars`：单次请求全部 Skill 与知识片段的总预算；
- `max_knowledge_documents`：单次请求最多注入的知识文档数；设为 `0` 可关闭知识检索。

配置变更需要重启；Skill 文档内容可以通过 `SIGHUP` 或移动端 `Reload` 重载。

## 可观测性与接口

- `GET /v1/skills` 返回已授权 Skill 的文档来源、摘要、内容和内容哈希；关系令牌只能看到
  `allowed_skills` 范围内的数据；
- planner Trace 的 `grounding_references` 以 `kind=skill|knowledge` 区分引用，记录
  Skill 或知识文档名、来源、内容哈希、检索分数，以及 Skill 是否由请求显式指定；
- Trace 只记录引用元数据，不复制完整文档正文，以控制证据体积并避免扩大数据暴露面。
