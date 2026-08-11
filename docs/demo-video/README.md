# ECS 路演 Demo 视频

`ecs-trusted-meeting-demo.mp4` 是 58 秒无声演示视频，用于路演现场配合讲解。画面依次覆盖：

1. iOS、Android 与 HarmonyOS 三个平台的 ECS 宿主启动状态；
2. Office 建单与派发控制台；
3. 真实人工审批门、任务交接与执行结果；
4. 证据时间线和跨节点 Trace；
5. Ollama `qwen3:1.7b`、关闭 reasoning 后的最新真实回归结论。

2026-08-06 的视频回归在 macOS 双 ECS 节点上完成：固定能力用例 7/7，通过真实人工审批门，工作单最终状态为 `completed`，2 条 Receipt 均通过 Verifier。Android 画面来自复用的 `Eve_Android_API_35` AVD，未在 USB 真机上安装 ECS。

视频中的移动端画面证明跨平台 ECS 宿主实际启动，并不表示三个移动端都承担了本次 1.7B 推理。HarmonyOS 展示的是已安装运行包；当前源码使用 Go 1.25 依赖，而固定 OpenHarmony Go fork 为 1.24，最新 HarmonyOS Runtime 的重编兼容性仍需单独解决。

原始可复现证据与运行命令见[会议闭环演示](../meeting-followup-demo.md)和[Qwen3 1.7B 证据](../../evidence/qwen3-1.7b-live/README.md)。
