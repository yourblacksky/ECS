# ECS 评审演示入口

建议按以下顺序查看交付包：

1. `ECS-Agent-Infra-初赛方案PPT-套模板版.pdf`：官方十九页结构的定版方案，优先阅读；
2. `ECS-Agent-Infra-初赛方案PPT-套模板版.pptx`：同版可编辑源文件；
3. `ecs-trusted-meeting-demo.mp4`：58 秒、1080p 的可信会议督办演示；
4. `images/`：多端宿主、Office 控制台、审批门、证据时间线和跨节点 Trace 的独立图片；
5. `GOAI-作品简介.md`：500 字内报名简介；
6. `reports/`：小模型能力边界与会议闭环说明。

可复现原始证据位于交付包顶层 `evidence/`：

- `meeting-followup/`：成功、失败路径各 5 次的本次构建证据；
- `agentteams-office/`：同一提交现场生成的 AgentTeams 四阶段 Office E2E 证据；
- `qwen3-1.7b-live/`：Qwen3 1.7B 双节点完整会议闭环；
- `qwen3-0.6b-live/`：Qwen3 0.6B 双节点完整会议闭环与小模型边界。

`ECS-source/` 是同一提交的评审源码快照。所有内容受其中的 `LICENSE` 约束，不是公开发布。
