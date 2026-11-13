> 历史验收快照（归档于 2026-11-13）。以下是递归标记批量读取优化之前的记录，不代表当前工作区通过；最新结果见 [当前验收](../ACCEPTANCE.md)。

# 最新验收：缺陷修复与补充排查（2026-11-10 至 11-11）

## 结论与范围

[缺陷排查与修复计划](2026-11-10-defect-audit.md) 列出的 D1–D11 全部修复，两轮补充排查中新发现的 N1–N7 同样修复；每项都有回归测试，其中 D11、N1、N3、N5、N7 在修复前先用测试复现。维护项 M1–M3 已处理（依赖升级与审计结论见计划）。疑点 Q1（NFS 改名）按决定暂不验证，Q3（登录限速的可信代理）决定不做，Q2（大小写命名）已于 2026-11-12 验证不成立，验证中发现的 Unicode 规范化问题已修复（见文末“后续”一节）。改动随提交 `18b1787` 合入，本记录的检查在该提交的内容上进行（提交前的最终工作区）。表单控件点击范围修复及更早的记录见 [历史快照](2026-11-10-before-defect-fixes-acceptance.md)。

本次改动了 Go 运行时代码、HTTP 接口、Web 界面、测试和文档，没有改动数据库结构（结构号仍为 2026102801）。依赖方面，Go 升到 1.27.4，前端开发依赖与 Vue 做了补丁升级（Vite 8.3.7、Vue 3.5.45、Vitest 5.1.0、Playwright 1.65.0 等，明细见计划 M2）。所有验证使用临时目录、合成媒体、本地假模型和隔离端口 18790，没有读取用户配置、媒体或数据库。

## 对外行为的变化

| 范围 | 变化 |
| --- | --- |
| 幂等请求 | 未完成预留 24 小时后过期；响应超过 512 KiB 时首次请求照常返回、同键重放为 409 `idempotency_result_unavailable`；处理函数异常记录为 500；因客户端断开而失败的请求记录为 503 `request_cancelled`（原为 500 `internal`） |
| 管理台重试 | 带键 POST 只在没有收到响应（网络错误、超时）时重试；收到 429 / 503 不再重试。GET 不变 |
| 单目录编号 | 预览响应只带前 100 项，新增 `GET /api/numberings/{id}/items` 分页读取；提交改走编号任务，有进度、可停止；删除 `POST /api/numberings/{id}/apply` 和 `POST /api/directories/{id}/numberings/tree-preview` |
| 编号任务 | 结果满 64 条时淘汰最早结束的，不再返回 `numbering_jobs_full`（错误码已删除）；未确认预览保留 24 小时，不再按 UTC 日期清理 |
| 批次 | 正常停止服务时运行中的批次记为“中断”，不再是“已取消”；“仅失败时通知”的渠道会收到 |
| 计划 | 单个计划出错不影响其他计划；调度器不再因记录失败而停止；同一错误只记录一次。保存时名称、目标分别报错，新增原因 `too_long`、`too_many` |
| 通知 | 渠道凭据读不出来时该条通知按发送失败处理，不再堵住其他渠道 |
| 配置 | `log.level`、`log.format` 显式写成空字符串时使用默认值，不再启动失败 |
| 后台扫描 | 状态新增 `error_count`，`errors` 最多列出前 100 个 |
| 运行分析 | `GET /api/runs/analytics` 不再接受从未生效的 `run`、`task`、`status` 参数 |
| 登录页 | 登录失败只在表单内提示一次 |
| 写入失败提示 | 弹窗和表单里的写操作失败时只在按钮上方的错误区提示，不再同时弹浮动通知；批量删除、还原、标记部分失败时汇总只写在弹窗内；卡片和表格行上的操作仍用浮动通知；设置页的保存失败从页面顶部移到表单底部 |
| 可点范围 | 按钮、`summary`、段落内链接不小于 24×24 CSS px |
| 弹窗焦点 | 焦点在弹窗面板本身（如图片、视频预览）时按 Shift+Tab 不再跳出弹窗 |
| 构建要求 | 从源码构建需要 Go 1.27.4 |

接口契约的完整说明见 [接口约定](../API.md) 的“幂等请求”和“编号”两节。

## 实现时做的取舍

- **未完成预留的过期（D1）。** 原设计是未完成预留永久拒绝同键请求。改为保留期内拒绝、24 小时后过期，依据是管理台的键每次操作随机生成，改名、还原和批次创建另有各自的操作日志键。原存储测试断言“48 小时后仍拒绝”，按新契约改为“保留期内拒绝、到期后释放”。
- **失败响应与重试（D7）。** 先实现了“服务端不固化 503、重试时重新执行”，实测后放弃：检测模型和测试通知在外部服务不可达时会真实重发 3 次，等待时间变为 3 倍；超过 60 秒的同步请求也会被重复执行。最终改为前端不重试已收到响应的带键 POST。
- **停止服务时的批次状态（D3）。** 没有选择“保持活动状态留给下次启动收尾”，而是停止时立即收尾为中断，这样完成时间是实际停止的时间。
- **调度器测试（D5）。** 原测试断言“记录失败时调度器返回错误”，这正是缺陷所在，替换为“调度器继续运行并在恢复后触发”。
- **批次收尾测试（D3）。** 原测试用“已停止”参数模拟用户取消；现在这个参数代表服务停止，测试改为先把状态置为 `cancelling`。
- **写入失败的提示位置（N5）。** `form-validation.spec.ts` 和 `toast-notice.spec.ts` 原先断言保存失败时出现浮动通知，按“只提示一次”的决定改为断言错误出现在表单内、没有浮动通知。
- **操作列右对齐断言。** Playwright 1.65 自带的 Chromium 158 把计算后的 `justify-content: flex-end` 报告为 `flow-end`；`delete.spec.ts` 改为直接比较最后一个按钮与单元格内容区的右边缘（相差不超过 1px），检查的仍是右对齐。
- **依赖版本。** 不采用发布不足两天的版本（Vite 8.3.8、typescript-eslint 8.72.2、rolldown 1.2.19），传递依赖用 `npm install --before` 按同一时间解析；未运行 `npm audit fix`，因为它会降级 `@vue/test-utils` 和 `glob`。

## 本次检查结果

环境：macOS / arm64，Go 1.27.4、Node.js 26.13.0、FFmpeg 9.0.2、Playwright 1.65.0（Chromium 158.0.8127.18）。以下结果均为 2026-11-11 在最终工作区上一次连续运行所得。

| 检查 | 结果 |
| --- | --- |
| Go 静态检查 | gofmt、goimports v0.50.0（`-local video-rename`）、`go vet ./...`、`go vet -tags=integration,scale ./...`、staticcheck v0.8.2 通过，无输出；`go mod verify`、`go mod tidy -diff` 通过 |
| 漏洞检查 | govulncheck：可达漏洞 0（Go 1.27.3 时为 1 条，GO-2026-6859）；模块级告警 1 条，代码未调用。`npm audit --omit=dev` 0；含开发依赖 14 条 moderate，来源与结论见计划 M1 |
| Go 测试 | `CGO_ENABLED=1 go test -race -count=1 ./...` 的 **27 个测试包**全部通过 |
| FFmpeg 集成 | `go test -tags=integration -count=1 ./internal/media ./cmd/video-rename` 通过（真实 ffprobe / FFmpeg 的横屏、竖屏 4K、SAR、极短和损坏视频，以及构建版本测试） |
| 规模测试 | `go test -tags=scale -run '^TestScale'`：`internal/library`、`job`、`app`、`runstat` 共 **12 项**通过（30 万文件索引与 1845.6 MiB 数据库、5 万文件分页、2 万文件目录树编号预览等） |
| 超大规模 | `go test -tags=integration,scale -run '^TestScaleLarge' ./internal/library` **3 项**通过：53 万真实文件 / 111 个目录的扫描（未变化重扫 14.8 秒）、50 万真实文件递归标记（用例共 175.4 秒，全部成功）、进度统计 |
| Web | Prettier、ESLint、Stylelint、vue-tsc 通过；Vitest **19 文件 / 65 项**通过；Vite 生产构建通过 |
| 完整 E2E | Playwright **195 / 195** 通过，约 9.0 分钟（含真实 FFmpeg 抽帧与本地假模型）；最终构建后没有再改代码 |
| Fuzz | 配置 YAML、ffprobe JSON、标题清理、模型响应四类各 10 秒通过，没有发现失败输入，工作区没有新增语料文件 |
| 其他 | `git diff --check` 通过 |
| 浏览器检查 | 应用内置浏览器实测登录失败提示和单目录编号的翻页、窄屏、进度；Playwright 脚本在 1440×1000 与 390×844 下逐页巡检 15 个地址，无横向溢出、控制台错误、失败请求或缺少名称的控件，Tab 焦点可见（第一轮，依赖升级前）。24×24 可点范围已改为 E2E 用例逐页检查。明细见修复计划的“浏览器检查” |

规模与基准数值来自本机合成数据，不代表 SMB / NFS 的实际吞吐。

## 新增与调整的测试

- `internal/app`：`TestIdempotencyDeliversOversizedResponseOnce`、`TestIdempotencyRecordsPanicAsFailure`、`TestIdempotencyReplaysFailures`、`TestIdempotencyExpiredReservationsFreeCapacity`、`TestLargeDirectoryNumberingPreviewIsPaged`、`TestNumberingJobsEvictOldestFinishedResults`、`TestNumberingJobsNeverEvictRunningJob`；`TestImageNumberingAPI`、`TestAudioNumberingAPI` 改走编号任务；`TestRunsAnalyticsRejectsInvalidParameters` 增加三个参数。
- `internal/storage`：`TestIdempotencyConcurrentReservationAndPendingRestart` 按新契约调整。
- `internal/job`：`TestShutdownInterruptsRunningRun`；`TestCompletionRejectsUnfinishedAndRollsBackCancellation` 调整。
- `internal/scheduler`：`TestSchedulerSurvivesUnrecordableFailure`、`TestSchedulerRunStopsOnlyWithContext`、`TestSchedulerIsolatesFailingPlan`、`TestSaveReportsEachInvalidField`，替换 `TestRunReportsUnrecordableFailure`。
- `internal/notification`：`TestUnreadableCredentialsDoNotBlockOtherChannels`、`TestFailureOnlyChannelReportsInterruptedRun`。
- `internal/config`、`cmd/video-rename`：`TestEmptyLogSettingsSelectDefaults`、`TestNewLoggerAcceptsLoadedEmptyLogSettings`。
- `internal/library`：`TestPreviewKeepsRecentPreviewsAcrossUTCMidnight`；`internal/framecache`：`TestCancelledVerificationKeepsCachedFrames`；`internal/libraryscan`：`TestScanErrorsAreBounded`。
- Web：Vitest `does not retry a keyed write that received a 503`、`ModalDialog.test.ts`“keeps Shift+Tab inside a dialog whose panel holds the focus”；E2E `login.spec.ts`、`write-feedback.spec.ts`（两项）、`accessibility.spec.ts`“可点击元素不小于 24×24”（两种宽度），以及 `numbering.spec.ts` 的“大目录预览按页读取，提交显示进度并在确认后停止”；`delete.spec.ts`、`mark-retry.spec.ts` 的部分失败用例补充“没有浮动通知”断言。

## 未验证与遗留

- 本机没有在 Linux、SMB / NFS 或其他架构上运行，也没有运行 GoReleaser 检查与快照、交叉编译。
- **CI 首轮（推送 `5fee0f9`）：** Web、E2E、GoReleaser 快照通过；Go 任务失败，原因是新增的 `TestLargeDirectoryNumberingPreviewIsPaged` 在两核机器开竞态检测时，8003 项批次的提交超过了测试等待写死的 1 分钟上限（本机不开竞态检测时整个用例 5 秒，提交 1.8 秒；开竞态检测时 118 秒，因为纯 Go 的 SQLite 驱动在竞态检测下慢约 30 倍）。修复：等待上限改为在 `go test` 自身超时前 30 秒停止并打印最后进度，防卡死与诊断不变，断言不变。本机以 `GOMAXPROCS=2` 开竞态检测运行 `internal/app` 全包通过，用时 212.6 秒。修复后的 CI 结果以 GitHub Actions 为准。
- 没有用真实的模型服务和通知渠道验证；钉钉、企业微信等只有本地假服务。
- 浏览器只用了 Chromium；没有真实触屏设备、Safari 和 Firefox。
- 基准测试本次没有运行。
- CI 与发布配置仍未逐步核对；记录但未改的现象（进度缓存的锁）和 Q1 列在 [修复计划](2026-11-10-defect-audit.md)。

## 后续：Q2 验证与 Unicode 规范化修复（2026-11-12）

**Q2 不成立。** 在本机 APFS（不区分大小写）上走完整的 `AutoCommit` 实测：`Movie.mp4` → `movie`、`Movie.MP4` → `movie`、`movie.mp4` → `MOVIE`、含中文和空格的名称，都直接改为目标名，目录中只剩一个文件，没有追加后缀。原因是名称分配器在目标命中源文件本身、且目录里没有另一个同名目录项时，视为同一文件。

**同类问题（已修复）。** 标题清洗统一为 NFC，而原名可能是 NFD（如 `e` + 组合重音符；常见于从 HFS+ 拷来的文件，影响带重音的拉丁字母、带浊音符的日文假名和韩文，汉字不受影响）。APFS 按规范化后的名称匹配，分配器的比较却只忽略大小写，于是 `Café.mp4`（NFD）自动命名为 `Café` 时变成 `Café (2).mp4`。修复：`internal/naming/conflict.go` 的 `sameDirectoryEntry` 先把两个名称归一为 NFC 再忽略大小写比较；目录中确实存在另一个同名目录项（硬链接）时仍追加后缀。实测 APFS 上 `renameatx_np(RENAME_EXCL)` 把 NFD 名改为 NFC 名可以成功。数据库结构与接口不变。

测试（修复前在 APFS 上先失败）：

- `internal/naming`：`TestAllocateAliasOfSourceKeepsTitle`（大小写、扩展名大小写、NFD 原名、NFD 原名加大小写四种），替换原来只打印日志、没有断言的 `TestAllocateUnchangedCaseOnly`。
- `internal/library`：`TestAutoCommitRenamesToCaseOrNormalizationAlias`（端到端检查目录内容），`TestAutoCommitAllocatesSuffixForNormalizationHardLink`（APFS 不能同时保存两种写法，跳过；在 Linux 上执行）。

检查结果（macOS / arm64，Go 1.27.4）：

| 检查 | 结果 |
| --- | --- |
| Go 静态检查 | 改动文件 gofmt、goimports v0.50.0 无输出；`go vet ./...`、`go vet -tags integration,scale ./...`、`GOOS=linux go vet`（naming、library）、staticcheck v0.8.2 通过；`go mod verify`、`go mod tidy -diff` 通过 |
| Go 测试 | `CGO_ENABLED=1 go test -race -count=1 ./...` 的 27 个测试包全部通过 |
| Linux | 本机 Docker `golang:1.27.3`（在代码副本中把 `go` 指令临时改为 1.27.3，仓库未改）运行上述新测试与 `TestAutoCommitAllocatesSuffixForHardLink`：全部通过，硬链接用例实际执行，没有跳过 |
| 其他 | `git diff --check` 通过 |

未运行：Web 检查与 E2E（没有改动前端）、FFmpeg 集成、规模测试、Fuzz、govulncheck（依赖未变；`golang.org/x/text` 原已是直接依赖）。没有在 SMB / NFS 上验证。编号走临时名两阶段移动，不经过这个分配器，本次没有改动。

## 后续：递归标记复用扫描暂存库并恢复耗时日志（2026-11-12）

**起因。** 用户反馈 50 万文件（分布在数千个以上的目录）递归标记约需 5–10 分钟，感觉比以前慢。排查结论：

- 标记时每刷新一个目录都会新建、删除一个临时 SQLite 扫描库，并重新捕获一次孤儿候选（自 `67e7be7` 起）。按目录数量线性增加开销。
- 10-27/28 规范整改把单条读查询改为显式读事务，逐文件复核的查询各多一次 BEGIN/COMMIT。本机 4000 目录 × 125 文件：10-07 版本（`1444962`）80.0 秒，`8e85b68` 94.4 秒。本次没有处理这一项。
- 10-02 在生产环境实测的 529654 文件为 394 秒，与反馈的量级一致；`9bfcfce` 删除完成日志后无法再从生产日志区分阶段。

**改动。** `internal/library/marks.go`：一次递归标记在第一次刷新目录时创建一个 `DirectoryScanner`，后续目录复用同一暂存库，结束（含取消）时关闭；只标记文件时不创建。结束时记录 `mark_finished`，含刷新目录、等待库锁、逐文件复核、写入四段耗时，字段见 [运行与恢复](../operations.md)。数据库结构、接口、复核规则和批次大小不变。

**本机实测**（macOS / arm64，Go 1.27.4，本地临时文件，50 万个 mp4，同日同机先后运行；临时探测用例未入库）：

| 布局 | 改动前（`8e85b68`） | 改动后 | 改动后分段（刷新 / 复核 / 写入） |
| --- | --- | --- | --- |
| 4000 目录 × 125 文件 | 94.4 秒 | 87.6 秒、89.5 秒（两次） | 43.1 / 39.0 / 4.6 秒 |
| 20000 目录 × 25 文件 | 159.9 秒 | 118.5 秒 | 68.8 / 40.8 / 6.6 秒 |

数值不代表 SMB / NFS 的实际吞吐；生产环境的阶段占比需以部署后的 `mark_finished` 日志为准。

测试（修复前在旧实现上先失败）：`internal/library` 新增 `TestProcessedMarksReuseOneScanStage`（多目录只出现一个暂存库、完成与取消后删除、日志计数与耗时字段）、`TestProcessedMarksFileTargetsSkipScanStage`（只标记文件不创建暂存库）。

| 检查 | 结果 |
| --- | --- |
| Go 静态检查 | gofmt（全部跟踪文件）、goimports v0.50.0（改动文件）无输出；`go vet ./...`、`go vet -tags integration,scale ./...`、staticcheck v0.8.2（library、app）通过 |
| Go 测试 | `go test -count=1 ./...` 全部通过；`CGO_ENABLED=1 go test -race -count=1` 的 `internal/library`、`internal/app`、`internal/libraryscan` 通过 |
| 规模测试 | `go test -tags=scale -run '^TestScale' -count=1` 的 `internal/library`、`internal/app` 通过 |
| 其他 | `git diff --check` 通过 |

未运行：全仓竞态检测（只跑了受影响的三个包）、`TestScaleLarge` 超大规模用例（改动前的 100 目录布局实测为 98.2 秒，改动后未复测）、Web 检查与 E2E（没有改动前端）、FFmpeg 集成、Fuzz、govulncheck（依赖未变）。没有在 Linux 与 SMB / NFS 上验证。

## 后续：批次详情与音频弹窗尺寸修复（2026-11-12）

**起因。** 批次记录中点“详情”打开的弹窗从 1160px 变成 650px。来源是 `eb549d9`（10-28 全仓规范整改）：弹窗样式从全局 `theme.css` 移入 `ModalDialog.vue` 的 `<style scoped>`，编译后的 `.modal.wide[data-v-…]` 优先级 (0,3,0) 高于 `run-detail.css` 的 `.modal.run-detail-dialog` (0,2,0)。修复前实测，被压住的不止宽度：批次详情标题 16px（应为 20px）、标题栏留白 19px 24px（应为 22px 28px）、关闭按钮居中（应顶端对齐）、窄屏标题栏留白 15px 20px（应为 18px）；音频弹窗宽 480px（应为 860px）。圆角、最大高度和内容留白与组件默认值优先级相同，只靠样式加载顺序碰巧正确。

**改动。** 没有退回全局样式。`ModalDialog` 的宽度、最大高度、圆角、标题栏留白与对齐、标题字号、内容留白改为读取 `--modal-*` 自定义属性，未设置时取原默认值；`run-detail.css` 与 `library.css` 改为在自己的类上设置这些属性，不再覆盖组件内部声明，因此结果与选择器优先级和加载顺序无关。约定见 [当前设计](../DESIGN.md) 的“弹窗尺寸”。其他弹窗的默认尺寸不变；数据库、接口与后端未改动。

测试（修复前先失败）：`e2e/run-detail.spec.ts` 新增“批次详情保持自己的宽度、标题与留白”，在 1440×1200 与 390×844 下检查计算后的宽度、最大高度、圆角、留白、标题字号，并用关闭按钮相对标题栏的 `offsetTop` 检查顶端对齐（Chromium 158 把 `flex-start` 报告为 `flow-start`，所以不比较该字符串；临时把对齐改回居中时该断言报 37 而非 22）。`e2e/library-lifecycle.spec.ts` 新增“音频弹窗使用自己的宽度”。

| 检查 | 结果 |
| --- | --- |
| Web | Prettier（`format:check`）、ESLint、Stylelint、vue-tsc 通过；Vitest 19 文件 / 65 项通过；Vite 生产构建通过 |
| 完整 E2E | 重新构建 Web 与 `bin/video-rename` 后，Playwright **197 / 197** 通过，约 9.0 分钟（含新增 2 项）；运行期间临时 stash 过约 2 秒工作区，因此另行重跑 `run-detail.spec.ts` 与 `library-lifecycle.spec.ts`，15 项通过 |
| 截图 | `test-results/run-detail-timeline-desktop.png` 中批次详情恢复为 1160px 宽、20px 标题 |
| 其他 | `git diff --check` 通过 |

未运行：Go 检查与测试、FFmpeg 集成、规模测试、Fuzz、govulncheck（没有改动 Go 代码与依赖）。只在本机 Playwright Chromium 158 中验证，没有在 Safari、Firefox 中实测。
