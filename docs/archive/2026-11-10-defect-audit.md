# 缺陷排查与修复计划（2026-11-10，历史）

状态：**已完成归档（历史）。** D1–D11、N1–N7 全部修复，随提交 `18b1787` 合入；维护项 M1–M3 已处理。疑点 Q1 按决定暂不验证，Q3 决定不做，Q2 已于 2026-11-12 验证（见该条）。2026-11-12 确认：D1 的 24 小时过期和 D7 的“已收到响应不重试”按推荐方案保留；删除的两个编号接口没有其他客户端调用。本文件记录的是当时的代码与结论，不作为当前行为依据；当前验证结果见 [最新验收](../ACCEPTANCE.md)。

## 怎么用这份文件

- 每项缺陷有固定编号（D1…）。修复时按“建议顺序”逐项处理，修完把总表的状态从 `待修复` 改为 `已修复`，并在该项末尾补一行“修复记录”（提交号、回归测试名）。
- 一次会话做不完时，下一次从总表第一个 `待修复` 继续即可；各项相互独立，除 D1 与 D2 建议一起改。
- “证据”一栏区分三种来源：**实测**（本次用临时测试跑出来的结果）、**走读**（只读了代码，没有运行验证）、**推测**（依据外部资料，需要目标环境确认）。走读和推测的条目，修复前先补一个能失败的测试。
- 排查只读了代码并运行了检查，没有改动任何源码；本次唯一新增的是本文件和文档索引中的一行链接。

## 排查范围与基线

基线提交 `e541899`，工作区干净。环境：macOS / arm64，Go 1.27.3，Node.js 26.13.0。

| 检查 | 结果 |
| --- | --- |
| gofmt、`go vet ./...`、`go vet -tags=integration,scale ./...`、staticcheck | 通过，无输出 |
| `go mod verify`、`go mod tidy -diff` | 通过 |
| `CGO_ENABLED=1 go test -race -count=1 ./...` | 27 个测试包全部通过 |
| govulncheck | 可达漏洞 0；模块级告警 1 条，代码未调用（与开发说明记录的 x/crypto OpenPGP 告警一致，本次未展开核对编号） |
| Web：Prettier、ESLint、Stylelint、vue-tsc、Vitest | 通过，19 文件 / 63 项 |
| `npm audit --omit=dev` | 0 |
| `npm audit`（含开发依赖） | 14 条 moderate，见 M1 |

本次**没有运行**：Playwright E2E、`-tags=integration` 媒体测试、规模测试、Fuzz、GoReleaser 快照、任何 Linux 或网络存储环境。

已通读的代码：`cmd/video-rename`、`internal/app` 的装配、路由、HTTP、幂等、播放、文件、批次、计划、标记和编号接口，`internal/auth`，`internal/storage` 的连接、事务和幂等，`internal/job` 的调度、执行、完成、暂停、并发、重试和扫描，`internal/library` 的索引核心、改名提交与恢复、删除、目录打开、播放，`internal/rename`，`internal/scheduler`，`internal/notification`，`internal/vision` 客户端，`internal/media` 抽帧，`internal/config`，`internal/model` 的发现与测试，`web/src/app` 的 API 封装、路由和入口。

**没有审查**的部分（下次可以从这里继续找问题）：

- 后端：`internal/libraryscan`、`internal/library` 的目录扫描（`directory_scan*.go`）、编号提交与恢复（`numbering_apply.go`、`numbering_recovery.go`、`numbering_snapshot.go`）、标记（`marks.go`）、处理状态与进度缓存；`internal/framecache` 的租约与淘汰逻辑（只看了启动清理）；`internal/record`、`internal/runstat`、`internal/tokenusage` 的查询；`internal/systemsetting`、`internal/prompt`、`internal/model` 的存储；`internal/app` 的模型、提示词、抽帧设置、用量和历史接口。
- 前端：所有业务页面（`web/src/features/**`）、共享组件和样式只做了关键字检索（未发现 `v-html`、`innerHTML`、`localStorage`），没有逐页审查，也没有在浏览器里验证桌面/窄屏、键盘焦点等界面行为。
- CI 与发布：只确认了 Action 按提交固定、审计门槛，没有逐步核对。

## 缺陷总表

| 编号 | 级别 | 问题 | 证据 | 状态 |
| --- | --- | --- | --- | --- |
| D1 | 高 | 未完成的幂等预留永不过期，累计 128 条后界面所有写操作永久被拒 | 实测 + 走读 | 已修复 |
| D2 | 高 | 单目录图片/音频编号预览在约 7000 个文件以上返回 500，并留下一条 D1 的永久预留 | 实测 | 已修复 |
| D3 | 中 | 正常停止服务时，运行中的批次被记为“已取消”而不是“中断” | 实测 | 已修复 |
| D4 | 中 | 目录编号任务一小时内最多 64 个，没有淘汰，之后一律拒绝 | 实测 | 已修复 |
| D5 | 中 | 计划调度器遇到一次错误且写失败记录也失败时永久退出；单个计划出错会拖住全部计划并每秒写一条失败记录 | 走读 | 已修复 |
| D6 | 低 | 同步的单目录编号提交不受 `Shutdown` 管理；耗时超过约 3 分钟时界面报超时而服务端仍在执行 | 走读 | 已修复 |
| D7 | 低 | 带幂等键的 POST 把失败响应也固化，界面对 503 和超时的自动重试只会重放同一个失败 | 走读 | 已修复 |
| D8 | 低 | 通知队列读取渠道凭据失败时不推进，后面所有渠道的通知都被堵住 | 走读 | 已修复 |
| D9 | 低 | `log.level: ""` 通过配置校验，但启动时创建日志器失败 | 实测 | 已修复 |
| D10 | 低 | 保存计划时名称或目标任一不合法，都同时返回“名称必填”和“请选择目标”两个字段错误 | 走读 | 已修复 |
| D11 | 低 | 登录失败时同一条错误同时出现在浮动通知和表单内 | 走读 | 已修复 |

级别含义：高 = 正常使用就会碰到的功能失效或不可自愈的状态；中 = 特定操作下的错误结果或功能受限；低 = 边界情况、提示或一致性问题。没有发现会导致媒体文件被覆盖、误删或越出媒体根目录的问题。

## 建议修复顺序

1. D1 + D2 一起改（同一段中间件，D2 是 D1 最容易触发的入口）。
2. D3（改动小，影响状态、通知和统计三处口径）。
3. D4。
4. D5。
5. D6–D11 按需处理。

---

## D1 未完成的幂等预留永不过期

**现象**：带 `Idempotency-Key` 的 POST 先在 `request_results` 写一条 `status=0` 的预留，业务完成后再补全。预留如果没被补全，就永远留在表里：清理语句只删 `status<>0` 的过期记录，代码里没有任何地方删除或过期 `status=0` 的记录。每条未完成预留按 512 KiB 计入 64 MiB 容量，累计到 128 条后，新的带键 POST 一律返回 409 `idempotency_capacity`。管理台的每个 POST 都带键，所以届时界面上所有创建、改名、标记、编号、批次操作都无法提交，重启也不恢复，只能手工改数据库。

**位置**：

- [internal/storage/idempotency.go:47](../../internal/storage/idempotency.go#L47) 清理只针对 `status<>0`；[:66–69](../../internal/storage/idempotency.go#L66) 容量计算。
- [internal/app/idempotency.go:99](../../internal/app/idempotency.go#L99) 响应超限时直接返回，不补全预留；[:120](../../internal/app/idempotency.go#L120) 补全失败同样留下预留。

**留下未完成预留的途径**：

1. 响应体超过 508 KiB（`IdempotencyResponseLimit-4096`）——已实测，见 D2。
2. 处理函数 panic：外层 `recoverPanic` 返回 500，但预留没有补全（走读）。
3. 进程在“预留成功”和“补全”之间被杀、断电，或平滑关闭 10 秒后仍有在途请求（走读）。
4. `CompleteIdempotency` 自身写入失败（走读）。

**证据（实测）**：对同一个键重复提交 D2 的请求，第一次 500，第二次 409 `idempotency_pending`，记录不会消失。容量上限按代码计算：`127 × 512 KiB + 512 KiB = 64 MiB` 不超限，第 129 条被拒。

**与文档的关系**：[API.md](../API.md) 写明“未完成保留项返回 409，避免再次执行不确定的文件操作”，这是有意设计；缺的是这类记录的退出途径和容量保护，文档没有说明它们会永久占用容量。

**修复方向**（实现前先定取舍）：

- 途径 1 和 2 的结果其实是确定的：响应超限和 panic 都可以把“失败”作为最终结果补全（例如固化为一个 500 响应），不应留成未完成。
- 途径 3 和 4 的结果确实不确定。可选做法：给未完成预留设置一个明显长于任何请求的过期时间（比如与已完成记录相同的 24 小时），过期后删除；或者保留记录但不再按 512 KiB 计入容量。前者更简单，但要确认“24 小时后同键请求会被当作新请求执行”可以接受——管理台的键是每次操作随机生成的，不会跨天重用。
- 同步更新 API.md 的幂等一节。

**验收标准**：

- 新增测试：响应超限、处理函数 panic 后，同键重放得到确定的失败响应，表里没有 `status=0` 的记录。
- 新增测试：人为插入超过过期时间的 `status=0` 记录，下一次预留后被清理（或不计入容量），容量不会被未完成记录永久占满。
- 现有 `internal/storage/idempotency_test.go`、`internal/app` 的幂等相关测试继续通过。

**修复记录**（提交 `18b1787`）：采用“已完成记录过期 + 结清所有可确定的结果”。未完成预留与已完成记录一样在 24 小时后过期（`internal/storage/idempotency.go`）；响应超限时首次请求照常收到完整响应，预留补全为固定的 409 `idempotency_result_unavailable`；处理函数 panic 时补全为 500 `internal`（`internal/app/idempotency.go`）。原测试“未完成预留 48 小时后仍拒绝”按新契约改为“保留期内拒绝、到期后释放”，原因写在测试注释里。回归测试：`TestIdempotencyDeliversOversizedResponseOnce`、`TestIdempotencyRecordsPanicAsFailure`、`TestIdempotencyExpiredReservationsFreeCapacity`（`internal/app`），`TestIdempotencyConcurrentReservationAndPendingRestart`（`internal/storage`）。API.md 幂等一节已重写。

## D2 大目录的单目录编号预览返回 500

**现象**：在图片或音频视图用“一键编号”预览当前目录时，`POST /api/directories/{id}/numberings/preview` 的响应包含全部文件的新旧名称。目录里有 8000 个文件时响应约 590 KiB，超过幂等中间件 508 KiB 的捕获上限，界面收到 500 `internal`，无法进入确认步骤。同名长度下大约 7000 个文件是分界线，文件名越长分界越低。[运行与恢复](../operations.md) 写的是“处理当前目录符合搜索条件的同类文件，包含全部分页”，没有数量限制。

**位置**：[internal/app/idempotency.go:47](../../internal/app/idempotency.go#L47)、[:99](../../internal/app/idempotency.go#L99)；接口在 [internal/app/numbering.go](../../internal/app/numbering.go) 的 `numberings/preview`；前端调用在 [web/src/features/library/MediaNumbering.vue:84](../../web/src/features/library/MediaNumbering.vue#L84)。

**证据（实测）**：临时测试在 `internal/app` 用 `go test -overlay` 加载，不落盘。媒体根下创建 8000 个空文件 `IMG_20240101_%06d.JPG`，`Library.Refresh` 后登录并提交预览：

| 请求 | 结果 |
| --- | --- |
| 不带 `Idempotency-Key` | 201，响应 589,968 字节 |
| 带 `Idempotency-Key` | 500 `internal` |
| 同一个键再提交一次 | 409 `idempotency_pending` |

每失败一次，还会按 D1 留下一条永久预留；服务端实际已经生成了预览批次，只是响应发不出去。

**其他可能超限的 POST**（未逐个验证）：`POST /api/directories/{id}/numberings/tree-preview`（界面已改用异步任务，接口仍在）、`POST /api/numberings/{id}/restore-preview`、`POST /api/runs/{id}/restore`、模型列表发现。修复应在中间件层面解决，而不是只改这一个接口。

**修复方向**：

- 中间件：响应超限时不应把一个成功的操作变成 500。可选做法是超限的响应照常返回给客户端、只是不保留重放内容（记录为“已完成但不可重放”，同键重放返回固定的冲突或 410 类响应）；或者像目录编号那样，让大结果走分页读取。
- 预览接口本身：响应只带总数和首页条目，其余用已有的分页模式读取（目录编号的 `GET /api/numbering/jobs/{key}/result?offset=` 已经是每页 100 条）。按项目约定直接改接口和前端调用方，不保留旧响应形态。

**验收标准**：

- 新增测试：8000 个文件的目录带键预览返回成功，随后能确认并完成编号。
- 新增 E2E 或 Vitest：前端对大目录预览能翻看全部条目。
- 同键重放的行为在 API.md 中有明确说明。

**修复记录**（提交 `18b1787`）：中间件见 D1。接口改为预览响应只带总数和前 100 项，其余用新增的 `GET /api/numberings/{id}/items?offset=` 分页读取（还原预览同样处理）；界面已不使用的同步 `tree-preview` 接口已删除。前端 `MediaNumbering.vue` 按页读取。回归测试：`TestLargeDirectoryNumberingPreviewIsPaged`（8003 个文件，带键预览 201、同键重放一致、逐页读完全部条目、经编号任务提交）；E2E `numbering.spec.ts` 在真实服务上翻到第 3 页并提交。

## D3 正常停止服务时批次被记为“已取消”

**现象**：收到 SIGINT/SIGTERM 平滑关闭时，正在运行的批次被写成 `cancelled`，未结束的条目写成“已取消”；而崩溃或强杀后重启，同样的批次是 `interrupted`。排队中尚未开始的批次在两种情况下都是重启后变 `interrupted`。结果是：

- 批次记录里“用户取消”和“服务重启打断”无法区分，`runs.error` 为空，没有中断原因。
- 通知：渠道开启“仅失败时通知”时，`cancelled` 且没有失败条目的批次不发通知，而 `interrupted` 会发“批次失败（服务中断）”。升级或重启打断的批次因此没有提醒。
- 运行分析把它们计入取消而不是中断。

[运行与恢复](../operations.md) 和 [当前设计](../DESIGN.md) 都写“前次活动批次收尾为中断”，没有区分停止方式。

**位置**：[internal/job/execute.go:174](../../internal/job/execute.go#L174) 用 `ctx.Err() != nil` 作为“已取消”的依据，[:62](../../internal/job/execute.go#L62) 同理；[internal/job/completion.go:93](../../internal/job/completion.go#L93)。服务关闭和用户取消都表现为同一个 ctx 被取消。通知筛选在 [internal/notification/worker.go:136](../../internal/notification/worker.go#L136)。

**证据（实测）**：临时测试在 `internal/job` 用 `go test -overlay` 加载。创建一个批次，等模型调用开始后取消传给 `Manager.Run` 的 ctx（等同于服务关闭，没有调用 `Cancel`），读到状态为 `cancelled`、错误为空；随后执行启动恢复 `interruptRuns`，状态仍是 `cancelled`。

**修复方向**：区分两种取消来源。用户取消已经先把状态写成 `cancelling`，`completeRun` 可以只在读到 `cancelling` 时收尾为 `cancelled`；ctx 被父级取消而状态不是 `cancelling` 时，要么收尾为 `interrupted` 并写入与 `interruptRuns` 相同的中断原因，要么保持活动状态交给下次启动的 `interruptRuns` 统一处理。后者改动更小，且两条路径共用一处逻辑；要确认关闭时已经进入文件提交阶段的条目仍然先完成记账。

**验收标准**：

- 新增测试：关闭服务后（或重启恢复后）批次为 `interrupted`，条目为取消并带中断原因；用户调用 `Cancel` 的批次仍为 `cancelled`。
- “仅失败时通知”的渠道对被关闭打断的批次入队通知。
- 现有 `TestLoopDynamicBatchCapacityAndShutdown`、`TestCancelInFlight`、`TestRestartFinalizesPendingItems` 等继续通过。

**修复记录**（提交 `18b1787`）：采用“停止时立即收尾为中断”。`completeRun` 只在状态为 `cancelling` 时收尾为 `cancelled`；ctx 被取消而不是用户取消时收尾为 `interrupted`，写入与启动恢复相同的批次原因和条目原因，并把同一次停止中已记为取消的条目和抽帧记录改为中断原因（`internal/job/completion.go`）。已保存致命错误的批次仍为 `failed`。原测试 `TestCompletionRejectsUnfinishedAndRollsBackCancellation` 直接以“已停止”参数模拟用户取消，按新语义改为先把状态置为 `cancelling`。回归测试：`TestShutdownInterruptsRunningRun`（`internal/job`）、`TestFailureOnlyChannelReportsInterruptedRun`（`internal/notification`）。

## D4 目录编号任务数量上限没有淘汰

**现象**：目录顶部的编号每次操作会在服务端登记两个任务（预览一个、提交一个）；取消请求如果先于提交请求到达，还会再登记一个占位。任务结束后保留一小时，总数达到 64 就拒绝新任务，返回 409 `numbering_jobs_full`，并且只有在新请求到来时才清理超过一小时的记录。一小时内完成 32 次目录编号后，下一次预览就会被拒，最长要等一小时或重启服务。同类的标记任务在达到上限时会淘汰最旧的已完成结果，编号任务没有。

**位置**：[internal/app/numbering_jobs.go:80](../../internal/app/numbering_jobs.go#L80)（过期清理）、[:102](../../internal/app/numbering_jobs.go#L102)（上限）、[:233](../../internal/app/numbering_jobs.go#L233)（取消占位）。对照 [internal/app/mark_jobs.go](../../internal/app/mark_jobs.go) 的淘汰循环。

**证据（实测）**：临时测试连续提交预览任务并等每个完成，第 65 个返回 409 `numbering_jobs_full`。

**修复方向**：达到上限时淘汰最旧的已完成任务；仍被引用的预览（提交时通过 `preview` 键引用）要么在提交成功后立即可回收，要么按完成时间最旧优先淘汰并让引用失效时返回已有的 `preview_stale`。取消占位可以用更短的保留时间。

**验收标准**：新增测试：连续完成 100 次“预览 + 提交”不被拒绝；运行中的任务不会被淘汰；淘汰后的预览键提交返回 `preview_stale`。

**修复记录**（提交 `18b1787`）：达到上限时淘汰最早结束的结果，运行中的任务不会被淘汰；取消占位只保留 1 分钟并同样参与淘汰；提交先读取被引用的预览再腾位置，引用已被淘汰的预览返回 `preview_stale`。不再产生 `numbering_jobs_full`，该错误码已从后端、前端文案和 API.md 删除。回归测试：`TestNumberingJobsEvictOldestFinishedResults`（100 轮预览 + 提交）、`TestNumberingJobsNeverEvictRunningJob`。

## D5 计划调度器的错误处理

**现象**（均为走读，未复现）：

1. `Scheduler.Run` 在 `Tick` 出错后写一条失败事件；如果这次写入也失败，`Run` 直接返回错误，调度器协程结束。之后服务照常运行，但所有计划不再触发，只有一条 `worker_failed` 日志，直到重启。磁盘满或数据库短暂写不进去时，`Tick` 的错误和失败事件的写入很可能一起失败。
2. `Tick` 逐个处理计划，任何一步出错（读取、解析下次时间、写入触发记录）都直接返回，后面的计划本轮不处理；出错的条件如果持续存在，每秒重试一次、每秒写一条失败事件到运行记录。

**位置**：[internal/scheduler/scheduler.go:371–372](../../internal/scheduler/scheduler.go#L371)（`Run`）；`Tick` 内多处 `return err`（约 [:293–352](../../internal/scheduler/scheduler.go#L293)）。

**修复方向**：`Run` 不因记录失败而退出，改为写服务日志后继续下一次 tick；`Tick` 对单个计划的失败记录到该计划的 `last` 并继续处理其他计划；同一错误的失败事件做去重或限频。

**验收标准**：新增测试：注入一次写入失败后调度器继续运行并在恢复后触发到期计划；一个计划出错不影响另一个计划触发；持续失败不会每秒新增一条事件。

**修复记录**（提交 `18b1787`）：`Run` 不再因记录失败而返回；`Tick` 按计划隔离失败，失败写入该计划的最近结果和失败记录，其余计划继续；同一失败持续期间只写一次日志（`schedule_failed`）和一条记录，记录写不进去时下一秒重试写入但不再重复日志。调度器构造函数新增日志参数。原测试 `TestRunReportsUnrecordableFailure` 断言“记录失败时 `Run` 返回错误”，按新契约替换为 `TestSchedulerSurvivesUnrecordableFailure` 和 `TestSchedulerRunStopsOnlyWithContext`；另有 `TestSchedulerIsolatesFailingPlan`。

## D6 同步的单目录编号提交

**现象**（走读）：`POST /api/numberings/{id}/apply` 在请求内同步执行，ctx 脱离请求取消并限时 30 分钟。

- 它没有登记到 `numberingWG`，也没有可取消的句柄。服务关闭时 HTTP 最多等 10 秒，之后 `App.Shutdown` 等完已登记的后台工作就关闭数据库，此时这个提交可能还在两阶段移动的中途。按设计重启后会走编号恢复，不会丢文件，但这与 [当前设计](../DESIGN.md) “请求发起的后台工作由 `Shutdown` 统一取消并等待”不一致。
- 前端单次请求 60 秒超时，带同一个键最多重试两次，每次重试会排队等第一次执行完。目录很大或网络存储较慢、提交超过约 3 分钟时，界面报“请求超时”，而服务端仍在继续编号，用户看不到进度和结果。

**位置**：[internal/app/numbering.go:108–111](../../internal/app/numbering.go#L108)；[web/src/features/library/MediaNumbering.vue:126](../../web/src/features/library/MediaNumbering.vue#L126)；[web/src/app/api.ts:79](../../web/src/app/api.ts#L79)；[internal/app/app.go:187](../../internal/app/app.go#L187)。

**修复方向**：单目录编号的提交改用已有的编号任务（`/api/numbering/jobs` 的 `apply` 已支持传 `batches`），前端复用 `useNumberingTask` 显示进度；随后删除同步接口。这样同时解决关闭管理和超时两个问题，也可以和 D2 的预览分页一起做。

**验收标准**：单目录编号提交有进度、可取消，关闭服务时被取消并等待；`/api/numberings/{id}/apply` 不再存在，文档同步更新。

**修复记录**（提交 `18b1787`）：单目录编号的提交改用编号任务（`action: apply` + `batches`），前端复用 `useNumberingTask` 显示进度，提交中关闭窗口需确认并取消任务；同步接口 `POST /api/numberings/{id}/apply` 已删除（现返回 404）。回归测试：`TestImageNumberingAPI`、`TestAudioNumberingAPI` 改走编号任务；E2E `numbering.spec.ts`。

## D7 失败响应被固化后的自动重试

**现象**（走读）：幂等中间件对任何状态码的响应都保存并在同键请求时重放。前端对带键 POST 在 503、网络错误和超时后用同一个键重试最多两次。

- 第一次得到 503（例如测试通知渠道不可达、模型列表超时、服务正在关闭）时，两次重试重放的都是保存下来的 503，只是让错误晚约 1.5 秒出现。
- 第一次因前端 60 秒超时被中断时，使用请求 ctx 的处理函数会以“已取消”结束并被记为 500；重试重放这个 500，界面最终显示的是一个服务端错误，而不是超时。

**位置**：[internal/app/idempotency.go](../../internal/app/idempotency.go) 保存响应处；[web/src/app/api.ts:100–113](../../web/src/app/api.ts#L100) 重试条件。

**修复方向**：先定契约再改一侧。可选：服务端不保存 503 和因请求取消产生的失败（这类请求没有产生业务结果，释放预留让同键可以重新执行）；或者前端不再对带键 POST 的 503 自动重试。前者让重试真正有用，但要确认“释放预留”只发生在确定没有执行业务动作的路径上。

**验收标准**：新增测试覆盖选定的契约（503 后同键重试会重新执行，或前端不再重试）；API.md 的幂等一节与实现一致。

**修复记录**（提交 `18b1787`）：契约定为“任何已收到的响应都是最终结果”。服务端仍固化所有状态码；前端对带键 POST 只在没有收到响应（网络错误、超时）时重试，收到 429 / 503 不再重试（`web/src/app/api.ts`）。另一方案（服务端不固化 503、重试时重新执行）实现后放弃：检测模型、测试通知在外部服务不可达时会真实重发 3 次，等待时间变为 3 倍，超过 60 秒的同步请求也会被重复执行。因客户端断开而失败的处理不再记为 500 `internal`，改为 503 `request_cancelled`，超时后的重放显示“请求超时或已中断，操作可能只完成了一部分”。回归测试：`TestIdempotencyReplaysFailures`（`internal/app`），Vitest `does not retry a keyed write that received a 503`。

## D8 通知队列被读取失败堵住

**现象**（走读）：`tick` 每次取最早一条到期的待发送通知，再读取该渠道的凭据。读取失败（凭据无法解密或内容损坏）时直接返回错误，这条通知的状态和下次尝试时间都没变，3 秒后又取到它。它后面其他渠道的通知永远轮不到。

**位置**：[internal/notification/worker.go:81–84](../../internal/notification/worker.go#L81)。

**修复方向**：读取凭据失败按一次发送失败处理——记录错误、增加尝试次数、推后下次尝试时间，三次后标记失败。

**验收标准**：新增测试：一个渠道凭据损坏时，另一个渠道的通知仍能送达；损坏渠道的通知最终标记失败并带原因。

**修复记录**（提交 `18b1787`）：读取凭据失败（无法解密或内容损坏）按一次发送失败处理：增加尝试次数、推后下次尝试、三次后标记失败并保留原因；数据库读取错误仍直接返回。回归测试：`TestUnreadableCredentialsDoNotBlockOtherChannels`。

## D9 空日志级别

**现象**：配置里写 `log: { level: "" }` 时，校验认为“空值表示 info”而放行，随后创建日志器失败，进程以 `video-rename: log: parse log level: slog: level string "": unknown name` 退出。不写该字段时使用默认值 `info`，不受影响。

**位置**：[internal/config/service.go:128](../../internal/config/service.go#L128)；[cmd/video-rename/log.go:15](../../cmd/video-rename/log.go#L15)。

**证据（实测）**：`slog.Level.UnmarshalText([]byte(""))` 返回 `unknown name`。

**修复方向**：二选一并保持一致——校验拒绝空字符串（报 `log.level` 字段错误），或在解析后把空值归一为 `info`、`json`。`log.format` 的空值目前在 `newLogger` 里落到 JSON 分支，行为正常，但校验注释同样写了“空值表示 json”，一并统一。

**验收标准**：新增配置测试覆盖显式空值的行为；错误信息带字段路径。

**修复记录**（提交 `18b1787`）：解码后把显式空的 `log.level`、`log.format` 归一为默认值 `info`、`json`，校验注释与 `newLogger` 注释同步；无效值仍报 `log.level` 字段错误。回归测试：`TestEmptyLogSettingsSelectDefaults`（`internal/config`）、`TestNewLoggerAcceptsLoadedEmptyLogSettings`（`cmd/video-rename`）。docs/configuration.md 已说明。

## D10 计划保存的字段错误不准确

**现象**（走读）：名称为空、名称超过 80 字、没有目标、目标超过 100 个，这四种情况返回的都是同一组两个字段错误：`name: required` 和 `targets: selection_required`。只是目标没选时，名称输入框也会被标成必填；名称过长时提示的是“必填”。

**位置**：[internal/scheduler/scheduler.go:150](../../internal/scheduler/scheduler.go#L150)。

**修复方向**：按实际不合法的字段分别返回，名称过长和目标过多使用各自的原因代码；前端 `server-codes.ts` 补对应文案。

**验收标准**：新增测试：四种输入各自只返回对应字段和原因。

**修复记录**（提交 `18b1787`）：名称和目标分别校验，各自返回对应字段和原因（`required` / `too_long`，`selection_required` / `too_many`）；前端补 `name:too_long`、`targets:too_many` 文案。回归测试：`TestSaveReportsEachInvalidField`。

## D11 登录失败提示重复

**现象**（走读，未在浏览器里确认）：登录页通过 `useApi` 发请求，写请求失败时 `useApi` 会弹出浮动通知；登录页自己又把同一段文字显示在表单里的 `role="alert"` 区域。用户名或密码错误时，同一句话出现两次，读屏也会播报两次。

**位置**：[web/src/app/use-api.ts:54](../../web/src/app/use-api.ts#L54)；[web/src/features/auth/LoginPage.vue:29](../../web/src/features/auth/LoginPage.vue#L29)。

**修复方向**：先在浏览器里确认现象，再决定保留哪一处；登录页保留表单内提示更合适，可以让该请求不触发浮动通知。

**验收标准**：E2E：错误密码登录后页面上只有一处错误提示。

**修复记录**（提交 `18b1787`）：修复前先加 E2E 复现（同一句“账号或密码错误”出现两次）。登录页的请求改为 `feedback: 'inline'`，在表单内提示，不再弹浮动通知。回归测试：E2E `login.spec.ts`。其他写操作弹窗的同类现象在第二轮按 N5 统一处理。

---

## 补充排查（2026-11-10，修复 D1–D11 时进行）

针对上文“没有审查的部分”补做。来源标注方式同前。

### 新发现并已修复

| 编号 | 级别 | 问题 | 证据 | 状态 |
| --- | --- | --- | --- | --- |
| N1 | 低 | 未确认的编号预览按 UTC 日期清理：UTC 零点（北京时间 08:00）之后保存任何预览，都会删掉零点之前的全部预览，包括同一次目录树预览里较早的目录，随后确认编号时报“记录不存在” | 实测 | 已修复 |
| N2 | 低 | 后台扫描把每个读取失败的目录都留在状态里，没有上限；失败目录很多时，每次状态轮询都返回全部条目 | 走读 + 实测 | 已修复 |
| N3 | 低 | 截图缓存复用时，校验过程被取消（批次暂停、取消或停止服务）会被当成校验失败：有效的截图被删除并立即重新抽帧 | 实测 | 已修复 |
| N4 | 低 | `GET /api/runs/analytics` 接受 `run`、`task`、`status` 三个筛选参数但完全不使用，返回的是未筛选的结果；API.md 规定未知查询字段返回 400 | 走读 + 实测 | 已修复 |

- **N1**：[internal/library/numbering_preview.go](../../internal/library/numbering_preview.go) 的清理条件是 `created < 当天日期`。改为保留 24 小时。没有文件被改动的风险，只是确认失败。回归测试 `TestPreviewKeepsRecentPreviewsAcrossUTCMidnight`（修复前失败）。
- **N2**：[internal/libraryscan/manager.go](../../internal/libraryscan/manager.go)。状态新增 `error_count`（全部失败数），`errors` 最多列出前 100 个，界面显示总数并注明只列出前 100 个。回归测试 `TestScanErrorsAreBounded`。
- **N3**：[internal/framecache/cache.go](../../internal/framecache/cache.go) 的 `Acquire`。校验因调用方取消而中断时直接返回取消，不删除缓存、不重新抽帧。回归测试 `TestCancelledVerificationKeepsCachedFrames`（修复前会重新抽帧）。
- **N4**：[internal/app/runstats.go](../../internal/app/runstats.go) 不再接受这三个参数；界面本来就不发送。回归测试补在 `TestRunsAnalyticsRejectsInvalidParameters`。

### 看过、没有发现问题

- 后端通读：`internal/library` 的目录扫描（`directory_scan.go`）、编号预览 / 提交 / 恢复 / 快照 / 命名 / 诊断、标记、处理状态与进度缓存；`internal/libraryscan`；`internal/framecache` 的租约与淘汰；`internal/record` 的历史与抽帧查询；`internal/systemsetting`、`internal/prompt` 的存储；`internal/model` 的复制、删除与模型列表发现；`internal/app` 的模型、提示词、抽帧设置、系统设置、用量、运行分析、历史、抽帧记录、通知和后台扫描接口。
- 前端通读：`app/api.ts`、`app/use-api.ts`、登录页、计划弹窗与表单、通知设置、模型配置、单目录与目录编号、编号任务；文件目录页的列表加载与状态轮询、批次页的筛选与加载。其余业务页面只做了模式检查：定时器、事件监听和观察器都有成对清理，没有直接调用 `fetch`、`toLocale*`、本地存储或 `v-html`。

### 仍然没有审查

- 第二轮（见下文）已读完上一版列出的其余后端与前端代码。
- CI 与发布配置仍未逐步核对。

### 浏览器检查

在隔离的 E2E 测试服务（临时目录、合成媒体、端口 18790）上进行：

- 应用内置浏览器：错误密码登录后页面上只有一处“账号或密码错误”（`role="alert"`），没有浮动通知，焦点留在密码框；单目录编号预览 205 项，翻到第 2、3 页内容正确；375 宽度下弹窗不溢出，按钮在视口内；确认编号后依次显示“当前目录 27 / 205”“147 / 205”等进度，完成后出现“已编号 205 张图片”并列出编号记录。
- Playwright 脚本逐页巡检（1440×1000 与 390×844，共 15 个地址：文件目录及其图片视图、运行记录、抽帧记录、计划、批次、用量两页、系统设置 7 个分类）：全部没有横向溢出、控制台错误、失败请求、缺少可访问名称的控件或页面级错误提示；每页连按 12 次 Tab，所有落点都有可见焦点样式。
- 没有覆盖：各页面内的弹窗和二级交互（依靠已有 E2E）、真实触屏设备、Safari 和 Firefox。

### 记录但没有改的现象

- 第一轮记录的“写操作失败时提示重复”和“部分可点元素小于 24×24”已按决定在第二轮处理，见 N5、N6。
- **进度缓存读取时持有互斥锁遍历查询结果**（`internal/library/progress_cache.go`）。只影响并发读取进度时的等待，不影响正确性，未改。

## 第二轮（2026-11-11，按决定处理）

决定：写入失败只提示一次（方案 A）；小于 24×24 的可点元素按规范放大；不加可信代理配置（Q3）；NFS 暂不验证（Q1）；更新依赖；读完剩余代码。

| 编号 | 级别 | 问题 | 证据 | 状态 |
| --- | --- | --- | --- | --- |
| N5 | 低 | 写操作失败时同一句错误既弹浮动通知、又显示在弹窗或表单内（D11 的同类问题，遍及计划、通知、模型、提示词、设置、编号、删除、改名等） | 走读 + 实测 | 已修复 |
| N6 | 低 | 处理设置的“说明”按钮 19×19、表头排序按钮和面包屑高 20、“重置”44×20、段落内文字链接高 15，低于 24×24 | 实测 | 已修复 |
| N7 | 低 | 图片/视频预览等焦点停在弹窗面板本身时，按 Shift+Tab 会跳出弹窗，落到背后的页面 | 实测 | 已修复 |

- **N5**：`useApi` 的每次请求新增 `feedback: 'inline' | 'toast'`。弹窗和表单里的写操作改为 `inline`，错误只写在按钮上方的错误区；设置页把保存失败从页面顶部（只用于读取失败）移到表单底部。卡片和表格行上没有错误区的操作仍用浮动通知。批量删除、批量还原、批量标记部分失败时，原来同时弹出汇总通知，现在只在弹窗内汇总。规则写入 [当前设计](../DESIGN.md)“提示与图表配色”。回归测试：`e2e/write-feedback.spec.ts` 两项（修复前失败），`delete.spec.ts`“批量删除部分失败…”与 `mark-retry.spec.ts`“终态部分失败…”补充“没有浮动通知”断言（修复前失败）；`form-validation.spec.ts`、`toast-notice.spec.ts` 原先断言失败时出现浮动通知，按新规则改为断言弹窗内提示。
- **N6**：`theme.css` 给按钮设最小宽高 `--control-hit-area`（24px），给 `summary` 和段落内链接补上下内边距；勾选框和开关输入框不在此列。回归测试 `accessibility.spec.ts`“可点击元素不小于 24×24”（1440px、390px，逐页）；临时去掉下限时该用例报出 33 个过小元素。
- **N7**：[web/src/components/ModalDialog.vue](../../web/src/components/ModalDialog.vue) 的焦点循环把“焦点在面板本身”视同在第一个控件上，并把链接计入可聚焦元素。回归测试 `ModalDialog.test.ts`“keeps Shift+Tab inside a dialog whose panel holds the focus”（修复前失败）。

第二轮读完的代码：`internal/library/directory_scan_diagnostics.go`、`internal/tokenusage/analytics.go` 全部；前端 `lib/*`、`app/use-toast.ts`、`use-route-state.ts`、`form-errors.ts`，用量页与四个图表，批次详情与实时记录，视频、音频、图片播放器，媒体预览、选择、吸顶表头、标记，目录选择器、扫描设置与扫描范围，`SelectField`、`DateField`、`PageNavigation`、`ToastNotice`、`DateTime`，文件目录的各个弹窗模板。除 N5（批量部分失败）和 N7 外没有发现新问题。

---

## 需要在目标环境确认的疑点

这些不是已确认的缺陷，修复前先验证。

- **Q1 Linux 上 NFS 挂载的媒体目录可能完全无法改名（推测；2026-11-11 决定暂不验证）。** 改名使用 `renameat2(RENAME_NOREPLACE)`。据我对内核源码的了解，NFS 客户端的 rename 对任何标志都返回 `EINVAL`，程序会把它映射为 `rename_unsupported`，于是手动改名、自动命名和编号都会失败。README 写“媒体目录可以是 SMB/NFS 挂载”。本机没有 Linux 和 NFS，无法验证；CIFS/SMB 据我所知支持该标志。需要在 Linux + NFS 上实测一次改名；如果确认，要么在 README 里明确 NFS 不支持改名，要么设计一个等价安全的替代方案。
- **Q2 仅大小写不同的自动命名。** 在 macOS 默认的 APFS 上实测，`renameatx_np(RENAME_EXCL)` 把 `Movie.MP4` 改成 `movie.mp4` 是成功的，手动改名不受影响。但自动命名的名称分配器会先检查目标是否存在；在不区分大小写的文件系统上，新标题与原名只差大小写时可能被当作冲突而追加后缀。未读分配器代码，未验证。

  **2026-11-12 验证：不成立。** 分配器在目标命中源文件本身且不是另一个目录项时不加后缀，APFS 上只差大小写的自动命名直接改名。验证时发现同类问题：标题统一为 NFC，原名为 NFD（如 `e` + 组合重音符）时，因比较未做 Unicode 规范化而被追加 “ (2)”；已修复，见 [最新验收](../ACCEPTANCE.md)。
- **Q3 登录限速可被用来挡住管理员。** 失败计数按来源 IP 每分钟 10 次、全局每分钟 100 次。反向代理后面所有来源都是代理的 IP，任何能访问登录页的人每分钟 10 次错误密码就能让管理员无法登录（已有会话不受影响）。这是不信任 `X-Forwarded-*` 的取舍，不算缺陷；是否需要“可信代理”配置由你决定。2026-11-11 决定：不需要，保持现状。

## 维护项

- **M1 开发依赖审计告警（已核对，保留）。** `npm audit` 报 14 条 moderate，来自 `minimatch`（GHSA-g3hv-75wg-m249，11.0.1 修复）和 `glob`（GHSA-3g3m-2fjr-6pw4，14.0.2 修复），经由 typescript-eslint、ESLint、`@vue/test-utils` 引入，只影响开发和 CI 工具链，不进入发布的二进制；`npm audit --omit=dev` 为 0。ESLint 10.14.0 仍依赖 minimatch 10.x，上游没有修复版本。实测 `npm audit fix` 会把 `@vue/test-utils` 从 2.5.1 降到 2.4.11、`glob` 从 13 降到 10，只少一条告警，未采用；结论记入 [开发说明](../IMPLEMENTATION.md)。
- **M2 可用的补丁更新。** `npm outdated` 显示 Vite 8.3.3 → 8.3.7、Vue 3.5.43 → 3.5.45、vue-tsc 3.3.12 → 3.3.14、Vitest 5.0.5 → 5.1.0、Playwright 1.63 → 1.65、typescript-eslint 8.72.0 → 8.72.2 等。未核对各版本的变更说明和漏洞修复内容。跨主版本的（TypeScript 7、vue-router 5、Stylelint 18、jsdom 31）按开发说明的既有结论暂不升级。

  **2026-11-11 已处理。** 核对变更说明与安全公告后升级：Vite 8.3.3 → 8.3.7（8.3.0–8.3.6 受严重公告 GHSA-p4f5-76q4-ffq7 影响，开发服务器对外监听时可远程执行代码；8.3.8 发布不足一天，未采用）、Vue 3.5.45、vue-tsc 3.3.14、Vitest 5.1.0、plugin-vue 6.0.10、Playwright 1.65.0（Chromium 158）、ESLint 10.14.0、typescript-eslint 8.72.1（8.72.2 发布不足两天）、eslint-plugin-vue 10.12.0、axe-core/playwright 4.14.0、@types/node 26.6.6；传递依赖用 `npm install --before=2026-11-10` 解析（rolldown 1.2.18，未取两小时前发布的 1.2.19）。Go 从 1.27.3 升到 **1.27.4**：govulncheck 报告 1.27.3 的 net/http 可达漏洞 GO-2026-6859（`MaxBytesReader` 竞态导致 HTTP/1 服务端崩溃），升级后可达漏洞为 0；README、设计与开发说明的版本号同步更新。TypeScript 7 重新实测仍不可用（vue-tsc 3.3.14 无法加载，typescript-eslint 8.72 不支持），维持 6.0.3。Playwright 1.65 的 Chromium 158 把计算后的 `justify-content: flex-end` 报告为 `flow-end`，`delete.spec.ts` 的操作列右对齐断言因此改为直接比较按钮与单元格右边缘的位置。
- **M3 本次未跑的检查（已完成）。** 完整回归结果见 [最新验收](../ACCEPTANCE.md)。

## 排查中确认没有问题的点

记录下来避免重复检查：

- 改名、删除、播放都逐层核对目录身份并拒绝符号链接；改名用内核的“不替换”语义，先写操作日志再移动，移动后和提交前各复核一次。
- FFmpeg 和 ffprobe 以参数数组启动，输入路径是媒体根下的绝对路径，不经过 shell。
- 会话令牌只存 SHA-256，Cookie 为 `HttpOnly` + `SameSite=Strict`，写请求经跨站校验；响应带 CSP、`nosniff` 和禁止内嵌。
- 模型和通知的 HTTP 客户端都设了超时并禁用自动重定向；通知错误信息不包含带凭据的 URL。
- 钉钉加签的算法与我了解的官方说明一致（未对真实服务验证）。
- macOS 上仅大小写变化的手动改名可以成功（见 Q2）。
