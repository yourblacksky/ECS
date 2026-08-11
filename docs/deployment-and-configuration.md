# ECS 部署与配置指南

本指南说明 CLI、系统服务、Docker、三节点 Demo 与移动端的部署边界。所有字段定义、默认值
和校验规则见[完整配置参考](configuration-reference.md)。

## 选择部署形态

| 形态 | 适用场景 | 状态与配置目录 | 主要限制 |
|---|---|---|---|
| CLI | 本地开发、单节点调试 | 配置文件目录下的相对路径 | 终端退出即停止 |
| macOS/Linux/Windows 服务 | 固定桌面或边缘节点 | 使用安装时指定的绝对配置路径 | 安装/卸载需要管理员权限 |
| Docker 单节点 | 服务器、隔离评审环境 | `/app/data` volume + 只读配置 | 宿主模型地址和 Token 需显式注入 |
| Docker 三节点 | 可复现多节点回归 | 三个独立 named volume | 使用确定性 mock LLM，不是生产拓扑 |
| Android/HarmonyOS | 前台 peer | 应用沙箱 + 平台 Secret Store | 受系统前台/后台生命周期限制 |
| iOS/浏览器 | 前台 leaf | 应用沙箱或页面会话 | 不适合作为永久 Worker |

## CLI

```bash
cp config.example.json config.json
# 修改 llm.model、llm.base_url，并通过环境变量注入密钥
ECS_LLM_API_KEY='...' ./bin/ecs -config ./config.json

curl --fail http://127.0.0.1:8090/healthz
./bin/ecs -version
```

连接官方 DeepSeek 时，macOS、Linux 和 Windows 使用同一份 Go 配置内核。将配置中的
`llm.base_url` 设为 `https://api.deepseek.com`、`llm.model` 设为
`deepseek-v4-flash`。macOS/Linux 通过上面的环境变量启动；Windows PowerShell 使用：

```powershell
$env:ECS_LLM_API_KEY = '...'
.\ecs.exe -config .\config.json
```

`config.example.json` 仍默认指向本机 Ollama，避免桌面安装在未明确配置时把请求发往外部服务。

不要把 `config.json`、API Key、关系 Token 或配对码提交到仓库。相对路径始终以配置文件所在
目录为基准，因此把配置复制到其他目录时，要同时复制 `prompts/`、`tools.d/`、`skills.d/`、
`agents.d/`、`pipelines.d/` 和 `office.d/`，或改用绝对路径。

## 原生系统服务

`ecs service` 在 Linux 使用 systemd、macOS 使用 system LaunchDaemon、Windows 使用 SCM：

```bash
sudo ./bin/ecs service install -config /absolute/path/config.json
./bin/ecs service status -config /absolute/path/config.json
sudo ./bin/ecs service restart -config /absolute/path/config.json
sudo ./bin/ecs service uninstall -config /absolute/path/config.json
```

安装命令会把当前可执行文件和配置文件解析为绝对路径。服务账户必须有权读取配置、Manifest、
Prompt 和 Secret Provider，并对 Session、Trace、Office、Network 数据目录具有读写权限。

## Docker 单节点

镜像使用 Go 1.25.12 多阶段构建、非 root 用户和 `/healthz` healthcheck：

```bash
docker build --target runtime --tag ecs:local .
docker run --detach --name ecs-local \
  --add-host host.docker.internal:host-gateway \
  --env ECS_AUTH_TOKEN='replace-with-random-token' \
  --env ECS_ADMIN_TOKEN='replace-with-separate-admin-token' \
  --env ECS_LLM_API_KEY='...' \
  --publish 127.0.0.1:18090:8090 \
  --volume ecs-data:/app/data \
  ecs:local

docker inspect --format '{{.State.Health.Status}}' ecs-local
curl --fail http://127.0.0.1:18090/healthz
docker logs ecs-local
```

内置 `config.docker.json` 监听 `0.0.0.0:8090`，因此必须注入 `ECS_AUTH_TOKEN`。默认模型地址
是 `http://host.docker.internal:11434/v1`；Linux Engine 需要上面的 `--add-host`，或者挂载
自定义配置：

```bash
docker run --detach --name ecs-local \
  --env ECS_AUTH_TOKEN='...' \
  --mount type=bind,src=/absolute/config.json,dst=/app/runtime/config.json,readonly \
  --volume ecs-data:/app/data \
  --publish 127.0.0.1:18090:8090 \
  ecs:local -config /app/runtime/config.json
```

## Docker 三节点回归

```bash
make demo-network
# controller  http://127.0.0.1:18090
# worker-a    http://127.0.0.1:18091
# worker-b    http://127.0.0.1:18092
make demo-network-down
```

该命令使用 [`demo/three-node/compose.yml`](../demo/three-node/compose.yml)，验证 broadcast、
recursive、真实 ToolRun、跨节点 Trace 聚合和 Metrics。Compose 中的 Token 只用于固定 Demo，
不能复用于部署环境。

## 移动端配置注入

- Android：默认资源位于 `android/app/src/main/ecs/config.json`；所有构建都使用无凭证模板，
  API Key 在安装后录入 Android Keystore，不提供通过 Gradle 属性或环境变量嵌入 APK 的路径，
  参见 [Android README](../android/README.md)。
- iOS：运行时资源位于 `ios/ECSApp/Resources`；API Key 使用 Keychain/内存 Secret 注入，参见
  [iOS README](../ios/README.md)。
- HarmonyOS：配置物化到应用沙箱，API Key 使用 Asset Store，参见
  [HarmonyOS README](../harmonyos/README.md)。

移动端默认评审配置可指向 DeepSeek；local-first 表示部署者能够选择本地 OpenAI-compatible
模型并保持业务状态本地化，不表示所有平台的默认包都离线运行。

## 安全基线

1. 非 loopback 监听必须配置 `ECS_AUTH_TOKEN`，跨机器使用 HTTPS、Tailscale 或 WireGuard；
2. Admin 只接受独立 `ECS_ADMIN_TOKEN`，不接受节点 Token；CLI loopback 会在首次启动生成
   `data/admin.token`，共享主机应覆盖为受管 Secret；
3. 关系 Token、Registry Token、IoT Header/MQTT 凭证只通过 Secret Provider 或环境变量注入；
4. 使用 named volume/持久目录保存 Session、Trace、Office、Network 状态，并限制文件权限；
5. 不把发现视为授权；Registry/mDNS 只提供地址，调用仍需关系和 scope；
6. 外部 Tool/Connector 上线前必须补幂等、Receipt、超时、补偿和审计策略。

## 升级、备份与回滚

- 升级前备份 `sessions`、`traces`、`office.order_directory` 和 `network_auth.directory`；
- 运行 `ecs -version`，核对节点版本和 Skill `compatibility.ecs`；
- 先在独立数据副本运行 `make test`、`make vulncheck` 和三节点回归；
- Manifest/Schema 不兼容变更应提升版本，不能只替换 Prompt；
- Docker 回滚应同时恢复匹配的镜像、配置、Manifest 和数据快照。

## 健康与排错

| 检查 | 命令/入口 | 说明 |
|---|---|---|
| 进程存活 | `GET /healthz` | liveness；不代表模型或外部 Connector 一定可用 |
| 最终配置 | 启动日志、`GET /v1/admin/config` | Admin 入口需要 Token；不会返回模型密钥明文 |
| 节点能力 | `GET /v1/capabilities` | 区分已注册与当前可调用 |
| 证据 | `GET /v1/traces`、`GET /metrics` | 查看执行树、跨节点聚合和指标 |
| Docker | `docker inspect`、`docker logs` | 检查 health 状态和启动校验错误 |

常见启动失败原因：没有显式 `llm.model`；把相对路径误当作工作目录路径；非 loopback 缺少
Token；Discovery/Registry URL 无效；expiry/lease 不大于 interval/heartbeat；移动端旧安装保留
了历史配置。完整字段与范围以[配置参考](configuration-reference.md)和代码校验为准。
