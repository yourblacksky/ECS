# 三个真实 ECS 节点演示

这份 Demo 用三个独立 ECS 容器和一个确定性模型替身，验证跨进程网络能力，而不是在单进程
测试里模拟多个角色。

```text
                    ┌──────────────────┐
                    │  ecs-controller  │ :18090
                    └────────┬─────────┘
                 broadcast / recursive
                   ┌─────────┴─────────┐
          ┌────────▼────────┐ ┌────────▼────────┐
          │  ecs-worker-a   │ │  ecs-worker-b   │
          │     :18091      │ │     :18092      │
          └─────────────────┘ └─────────────────┘
```

需要 Docker Engine 与 Compose v2。启动并自动验证：

```bash
make demo-network
```

该命令会：

1. 构建 ECS runtime 和测试专用 mock LLM；
2. 启动三个拥有独立进程、配置、认证令牌和数据卷的 ECS；
3. 分别检查三个 `/healthz`；
4. 直接请求两个 worker 执行 `clock`；
5. 由 controller 以 `broadcast` 同时调用两个 worker；
6. 由 controller 递归调用两个 worker；
7. 通过 `trace:read` 拉取两个 worker 的 Trace，聚合为 controller → worker 的证据树；
8. 校验 Trace 列表和 `/metrics` 中的 Peer 成功数、延迟以及本地执行计数；
9. 校验响应中的节点身份和真实 `ToolRun`；
10. 驱动完整四阶段会议督办闭环（`go run ./cmd/ecs-office-network-demo`）：controller 创建
    `meeting_followup` work order，把 scope/extract/verify/dispatch 四个 stage 按
    group 派发到 worker 执行；dispatch 停在人工审批门，admin 批准后二次派发完成；
    断言四 stage 由 worker 节点执行、审批记录与外部回执完整。

正常结果会显示三个节点名称、group/recursive 覆盖范围和 `evidence_tree`，并打印会议
闭环的 `stages` 分布（如 scope/extract/verify 在 worker-a、dispatch 在 worker-b）。
节点会继续运行，便于使用浏览器或 curl 调试。停止并删除 Demo 数据卷：

```bash
make demo-network-down
```

本机没有 Docker 时，可用真实进程跑同一闭环：

```bash
make verify-three-node-meeting
```

该脚本从 `demo/three-node/*.json` 生成本地 localhost 配置，启动一个 mock-llm 与三个
ECS 进程，然后运行 `cmd/ecs-office-network-demo` 完成同样的四阶段闭环验证。

端口和演示令牌：

| 节点 | 地址 | Token |
|---|---|---|
| controller | `http://127.0.0.1:18090` | `demo-controller-token` |
| worker A | `http://127.0.0.1:18091` | `demo-worker-a-token` |
| worker B | `http://127.0.0.1:18092` | `demo-worker-b-token` |

这些固定令牌只用于绑定到本机回环地址的演示环境，不得用于生产部署。静态 Peer 配置用于让
演示完全可复现；动态发现、入网审批和关系租约由各自的集成测试及多设备回归覆盖。

常用观测请求：

```bash
demo_token='demo-controller-token'

curl -H "Authorization: Bearer ${demo_token}" \
  'http://127.0.0.1:18090/v1/traces?limit=20'

curl -H "Authorization: Bearer ${demo_token}" \
  'http://127.0.0.1:18090/v1/traces/<trace-id>?aggregate=true'

curl -H "Authorization: Bearer ${demo_token}" \
  'http://127.0.0.1:18090/metrics'
```

桌面默认保留最近 1000 条且最长 7 天；Android、iOS、HarmonyOS 默认保留 300 条且最长
72 小时。`trace.max_traces` 与 `trace.retention` 可按部署存储预算调整，正在执行的 Trace
不会被清理。
