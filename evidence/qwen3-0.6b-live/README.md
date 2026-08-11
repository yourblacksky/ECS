# Qwen3 0.6B 真实模型回归证据

这组证据使用 Ollama `0.31.1`、`qwen3:0.6b`（596.05M、Q8_0）、关闭 reasoning、关闭
原生 Tool API，并启动真实 ECS Controller 与 Worker。输入均为固定合成数据。

## 结论

- 固定 7 问：**7/7 通过**，总耗时 20.96 秒；
- 会议主场景：**通过**，四个 Stage 全部完成；
- 人工审批门：实际触发并在授权后继续；
- 派发与 Receipt：创建 2 条幂等回执，验证结果为 `verified`；
- 最终状态：`completed`。

详细判断和与 1.7B 的对比见
[Qwen3 0.6B 能力边界报告](../../docs/qwen3-0.6b-evaluation-report.md)。

## 复现

```bash
ollama pull qwen3:0.6b
ollama serve
make eval-qwen06-live
```

脚本无论成功或安全失败都会落盘 `summary.json`，便于区分模型失败和运行时故障。

## 证据索引

- `reference/summary.json`：固定用例和会议流程的机器可读结论；
- `reference/raw/replies/`：七个用例的原始请求与响应；
- `reference/raw/meeting-before-approval.json`：审批门前的完整 WorkOrder；
- `reference/raw/meeting-completed.json`：授权并完成后的完整 WorkOrder；
- `reference/traces/`：Controller 与 Worker Trace；
- `reference/model.json`：模型参数与量化信息；
- `reference/SHA256SUMS`：证据完整性校验。
