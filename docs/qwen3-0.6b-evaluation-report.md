# Qwen3 0.6B 在 ECS 中的能力边界报告

## 摘要

2026-08-06，在 Apple M4、Ollama 0.31.1 上，对 `qwen3:0.6b`（596.05M 参数、Q8_0）进行
真实双节点 ECS 回归。模型关闭 reasoning 与原生 Tool API，沿用 ECS 的 Manifest grounding、
紧凑参数判定、JSON Schema、SkillRunner 和 Verifier。

结论是：**在显式 Stage → Skill 契约和 Schema 类型交接的约束下，0.6B 已能完成真实会议
督办闭环。** 固定 7 问全部通过；会议流程依次完成范围定义、事实提取、核验、人工审批、
行动派发与 Receipt 验证。这里证明的是 ECS Harness 能把小模型限制在合适的判断边界内，
并不表示 0.6B 已具备无约束的自由 Agent 规划能力。

## 测试结果

| 项目 | 结果 | 耗时 |
|---|---:|---:|
| 问候 | 通过 | 2.23 秒 |
| 当前时间 | 通过，调用 `clock` | 3.47 秒 |
| 当前日期 | 通过，调用 `clock` | 3.92 秒 |
| 本节点能力列表 | 通过，Manifest 确定性回答 | 0.01 秒 |
| 指定节点能力列表 | 通过，Manifest 确定性回答 | 0.01 秒 |
| 12 × 3 | 通过，调用 `calculate` | 4.13 秒 |
| 中英文字符统计 | 通过，调用 `text_count_cn` | 7.20 秒 |
| 固定集合计 | **7/7** | **20.96 秒** |
| 会议督办闭环 | **通过** | 审批前 17.12 秒；审批后 6.09 秒 |

能力列表两项由 ECS 直接查询 Manifest，不消耗模型推理；其余用例保留真实模型判定与
Skill/Tool 执行证据。7/7 表示当前 ECS Harness 下的端到端成功率，不代表模型能自由生成
任意复杂 Tool 协议。

## 修复与闭环结果

首次测试中，`scope` 是唯一没有必选 Skill 的自由规划阶段。0.6B 把 Office Component
`meeting_followup` 误当成 Tool，并输出裸 `<tool_call>`；Verifier 正确拒绝，流程没有进入审批
或产生外部动作。

修复没有向提示词继续堆叠 Component/Tool 特例，而是补齐两层运行时契约：

- 将范围定义收敛为显式 `meeting.scope.plan` Skill，让模型只填写紧凑、可验证的输入；
- 多 Skill 序列中，若前一 Skill 的可信 JSON 输出完整满足后一 Skill 输入 Schema，运行时直接
  进行 `typed_handoff`，避免要求小模型重抄 Receipt 等执行证据；不匹配时仍回退模型判定。

最终参考运行创建工单 `office_3780034e357c3b78f2268bb6`，四个 Stage 全部完成，真实人工审批
门被观察到；`office.action.dispatch` 创建 2 条幂等 Receipt，随后由
`execution.receipt.verify` 验证为 `valid: true`，工单状态为 `completed`。

类型交接仍经过完整 JSON Schema 校验、角色白名单、审批策略、SkillRunner 和 Verifier，不能
绕过权限，也不会把类型不兼容的任意前序文本当成参数。这使模型负责语义选择，ECS 负责可信
数据流和执行边界。

历史中间复测曾出现固定集 6/7、残缺 Tool 协议和 Receipt 参数重抄失败，说明 0.6B 仍有采样
波动；当前成功口径必须限定为“在 ECS 约束执行链下通过”，不能外推为自由规划可靠。

## 与 1.7B 对比

| 能力 | Qwen3 0.6B | Qwen3 1.7B |
|---|---|---|
| 固定 7 问 | 7/7 | 7/7 |
| 单一必选 Skill + 紧凑 Schema | 可用 | 可用 |
| Manifest 元问题 | 确定性通过 | 确定性通过 |
| 范围定义 | 显式 `meeting.scope.plan` 通过 | 通过 |
| 事实提取与核验 | 通过 | 通过 |
| 人工审批门 | 通过 | 通过 |
| 派发与 Receipt 验证 | 2 条、verified | 2 条、verified |
| 完整会议闭环 | 在 ECS 约束链下通过 | 通过 |

0.6B 的固定集总耗时低于本次 1.7B 参考运行，但两次运行受模型预热和生成长度影响，耗时只能
用于本机体验比较，不能当作严格吞吐基准。

## 使用建议

0.6B 适合：

- 已锁定单个 Skill 后判断是否执行并填写小型参数对象；
- 时间、计算、文本统计等确定性能力的自然语言入口；
- Manifest 查询、权限门禁和确定性输出的外围交互；
- 资源紧张、允许上层节点接管复杂规划的边缘节点。

0.6B 仍不适合：

- 没有必选 Skill、Schema 和顺序约束的自由 Stage 规划；
- 同时理解 Component、Stage、Skill、Tool 多层命名空间；
- 无约束地产生完整 Tool 协议；
- 脱离 ECS Harness 单独负责会议闭环。

部署时应维持当前模式：Stage 显式声明 Skill，模型填写紧凑参数，ECS 管理 Schema、顺序、
类型交接、审批、Receipt 和状态迁移。开放式业务规划仍建议使用更强模型，0.6B 作为受限
Worker 或低资源演示节点更稳妥。

## 可复现材料

执行命令：

```bash
make eval-qwen06-live
```

原始用例、响应、模型信息、WorkOrder 和跨节点 Trace 位于
[0.6B 参考证据](../evidence/qwen3-0.6b-live/README.md)。
