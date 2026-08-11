# AGENTS.md — ECS

这是编码 Agent 的 **≤8 KiB 启动入口**。项目哲学与用法见 [README](README.md)；容易静默失效的
实现事实和历史陷阱下沉在 [AGENTS_REFERENCE.md](AGENTS_REFERENCE.md)，命中后按章节读取。

ECS 的核心原则：**路由、Schema、权限、状态、重试、Receipt 和验证留在确定性的 Go 内核；
小模型只理解意图、选择能力、补参数和组织答案。** 把复杂度推回模型前必须说明理由。

## 不可违反

- 仓库允许按根目录 `LICENSE` 公开发布；发布前必须完成密钥、个人信息、内部材料和第三方许可审查。
- 密钥、token、签名材料、个人路径和构建凭证不得提交。项目 `.codex/config.toml` 只允许文档
  预算；模型、MCP、凭证、审批策略和个人偏好留在用户配置。
- `skills.d/` 是 **ECS Runtime Skill**，不是编码 Agent Skill；不得移动或链接为
  `.agents/skills/`。Tool、Skill、Office Component 也不能混称。
- 不手改 `.codex/hooks.json`；修改 Harness bundle 后运行
  `python3 scripts/render-codex-hooks.py --write`，并在 Codex `/hooks` 重新授信。
- 不用 `--no-verify` 绕过门禁，不削弱审批、授权、Receipt、Trace、Session owner 或 durable
  execution 边界。发现门禁自身异常时应停下修门禁，不能把异常当放行。
- 保留用户已有改动；禁止未经明确授权的破坏性 Git/文件操作。编辑文件使用 `apply_patch`，
  搜索优先 `rg` / `rg --files`。

## 开始与交付

1. 先看 `git status --short --branch`，只处理任务范围。
2. 改动前从下表读取对应参考；不要靠 README 或旧评审猜当前代码。
3. 实现应覆盖 Handler、Manifest、Schema、注册、授权、验证和测试中的相关层，避免只补文案。
4. 先跑定向测试，再跑本页要求的门禁；失败要定位，不能静默归为“既有问题”。
5. 提交前明确 stage 范围并运行本页要求的测试；未经用户要求不 commit、push、开关
   Issue/PR、发布或部署。

## 改动路由

| 改动面 | 先读 / 检查 |
|---|---|
| Tool、Skill、Manifest、Grounding | [能力新增清单](AGENTS_REFERENCE.md#新增一个能力完整清单)、[Skill 注册表](AGENTS_REFERENCE.md#skill-可见性与注册表)、[Skill Grounding](docs/skill-documentation-grounding.md) |
| JSON Schema、小模型工具调用 | [Schema 陷阱](AGENTS_REFERENCE.md#schema-校验go-侧是完整的-draft-2020-12浏览器叶子仍是手写子集)、[Schema 契约](docs/json-schema-contracts.md)、[小模型调优](docs/small-model-tuning.md) |
| Agent、Prompt、Verifier、Executor | `internal/agent/`、`internal/roles/`、`agents.d/`、[内核兜底](AGENTS_REFERENCE.md#内核侧的兜底防线改动时别削弱) |
| Office、审批、补偿、durable attempt | [Stage 链](docs/office-stage-skill-connector.md)、[人工审批](docs/office-human-approval.md)、[Receipt/补偿](docs/office-receipt-compensation.md)、[durable execution](docs/office-durable-execution.md) |
| 网络发现、节点授权、跨节点调用 | `internal/discovery/`、`internal/networkauth/`、`internal/server/`、[其他硬约束](AGENTS_REFERENCE.md#其他硬约束) |
| Session、Trace、审计与恢复 | `internal/session/`、`internal/trace/`、`internal/observability/`、[当前未决](AGENTS_REFERENCE.md#当前未决) |
| 热重载与配置 | [热重载语义](AGENTS_REFERENCE.md#热重载语义)、[配置参考](docs/configuration-reference.md) |
| Android / iOS / HarmonyOS / 浏览器 | 对应平台目录、`mobile/`、相关生成/打包脚本；运行时能力不能仅凭 UI 文案判断 |
| CI、Docker、交付包、版本 | [提交门禁](AGENTS_REFERENCE.md#提交门禁)、`.github/workflows/`、`Dockerfile`、`scripts/build-multiplatform-package.sh` |
| Harness 与跨 Agent 治理 | [Harness 递归树](docs/harness-governance.md)、`scripts/harness/`、`.codex/`、`.githooks/` |

## 必跑验证

普通代码或配置改动：

```bash
make test
python3 scripts/verify-agents-md.py
git diff --check
```

按改动追加：

- Tool/Skill 文档：`make verify-skill-docs`
- Harness：`make verify-harness`，并用真实阳性样本确认 exit 2
- AgentTeams：`./scripts/verify-agentteams-integration.sh`
- 三节点网络：`make demo-network`，结束后 `make demo-network-down`
- Android UI：`make android-ui-test`
- 依赖安全：`make vulncheck`
- 多平台交付：`./scripts/build-multiplatform-package.sh --check`

`.githooks/pre-commit` 做快速一致性检查；`.githooks/pre-push` 追加 Go 测试；GitHub Actions 是
最终兜底。项目不运行模型驱动的本地提交审核；代码审查由 GitHub Review 承担。测试门禁本身
有改动时，必须先用真仓已知阳性证明它会报红，再修复并归零。

## 当前安全边界

- Admin 鉴权已 fail closed，但普通调用路径的 principal/owner 粒度、可信输入和跨节点传播仍需
  以代码与 Issue 为准；不要把 bearer token、调用方 ID 或模型输出当独立信任根。
- 调用方提供的 ApprovedTools、ApprovalRecord、SessionID、TraceID 都必须经过接收侧收窄、
  绑定或服务端签发；新增入口要复用相同边界。
- Verifier 的格式通过不等于事实正确；新增能力必须明确验证语义，外发或高风险 Tool 必须审批。
- Outbox、attempt、reaper、补偿和恢复已有实现，不能用内存状态或“模型会重试”替代。

更精确的残留风险与符号名见 [当前未决](AGENTS_REFERENCE.md#当前未决)。

实现与文档冲突时，以当前代码和可复现实验为证据；修正入口或参考文档，避免留下静默漂移。
