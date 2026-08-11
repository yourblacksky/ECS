# AgentTeams 上游集成

ECS 已有两层 AgentTeams 集成：ECS 服务端接收 AgentTeams Task 并映射到 Office Stage；本目录中的上游补丁让 AgentTeams Controller 将 ECS 识别为一种 Edge Worker Runtime。这里的补丁由 ECS 项目维护，尚未合入 AgentTeams 上游。

## 可复现基线

- 上游仓库：[`agentscope-ai/AgentTeams`](https://github.com/agentscope-ai/AgentTeams)
- 基线提交：[`2540c968a642845c4b9382afd75d8c80ed861137`](https://github.com/agentscope-ai/AgentTeams/commit/2540c968a642845c4b9382afd75d8c80ed861137)
- 上游许可证：[Apache License 2.0](https://github.com/agentscope-ai/AgentTeams/blob/2540c968a642845c4b9382afd75d8c80ed861137/LICENSE)
- 上游 Go 版本：1.25.0

在 ECS 仓库根目录运行：

```bash
make verify-agentteams-integration
```

验证脚本会把固定提交拉到临时目录，检查实际提交哈希，应用补丁，再运行：

```bash
go test ./internal/backend ./internal/controller ./internal/service
```

CI 使用同一个脚本。验证不依赖本机已有的 AgentTeams checkout；执行结束后会删除临时目录。

## Controller → ECS → Office 端到端证据

只验证补丁能够应用还不足以证明运行链路。下面的命令会调用补丁后的真实 AgentTeams
Controller `Deployer` 生成 `runtime.yaml`，再由真实 ECS 进程加载它、向 Controller
报告 ready/heartbeat、接收 Matrix Assignment，并把任务映射到 Office Stage：

```bash
make demo-agentteams-office
```

Matrix、LLM 和对象存储使用确定性测试替身；AgentTeams Runtime 投影代码、ECS Worker、
AgentTeams Task Store、Office Registry、Trace 和 Matrix Worker 均运行真实实现。完整边界、
证据文件与验收条件见 [`docs/agentteams-office-e2e.md`](../../docs/agentteams-office-e2e.md)。
CI 会上传 `agentteams-office-e2e-<commit>` Artifact，默认保留 30 天。

## 手动应用

如果已经检出相同的基线提交，可以手动验证和应用：

```bash
git apply --check /path/to/ECS/integrations/agentteams/0001-add-ecs-edge-runtime.patch
git apply /path/to/ECS/integrations/agentteams/0001-add-ecs-edge-runtime.patch
cd agentteams-controller
go test ./internal/backend ./internal/controller ./internal/service
```

补丁保留完整 Git blob index；自动验证还会核对 checkout 的提交哈希，避免把结果误记到未经验证的上游版本。

## Worker 配置

ECS Worker 必须使用 Edge 模式，因为它的生命周期由边缘机器而不是 AgentTeams Controller 管理：

```yaml
apiVersion: agentteams.io/v1beta1
kind: Worker
metadata:
  name: ecs-worker-1
spec:
  model: qwen3:0.6b
  runtime: ecs
  deployMode: Edge
  containerManaged: false
  identity: |
    - Name: ECS Worker 1
    - Specialization: small-model local tool execution
  agents: |
    - Complete only the assigned task.
    - Use only the tools configured on this ECS node.
  # These names are projected to runtime.yaml as the ECS Skill allowlist.
  skills:
    - calculate
    - text_stats
```

完整示例见 [`ecs-worker.example.yaml`](ecs-worker.example.yaml)。Controller 负责创建 Matrix 身份、房间和凭证，把 `member.runtime: ecs` 写入 `runtime.yaml`，并接收 ready/heartbeat 上报。ECS 读取同一份 runtime snapshot 和 TeamHarness 任务目录；每个 Skill 仍由 ECS 校验其底层 Tool 依赖。

## 当前边界

- 这是针对固定上游提交验证的适配补丁，不代表 AgentTeams 官方已经原生支持 ECS。
- 新版上游需要先更新基线、重新生成补丁并通过验证脚本，不能据此假定自动兼容。
- 补丁覆盖 Controller 的 Runtime 枚举、Edge 配置投影和相关单元测试；ECS 侧 Task 接入与 Office Stage 映射见 [`docs/agentteams-office-mapping.md`](../../docs/agentteams-office-mapping.md)。
