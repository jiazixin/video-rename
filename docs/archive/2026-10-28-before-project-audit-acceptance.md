# 历史记录：本次全仓复查前的验收

状态：历史快照。以下是 2026-10-28 本次复查之前的记录，其中“完成整改”“全部通过”等描述只属于当时的工作区，不代表当前全仓符合规范。当前结论以 [最新验收](../ACCEPTANCE.md) 为准。

# 最新验收：全仓规范整改（2026-10-28）

范围：基线 `9329a23` 之后的当前工作区，按目录规范、代码规范及 Go / Web / Vue 技术栈规范完成本轮整改。本文区分原检查的误判、实际修复、已执行验证和未验证范围；不沿用旧验收的通过结论。此前累计记录已归档为 [整改前验收](../archive/2026-10-27-before-full-compliance-acceptance.md)。

原有 `Go代码规范.md` 的用户修改保持原样，未纳入本轮修复提交，未改规范来迁就实现。以下验收基于提交前工作区；发布构建仅为本地快照，未向远端发布。

本轮直接更新当前接口、配置及调用方，不保留旧字段、旧导入路径或旧数据库迁移分支。当前数据库结构号为 **20261028**；已有不支持的数据库拒绝打开并保留原样，不自动清空或重建。测试使用临时数据库、隔离目录、合成媒体和本地假服务，不以用户数据或真实外部服务代替夹具。

## 对原检查的核实与修正

原报告的 P1/P2 主体判断成立：数据库连接暴露、HTTP 直接 SQL、缺少统一写事务、PATCH 实际全量保存、200/409/请求体幂等与版本契约、业务全局样式、秘密类型、参数数量、Fuzz、测试诊断和清理边界等问题均已核实并整改。以下区分确实需要收紧的措辞、原报告已经判断正确的部分，以及本轮补查得到的技术约束；不把原报告未作的判断归给它。

| 原报告内容或检查项 | 核实结论 |
| --- | --- |
| DSN 缺少 `journal_mode(WAL)`、`_txlock=immediate` | DSN 缺项属实，但原实现已在启动时执行 `PRAGMA journal_mode=WAL`，不能推导为数据库从未启用 WAL。本轮统一所有连接的 DSN 与事务行为。 |
| 数据库访问未收口、SQL 分散在 13 个包 | 连接暴露与 HTTP SQL 是实际问题；SQL 位于多个领域仓储本身不违规，不需要全部集中到一个全局包。当前保留领域归属，隐藏连接并统一 Write/View，HTTP 记录查询移至 `internal/record`。 |
| “非法”构建标签 `scale_large` | 它是 Go 可识别的自定义标签，只是违反本项目白名单。现改为 `integration && scale`；没有扩大规范允许范围。 |
| 超长函数与文件“只是拆分信号” | 原判断正确，规范不是硬性行数上限。本轮按职责拆分，未为凑行数增加转发层；仍较长的扫描、分析与目录页面不自动构成违规。 |
| `newLogger`、`app.Serve` 与模板有差异 | logger 的错误/关闭边界及构建身份传播确需补齐，但类型必须叫 `app.Build` 不是硬性要求。本轮完整传递版本、提交、时间和 modified 状态。 |
| 中文提示词/通知/种子数据不是违规；既有 Prettier 配置可沿用 | 原判断正确。保留已登记的业务数据例外和已有格式配置；整改的是英文开发者诊断、测试失败说明及源码注释。中文测试输入本身不是问题。 |
| 存储与接口时间格式不固定、缺少统一类型 | 原问题成立。补充约束是文件安全指纹必须保留纳秒，不能随展示时间截断。本轮用 `timefmt` 固定接口/业务时间，单独保存 `mtime_ns`。 |
| cron 维护状态尚未核实 | 原报告没有作出错误结论。本轮确认 v3.0.1 仍为最新稳定版，仓库未归档但默认分支长期无新提交；不能称为活跃维护，证据与保留理由见 [依赖维护](../IMPLEMENTATION.md#依赖维护与构建版本)。 |
| race、漏洞、integration、scale、E2E 未执行；Sleep 未逐个核对 | 这是原报告明确披露的验证缺口，本轮补做。余下 Sleep 属有截止时间的真实 I/O 轮询或显式模拟慢媒体/模型，不以固定睡眠猜测异步完成。 |

项目支持的 OpenAI 兼容模型协议属于当前产品能力，与本次删除旧 API、配置、数据库兼容分支是两件事。漏洞扫描也应区分可达代码与整个依赖模块的告警范围，详见下文；这两点是本轮补充约束，不是原报告的误判。

运行期编辑模型、提示词、采样等设置是现有产品功能，已登记为配置生命周期例外；任务仍冻结自己的快照。文件改名、编号及目录发布在最终事务中保留窄范围 stat/fstat 身份复核，也已登记例外。外部进程不受应用锁约束，不能为了消除事务内文件 I/O 而删除这道安全检查；遍历目录、读写文件内容、网络和外部进程仍在写事务之外。

## 实际完成的修复

### 存储、认证与生命周期

- 数据库连接私有化，统一 `Write/View`、有返回值的事务辅助及 `CollectRows`；提交、回滚、行迭代、关闭、`RowsAffected` 和 `LastInsertId` 错误按边界传播，确实允许忽略的错误说明原因。目录扫描暂存库也使用明确的读写边界；分页计数和数据在同一读快照中完成。
- DSN 固定 WAL、FULL 同步、外键与 busy timeout；写池单连接并使用 immediate 事务，读池使用 `file:`、`mode=ro` 和 `query_only`。补充替换连接、只读隔离、旧库原样保留、实例锁、密钥缺失、ID 不复用及关闭失败的回归。
- 新增 `secret.Value`，普通格式化、结构化日志及 JSON 默认脱敏；配置、加密持久化、密码派生和出站认证只在明确边界 `Reveal`。管理员密码使用独立随机盐和 Argon2id（19 MiB、2 轮、并行度 1）；认证组件不保留密码明文或可绕过 KDF 的快速密码摘要。会话令牌仍只存 SHA-256。
- 读写与外部请求传递调用方 context；构造、启动、退出分离，后台组件提供 `Run(ctx) error`，结构体不保存 context。已开始且需要落盘的结果使用有界清理 context；退出等待 worker，超时不提前关闭仍被使用的存储。会话读取响应取消，有效会话续期最多等待写池 100 毫秒，失败不发新 Cookie。
- 调度、目录扫描与帧缓存等后台错误进入注入的日志器或返回到监督边界；修复帧缓存失败路径因日志器未正确保存而 panic。暂停抽帧只取消调度等待，已成功生成的帧仍在有界时间内完成校验与发布。

### API、时间与命令行

- 请求边界拒绝未知字段、只读元数据、非法数字及不支持的媒体类型；列表使用对象封装，创建/受理/空响应状态与 `Location` 对齐。请求 ID、缓存头、404/405 JSON 错误及 `Allow` 保持一致，完整契约见 [API](../API.md)。
- 提示词、模型与计划使用 JSON Merge Patch；并发修改以 `ETag` / `If-Match` 校验，数据库继续原子比较内部版本。POST 幂等键通过请求头传递，响应持久化后再发出；并发、重启、容量、待确认结果和重放响应头均有回归。修复空响应被存为 NULL 后触发 500 的问题，204 结果可以重放。
- 普通存储与 API 时间使用 `internal/timefmt` 的 `2006-01-02T15:04:05.000Z`，严格读取当前格式；`/api/info` 的已知构建时间采用同一类型，未知时省略；文件指纹独立保留纳秒，不用展示时间代替安全证据。JSON 使用 v2，零值省略遵循 `omitzero` 语义。
- 部署配置与任务配置分离；配置验证不偷偷归一化，YAML 拒绝未知/重复字段、多文档和超限输入。CLI 的帮助、参数错误、配置错误、stdout/stderr、退出码与信号关闭行为对齐规范。无 Git 源码包显示 `dev`；有 VCS 的构建保留可取得的版本、revision 和 modified 信息。

### Go、Web 与验证基础

- Go 包统一使用单数名称（`job`、`model`、`prompt`、`notification`、`record`、`runstat`、`systemsetting`），更新调用方，不保留旧导入别名。整理参数对象、接收者、导出注释、英文错误与测试诊断，保留中文业务数据。
- Web API 客户端统一错误对象、请求 ID、取消、超时和受限重试；同一逻辑 POST 重试沿用同一幂等键。模型编辑只提交允许写入的字段，修复把 ID、ETag 等响应元数据再次提交而被服务端拒绝的问题。
- 业务样式迁回 features，theme.css 从 2566 行减至 1246 行；SystemSettingsPage 迁入 features/settings，ToastNotice 成为共享组件。composable 使用 onScopeDispose，覆盖 effectScope 停止；共享会话只读、watch 取消并防止迟到结果回写。补 color-scheme、嵌套弹窗焦点及组件测试，删除 execCommand 旧剪贴板回退；SPA 直接地址由服务端回退，未知静态文件仍为 404。当前 CSP 与设计变量保持一致。
- 增加 YAML 配置、ffprobe JSON、文件名清洗和模型响应四类 Fuzz 及种子；CI 使用固定工具版本检查格式、静态分析、全部发布目标 vet、竞态、短时 Fuzz、集成测试、Chromium E2E 和发布快照。沿用已有 `go.yaml.in/yaml/v3`；新增 Argon2id 使用 Go 项目的 `x/crypto`，维护与漏洞范围见 [开发与测试](../IMPLEMENTATION.md#依赖维护与构建版本)。

### 复核中确认并修复的文件安全与并发缺陷

- 编号最后提交事务期间，外部进程替换文件、改写内容或替换父目录时，旧流程仍可能记录 committed。回归先复现失败，再在事务提交前完成持有句柄和实际路径的最终核验；失败保留 review 日志，不提交成功事件，也不覆盖外部新内容。证据为 `commit_identity_test.go` 的改名/编号回归，以及本地 `numbering-before.log`、`numbering-after.log`。
- `NumberingTreeHistory` 在占有只读连接时又调用 `Path/Numbering` 获取连接，可能耗尽四连接读池。现先验证路径，只在一个 View 中收集 ID 并关闭，再逐项读取。含两条已提交历史的 8 并发回归在修复前全部触发 5 秒截止；修复后 64 次读取通过，并纳入全量测试。

## 已执行验证

本机 **macOS / arm64（Apple M2 Max），Go 1.27.3，Node.js 26.12.1，npm 11.20.0，FFmpeg 9.0.2**。日志位于被忽略的 `.cache/compliance/`；下表列出本轮证据文件，不把它们当作需要提交的源码。完整复现命令在 [开发与测试](../IMPLEMENTATION.md)。

| 检查 | 本轮结果 | 本地证据 |
| --- | --- | --- |
| `go mod verify`、`go mod tidy -diff`、gofmt、goimports | 通过，模块可验证，无待整理差异 | `mod-verify.log`、`mod-tidy.log`、`gofmt-final.log`、`goimports-final.log` |
| `go vet ./...`，全部 8 个发布目标；`go vet -tags integration,scale ./...` | 通过，无诊断；交叉 vet 不代表目标实机测试 | `vet-<os>-<arch>.log`、`vet-tags.log` |
| staticcheck v0.8.2 | 通过，无遗留诊断 | `staticcheck-final-confirmed.log` |
| `go test -p=2 -count=1 ./...` | 27 个有测试包通过；另 2 包无测试文件 | `unit-final-confirmed.log` |
| `CGO_ENABLED=1 go test -p=2 -race -count=1 ./...` | 全量通过，无竞态报告 | `race-final.log` |
| `go test -tags=integration -count=1 ./internal/media ./cmd/video-rename` | 真实 FFmpeg 与临时 Git 仓库构建版本测试通过 | `integration-final.log` |
| 四类 Fuzz，各 `-fuzztime=15s -parallel=2` | YAML、ffprobe JSON、文件名、模型响应全部通过；这是短时输入探索 | `fuzz-config.log`、`fuzz-media.log`、`fuzz-naming.log`、`fuzz-vision.log` |
| Web `format:check`、`lint`、`typecheck`、`test`、`build` | 全通过；16 个单测文件、53 项测试通过 | `web-format.log`、`web-lint.log`、`web-typecheck.log`、`web-unit.log`、`web-build.log` |
| `npm audit` | 本次扫描 0 漏洞 | `npm-audit.log` |
| 重新构建 Web 与服务后的 Chromium E2E | **183 通过、0 失败、0 跳过，10.4 分钟**；隔离端口 18790、合成媒体、真实 FFmpeg、本地假模型 | `e2e.log` |
| govulncheck v1.9.0（含 verbose） | 0 可达符号漏洞、0 导入包漏洞；1 项模块告警，见下一节 | `govulncheck-final.log`、`govulncheck-verbose.log` |
| GoReleaser v2.18.3 配置检查及本地 snapshot | 8 个目标构建、归档成功，8 个 SHA-256 校验通过；只预演，未向远端发布 | `release-check.log`、`release-snapshot.log`、`release-checksums.log` |
| 本机 snapshot 的 `--version` | 输出保留当前伪版本和 `+dirty (modified)`，没有冒充正式 tag 版本 | `release-version.log` |
| 从 darwin/arm64 压缩包提取的二进制运行冒烟 | 隔离配置启动、健康检查、构建时间格式、登录/会话、3 个 SPA 直达地址、内嵌 JS、缺失静态文件 404、SIGTERM 退出码 0 均通过；临时夹具已删除 | `release-runtime-smoke.py`、`release-runtime-smoke.log` |
| 无 `node_modules`、`web/dist`、`bin`、`.git` 的源码副本重建 | `npm ci`、Web 构建、Go 构建和 `--version` 均通过，版本为 `video-rename dev`；使用本机已安装工具链 | `source-copy.py`、`fresh-build.log` |

8 个发布目标为 linux/amd64、linux/arm64、linux/ppc64le、linux/s390x、linux/riscv64、linux/loong64、darwin/amd64、darwin/arm64。除本机 darwin/arm64 外，其余 7 个目标只有本轮交叉检查和构建证据。

发布包冒烟脚本首次以完整字符串匹配 HTML，因结束标签中的合法换行误判失败；改用 HTML 解析器检查应用挂载节点与脚本引用后通过，未修改产品代码或放宽 HTTP 状态、资源加载和退出断言。

## 规模与基准

三组命令均已结束并返回 0；普通 scale 与超大文件测试顺序执行。没有用历史报告的耗时或中间进度替代本轮终态。

| 项目 | 结果 | 本地证据 |
| --- | --- | --- |
| `-tags=scale -run '^TestScale'`，library、job、app、runstat | 12 个顶层测试通过；包含 30 万索引/候选、5 万分页、2 万标记/编号、断线后台标记、历史模型来源与 10 万条目/请求统计 | `scale-final.log` |
| `-tags=integration,scale -run '^TestScaleLarge'` | 通过，总 393.302 秒；53 万真实文件、111 目录首次/不变重扫；50 万文件、100 目录标记，Changed=500000、Failed=0；精确计数、候选清理、完整性和外键检查通过，另含 30 万索引进度测试 | `large-scale.log` |
| `-tags=scale -run '^$' -bench . -benchtime=1x -benchmem -p=1 ./...` | 全部通过，4 个 benchmark 入口、16 个案例；历史来源查询、缓存账目保留、目录扫描与递归扫描。每案只运行一次，是基准冒烟，不是稳定性能估计 | `benchmarks.log` |

首轮规模失败有两处夹具问题：为了记录库大小而将 wal_checkpoint 放进写事务，以及 SQL strftime 仍生成秒精度时间。前者改为记录数据库与 WAL 的真实总大小，后者输出固定毫秒；计数、分页、统计及完整性断言均保留，重跑全套通过。

规模结果只描述本机隔离数据和当次负载，不代表 NAS 吞吐，也不替代断电与真实磁盘故障测试。

## 依赖告警与验证边界

本次 `govulncheck -show verbose` 报告 **0 个影响代码的漏洞、0 个导入包漏洞、1 个依赖模块告警**。告警 [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) 针对 `golang.org/x/crypto/openpgp` 及相关子包：这些包停止维护，所有版本受影响，没有已知修复版本。本项目使用 `golang.org/x/crypto v0.58.0` 的 Argon2id，没有导入 OpenPGP；未通过隐藏告警或移除密码 KDF 来获得“零告警”。结论仅代表本次漏洞数据库与当前导入图。

以下仍未验证，不能由本机通过结果推导为已支持或已发布：

- Linux 实机以及本机之外的 7 个目标运行环境；交叉 vet、编译与归档不能证明运行正确。
- 真实 SMB/NFS 延迟、服务器差异、掉线重连、断电及物理磁盘故障。隔离故障注入验证了错误路径，不等于这些目标环境实测。
- Firefox、Safari 及真实移动设备；本轮浏览器结果来自 Chromium，包含桌面/窄屏、交互与可访问性回归。
- 真实模型、通知渠道和其他外部服务的费用、限流、协议差异与可用性；这些测试使用本地假服务。
- 修改后的 GitHub Actions 在线运行、正式 tag 发布、远端 Release 下载与安装、Apple 签名公证；本地 snapshot 没有执行发布。
- 长时间 Fuzz 浸泡；四类 15 秒运行不能证明输入空间已穷尽。
