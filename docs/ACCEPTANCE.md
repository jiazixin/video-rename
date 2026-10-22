# 最新验收：代码质量整理与 Go 1.27.3 升级（2026-10-22）

范围：基线 `92a6f78` 加本轮变更。本轮按代码质量检查结果清理死代码、未使用参数与样式，合并重复逻辑，改善命名并拆分过长函数；Go 升级至 1.27.3，GitHub Actions 改用 Node.js 26 并取最新补丁。未修改数据库结构、接口字段、配置协议或界面文案。上一轮记录见 [历史验收](archive/2026-10-20-preview-window-acceptance.md)，不用于证明本轮通过。

## 变更要点

- 删除仅测试使用的 `Library.List`/`ListSorted`/旧 `ListOrdered`/旧 `ListPage` 包装，原 `*Filtered` 方法改名为 `ListOrdered`、`ListPage`；删除按路径改名的 `rename.NoReplace`（绕过逐层打开父目录的约定），测试改用 `NoReplaceAt`；`ValidateTitleWithKeywords` 改名为 `ValidateTitle`。
- 删除 18 个无引用的 CSS 选择器（约 230 行）、5 个多余的类型导出、`parseMeta`/`startMark` 未使用参数、对整数时长的 NaN/Inf 判断，以及 History 页从未触发的数组响应分支（前端始终请求分页结果）；相应 E2E 模拟响应改为真实的分页结构。
- 新增 `internal/timebucket`，供运行分析与用量分析共用趋势分桶；新增 `domain.ActiveRunStatusesSQL`/`ActiveRunStatus` 与前端 `isActiveRunStatus`，替代 11 处 SQL 与 6 处前端硬编码列表（SQL 文本与 `run_active` 部分索引保持逐字一致）。
- 拆分 `runstats.Analytics`、`jobs.inferWithRetry`、`jobs.Create`、`jobs.process`、`SetProcessedWithProgress`、`ThinkingFields`、`retryHistory`，认知复杂度均降至 60 以下；运行来源分类只保留 SQL 一处定义。
- Library.vue：标记流程与吸顶表头移入 `mark-entries.ts`、`sticky-header.ts`；合并批次创建、还原、删除请求与面包屑路径；请求计数器、弹窗模式、`load` 参数改为有含义的名称与类型；`any` 改为 `Operation`、`LogRecord`、`ProbeResult` 等契约类型。通用函数改名为 `formatSize`、`formatTime`、`newRequestKey`、`isVideo`、`isMarked`，两个播放器共用 `formatPlaybackTime`。

## 已执行验证

本机 macOS / arm64，Go 1.27.3，Node.js 26.12.0。浏览器测试使用隔离服务器 18790、临时数据库、合成媒体、真实 FFmpeg 和本地假模型。

| 命令或检查 | 结果 |
| --- | --- |
| `npm --prefix web ci`、`format:check`、`check`、`build` | 通过 |
| `go mod verify`、gofmt、`go vet ./...`（本机、darwin/arm64、linux/amd64） | 通过 |
| `go test -count=1 ./...`、`go test -race -count=1 ./...` | 通过 |
| `go test -tags=integration -count=1 ./internal/media ./cmd/video-rename` | 通过 |
| `go build -buildvcs=true` 本机与 linux/amd64 交叉编译 | 通过 |
| 全量 `npm --prefix web run test:e2e` | 183 通过、0 失败（9.9 分钟） |
| `npm --prefix web audit`、`govulncheck ./...` | 均未发现漏洞 |
| staticcheck、deadcode（macOS / Linux 目标）、unparam、knip | 无死代码与未使用参数；staticcheck 剩余 6 条 ST1005 为 Anthropic、Claude、Bark、Cron 等专有名词开头的中文提示，保留 |

## 验证边界

未运行规模测试（`-tags=scale`、`scale_large`）。未在 Linux 实机运行二进制，交叉编译只证明可构建；Node.js 26 与 Go 1.27.3 的 GitHub Actions 结果以推送后的运行页面为准。`npm outdated` 列出的小版本更新（如 vite 8.3.5、vue 3.5.44、vue-tsc 3.3.13）本轮未升级。
