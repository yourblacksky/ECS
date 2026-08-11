# 会议督办参考运行证据

这里保存一组可以直接浏览的成功与失败运行记录，用来证明固定会议督办 Demo 不只包含输入 Fixture。证据由 `main@573ded1fe5fb63fb4c3ba87bfcc2ead134afef71` 上的实际执行链生成。

输入来自 `internal/meetingdemo/fixtures/`，人物与会议内容均为固定合成数据，不是真实用户、客户或生产案例。执行使用本地可替换 Connector，不代表已经接入真实办公系统。

## 文件

- [`success.json`](reference/success.json)：审批通过，执行两项动作并核验 Receipt；
- [`failure.json`](reference/failure.json)：行动项缺少责任人和期限，在外部动作前阻断；
- [`orders/success-work-order.json`](reference/orders/success-work-order.json)：成功路径的持久化 Office 工单；
- [`orders/failure-work-order.json`](reference/orders/failure-work-order.json)：失败路径的持久化 Office 工单；
- [`manifest.json`](reference/manifest.json)：生成命令、源码提交、结果与文件哈希；
- [`SHA256SUMS`](reference/SHA256SUMS)：参考文件的独立校验清单。

重新运行当前代码：

```bash
make demo-meeting
```

本地输出默认写入被 Git 忽略的 `data/demo/meeting-followup/`，避免随机 ID 和时间戳持续污染工作区。CI 会为每个提交上传完整的五轮运行 Artifact；正式交付 ZIP 也会从对应提交重新生成证据。仓库内这组文件只是可浏览的参考快照，不代替逐提交证据。
