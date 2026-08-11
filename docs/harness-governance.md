# ECS 精简 Harness 治理

ECS 的编码 Agent Harness 只负责三件事：维护单一规则入口、按改动路径递归提示上下文、在危险
Git 操作前运行确定性预检。它不再运行模型驱动的本地提交审核，也不生成审核 Receipt。

## 分层

| 层 | 位置 | 职责 |
|---|---|---|
| 内容真源 | `AGENTS.md` | 项目约束和按需读取入口；`CLAUDE.md` 只做薄转引 |
| 递归路由 | `scripts/harness/context-routes.json`、`route_tree.py` | 根据目标路径提示需要读取的文档 |
| 共享判定 | `scripts/harness/core.py` | Git 操作预检、生成物保护和明确阻断 |
| Codex 注册 | `.codex/hooks.json`、`.codex/hooks/*.py` | 注册 SessionStart / PreToolUse 并调用共享判定 |
| 工程门禁 | `.githooks/`、GitHub Actions | staged diff 检查、测试、CI 与远端 Review |

`.codex/config.toml` 只设置 8 KiB 项目规则预算。模型、凭证、MCP、审批策略和个人偏好只属于
用户配置。`skills.d/` 是 ECS Runtime Skill，不能暴露成编码 Agent Skill。

## 递归上下文树

`scripts/harness/context-routes.json` 是 version 2 树形 manifest。路径命中沿父链递归，子节点
只能在完整父链内生效；PreToolUse 只注入命中树枝的文档指针，不注入文档正文。环、孤儿、
不可达节点、同级 rank 歧义、不安全路径和预算超限均 fail closed。

启动总量固定为 8 KiB：`AGENTS.md` ≤ 6144 B、SessionStart ≤ 512 B、加总 ≤ 8192 B；单次
递归上下文提示 ≤ 1024 B。详细事实留在 `AGENTS_REFERENCE.md`，命中后才读取。

## 摘要授信

Codex 原生授信只覆盖 hook command。`scripts/render-codex-hooks.py` 因此把两个 adapter、共享
核心、递归解析器和路由 manifest 计算为组合 SHA-256，并写入 hook command：

- 内容未变化：正常执行；
- 内容变化但未重新生成：exit 2；
- 重新生成后 command 摘要变化，成员需要在 `/hooks` 重新授信。

修改 Harness bundle 后运行：

```bash
python3 scripts/render-codex-hooks.py --write
make verify-harness
```

禁止直接编辑 `.codex/hooks.json`。

## Git 与远端边界

安装入口：

```bash
./scripts/install-harness.sh
```

安装器只从已提交 HEAD 复制 `.githooks/pre-commit` 和 `.githooks/pre-push` 到 Git common-dir，
拒绝安装未提交的候选控制文件：

- pre-commit：只运行 `git diff --cached --check`；
- pre-push：要求推送 SHA 等于当前 HEAD、tracked 工作树与 index 干净，再运行 `make test`，总检查时间上限为 60 秒；
- GitHub Actions：执行可复现测试和构建；
- GitHub Review / 分支保护：承担人工或团队代码审查。

本地不调用 LLM Reviewer，不创建 `.git/ecs-harness/reviews`，也不要求 staged-tree 审核凭证。
Harness 仍拒绝 `--no-verify` 和直接修改 `core.hooksPath`，避免静默跳过保留的确定性门禁。
未知 Git helper 和不涉及 Git、Hook 或受保护生成文件的普通嵌套 shell 不再被一概阻断；这些
命令仍受安装后的 Git hooks 和 CI 约束。`git push` 不重复运行 Codex 快速预检，统一交给有
60 秒上限的 pre-push 门禁。

## 已知阳性与多人使用

`scripts/test-harness.py` 同时验证正常路径和确定失败的样本：

- `ecs-harness-block-canary` 返回 exit 2；
- `git commit --no-verify` 被拒绝；
- 直接写生成的 `.codex/hooks.json` 被拒绝；
- 非对象 payload fail closed；
- 长 SessionStart 输出触发 Codex 的预算处理。

`make verify-harness-e2e` 使用真实 Codex CLI 验证 hook 阻断，不进入普通 `make test`。授信状态
属于每位成员本机；Harness 摘要变化时每人需重新确认一次，但提交不再等待本地模型评审。
