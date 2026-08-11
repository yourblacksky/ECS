# ECS 版本与 Release

ECS 使用语义化版本。唯一版本源是 [`version/VERSION`](../version/VERSION)，当前版本为
`0.3.0`；Skill Manifest 通过 `compatibility.ecs: ">=0.3.0"` 声明最低兼容版本。

查看运行二进制版本：

```bash
ecs -version
```

输出包含 ECS 版本、Git commit、构建时间、工作区是否带修改以及 Go 版本。普通 `go build`
读取 Go 自动嵌入的 VCS 元数据；Release 构建会用 `-ldflags` 固定 commit 与构建时间。
节点启动日志和运行状态也会输出当前 ECS 版本，便于多节点协作时核对兼容性。

## 发布规则

- 修复且保持兼容：增加 patch；
- 新增兼容能力：增加 minor；
- 不兼容的协议、配置或 Manifest 变更：增加 major；
- 修改版本时同步 Skill 的 `compatibility.ecs`，并运行 `make verify-version`；
- Git Tag 必须严格等于 `v<version/VERSION>`，例如 `v0.3.0`。

推送匹配 Tag 后，`Release` 工作流会重新测试、生成 Linux/Windows 二进制、源码包和
SHA-256，再在当前仓库创建 GitHub Release。Tag 和 Release 需要项目所有者明确执行；
普通 `main` 提交不会自动发布。
