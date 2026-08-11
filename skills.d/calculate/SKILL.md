---
name: calculate
description: Use for one explicit arithmetic, power, or base-logarithm operation that requires an exact result.
---

# Calculate

## When to Use

- The task contains two known numbers and one add, subtract, multiply, divide, power, or base-logarithm operation.
- For “a is b to what power”, use `log_base` with `a` as the value and `b` as the base.
- An exact deterministic result is required.

## Do Not Use

- Do not use for conceptual math discussion, equations, calculus, or statistics.
- Do not guess missing operands or divide by zero.

## Input and Output

Input is `{a, op, b}`. Output is `{"result": number}`.

## Workflow

Validate the three inputs. Use only `add`, `subtract`, `multiply`, `divide`, `power`, or
`log_base` for `op`. Invoke the `calculate` tool once, validate its JSON result, and return it.

## Failure, Risk, and Compensation

The Skill runner does not retry invalid input. ECS may ask the model for one corrected call. Return the
single blocking reason after that correction fails. This skill has no side effects and needs no rollback.

## Compatibility and Reuse

ECS 0.3+, tool protocol v1, and models from 0.5B. Suitable for worker or calculator agents.

## 工程契约

<!-- ecs-skill-contract:start -->
### 依赖工具
- `calculate >=1.1.0`
- 节点能力：无额外要求。

### 失败处理
- 最多尝试 1 次，单次超时 `3s`；失败后返回无法计算的具体原因。
- 风险等级 `low`，审批策略 `none`；边界：不执行表达式代码；不访问文件或网络；除数不能为零；对数真数必须大于零且底数大于零且不等于一。
- 不需要补偿；失败不会留下需要回滚的外部副作用。

### 复用价值
- 为任意 Worker 提供确定性的基础、幂和对数计算。
- 标签：`math`、`local`、`deterministic`。

### 版本演进
- Skill `calculate v1.1.0`；要求 ECS `>=0.3.0`、Tool Protocol `v1`、最低模型 `0.5b`。
- 输入/输出、依赖、工作流或失败语义变化时，必须同步更新 `skill.json` 与本文档，并按语义化版本提升 Skill 版本。
<!-- ecs-skill-contract:end -->
