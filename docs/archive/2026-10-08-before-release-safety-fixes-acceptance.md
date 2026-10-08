# 历史验收：1.0 发布前全仓审查（2026-10-08）

> 历史审查，仅代表修复前基线；三项问题后续修复与当前发布判断见 [最新验收](../ACCEPTANCE.md)。

**结论：暂不建议发布 1.0。确认 2 项 P1、1 项 P2，均有确定性的隔离复现，尚未修复。** 现有完整测试通过，但没有覆盖这三个故障边界，不能据此作出“已经没有问题”的结论。

审查基线为干净工作区 `276a19409ff2a192adf9ee80d34001d2a41da7fd`，共 417 个 Git 跟踪文件。本轮没有修改业务源码、依赖、配置示例或原有测试；仅更新验收文档并保存被 Git 忽略的审查证据。没有提交、打标签或发布。上一轮验收见 [历史记录](2026-10-07-rename-record-fix-acceptance.md)。

## 确认的问题

### P1：缓存与媒体目录重叠会导致启动时删除媒体

位置：[配置路径校验](../../internal/config/service.go#L93)，[缓存启动清理](../../internal/framecache/cache.go#L120)。

当前校验只禁止配置、storage、tmp-storage 位于媒体根目录内，没有禁止媒体位于缓存的受管目录内。例如以下关系会通过 `ValidateService`：

```text
tmp-storage = /临时夹具/base
media-path  = /临时夹具/base/video-rename-frames-v1/pending-videos
storage     = /临时夹具/base/state
ConfigDir   = /临时夹具/base
```

`framecache.New` 将 `pending-videos` 当作残留缓存递归删除。复现先创建合成媒体，再执行正常配置校验和缓存初始化，结果是媒体文件消失。默认配置不受影响；触发条件是上述被接受的路径重叠，不要求模型调用或运行命名任务。

建议在任何建库、恢复及缓存清理前，解析真实路径并核对缓存实际受管根目录与媒体、持久状态和配置的重叠关系；危险配置应明确拒绝。应补充父子关系与符号链接回归，不能只依赖缓存子目录命名规则来保护用户数据。

### P1：源文件被外部替换后仍移动并记录成功

位置：[改名提交](../../internal/library/operations.go#L309)。

源文件的身份、大小和时间在写入 prepared 日志之前检查；之后按文件名直接执行 `NoReplaceAt`，移动后仅复核父目录，未复核目标文件是否仍是原快照。若下载器、同步程序等在这个窗口替换了源文件，实际被移动的是另一 inode，数据库却按旧身份记录 committed，并写入旧身份的处理标记。

复现用 SQLite 插入触发器，在 prepared 日志写入时将原文件移走并创建同名替代文件，确定性模拟外部替换。实际输出为 `operation status=committed err=<nil>`，但目标 inode 与索引身份不同。无覆盖改名只保护目标不被覆盖，不能保证名称当前指向的源文件身份；后续依赖日志身份的还原也会遭遇不匹配。

建议写日志后、移动前再次核对源快照，移动后核对目标快照，通过后才能提交成功状态。发现身份变化时保留可核对证据并停止相关提交，不能无条件回滚覆盖外部文件。回归需覆盖替换、同 inode 内容变化及自动同名提交；检查前后的极短外部竞争仍需依靠提交后验证处理。

### P2：忽略终态写库失败，批次仍显示完成

位置：[条目结束处理](../../internal/jobs/processing.go#L97)。

`finish` 分别更新条目终态和批次计数，两次写入均丢弃错误，也不在同一事务内。条目终态写入失败后仍可能增加 done，并继续结束批次。

复现用触发器拒绝 `run_items` 的 done 更新，其余写入正常。运行结果为 `run status=completed done=1 item status=running`。这是明确的错误处理缺口；本次证明的是选择性数据库写入失败，不声称实测了物理磁盘满或断电。终态批次也不属于启动时中断活动批次的恢复范围。

建议将条目终态与计数更新放进事务并传播错误；批次收尾也必须处理写入失败，不能发送或返回成功结果。文件改名已提交时应保留其日志事实，恢复明细状态，避免为修正统计而再次盲目改名。

## 审查范围与方法

这次审查对象是当前完整 HEAD，不是最近一次提交。OCR delegation 用于规则解析；干净工作区的差异预览统计为 `total_files=0 / reviewed_files=0 / skipped_files=0 / coverage_rate=N/A`，这个数字不代表全仓审查覆盖率。随后按以下风险链路检查源码、测试、调用关系和文档，并运行完整测试。

| 范围 | 检查重点 |
| --- | --- |
| CLI、配置、构建与双语 README | 路径规则、版本注入、干净构建、安装顺序、支持平台和风险说明 |
| HTTP、认证和播放 | 路由保护、CSRF、会话与限流、请求取消、媒体打开与路径身份 |
| 存储、索引和文件操作 | 结构校验、实例锁、只读池、密钥、事务、扫描发布、改名/还原/编号/删除与恢复 |
| 队列和后台任务 | 范围重叠、重试、暂停取消、并发、计划、扫描和进度状态 |
| 媒体、缓存、模型和通知 | 子进程退出、抽帧、缓存租约与清理、模型协议与诊断、通知持久化 |
| 历史和统计 | 分页、时间范围、来源关联、索引、报告缓存与统计一致性 |
| Web 页面与共享组件 | API 契约、异步刷新、选择/滚动/焦点保持、弹窗、桌面和窄屏状态 |

人工审查按风险深浅展开；417 是跟踪文件清单数量，**不是宣称 417 个文件逐行审核、也不是百分之百缺陷覆盖**。锁文件、图片和历史文档不以人工逐行审查计算。没有将风格偏好、存在新版本或缺少 CI 配置单独列作发布阻塞。

## 已执行验证

本机为 macOS / Apple M2 Max / arm64；实际工具为 Go 1.27.1、Node 26.8.2、FFmpeg 9.0.1、TypeScript 6.0.3、Vite 8.3.3。Go 命令使用独立构建缓存 `GOCACHE=/tmp/video-rename-go-cache`。

| 原生命令或检查 | 本轮结果 |
| --- | --- |
| `npm --prefix web ci` | 通过；未改锁文件 |
| `npm --prefix web run format:check` | 通过 |
| `npm --prefix web run check` | 通过 |
| `npm --prefix web run build` | 通过 |
| `go mod verify` | 通过 |
| `go test -count=1 ./...` | 全仓通过 |
| `go test -race -count=1 ./...` | 全仓竞态检查通过 |
| `go vet ./...` | 通过 |
| `go test -tags=integration -count=1 ./internal/media` | 通过，真实 FFmpeg/ffprobe |
| `npm --prefix web run test:e2e -- --output=test-results/release-audit` | **182/182 通过，9.1 分钟** |
| `go test -tags=scale -run '^TestScale' -count=1 -timeout=15m -v ./internal/library ./internal/jobs ./internal/app ./internal/runstats` | 全部通过；覆盖 30 万索引、5 万分页、2 万标记/编号、任务提交与统计 |
| `go test -tags=scale_large -run '^TestScaleLarge' -count=1 -timeout=15m -v ./internal/library` | 通过，314.654 秒；53 万文件/111 目录扫描及 50 万真实临时文件标记，核对精确计数、SQLite 完整性和外键 |
| `go test -tags=scale -run '^$' -bench '^BenchmarkHistoryModelSource$' -benchtime=1x -count=1 -benchmem ./internal/app` | 通过；五个子场景单次约 2.95–74.71 毫秒 |
| `go test -run '^$' -bench '^BenchmarkCacheSweepRetained$' -benchtime=1x -count=1 -benchmem ./internal/framecache` | 通过；11000 条保留账目单次约 1.40 毫秒 |
| `go build -buildvcs=true -ldflags '-X main.version=1.0.0' -o /tmp/video-rename-release-audit ./cmd/video-rename` | macOS 构建通过，版本输出 `video-rename 1.0.0 (revision=276a19409ff2)`；只是审查产物，未发布 |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/video-rename-release-linux-amd64 ./cmd/video-rename` | Linux amd64 交叉编译通过，未在 Linux 执行 |
| `git archive HEAD` 隔离源码副本，重新执行 npm ci、Web 构建、Go 构建 | 通过；不复制现有 node_modules 或 dist |
| 新构建程序的隔离启动冒烟 | 临时配置/媒体/数据库、随机回环端口；首页 200、登录 200 并取得 CSRF、SIGTERM 正常退出 |
| `npm --prefix web audit --json` | 公告库报告 0 个漏洞 |
| `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` | `No vulnerabilities found.` |
| 新增的三项隔离复现 `go test -count=1 -run '^TestReleaseAudit' -v ./internal/config ./internal/library ./internal/jobs` | **3/3 按预期失败**，证明以上问题；仅在临时源码副本执行 |

已查看本轮 `web/test-results/real-rename-record-desktop.png` 与 `real-rename-record-mobile.png`：真实改名详情中的原名、新名和完成状态可读，390px 窄屏可滚动查看；完整浏览器套件同时覆盖加载/空/错误状态、键盘焦点、选择与滚动保持。

最初部分 Go/浏览器检查因沙箱禁止本机监听而中断，允许隔离回环服务后重新执行成功；联网漏洞查询也在取得网络执行权限后完成。没有修改断言或关闭检查。规模测试部分时段与浏览器测试重叠，其耗时不用于性能回归比较；两个基准在它们结束后运行，仍只是单次本机采样，不是吞吐承诺。

## 依赖状态

另执行了 `npm --prefix web outdated --json` 与 `go list -m -u all`。执行时前端可用更新包括 Vite 8.3.4、Playwright 1.64.0、@lucide/vue 1.53.0 和 TypeScript 7.0.2；Go 查询显示部分间接模块有更新，当前直接依赖未显示更新。存在更新不等于当前版本存在漏洞，本轮未升级任何依赖。

TypeScript 仍按项目已记录的 Vue 类型工具兼容决定固定在 6.0.3；本轮验证了当前组合可用，没有重新试验 TypeScript 7 的兼容性。漏洞检查仅代表执行时的公告与分析结果，不能保证不存在尚未公开的漏洞。

## 可复现证据

日志、结构化问题与基线文件清单位于被忽略的 [本地审查证据目录](../../test-results/release-audit/)。三个新增复现源码在该目录的 `repro/internal/{config,library,jobs}/release_audit_test.go.txt`，没有放入当前业务源码目录。`vr-release-repro.log` 保留三项失败的实际输出，`review.json` 保留定位、分类与严重性。

需要再次复现时，从仓库根执行以下命令，所有数据由 Go 测试的临时夹具创建。三个失败是当前基线的预期结果；修复后应将这些场景完善为正式回归，再验证变绿。

```bash
review_repo="$PWD"
review_tmp=$(mktemp -d /tmp/video-rename-review.XXXXXX)
git archive 276a19409ff2a192adf9ee80d34001d2a41da7fd | tar -x -C "$review_tmp"
for review_pkg in config library jobs; do
  cp "$review_repo/test-results/release-audit/repro/internal/$review_pkg/release_audit_test.go.txt" "$review_tmp/internal/$review_pkg/release_audit_test.go"
done
cd "$review_tmp"
npm --prefix web ci
npm --prefix web run build
GOCACHE=/tmp/video-rename-go-cache go test -count=1 -run '^TestReleaseAudit' -v ./internal/config ./internal/library ./internal/jobs
```

## 未实测的环境与发布条件

- 所有测试使用隔离数据库、合成媒体、本地假模型及通知夹具；未读取用户配置、数据库或真实媒体，未调用真实付费模型或真实消息接收端。
- Linux 只有交叉编译，没有 Linux/NAS、SMB/NFS 实机运行；不能将 macOS 本机规模结果解释为目标环境保证。
- 未执行物理断电、文件系统损坏或真实磁盘满测试；已有故障恢复用例和本次数据库故障注入不能替代这些场景。
- 两项 P1 修复并形成回归前不建议发布；P2 应在 1.0 前一并处理。修复后需重跑相关故障、恢复及完整回归，更新本文件，不能沿用本轮通过记录宣称修复版本已验收。
