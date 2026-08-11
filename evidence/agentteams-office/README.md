# AgentTeams → ECS → Office 参考证据

`reference/` 是一次完整端到端运行的脱敏输出，输入是固定合成任务，不包含用户或生产数据。
它证明固定上游 AgentTeams Controller 代码生成的 ECS Runtime 配置被真实 ECS Worker 消费，
并依次完成 ready/heartbeat、Matrix Assignment、AgentTeams Task、Office Stage、Trace 和
Matrix Completion 回传。

机器验收入口是 [`reference/summary.json`](reference/summary.json)，原始证据和摘要由
[`reference/SHA256SUMS`](reference/SHA256SUMS) 关联。重新生成：

```bash
make demo-agentteams-office
```

详细边界见 [`docs/agentteams-office-e2e.md`](../../docs/agentteams-office-e2e.md)。Matrix、LLM、
Controller lifecycle HTTP 与对象存储在回归中使用确定性替身；Controller Runtime 投影、
ECS Worker、Task Store、Office Registry 和 Trace 使用真实实现。
