# 历史验收：代码质量整理与 Go 1.27.3 升级（2026-10-22）

> 历史记录，已由 2026-10-28 的全仓验收替代。下述路径、接口、结构号和旧测试结果不代表当前实现。当前结果见 [最新验收](../ACCEPTANCE.md)。

范围：基线 `92a6f78` 加本轮变更。本轮按代码质量检查结果清理死代码、未使用参数与样式，合并重复逻辑，改善命名并拆分过长函数；Go 升级至 1.27.3，GitHub Actions 改用 Node.js 26 并取最新补丁。未修改数据库结构、接口字段、配置协议或界面文案。上一轮记录见 [历史验收](../archive/2026-10-20-preview-window-acceptance.md)，不用于证明本轮通过。

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

## 补充：竞态模式下的偶发测试失败（2026-10-22）

`92a6f78` 的 GitHub Actions 在 `go test -race` 中失败于 `TestDirectoryProcessingFiltersUseSubtreesBeforePaging`：游标报"目录已刷新，请返回第一页"，随后各子测试计数为 0。原因在测试夹具：它直接写入合成的目录索引，磁盘上的媒体根目录为空；`listOrdered` 遇到刷新时间超过 30 秒的目录会启动后台重扫，CI 竞态模式下该测试耗时 31.9 秒，重扫把空目录发布为新 generation。把根目录刷新时间回拨 31 秒后本机稳定复现同样报错，与产品行为无关。

修正：测试把合成根目录的刷新时间写为未来时间，不再受 30 秒过期检查影响。验证：在列表断言前临时加入 31 秒等待后该测试通过（等待已移除）；`go test -race -count=1 ./internal/library` 通过。

## 接口约定对齐（2026-10-27，未提交）

按 [代码规范.md](../../代码规范.md) 第 5 节调整，项目未上线，不保留旧接口：错误码改为小写（`internal` 等），新增未认证、无权限两类；运行记录严格解析为 JSON 编码的错误记录，不再兼容纯文本；会话表去掉 `csrf` 列（`schemaVersion` 20261027）；配置键改为 `media_path`/`tmp_storage`，新增 `log.level`/`log.format`；认证改为 `Authenticate`/`Login`/`Logout`，登出返回 204；新增 `/healthz` 与 `/api/info`；列表统一返回 `{"items": [...]}`；数字型路径/查询参数非法时返回 400 `invalid_argument`；非 JSON 请求体返回 415，过大返回 413；日志器由构造参数注入。前端改用 `apiItems` 读取列表（含计划预览），错误文本按新错误码；e2e 夹具改为编码记录与 `{items}` 列表。

验证：Go 全套检查（gofmt、goimports、vet、staticcheck、race 测试、govulncheck）通过；vue-tsc、lint、vitest 21 项通过；重新构建 `web/dist` 与 `bin/video-rename` 后 Playwright 184 项全部通过。首轮全量 e2e 有 15 项失败（直接调用期望裸数组、旧消息夹具、`processing_log` 位置），第二轮剩 2 项（计划预览仍按数组读取、测试直接读取日志列表），均已修正后重跑通过。

## 代码规范与目录规范对齐（2026-10-27）

按 [代码规范.md](../../代码规范.md)、Go / Web / Vue 技术栈规范与 [目录规范.md](../../目录规范.md) 逐条整改：

- Go：15 个带连字符的文件改为下划线命名；`framecache` 不再混用 JSON v1/v2（改用 v2 自定义编解码，manifest 与缓存键字节不变，已用临时测试对比）；`%v` 包装错误、无参 `fmt.Errorf`、`os.IsNotExist`/`IsExist` 全部改正；文件权限统一为 `0o600` 写法；删除 `Library` 中“无 ctx + `XxxContext`”的成对 API，`Get`、`PreviewNumbering(ctx, NumberingRequest)`、`ApplyNumbering`、`AutoCommit` 等改为 ctx 首参，并沿 `jobs.Create`、`Scheduler.Save`/`Tick` 与 HTTP 处理器传递；应用编号使用 `context.WithoutCancel`，关闭阶段不再用 `context.Background()`；稳定等待可注入计时器，暂停与租约测试改为轮询断言；页面与 API 统一使用规范的默认 CSP（去掉 `'unsafe-inline'` 与 `data:`），并有测试断言。
- Web：全局提示改为 `app/use-toast.ts` 的 `useToast()` 与 `App.vue` 中唯一的 `ToastNotice`，离开页面时立即撤下本页提示；用户文案统一经 `errorText`（`error-text.ts` 改为内部码表 `server-codes.ts`，`displayPath` 移至 `lib/format/path.ts`）；剪贴板使用自有 `ClipboardError`，`lib` 不再依赖 `app`；图表颜色移入 `theme.css` 的 `--chart-*` 变量；去掉脚本与模板中的嵌套三元并启用 `no-nested-ternary`；去掉模板非空断言、补全空 `catch` 注释、时区统一用 `localTimeZone()`；`LibraryPage` 拆出删除、批量操作、标记三个弹窗组件。修复 `AudioPlayer` 把“读取中”显示为“读取失败”的问题。
- 目录与文档：`SystemSettings.vue` 改名 `SystemSettingsPage.vue`；`.gitignore` 去掉旧路径；README 目录例外去掉冗余行，代码例外登记 ESLint `allowNumber`；`DESIGN.md` 登记断点、提示与图表配色约定，修正 `Modal`、`error-text.ts` 旧引用。

本机 macOS / arm64，Go 1.27.3，Node.js 26。

| 命令或检查 | 结果 |
| --- | --- |
| `go mod verify`、`go mod tidy -diff`、gofmt、goimports | 通过 |
| `go vet`（本机、darwin/arm64、linux/amd64、`-tags integration,scale`）、staticcheck | 通过 |
| `go test -race -count=1 ./...`、`go test -tags=integration ./internal/media ./cmd/video-rename` | 通过 |
| `govulncheck ./...`、linux/amd64 交叉编译 | 无漏洞；可构建 |
| `npm --prefix web run format:check`、`lint`、`typecheck`、`test`（25 项）、`build`、`npm audit --omit=dev` | 通过，0 漏洞 |
| 无头 Chromium 逐页检查（隔离夹具服务器） | 新 CSP 下无违规 |
| 全量 `npm --prefix web run test:e2e`（重新构建 `web/dist` 与 `bin/video-rename` 后） | 184 通过、0 失败（10.2 分钟）。上一轮全量有 1 项失败：离开页面时提示随页面淡出而未立即消失，改为路由离开即撤下本页提示后通过 |

保留未改：E2E 中约 600 处 CSS 定位器未改为 `getByRole`/`getByLabel`；`LibraryPage`（1913 行）与 `RunsPage`（1056 行）未进一步拆分。另发现 `models.Store.Get` 等 I/O 方法缺少 ctx、`App` 与 `Library` 在结构体中保存 context，属 Go 规范第 5 节的存量差异，本轮未处理。

## 验证边界

未运行规模测试（`-tags=scale`、`scale_large`）。未在 Linux 实机运行二进制，交叉编译只证明可构建；Node.js 26 与 Go 1.27.3 的 GitHub Actions 结果以推送后的运行页面为准。`npm outdated` 列出的小版本更新（如 vite 8.3.5、vue 3.5.44、vue-tsc 3.3.13）本轮未升级。

## 补 ctx 与生命周期、E2E 定位器、页面拆分（2026-10-27，未提交）

处理上一节“保留未改”的三项。

- Go 第 5 节：`storage.Open`、各 Store 方法、`library`/`jobs`/`scheduler`/`notifications` 的数据库与出站请求全部改为 ctx 首参并沿调用链传递（HTTP 用 `r.Context()`，测试用 `t.Context()`）。`App` 改为 `New(ctx)` 只构造、`Start(ctx)` 启动、`Shutdown(ctx)` 取消并等待后关闭存储（可重复调用）；通知、批次、计划、目录扫描、帧缓存的 `Loop` 改为 `Run(ctx) error`。结构体不再保存 context：删除 `App.ctx`、`Library.RefreshContext`、`markWalk.ctx` 与批次暂停的 `runControl.parent/schedule`，请求发起的后台工作（标记、编号、目录刷新、手动扫描）用 `context.WithoutCancel(r.Context())` 并登记取消函数，暂停改为登记调度等待的取消函数。取消后仍须写入的结果（批次完成/取消、失败诊断、抽帧记录、模型尝试、改名与编号的提交和回滚、删除后的索引、通知投递结果）经 `storage.Detach` 脱离取消并限时 30 秒；早期读取被取消时按取消而非失败结束批次。
- E2E：约 700 处 CSS 定位器改为 `getByRole`/`getByLabel`/`getByText`/`getByTitle`，公共辅助在 `e2e/support.ts`（`toasts`、`bodyRows`、`pageReadout`）与 `e2e/run-record-pages.ts`。为此补充了可访问名称与角色：批量操作与标签栏 `role=group`、卡片与批次条目 `article` 名称、文件名列 `rowheader`、目录路径导航、媒体元素标签、运行详情的统计/生命周期/请求尝试列表与具名区域、用量页面板区域与 KPI/图例/分档列表、图表提示名称等。剩余 56 行 CSS 均为布局几何、配色样式、SVG 内部图形，以及剪贴板降级用的临时 `textarea`（无可区分角色），已就近注释。4 处 `waitForTimeout` 均为测量时长并已有注释。
- 页面拆分：`LibraryPage` 先抽出 `delete-entries.ts`、`batch-actions.ts`、`entry-dialog.ts`，再拆出 `EntryDialog`、`ImagePreviewDialog`、`DirectoryActions`（1914 → 1613 行）；`RunsPage` 抽出 `run-filters.ts`、`run-timing.ts`、`plan-form.ts` 与 `PlanDialog`（1057 → 750 行）。

本机 macOS / arm64，Go 1.27.3，Node.js 26。

| 命令或检查 | 结果 |
| --- | --- |
| gofmt、goimports、`go vet`（本机、linux/amd64、linux/arm64、`-tags integration,scale`）、staticcheck、`go mod tidy -diff` | 通过 |
| `go test -count=1 ./...`、`go test -race -count=1 ./...`、`go test -tags=integration ./internal/media ./cmd/video-rename` | 通过 |
| `govulncheck ./...` | 无漏洞 |
| `npm --prefix web run format:check`、`lint`、`typecheck`、`test`（25 项）、`build` | 通过 |
| 全量 `npm --prefix web run test:e2e`（重新构建 `web/dist` 与 `bin/video-rename` 后） | 184 通过、0 失败（10.4 分钟）。按批改定位器期间的失败：复制按钮点击后改名为“已复制”、分页读数设为 `role=status` 与空状态提示冲突（已撤回该角色）、运行记录表含诊断行导致行序错位、内容列与状态列同为“待统计”，均已修正 |

未覆盖：仅本机实测，未在 Linux 目标环境运行 E2E；`LibraryPage` 仍超过 1600 行，文件表格行与媒体播放弹窗未继续拆分。
