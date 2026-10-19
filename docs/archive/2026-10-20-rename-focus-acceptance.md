# 历史验收：重命名输入框焦点、尝试记录展开按钮与标记日志（2026-10-20）

> 历史状态：此记录为重命名输入框焦点、尝试记录展开按钮与标记日志一轮的验收，已被批次详情处理中视图与状态筛选的验收取代，不作为当前行为依据；当前记录见 [最新验收](../ACCEPTANCE.md)。

范围：基线 `2702e8b0a3e2` 加本轮变更。本轮只调整两处前端样式并删除标记任务完成日志及仅供该日志使用的耗时统计；未修改接口、数据库、配置协议、标记行为或测试断言。上一轮记录见 [历史验收](2026-10-19-ci-release-acceptance.md)，不用于证明本轮通过。

## 实现要点

- 重命名弹窗“新名称”：焦点环由外层 `.extension-input` 的 `:focus-within` 绘制，内部输入框不再单独绘制轮廓。此前内部输入框的焦点环比外层边框窄，右侧露出未被包住的边框和扩展名区域（文件夹扩展名为空时尤为明显）。
- 批次详情“全部 N 次尝试”展开按钮：悬停时保持透明背景，点击后不再出现浅色底；键盘焦点轮廓保留。
- 删除 `mark task finished` INFO 日志；`MarkResult.Metrics`、`MarkMetrics`、`RefreshMetrics` 与 `refreshWithMetrics` 只服务于该日志，一并删除，`Refresh` 直接调用 `ScanDirectory`。启动、HTTP 监听和调度器启动日志保留。

## 已执行验证

本机 macOS / arm64，Go 1.27.2，Node.js 26.12.0。浏览器测试使用隔离服务器 18790、临时数据库、合成媒体、真实 FFmpeg 和本地假模型。

| 命令或检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check`、`npm --prefix web run check`、`npm --prefix web run build` | 通过 |
| `go mod verify`、gofmt、`go vet ./...`、`GOOS=linux GOARCH=amd64 go vet ./...` | 通过 |
| `go test -count=1 ./...`、`go test -race -count=1 ./...` | 全部包通过 |
| `go test -tags=integration -count=1 ./internal/media ./cmd/video-rename` | 通过 |
| `go build -o bin/video-rename ./cmd/video-rename` 后全量 `npm --prefix web run test:e2e` | 182 通过、0 失败（10.3 分钟） |
| 临时 Playwright 检查（隔离夹具，未入库） | 文件夹重命名时外层容器轮廓为实线、内部输入框无轮廓，截图确认焦点环完整包住输入框；尝试记录展开按钮点击后背景为透明 |

## 验证边界

样式检查覆盖桌面宽度截图与计算样式，窄屏只截图未做断言。未在 Linux 实机运行二进制，未执行真实模型或通知服务测试；GitHub Actions 结果以推送后的运行页面为准。
