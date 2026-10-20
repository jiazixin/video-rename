# 历史验收：批次详情处理中视图与状态筛选（2026-10-20）

> 历史状态：此记录为批次详情处理中视图与状态筛选一轮的验收，已被媒体预览留边窗口与还原断言时序修正的验收取代，不作为当前行为依据；当前记录见 [最新验收](../ACCEPTANCE.md)。

范围：基线 `9bfcfce` 加本轮变更。本轮为批次详情运行记录新增处理中、已完成、已跳过筛选，处理中视图显示逐文件阶段时间轴、新旧名称与最近结束文件；处理阶段只保存在 jobs 内存中，未修改数据库结构、配置协议或其他页面行为。上一轮记录见 [历史验收](2026-10-20-rename-focus-acceptance.md)，不用于证明本轮通过。

## 实现要点

- `GET /api/runs/{id}/items` 的 `status` 接受 `running`、`done`、`skipped`、`failed`，并返回各状态整批计数；`running` 按开始时间排序、附带内存阶段和当前范围最近结束的 20 项（使用已有 `item_logs` 部分索引），`skipped` 附带跳过原因汇总。
- jobs 在文件开始处理、等待模型名额、发起第 N 次请求（含兜底）、等待重试、提交改名时更新阶段，文件进入终态即清除；模型返回有效标题后记录清洗后的拟用名称。
- 前端筛选为全部文件、处理中、已完成、已跳过、最终失败，计数跟随目录筛选；活动批次默认处理中，已结束批次默认全部文件。处理中视图位于 `RunLiveRecords.vue`。

## 已执行验证

本机 macOS / arm64，Go 1.27.2，Node.js 26.12.0。浏览器测试使用隔离服务器 18790、临时数据库、合成媒体、真实 FFmpeg 和本地假模型。

| 命令或检查 | 结果 |
| --- | --- |
| `npm --prefix web run format:check`、`npm --prefix web run check`、`npm --prefix web run build` | 通过 |
| `go mod verify`、gofmt、`go vet ./...`、`GOOS=linux GOARCH=amd64 go vet ./...` | 通过 |
| `go test -count=1 ./...` | 全部包通过 |
| `go test -race -count=1 ./internal/jobs ./internal/app` | 通过（仅对本轮改动包执行竞态检测） |
| `go test -tags=integration -count=1 ./internal/media` | 通过 |
| `go build -o bin/video-rename ./cmd/video-rename` 后全量 `npx playwright test` | 180 通过、3 失败（10.0 分钟）；失败均为 `run-detail.spec.ts` 打开活动批次后仍按旧默认断言全部文件 |
| 修正上述断言后重跑 `run-detail.spec.ts`、`run-detail-records.spec.ts` | 9 通过、0 失败 |

新增测试：`TestLiveStageFollowsRetries`、`TestLiveStageTitleAndClear`（阶段随重试推进与清除）、`TestRunRecordsLiveStage`（真实执行中的阶段与结束后的最近记录）、`TestRunDirectoryRecords` 新增各状态筛选与计数断言；浏览器用例覆盖处理中排序、阶段与拟用名称、最近结束、跳过原因、已完成筛选、目录组合、键盘切换与 320px 无横向溢出。

## 验证边界

修正断言后未再跑一次完整浏览器套件，其余 180 项在修正前已通过且不涉及本次修改的测试文件。窄屏只验证无横向溢出并截图。未在 Linux 实机运行二进制，未执行真实模型或通知服务测试；GitHub Actions 结果以推送后的运行页面为准。
