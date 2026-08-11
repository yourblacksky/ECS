# Qwen3 1.7B 真实模型回归证据

这组证据由 Ollama `0.31.1`、`qwen3:1.7b`、关闭 reasoning、关闭原生 Tool API 的两个真实
ECS 进程生成。输入均为固定合成数据，不含用户数据或 API Key。

## 结论

- 固定 7 问：**7/7 通过**；每问均保存请求、响应、Verifier 判定与耗时。
- 会议主场景：**通过**；`scope → meeting.fact.extract → meeting.evidence.verify → 人工审批
  → office.action.dispatch → execution.receipt.verify` 完整执行。
- 最终状态：`completed`；生成 2 条 Connector Receipt，Receipt 验证状态 `verified`。
- 审批前耗时 43.37 秒，审批后耗时 30.24 秒；固定 7 问单项耗时见 `summary.json`。

本次参考运行以 `c63ea6b` 为基线，并包含同一工作树内随后写入本提交的修复，所以
`summary.json` 明确记录 `worktree_dirty: true`。重新在干净提交运行时，该字段会变为 false。

## 复现

先安装模型并启动 Ollama：

```bash
ollama pull qwen3:1.7b
ollama serve
```

另开终端运行：

```bash
make eval-qwen17-live
# 或指定证据目录
./scripts/eval-qwen17-live.sh --out /tmp/qwen17-evidence
```

脚本会自行构建 ECS，启动独立 Controller/Worker，执行固定用例，创建会议工单，确认真实
审批门，写入批准决定，再完成派发与 Receipt 校验；脚本只停止自己启动的进程。

## 证据索引

- `reference/summary.json`：机器可读的总结果、逐用例耗时和会议闭环结果；
- `reference/cases.json`：固定 7 问用例；
- `reference/raw/`：原始请求、响应、审批记录和最终 WorkOrder；
- `reference/traces/`：Controller 与 Worker 的原始 Trace；
- `reference/model.json`、`ollama-process.json`：模型元数据和运行快照；
- `reference/SHA256SUMS`：全部证据文件的完整性校验。

这组证据证明的是“小模型参与路由与参数生成，ECS 负责 Manifest grounding、Schema 校验、
Skill 顺序、审批、执行与 Verifier 门禁”。它不把确定性替身结果冒充模型能力。
