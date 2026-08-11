# 可信会议督办双路径演示

路演可直接播放[58 秒 Demo 视频](demo-video/ecs-trusted-meeting-demo.mp4)，画面覆盖多端宿主、
Office 控制台、人工审批门、证据时间线、跨节点 Trace 与真实 Qwen3 1.7B 回归结果。

这条固定演示同时展示 AgentTeams 协作层与 ECS 可信执行层：

```text
会议输入
→ meeting_planner 定义范围
→ minutes_clerk / meeting.fact.extract / meeting_fact_extract
→ decision_reviewer / meeting.evidence.verify / meeting_evidence_verify
→ 人工审批
→ followup_coordinator / office.action.dispatch / office_action_dispatch
→ execution.receipt.verify / execution_receipt_verify
→ 证据时间线
```

运行一条命令：

```bash
make demo-meeting
```

命令会把成功与失败路径各连续执行 5 次：

- 成功：行动项完整，真实进入持久化审批，批准后执行本地可替换 Connector，验证 Receipt，
  最终完成；
- 失败：行动项缺少责任人和截止时间，reviewer 确定性阻断，dispatch 保持未执行，不创建
  审批和 Receipt。

回归命令固定输入并直接驱动实际 Office Registry、AgentTeams Stage 映射、SkillRunner 和
builtin Connector，从而排除模型采样造成的不稳定；它验证的是生产执行合同而不是伪造的
输出。交互路演仍可通过管理后台和真实模型发起同一个 `meeting_followup` Component。

每次运行的完整 JSON 写入 `data/demo/meeting-followup/`。其中包括原文摘要哈希、四个 Agent
及 Stage、按序排列的 Stage → Skill → Tool → Connector 执行链、审批记录、Receipt、
校验结果、失败原因和 Office 审计时间线。
`stable_signature` 排除随机 ID 与时间戳，用来证明五次运行的业务状态、参与角色和执行链
保持一致。输出目录同时包含使用相对文件名的 `summary.json` 和 `SHA256SUMS`。

仓库中的[参考运行证据](../evidence/meeting-followup/README.md)来自固定合成输入，不包含真实
用户数据。CI 会执行同一条回归，并把完整输出上传为保留 30 天的
`meeting-followup-evidence-<commit>` Artifact；手动多平台交付包也会从打包提交重新生成同样的
证据目录。

另有一组[Qwen3 1.7B 真实模型双节点证据](../evidence/qwen3-1.7b-live/README.md)，覆盖相同
会议主场景、人工审批门、派发与 Receipt 校验，并保留固定用例、原始模型响应和跨节点 Trace。

[Qwen3 0.6B 真实模型双节点证据](../evidence/qwen3-0.6b-live/README.md)进一步证明：将 Office
Stage 收敛为显式 Skill，并由运行时在 JSON Schema 匹配后完成可信类型交接，可以让 596M
参数模型在不承担自由规划和复杂证据转写的前提下完成同一闭环。参考运行固定 7 问 7/7，
四个 Stage 全部完成，人工审批门实际触发，2 条 Receipt 验证通过。

失败路径在外部动作前阻断，因此没有补偿动作；这不是遗漏，而是“无副作用则无需补偿”。
外部动作已经发生后再失败的补偿路径由 `office_action_compensate` 和
`TestOfficeAndAgentTeamsUseSameReceiptContract` 回归覆盖。
