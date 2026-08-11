# JSON Schema 与参数填槽契约

ECS 使用 JSON Schema Draft 2020-12 作为 Tool、Skill、Agent 与 Office Manifest 的
确定性数据边界。Tool 与 Skill 还可以同时声明面向模型的精简输入和面向运行时的完整输入：

- `input_schema`：模型只需理解和填写的字段；
- `canonical_input_schema`：Tool、Skill、Stage 与跨节点传输使用的完整契约；
- `input_adapter`：从模型参数到规范参数的确定性转换；
- `output_schema`：执行结果契约。

未声明 `canonical_input_schema` 时，运行时继续使用 `input_schema`，因此旧 Manifest
不需要迁移即可继续工作。

## 示例

```json
{
  "name": "meeting.create",
  "description": "创建会议",
  "command": "@builtin",
  "input_schema": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "properties": {
      "title": {"type": "string", "minLength": 1},
      "start_time": {"type": "string"}
    },
    "required": ["title", "start_time"],
    "additionalProperties": false
  },
  "canonical_input_schema": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "properties": {
      "title": {"type": "string", "minLength": 1},
      "schedule": {
        "type": "object",
        "properties": {
          "start_at": {"type": "string"},
          "timezone": {"const": "Asia/Shanghai"}
        },
        "required": ["start_at", "timezone"],
        "additionalProperties": false
      },
      "execution": {
        "type": "object",
        "properties": {"request_id": {"type": "string"}},
        "required": ["request_id"],
        "additionalProperties": false
      }
    },
    "required": ["title", "schedule", "execution"],
    "additionalProperties": false
  },
  "input_adapter": {
    "version": "ecs.adapter/v1",
    "mappings": {"schedule.start_at": "start_time"},
    "defaults": {"schedule.timezone": "Asia/Shanghai"},
    "context": {"execution.request_id": "request_id"}
  },
  "output_schema": {"type": "object"}
}
```

`mappings` 的键是 Canonical 路径，值是模型输入路径。`defaults` 仅在目标不存在时
写入；`context` 只能读取 ECS 提供的 `request_id`、`trace_id` 和 `session_id`，不能
读取模型生成的身份或权限信息。

## 填槽行为

模型输入或转换后的 Canonical 对象缺少 `required` 字段时，ECS 不执行能力，也不猜值，
而是返回：

```json
{
  "input_request": {
    "capability": "meeting.create",
    "missing_fields": ["start_time"],
    "partial_arguments": {"title": "季度复盘"}
  },
  "verification": {
    "verifier": "schema-slot-gate",
    "status": "needs_input",
    "validation_result": "incomplete"
  }
}
```

客户端可以保留 `partial_arguments`，补齐字段后重新提交。普通无参数 Tool、文本统计等
确定性能力无需进入填槽流程。

## 安全与兼容性

- Schema 在 Manifest 加载时编译，运行时复用缓存的 Validator；
- 默认 Draft 为 2020-12，也允许 Manifest 显式声明 `$schema`；
- 支持 `$defs`、本地 `$ref`、组合规则、条件规则、格式与其他 Draft 2020-12 关键字；
- 禁止远程 `$ref`，加载 Manifest 不会访问外部网络；
- 模型只能看到 `input_schema`，审批、身份、Receipt 等字段应由 Adapter 或后续可信执行层注入；
- `direct_tool` 和 Office 内部调用继续提交 Canonical 参数，不经过模型 Adapter。

复杂业务应保持模型 Schema 小而扁平，把完整契约留在 Canonical Schema；高风险动作仍需
经过既有的授权、审批、Receipt 和 Verifier，Schema 不能替代这些门禁。
