# 小模型提示词与执行契约记录（0.5B–2B）

本文记录在 Qwen3-0.6B（Q8_0，Ollama 本地运行）上三轮系统提示词实验的过程与结论。
适用于为 ECS 新增 Skill、或更换同级别小模型时参考。

## 当前可复现基线：Qwen3 1.7B

2026-08-06 使用 Ollama `qwen3:1.7b`、关闭 reasoning 和原生 Tool API，启动真实 Controller
与 Worker 后完成了两组回归：

| 回归 | 结果 | 关键门禁 |
|---|---:|---|
| 固定 7 问 | **7/7** | Manifest 直答、SkillRun、ToolRun、Verifier |
| 可信会议督办闭环 | **通过** | 四 Stage、人工审批、2 条 Receipt、Receipt verified |

参考运行中，审批前执行耗时 43.37 秒，审批后执行耗时 30.24 秒。完整用例、执行脚本、
逐请求原始输出与跨节点 Trace 见
[Qwen3 1.7B 真实模型证据](../evidence/qwen3-1.7b-live/README.md)。复现命令：

```bash
make eval-qwen17-live
```

这次回归同时确认了三条执行设计：Office 必选 Skill 必须进入可信 Plan；Office persona 角色
不能被 Pipeline executor 覆盖；多 Skill Stage 应让小模型逐个填写紧凑参数，ECS 保留顺序、
完整 JSON Schema、审批和最终调用集验证。跨 Stage 交接优先携带原始 Skill JSON，避免把
结构化 owner/deadline 压成展示文本后要求小模型二次抄写。

以下三轮对比是早期 0.6B 历史实验，保留用于解释当前设计来源。

## 当前 0.6B 能力边界

同一套 2026-08-06 回归在 `qwen3:0.6b`（Q8_0）上得到固定 7 问 **7/7**，说明 Manifest
短路、单能力判定和 Schema 门禁已经显著改善早期表现。补齐显式 `meeting.scope.plan` Skill，
并让前序可信 JSON 在满足后一 Skill 输入 Schema 时进行类型交接后，真实会议闭环也已通过：
四 Stage 完成，人工审批门实际触发，2 条 Receipt 验证成功。

因此当前推荐口径不是“0.6B 已支持自由 Agent”，而是“0.6B 可在 ECS 的显式 Skill、Schema、
审批和 Verifier 约束下完成闭环；开放规划仍交给更强模型”。完整报告与原始证据见
[Qwen3 0.6B 能力边界报告](qwen3-0.6b-evaluation-report.md)。

## 实验设置

- 节点：`ecs-local`，模型 `qwen3-0.6b-instruct`（`Qwen/Qwen3-0.6B-GGUF` 的 Q8_0）
- 固定回归集（7 问）：你好 / 现在几点？/ 今天几号？/ 我有什么工具可以用？/
  我在 ecs 这个结点有什么工具可以用 / 计算 12 乘 3 / 统计"你好 world"的文字数量
- 判定标准：工具选择正确、协议格式正确、答案内容真实（不编造、不照抄示例）

## 三轮架构对比

| 架构 | 思路 | 回归结果 |
|---|---|---|
| 分类版（历史） | A/B/C/D 四类分流 + 每类示例 + 照念规则 | **7/7 通过** |
| 极简版 | 身份 + 工具目录 + 6 条轻规则，依赖多轮循环 | 1/7 |
| 折中版 | 极简 + 2 个通用格式示例 | 约 1/7（示例污染严重） |

当前 `prompts/system.small.zh.txt` 已演进为按优先级执行的短路路由；能力目录由 ECS
直接读取 Manifest，不再依赖模型发起并总结 `capabilities`。

## 0.6B 的实测短板

1. **协议遵从不稳定**：没有 few-shot 示例时，`<tool_call>` 标签会写丢、
   JSON 不闭合（极简版 7 问中 4 次格式错误）。多轮循环救不了第一轮就
   不发起工具调用的情况。
2. **幻觉接地差**：问时间会凭空编一个（"当前时间是 14:30"），问能力会
   乱调无关工具并采信结果（verifier 只验证工具执行成功，不验证语义相关性）。
3. **示例照抄**：示例中的具体值会被原样复读（时间示例导致"现在是 0 点 09 分"
   在不调工具时直接输出；计算示例导致"计算结果是 36"污染无关回答）。
4. **矛盾指令失稳**：多条"必须调工具"与"能直答就直答"并存时，模型行为随机。
5. **数字理解弱**：把 `00:26` 读成"早上 26 点"，需要"照念"规则绕过换算。

## 有效模式（推荐）

- **四类分流**：问候闲聊（直答）/ 时间（clock）/ 能力查询（capabilities）/
  工具任务（选一工具），每类一行指令 + 一个示例；给唯一兜底
  （"判断不了按 A 类直答"）。
- **每类都要有"直接说话"的示例**，不能全是 `<tool_call>`，否则模型学不到
  何时不调工具。
- **示例用中性域**：格式示例用计算（`{"result":36}`），不要用时间等会被
  照抄的值。
- **照念规则**：报时间/日期直接照念字段数字
  （`T00:26` → "0 点 26 分"），绕开数字换算短板。
- **元问题走 Manifest 直答**：能力查询由 ECS 直接读取并格式化 Manifest，
  同时显示“已注册”和“当前 Pipeline 可调用”，不经过模型选择或二次总结。

## 确定性回归与模型兼容性回归

不要用同一条自然语言用例同时证明模型、工具和多节点编排都可靠：

- 真实模型连通性使用 `expected_text`。ECS 只给模型两条极简消息、不注入能力目录、
  temperature 固定为 0，并由 Verifier 对结果逐字符校验：

  ```json
  {
    "prompt": "执行真实模型字面输出探针",
    "expected_text": "READY_QWEN06_WORKER1"
  }
  ```

- ECS 工具链、broadcast 与 recursive 回归使用 `direct_tool`，避免把小模型协议波动
  错算成编排故障：

  ```json
  {
    "prompt": "读取本节点时间",
    "direct_tool": {"name": "clock", "arguments": {}}
  }
  ```

- “请调用 clock”这类自然语言用例单独统计为模型工具协议兼容率。0.6B 输出残缺
  `<tool_call>` 时 Verifier 必须拒绝，但该结果不影响 `direct_tool` 的 ECS 核心门禁。

## Tool 设计模式（0.6B 友好）

复杂度一律留在 Tool 代码里，模型只做选择题。实测有效的两个模式：

- **输出带 `summary` 预消化字段**：多字段数值结果模型会读错
  （`workdays` 返回 23 个工作日，模型答成 31 总天数）。Tool 直接输出
  `"summary": "共 31 天，其中工作日 23 天"`，模型照念即对。
- **输入做宽容解析**：模型给的参数格式不规范时由 Tool 归一化，例如
  `workdays` 接受"8月3号"（缺少年份补当前年）。不要让小模型承担格式转换。

## 上下文长度管理

三层各自的限制（2026-07 改进后）：

- 发给模型的历史：`sessions.context_messages`（按条数）+
  `sessions.max_context_chars`（按字符数，粗略代理 token）双重裁剪，
  始终保留最新一条。默认值 20 条 / 4000 字符。
- 磁盘会话：`sessions.max_messages: 0` 仍无限制，无 TTL，会话污染
  问题依旧存在，调试应勤换 session。
- 模型窗口：Ollama `num_ctx` 默认 4096，超出会被**静默截断**（可能截掉
  系统提示词和工具目录，表现为"模型突然变笨"且无报错）。务必在 Modelfile
  中显式设置 `PARAMETER num_ctx 8192`（0.6B 显存代价很小），并让
  `max_context_chars` + 系统提示词总量留有余量。

## 失效模式（避免）

- 极简提示词 + 依赖多轮循环：对 7B+ 模型成立（OpenClaw/Hermes 风格），
  0.6B 上协议格式和事实接地都撑不住。
- 与运行时追加内容矛盾的规则：ECS 会在系统提示词后追加会话引导
  （`prompts/conversation.default.zh.txt`）和工具目录，新增规则前先检查冲突。
- "答案尽量短"+"不知道就说不知道"这类笼统规则：会把问候也压成"不知道。"。

## 其他注意事项

- **角色白名单**：新增 Tool/Skill 后必须在 `agents.d/executor.json` 的
  `allowed_skills` / `allowed_tools` 中加入，否则 planner 报
  `no executor covering requested skills`。
- **会话污染**：失败回答会进入会话历史并被模仿（"不知道。"传染），
  调优时用全新 `session_id`。
- **提示词改动会在下一次请求立即生效**：`system_prompt_file` 与
  `conversation_guidance_file` 会在每次 Reply 开始时重新读取，不需要重启，也不需要
  发送 `SIGHUP`。生产环境必须先写入同目录临时文件，再用原子 rename 替换正式文件；
  直接原地编辑可能让请求读到编辑中间态，删除、移动或写入不可读内容会让新的 Reply
  以 `read system prompt` / `read conversation guidance` 失败。`SIGHUP` 负责重载
  Tool、Skill、角色、Pipeline 与 Office 注册表，不负责提示词文件的一致性发布。
- **半格式错误要靠内核兜底**：0.6B 偶尔会输出未闭合的 `<tool_call>`
  （缺 `</tool_call>`），解析器将其当作普通文本，旧版 verifier 会放行，
  把裸露 JSON 漏给用户。自 commit `36dbb91` 起，`Verify()` 会拒绝任何
  包含 `<tool_call` 字样的最终答案并强制重试。教训：小模型的"想调工具
  但没写对"是常态，解析器之外的最后一道防线必须在内核里，不能指望模型
  或提示词。
- **转写保真度无法根治**：把用户长文本抄进 JSON 参数时 0.6B 会丢字符
  （结尾标点最常丢，提示词规则只能缓解）。设计 Tool 时尽量降低对逐字
  保真的依赖（如计数、提取类任务对结尾标点不敏感），不要用它来复制
  需要一字不差的内容（合同条款、密钥等）。
