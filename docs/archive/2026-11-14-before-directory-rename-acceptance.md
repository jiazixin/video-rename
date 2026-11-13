# 历史验收：处理标记优化、并发补验与默认端口调整（2026-11-13）

> 历史快照：本记录已于 2026-11-14 归档，仅供追溯。当前实现与验证范围见 [最新验收](../ACCEPTANCE.md)。

## 范围与实现

性能优化基线为 `d83185c`，完成于 `66d0c85`，改动为 Go 的扫描暂存、标记复核、相关测试和文档。后续补充并发交错测试、将默认端口改为 8080 并同步 Vite 开发代理与文档，各批验证范围见对应章节；数据库结构、依赖、接口和页面源码未变。此前缺陷修复与扫描库复用的验收已移入 [历史快照](2026-11-13-before-mark-performance-acceptance.md)，不能将旧通过记录当作本次验证。

- 可重建扫描暂存库由 WAL/FULL 改为 WAL/NORMAL；业务主库保持 WAL/FULL。暂存库关闭后删除，重启不读取它恢复业务状态。
- 每批标记先执行任务占用回调，再在一个短读快照中检查待恢复操作、读取文件和公共祖先；在现有库锁内使用该快照，读事务结束后仍逐文件复核根目录、祖先、文件身份、大小和 mtime。不跨批次缓存索引或磁盘证据。
- 批次上限仍为 128，扫描仍为两遍、最多 4 个属性 worker。部分文件失败、整批提交失败、取消和已提交进度规则保持。
- `mark_finished` 增加 6 个刷新子阶段和 3 个复核子阶段。子阶段互不重复，已包含在对应父阶段中；字段口径见 [运行与恢复](../operations.md)。任务占用回调仍逐文件执行，其成本单独记录，未借此改动 job 的占用语义。

## 回归证据

新增日志字段断言在旧实现上失败（字段缺失）；暂存库同步策略测试在旧实现上失败（两次连接读取均为 FULL/2），改动后为 NORMAL/1。测试同时验证附加暂存库发布时、分离暂存库后主库仍为 FULL/2，暂存库删除后已发布数据可读。

批量快照回归覆盖缺失/循环祖先、祖先被替换但叶子硬链接身份未变、符号链接、批次间祖先改名、占用回调中新出现的待处理操作及错误优先级。原有标记/扫描测试继续覆盖逐项失败、文件内容与类型变化、硬链接、持久进度、取消、写入/提交回滚、扫描失败保留旧索引、临时库清理及任务占用。

## 任务创建与标记提交的并发补验

性能优化已提交为 `66d0c85`。本次补验基于该提交，只新增 job 测试与更新文档，生产代码、锁和事务未改。`TestProcessedMarkAndCreateInterleavings` 使用同批两个真实临时文件，包装实际 `Manager.Busy` 回调并保留其检查结果，通过通道精确暂停；任务通过真实 `Create`、`Execute` 和 SQLite 提交，不用固定睡眠争抢时机。分析由现有测试回调替代，未调用真实模型或 FFmpeg。

| 受控顺序 | 核对结果 |
| --- | --- |
| 标记已开始，任务在目标 Busy 检查前入队 | 目标返回 `entry_busy`，同批无关文件仍成功标记；任务正常执行 |
| 标记提交完成后创建任务 | 普通命名跳过该文件，不调用分析 |
| 两个 Busy 检查已通过，文件任务先入队，随后标记提交 | 标记成功，之后任务跳过该文件 |
| 相同时序，任务目标改为递归祖先目录 | 按目录范围找到文件并跳过 |
| 相同时序，任务目标改为另一条硬链接 | 按文件身份识别标记并跳过 |
| Busy 检查通过后创建任务，候选已入队，标记才提交 | worker 处理前重新读取标记，跳过且不调用分析 |
| Busy 检查通过后创建任务，分析已开始，标记才提交 | 最终 AutoCommit 拒绝改名，条目与批次记录失败，条目代码为 `already_processed` |

每个用例核对精确的 Changed/Failed、任务与条目计数/终态/原因、模型调用次数、处理标记来源、文件名和内容、索引指纹、marks 与 operations 行数。时间限制只用于发现卡住的用例；每次测试退出前取消并等待所有测试 goroutine。夹具复用现有 PATH 替换，因此子测试不并行。

同一套 7 个用例分别复制到优化前 `d83185c` 和当前 `66d0c85` 的隔离源码副本执行，全部通过。在当前版本的隔离副本分别移除 worker 处理前复核、AutoCommit 提交前复核，对应测试均按预期失败：前者不再跳过并多调用一次分析，后者实际改名成功，证明断言能发现这些保护失效。故障注入未修改工作区生产代码。

补验检查（macOS / arm64，Go 1.27.4）：

| 命令或检查 | 结果 |
| --- | --- |
| `go test -run '^TestProcessedMarkAndCreateInterleavings$' -count=50 -timeout=3m ./internal/job` | 通过，7 个场景各执行 50 次 |
| `CGO_ENABLED=1 go test -race -run '^TestProcessedMarkAndCreateInterleavings$' -count=20 -timeout=3m ./internal/job` | 通过，7 个场景各执行 20 次，无竞态报告 |
| `CGO_ENABLED=1 go test -race -count=1 -timeout=5m ./internal/job ./internal/library` | 两包完整默认测试通过 |
| 新文件 gofmt / goimports v0.50.0、`git diff --check` | 通过 |
| `go vet ./internal/job`、staticcheck v0.8.2 `./internal/job` | 通过 |
| `go build ./internal/job ./internal/library` | 通过，包构建，不是发布产物构建 |

这证明所覆盖交错在优化前后行为一致，并不代表任务创建和标记提交严格互斥：占用检查通过后，二者都可能成功。已开始的分析也可能产生一次无效调用，最终任务显示失败；该表现同样存在于优化前。本次没有将这种情况改成跳过，也没有做断电或 NAS 故障测试。原始输出位于忽略的 `test-results/mark-concurrency/`；下方性能数字属于此前优化验证，本次未重跑规模基准。

## 本机性能对照

macOS / arm64，Apple M2 Max，Go 1.27.4；全部为新建临时目录、合成小文件和隔离数据库。library 夹具不装配 job 的真实 Busy 回调，不含 NAS 延迟或真实媒体大小分布。先前用户日志的 402.8 秒不能用本机结果直接换算；已有索引也不等于冷文件系统缓存。

### 分项基准

每目录 53 个文件，预建索引，计时外清空 marks，计时内递归刷新并重新标记。`-benchtime=3x -count=3`，下表为三组输出的中位数（每组 3 次迭代）。单独 NORMAL 版本来自相同基线，仅修改暂存同步策略；最终版本另外包含批量复核、可用性读快照合并及细分计时。

| 布局 | 改动前 | 仅暂存 NORMAL | 最终实现 |
| --- | ---: | ---: | ---: |
| 1 目录 × 53 文件 | 11.00 ms | 8.59 ms | 5.58 ms |
| 100 目录 × 53 文件 | 690.21 ms | 602.70 ms | 257.34 ms |

100 目录场景仅 NORMAL 缩短约 12.7%，全部改动缩短约 62.7%。最终该场景约 45.0 MB、77.1 万次分配/轮，基线约 89.5 MB、195.9 万次分配/轮。短基准会受本机缓存及背景负载影响，不代表所有目录布局。

### 530000 文件、10000 目录

`TestScaleLargeProcessedMarksManyDirectories` 在同一隔离夹具先执行首次索引标记，再清空 marks、保留索引进行第二轮标记。改动前/后各执行一次完整用例，分开顺序运行；下表仅为 `mark_finished` 操作耗时，不含夹具生成、完整性检查及清理时间。

| 阶段 | 改动前 | 最终实现 | 耗时减少 |
| --- | ---: | ---: | ---: |
| 首次建立索引并标记 | 92.290 s | 55.837 s | 39.5% |
| 已有索引后重新标记 | 64.971 s | 28.983 s | 55.4% |

| 分段 | 首次：前 → 后 | 已有索引：前 → 后 |
| --- | --- | --- |
| 刷新 | 46.330 → 43.915 s | 18.934 → 16.564 s |
| 复核 | 39.452 → 5.574 s | 39.082 → 5.623 s |
| 标记写入 | 5.207 → 5.088 s | 5.611 → 5.523 s |

每轮均精确标记 530000 条、刷新 10001 个目录（含根）、提交 10000 批，失败和中断为零。主库标记/索引精确计数、`integrity_check`、`foreign_key_check` 全部通过。新的子阶段显示首次索引构建/发布仍占约 31 秒；已有索引时该项约 2.1 秒。

复现命令：

```sh
go test -run '^$' -bench '^BenchmarkProcessedMarks$' -benchtime=3x -count=3 -benchmem ./internal/library
go test -tags=integration,scale -run '^TestScaleLargeProcessedMarksManyDirectories$' -count=1 -timeout=15m -v ./internal/library
```

本机原始输出保留于忽略的 `test-results/mark-performance/`。对照版本分别编译成独立测试二进制，后续工作区修改不会改变正在运行的基线。未使用用户配置、媒体、数据库或外部模型。

## 用户运行环境回传结果

来源为用户提供的 2026-11-13 19:56:14 与 23:33:21（UTC+8）两条 `mark_finished` 日志。两次均为 1 个目标、532322 个文件实际变更、10788 个目录、11352 个批次，`failed=0`、`interrupted=false`。

| 阶段 | 优化前 | 优化后 |
| --- | ---: | ---: |
| 总耗时 | 402.817 s | 160.412 s |
| 目录刷新 | 280.963 s | 109.555 s |
| 文件复核 | 84.740 s | 23.837 s |
| 标记写入 | 34.967 s | 24.995 s |

这组日志的总耗时下降约 60.2%。新日志中构建/发布索引为 73.253 秒，任务占用检查为 16.665 秒。硬件、存储介质、挂载方式、文件系统缓存及后台负载未记录，因此不能将全部差异归因于单个代码改动，也不能将这些结果视为本机受控基准或断电可靠性验证。

当前决定保留此性能实现，先使用和观察，不继续扩大优化范围。后续若全库标记频繁执行、耗时明显增长或影响日常浏览，再细分索引发布阶段并评估下一轮改动。

## 默认端口与文档收尾

基于 `66d0c85` 及并发补验工作区，将 `internal/config` 的内置默认端口、配置示例、中英文 README、配置参考及 Vite `/api` 代理统一为 8080。显式 YAML 端口仍优先，不改写已有本地配置；浏览器测试继续使用隔离端口 18790。运行说明及设计文档补充上述标记与任务并发边界。

验证使用 macOS / arm64、临时媒体目录、临时配置和独立数据库。启动验证故意省略 `port`：实际监听 `127.0.0.1:8080`，`GET /healthz` 返回 200；再用实际 Vite 配置启动临时开发服务，代理 `GET /api/info` 返回 200。测试服务正常停止，临时夹具清理，未读取或修改用户配置/媒体/数据库。服务原始日志保留在忽略的 `test-results/default-port/server.txt`。

| 检查 | 结果 |
| --- | --- |
| `go test -count=1 ./internal/config` | 通过，含默认值与显式端口覆盖 |
| `CGO_ENABLED=1 go test -race -count=1 ./internal/config ./cmd/video-rename` | 通过 |
| `go vet ./internal/config ./cmd/video-rename`、同范围 staticcheck v0.8.2 | 通过 |
| 改动 Go 文件 gofmt / goimports v0.50.0、`git diff --check` | 通过 |
| `npm --prefix web run typecheck`、`npm --prefix web run build` | 通过 |
| 在 web 中对 `vite.config.ts` 运行 Prettier 检查与 ESLint | 通过 |
| `go build -o bin/video-rename ./cmd/video-rename` | 通过，使用刚构建的 Web 资源 |
| 实际默认端口启动、健康检查及 Vite 代理请求 | 通过 |
| `npm --prefix web run test:e2e -- login.spec.ts media-marks.spec.ts` | 2 个 Chromium 用例通过；使用现有 18790 隔离夹具，覆盖登录错误反馈及图片/音频标记保护、编号跳过和取消标记 |

CI 的 E2E 夹具显式配置 `port: 18790`，Playwright 的 `baseURL` 与服务就绪检查同样使用 18790，不依赖应用默认端口，因此 CI 与 E2E 配置无需修改。现有 CI `go test -race ./...` 会包含默认端口断言与新增并发用例。上述 E2E 是本机验证，未触发远端 CI，原始输出在 `test-results/default-port/e2e.txt`。

此次端口调整未重跑全仓 Go、Web 单测/完整 E2E、性能规模基准或跨平台构建；生产行为只改变省略端口时的默认值及开发代理目标，页面与媒体处理逻辑未改。性能与并发补验的已执行范围保留在各自章节。

## 性能优化阶段检查结果

| 检查 | 实际结果 |
| --- | --- |
| 改动文件 gofmt / goimports v0.50.0、`git diff --check` | 通过 |
| `npm --prefix web run build` | 通过，生成真实嵌入资源 |
| `go test -count=1 ./...` | 全部通过 |
| `go vet ./...`、`go vet -tags=integration,scale ./...` | 通过 |
| staticcheck v0.8.2：library、storage、job、app、libraryscan | 通过 |
| `go build -o bin/video-rename ./cmd/video-rename` | 通过，macOS arm64；`--version` 可独立运行 |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/video-rename-linux-amd64 ./cmd/video-rename` | 交叉编译通过，不代表 Linux 实机运行 |
| `CGO_ENABLED=1 go test -race -count=1 ./internal/library ./internal/storage ./internal/job ./internal/app ./internal/libraryscan` | 全部通过 |
| `go test -tags=scale -run '^TestScale' -count=1 -timeout=10m ./internal/library ./internal/app ./internal/job` | 全部通过，含实际 app/job 装配的后台标记、断连与占用检查 |

性能优化阶段未执行：SMB/NFS 目标环境实测、断电/系统崩溃故障注入、Linux 实机运行、其他发布架构构建、Web lint/typecheck/单测/E2E、FFmpeg 集成、Fuzz 与漏洞扫描。该阶段前端源码、媒体处理和依赖未变，仅重建 Web 供 Go 嵌入；随后默认端口调整的检查另见上节。WAL/NORMAL 的断电语义依据 [SQLite 官方说明](https://www.sqlite.org/pragma.html#pragma_synchronous)，不能把普通回滚测试当作物理断电验证。
