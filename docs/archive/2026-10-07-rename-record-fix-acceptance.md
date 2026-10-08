# 历史验收：批次详情丢失新文件名修复（2026-10-07）

> 历史记录，仅反映当时工作区；当前审查与验证结果见 [最新验收](../ACCEPTANCE.md)。

基于干净工作区 `af2e3e4140b0`。上一轮扫描排除验收已移至 [历史记录](2026-10-07-before-rename-record-fix-acceptance.md)。

## 问题与漏测原因

实际自动改名操作键为 `run:R:item:I:N`，末尾 N 为文件提交遇到名称冲突时的尝试序号；详情接口此前只匹配 `run:R:item:I`。文件已经改名、日志已经保存，页面却显示“已完成 / 主模型成功 / 未记录改名结果”。

此前接口夹具手写了不符合实际写入格式的操作键，页面测试又直接模拟 `new_name`。已有真实 FFmpeg 命名用例只检查文件目录和成功诊断，没有打开详情核对新名称。局部测试通过无法证明写入、读取与页面之间的契约一致，上一轮验收对此存在覆盖缺口。

新增隔离回归在修复前明确失败：磁盘存在 `本批次标题.mp4`，详情返回空 `new_name`；真实 FFmpeg 和本地假模型浏览器用例也复现了截图中的原文。原始浏览器失败证据在 `web/test-results/rename-record-red/`。

## 修复与回归范围

- 按包含末尾冒号的精确任务键范围查找操作，按操作 ID 选择最后一次尝试；保留批次、条目与自动命名来源核对，不使用当前文件名冒充本批次结果。
- 多次冲突只产生一条详情记录，序号 9/10 按实际写入顺序选取；item 1/10 不串名。键范围可通过唯一 request_key 索引定位，避免为每条文件扫描整个批次的操作。
- 后端贯通真实任务执行、临时文件改名、持久日志和详情 HTTP handler，并验证还原、后续手动改名不改变本批次历史名称。该用例替换分析函数和工具探测，不声称执行了 FFmpeg；真实抽帧与模型 HTTP 协议由浏览器用例覆盖。
- 原有真实 FFmpeg 浏览器用例增加详情新名称断言、桌面/390px截图、无水平溢出及 Escape 关闭后焦点恢复检查。模型使用本地假服务，详情接口不拦截、不模拟。
- 不修改数据库结构或历史数据。已保存且遵循实际操作键格式的日志可由修复后的接口直接读出，无需重新命名媒体。

## 已执行验证

| 命令 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` / `npm --prefix web run check` / `npm --prefix web run build` | 通过 |
| `go mod verify` | 通过 |
| `GOCACHE=/tmp/video-rename-go-cache go test -count=1 ./...` | 全仓通过 |
| `GOCACHE=/tmp/video-rename-go-cache go test -race -count=1 ./...` | 全仓通过 |
| `GOCACHE=/tmp/video-rename-go-cache go vet ./...` | 通过 |
| `GOCACHE=/tmp/video-rename-go-cache go test -tags=integration -count=1 ./internal/media` | 通过，真实 FFmpeg/ffprobe |
| `GOCACHE=/tmp/video-rename-go-cache go test -race -count=1 ./internal/app` | 最终键范围查询版本再次通过，44.9秒；该版本再执行 app vet 通过 |
| `GOCACHE=/tmp/video-rename-go-cache go build -o bin/video-rename ./cmd/video-rename` | 最终代码构建通过 |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOCACHE=/tmp/video-rename-go-cache go build -o bin/video-rename-linux-amd64 ./cmd/video-rename` | 最终代码交叉编译通过 |

`npm --prefix web run test:e2e -- --output=test-results/rename-record-full`：**182/182通过，9.1分钟**。覆盖本次真实改名详情、全套目录/历史/批次/模型/设置/通知/播放操作，以及桌面、窄屏、加载/空/错误、焦点、选择和滚动保持。

完整套件运行期间仅调整了等价查询的索引定位方式，并补充窄屏截图滚动；最终版本已补跑整个 app 包竞态和构建，以及以下浏览器命令，**3/3通过，1.4分钟**：

```bash
npm --prefix web run test:e2e -- workspace.spec.ts run-detail-records.spec.ts --grep '真实 FFmpeg|目录统计覆盖整批|多目录同名' --output=test-results/rename-record-final
```

已查看 `web/test-results/real-rename-record-desktop.png` 与 `web/test-results/real-rename-record-mobile.png`：桌面和390px窄屏均显示真实新文件名；窄屏已滚动到记录，原名、新名、状态完整可读。最终 `git diff --check` 通过。

首次浏览器尝试因沙箱禁止本机监听而中断，获准启动隔离服务后正常执行。修复前失败断言保持不变；未修改断言掩盖产品失败。

## 验证边界

所有运行均使用本机隔离数据库、合成媒体和本地假模型，不读取用户配置、数据库或媒体。未访问截图中的局域网服务，未部署或重启用户实例；用户实例的实际文件与日志尚未核验。Linux 交叉编译仅代表可构建，不代表 Linux/NAS 目标环境实测。

本次未重跑 `scale` / `scale_large` 专项规模与吞吐基准；没有依赖升级，未重新进行联网漏洞审计。
