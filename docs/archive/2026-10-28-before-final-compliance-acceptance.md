# 历史快照：最终整改前的规范复查

状态：已归档。本记录保留当时发现的六类待整改差异；当前实现与验证结果以 [最新验收](../ACCEPTANCE.md) 为准，不以此处的旧接口、结构号或测试结果判断当前行为。

# 最新验收：全仓规范复查（2026-10-28）

## 结论与范围

**当前不能认定整个项目已经完全符合规范。** 本次完成全仓检查，修复了确认的旧分页分支、数据库结构识别、请求生命周期和错误处理问题；下文单列仍然存在的规范差异，没有把它们改写为批准的例外。此前验收已移至 [历史快照](2026-10-28-before-project-audit-acceptance.md)，不再用旧的“完成整改”表述证明当前合规。

检查覆盖根目录归属、Go / Web / Vue 规范、依赖与构建配置、CI、后端全部包及前端源码/测试；对入口、持久化、文件安全、模型/通知出站请求、接口与管理台调用链进行了人工核对，并运行全仓原生静态检查和回归。静态检查通过并不证明每条人工评审规则均已满足。

开始时工作区已有 `Go代码规范.md` 的用户修改；本次保留原样，并以 SHA-256 复核。没有修改规范来迁就实现，也未创建提交、推送或发布。所有数据库、文件、模型及通知验证使用临时目录、合成媒体、本地假服务与隔离端口，不读取用户媒体或运行配置。

## 本次已修复

| 问题 | 实际修改与证据 |
| --- | --- |
| 目录、批次、通知历史、运行记录和抽帧历史仍保留多套分页入口 | 删除 `Library.ListOrdered`、`Manager.List`、`Service.History` 及 HTTP 中按参数切换旧实现的分支。五类列表统一页码响应，缺省页为 1，旧 `next` 字段删除；未知/重复参数返回 400。管理台、播放器队列、类型与隔离测试同时更新，契约见 [API](../API.md#列表分页)。修改前 `pagination-before.log` 复现旧入口仍被接受、响应缺页码等问题。 |
| 播放器队列依赖旧目录游标 | 改为页码读取；跨页检查 generation，变化后拒绝拼接不一致的列表。新增 201 首合成音频条目的跨页/版本变化单元测试。 |
| 数据库仅看 user_version，同号但缺表、缺索引或多字段仍能打开 | 只读检查实际结构目录，并与从唯一 `schema.sql` 创建的参考结构比较；不迁移、不补列、不重建。同时修正 `LIKE 'sqlite_%'` 把用户表 `sqlitex` 误当内部表的问题。四类同版本坏结构和零版本无关数据库在修改前被接受，修改后拒绝且原数据库字节保持不变，见 `schema-before.log`、`schema-prefix-before.log` 与存储测试。 |
| 页面卸载、KeepAlive 停用和筛选切换未统一取消请求 | `useApi` 将页面请求绑定作用域/活跃生命周期，继续调用现有请求包装；目录、历史、批次、通知与目录选择器取消被替代请求。关闭条目历史时取消读取，计划预览取消前一次请求。保留原请求序号防止迟到结果覆盖。已提交后台任务仍由其服务生命周期管理。 |
| 模型响应读取结束与取消竞争，可能误报解析失败或在取消后返回成功 | 全量竞态回归发现 `TestResponseBodyCancellationDoesNotRetry` 失败；受控传输进一步稳定复现空响应和有效响应两种情形。`vision.Client.doOnce` 在读取结束后检查调用方 context，按取消终止且不重试。保留真实 HTTP 取消回归，新增受控 EOF 用例，50 次竞态复验通过；见 `model-cancel-before.log`、`model-cancel-after.log`。 |
| 记录查询丢弃结果集关闭错误 | `internal/record` 的历史、抽帧、批次明细及重试查询使用 `CollectRows` 或合并关闭错误，不把失败结果当成功返回。 |
| 结果记录编码失败后变成空字符串 | 用非有限数值、不支持的值和非 UTF-8 文本复现；现在保存带原代码和编码诊断的 `internal` 记录，避免失败变成“无错误”。新增 `internal/domain/record_test.go`，修改前/后证据为 `record-before.log`、`record-after.log`。 |
| 成功记录的空诊断被序列化为 `{}`，界面会将其当成未知失败 | 运行明细、最近结束和历史列表只在诊断非空时输出 `message` / `error`。新增 `outcome_contract_test.go` 同时验证无诊断时字段省略、有诊断时原代码保留，证据为 `outcome-before.log` 与 `outcome-after.log`。 |
| 部分浏览器夹具仍发送旧错误字符串或旧提示字段 | 通知、编号历史、标记失败和历史列表改用当前结构化记录，成功时省略诊断；清除无效的旧提示字段。通知用例增加成功记录显示“—”的断言。运行记录辅助生成器中的字符串仅是夹具输入，输出一直为当前记录对象，未把它误当成生产兼容入口。 |
| 接口仍接受未约定的日期及目录枚举 | 历史筛选只接收 RFC 3339 时间，删除 DateOnly 解析分支；目录 kind/sort 在边界拒绝未知枚举。旧成功断言改为新契约对应的成功/拒绝用例，时区与跨天筛选验证保留。 |
| 浏览器文档和构建目标低于实际 Web API 要求 | 同步 Vite 与中英文 README 为 Chrome/Edge 124、Firefox 124、Safari 17.4 起；使用原生 AbortSignal API，不增加旧浏览器补丁。依据链接保存在 [当前设计](../DESIGN.md)。 |

OpenAI 兼容协议、主模型失败后的备用模型、首次使用的默认设置、平台文件操作实现、崩溃后恢复和实体版本检查是当前功能，不是旧项目版本的兼容入口。`/entries/{id}/history` 的 before 与编号结果 offset 各为其接口的现行定位方式，没有并行旧入口。本次未发现仍在运行的旧配置迁移、旧数据库迁移或替代上述五类分页的旧实现。

## 仍存在的规范差异

以下为当前源码可定位的事实，尚未整改；不能因 lint/test 通过而标为完成。它们需要后续整体对齐，未自动登记长期例外。

| 优先级 | 规则与当前差异 | 位置与影响 |
| --- | --- | --- |
| P2 | Vue §5：筛选、分页等可分享状态应放 URL query | [run-filters.ts](../../web/src/features/runs/run-filters.ts)、[HistoryPage.vue](../../web/src/features/history/HistoryPage.vue)、[UsageTokens.vue](../../web/src/features/usage/UsageTokens.vue)、[UsageAnalysis.vue](../../web/src/features/usage/UsageAnalysis.vue) 仍主要使用本地 ref；目录页只把目录 ID 放路由。刷新/复制地址不能还原完整筛选和页码。 |
| P2 | Web §6：400 字段错误应显示在对应控件旁；写操作应统一反馈 | [NumberingSettings.vue](../../web/src/features/settings/NumberingSettings.vue)、[ProcessingSettings.vue](../../web/src/features/settings/ProcessingSettings.vue)、[ModelsPage.vue](../../web/src/features/models/ModelsPage.vue) 等仍主要把 errorText 放在页面/弹窗总提示中，未按 details.fields 绑定各输入项；模型保存成功路径也没有 toast。 |
| P2 | Go §3：HTTP 错误码由 HTTP 层映射 | [domain/errors.go](../../internal/domain/errors.go) 的 `CodeInvalidArgument` / `InvalidField` 直接构造 `invalid_argument`，多个领域包使用；当前 HTTP 层直接复用 code，领域诊断与接口错误码仍耦合。 |
| P2 | 通用 §5.2：可选字段省略、大小字段名带单位 | [library.go](../../internal/library/library.go) 的可选 `Entry.Contents` 没有 omitzero，会返回 contents:null；字节数仍叫 size，客户端契约同名。需要同时更新接口与所有客户端，不能只改一侧。 |
| P3 | Go §2：超过三个参数（不计 ctx/接收者）改参数结构体 | AST 扫描确认 17 个测试辅助函数仍超限，例如 `markRequest`、`scanTestCandidate`、`seedUsage`；生产函数声明未检出此类超限。定位见下表，扫描原始记录为本地 `go-parameter-findings.txt`。 |
| P3 | Web §8：设计变量集中定义 | 颜色已有变量与 Stylelint 约束，但多个组件的字号、间距和圆角仍直接写 px，例如 [FailureDiagnostics.vue](../../web/src/features/history/FailureDiagnostics.vue) 和 [NumberingSettings.vue](../../web/src/features/settings/NumberingSettings.vue)；当前自动检查主要约束颜色，不能证明所有设计变量已统一。 |

较长函数/文件本身是评审信号，不是硬性行数违规。SQL 分布在各领域仓储也不等于应集中到一个全局包。README 已登记的中文业务数据、数字模板插值、运行期设置和最终事务内文件身份核对等例外继续有效。

<details>
<summary>测试辅助函数参数超限定位</summary>

| 文件 | 函数及参数数目（不计 ctx / 接收者） |
| --- | --- |
| [mark_jobs_test.go](../../internal/app/mark_jobs_test.go) | markRequest：6 |
| [numbering_jobs_test.go](../../internal/app/numbering_jobs_test.go) | numberingRequestTest：4 |
| [batch_limits_test.go](../../internal/job/batch_limits_test.go) | batchTargets：4 |
| [directory_scan_test.go](../../internal/library/directory_scan_test.go) | scanTestCandidate：5；assertScanIndexCounts：5 |
| [library_test.go](../../internal/library/library_test.go) | entry：4 |
| [processing_status_test.go](../../internal/library/processing_status_test.go) | setTestProcessingSource：5 |
| [refresh_parallel_test.go](../../internal/library/refresh_parallel_test.go) | assertFailedRefreshPreservedGeneration：4 |
| [libraryscan/manager_test.go](../../internal/libraryscan/manager_test.go) | entryID：4 |
| [extract_integration_test.go](../../internal/media/extract_integration_test.go) | genVideo：5 |
| [extract_test.go](../../internal/media/extract_test.go) | makeJPEG：5；fakeFFmpeg：4 |
| [model_test.go](../../internal/model/model_test.go) | newPrompt：4 |
| [notification_test.go](../../internal/notification/notification_test.go) | addRun：4 |
| [analytics_test.go](../../internal/tokenusage/analytics_test.go) | seedUsage：9 |
| [tokenusage/store_test.go](../../internal/tokenusage/store_test.go) | seedRun：4；seedItem：4 |

</details>

## 验证记录

环境：macOS / arm64，Go 1.27.3、Node.js 26.12.1、npm 11.20.0、FFmpeg 9.0.2。日志位于被忽略的 `.cache/project-audit-20261028/`，完整原生命令见 [开发与测试](../IMPLEMENTATION.md)。

| 检查 | 实际结果与本地证据 |
| --- | --- |
| 格式与依赖一致性 | 全仓 gofmt / goimports、Web Prettier 通过；`go mod verify`、`go mod tidy -diff` 通过且无依赖变更。`gofmt-complete.log`、`goimports-complete.log`、`web-format-delivery.log`、`go-mod-*-final.log`。 |
| 前端静态检查、单测与构建 | ESLint、Stylelint、vue-tsc 通过；Vitest **17 文件 / 58 项**通过；Vite 生产构建通过。`web-lint-delivery.log`、`web-typecheck-delivery.log`、`web-test-final.log`、`web-build-final.log`。 |
| 后端静态检查 | 8 个发布目标的 vet、`go vet -tags=integration,scale ./...`、staticcheck 通过。`cross-checks-complete.log`、`go-vet-tags-complete.log`、`go-staticcheck-complete.log`。 |
| 全量 Go 竞态回归 | `CGO_ENABLED=1 go test -p=2 -race -count=1 ./...` 最终通过，**27 个有测试的包**通过，pipeline / web 两包无测试文件。`go-race-complete.log`。中途发现的模型取消竞态已修复，并单独运行相关用例 50 次；未将失败轮次算作通过。 |
| FFmpeg 与版本集成 | `go test -tags=integration -count=1 ./internal/media ./cmd/video-rename` 通过，使用真实 FFmpeg、合成输入和临时 Git 仓库。`go-integration.log`。 |
| 浏览器回归 | 最终 Playwright **183 / 183** 通过，耗时约 **10.2 分钟**。覆盖桌面/窄屏、长文本、加载/空/错误状态、焦点、取消、跨页选择及滚动恢复。`web-e2e-delivery.log`；桌面/窄屏分页与通知截图已人工复核，保存在 `web/test-results/`。 |
| 输入 Fuzz | ConfigurationYAML、ProbeJSON、CleanTitle、ModelResponse 四类各以 `-fuzztime=10s -parallel=2` 通过。`fuzz-config.log`、`fuzz-media.log`、`fuzz-naming.log`、`fuzz-vision.log`。 |
| 目录分页规模 | 创建 **5,001 个直接子目录及 100,000 个临时文件**（5,000 目录各 10 文件，加一个含 50,000 文件的目录）。根索引约 491 ms，大目录索引约 5.96 s；100 行分页 30 次的 P95 **17.56 ms**；第 1 / 250 / 500 页约 **26.53 / 53.10 / 77.97 ms**，总数、顺序及末页正确。`scale-pagination.log`。 |
| 历史筛选规模 | **11,000 条**记录；主模型 10,400 条、备用模型 600 条、未知来源 0 条的首末页、页码裁剪和精确条目均通过；所测请求约 3.57–70.55 ms。`scale-history.log`。 |
| 构建与发布配置 | 当前源码原生构建通过；linux amd64 / arm64 / ppc64le / s390x / riscv64 / loong64、darwin amd64 / arm64 的 CGO=0 构建通过。GoReleaser v2.18.3 `check` 通过；本次未重跑快照归档或发布。`cross-checks-complete.log`、`goreleaser-check.log`。 |
| 漏洞检查 | npm 生产依赖审计 **0**；govulncheck 可达符号 **0**、已导入包 **0**，依赖模块有 **1** 条 `GO-2026-5932`（未使用的 x/crypto/OpenPGP），不将其描述为“依赖树零告警”。`web-audit.log`、`go-vuln.log`。 |

修改过的文档本地链接目标均存在；`git diff --check` 通过。`Go代码规范.md` 的 SHA-256 与任务开始时一致。

## 验证边界与成本

- 没有使用用户数据库或媒体；没有执行数据库转换，也没有自动删除不支持的数据。
- 交叉 vet/编译只证明目标可检查和构建，不证明 Linux、NAS 或其他 CPU 架构实机运行。
- 浏览器测试为本机 Chromium；声明的 Firefox / Safari 最低版本依据官方兼容数据，本次未在这些浏览器实测。
- 页码分页满足跳页和总数，但深 OFFSET 的成本随条目数增长；本地合成数据测量不能代表网络文件系统。
- 本轮只复测受分页改动影响的目录与历史规模场景，未重跑全部规模用例或 50 万 / 53 万真实临时文件场景；短时 Fuzz 也不等于穷尽输入空间。
- 本次不升级依赖。govulncheck 的 x/crypto/OpenPGP 模块告警与可达代码结果区分记录；项目未导入 OpenPGP。cron 的维护停滞和 TypeScript 配套工具约束仍见开发说明。
