# 开发与测试

应用是 Go 1.27.2 + Vue 3 / TypeScript + SQLite 的单实例服务。安装入口见 [中文 README](../README.md) / [English README](../README.en.md)，行为约定见 [当前设计](DESIGN.md)，文件归属见 [目录规范](../目录规范.md)。

## 模块入口

| 路径 | 职责 |
| --- | --- |
| cmd/video-rename、internal/app | 命令行、构建身份、生命周期、路由和 HTTP 参数处理 |
| internal/auth、config、storage | 会话/CSRF、部署配置、SQLite、加密密钥和实例锁 |
| internal/library、rename、naming、domain、discovery | 索引、文件身份、无覆盖移动、编号、删除及恢复 |
| internal/jobs、scheduler、pipeline | 持久队列、动态并发、暂停/重试、Cron 和媒体处理链 |
| internal/libraryscan | 独立后台目录扫描、范围规范化、调度与停止 |
| internal/media、framecache、sampling | ffprobe/FFmpeg、采样、截图租约与容量控制 |
| internal/models、vision、prompts、systemsettings | 模型配置与协议、提示词及运行设置 |
| internal/notifications、tokenusage、runstats | 通知队列、请求账本、运行分析 |
| web/src | Vue 应用装配、业务页面、共享组件、技术模块及样式 |

部署 Config 与处理 TaskConfig 分离，任务快照不包含登录、部署路径或监听设置。HTTP 入口只负责装配，领域行为留在所属包。Web 的 API、契约与标签位于 app；日期、大小与剪贴板位于 lib；业务状态靠近所属 features。

## 数据库与文件安全

- `internal/storage/schema.sql` 是唯一建表来源，当前结构号 **20261005**。空数据库事务创建完整结构；不支持的旧结构拒绝打开，不迁移、不自动重建。实体 ID 使用 AUTOINCREMENT。
- SQLite 使用 WAL、FULL 同步、单写连接和最多四个只读连接。读池设置 `mode=ro` 与 `query_only`；所有新连接启用外键和 5 秒 busy_timeout。分页计数与数据使用同一快照，避免持有读连接时嵌套占满读池。
- 数据库符号链接解析到真实路径后选择锁和密钥；多硬链接数据库拒绝打开。SQLite 不放在网络盘。密钥、数据库与辅助文件的权限有回归检查。
- 请求读取及认证写入传播取消。有效会话续期最多等待写池 100 毫秒，失败不发新 Cookie；之后请求重试续期。登录失败计数与预算检查在同一锁内进行。
- 文件操作逐层打开父目录并拒绝符号链接，检查设备/inode，文件还检查 size/mtime。Linux 使用 renameat2(RENAME_NOREPLACE)，macOS 使用 renameatx_np(RENAME_EXCL)，操作后同步目录并复核路径；prepared 写入后和移动后复核文件身份/大小/mtime，成功事务提交前再次核对，异常保留 prepared 并停止提交。
- 启动校验解析缓存受管目录与媒体、配置和持久状态的真实路径关系，禁止把持久数据放进自动清理命名空间；配置文件符号链接也核对目标。
- 条目终态与批次计数在同一事务内保存，持久化错误传播至工作池并停止派发。批次终态提交失败时报告失败；持续不可写则保留活动状态待重启恢复，并输出服务错误日志。
- 改名先持久化 prepared 日志再移动；编号使用冻结预览和两阶段移动。恢复按实际位置和身份判定，证据不足进入 review；统计缓存不参与文件安全判断。

## 性能约束

目录扫描在文件操作全局锁外分两遍观测，每页最多 128 项、最多四个属性 worker；本地暂存 SQLite 复用于整轮扫描。完整观测后短锁发布 generation，失败保留旧完整代。发布核对直接子项和祖先的索引证据，其他目录变化不会单独导致本目录失败。观测不一致完整重试一次，普通读取错误和取消不重试。

标记每批最多 128 个文件，先复核再提交短事务，进度只统计已提交记录。后台标记结果缓存最多 128 条、终态保留最多一小时；断线恢复只查询原键，结果回收后需重新选择。编号任务每次查询续租，超过 20 秒未查询时停止。

目录进度缓存只保存直接子项计数，最多 8192 个目录，按事务触发器修订号失效后合并目录树。报告与选项共用 8 MiB / 32 项 LRU，按同一事务 report_revision 失效；大报告不缓存。统计由 SQL 筛选并流式汇总，精确分位数仍保留数值样本。

历史模型来源和 token 汇总按 entry/item 查找，相关测试检查 EXPLAIN QUERY PLAN。`sqlite_autoindex_token_usage_records_1` 对应 `(item,attempt)` 唯一约束，修改约束时同步指定该索引的查询和回归。外键反向、活动队列、日志排序、候选清理和分析时间均有索引验证。

截图缓存以增量账目维护容量；设置读取原子统计快照，不等待截图校验/删除持有的工作锁。批次详情目录汇总覆盖整批，按提交时目录排序后分页；终态运行记录完成最后一次读取后停止轮询。目录页有交互时延后应用更新，保留选择、编辑与播放状态。

首次统计、精确总数、深 OFFSET、包含搜索及全历史分位数仍有随数据量增长的成本。提高队列容量不等于提高磁盘或模型吞吐；真实 NAS 延迟与并发解码内存需要单独测量。

## 本地开发与完整回归

从仓库根执行。Go 编译嵌入 `web/dist`，因此必须先构建 Web；不提交或复制构建产物。

```bash
npm --prefix web ci
npm --prefix web run format:check
npm --prefix web run check
npm --prefix web run build
go mod verify
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go test -tags=integration -count=1 ./internal/media
go build -o bin/video-rename ./cmd/video-rename
npm --prefix web exec -- playwright install chromium
npm --prefix web run test:e2e
```

开发时先启动 Go 服务，再运行 `npm --prefix web run dev`，Vite 将 `/api` 代理到 127.0.0.1:8790。前端由 Prettier 管理格式，`check`/`build` 包含严格类型和未使用变量/参数检查。

Go 测试需要临时目录和本地 HTTP 监听权限。浏览器串行使用隔离服务器 **18790**、临时数据库、合成媒体、真实 FFmpeg 和本地假模型；不要占用该端口或复用真实服务。截图和 trace 在被忽略的 `web/test-results/`。

覆盖率另行生成：

```bash
mkdir -p coverage
go test -count=1 -coverprofile=coverage/go.out ./...
go tool cover -func=coverage/go.out
```

## 数据库规模与性能测试

```bash
# 30万索引、5万分页、2万标记/编号、提交、来源筛选、10万条目/请求统计
go test -tags=scale -run '^TestScale' -count=1 -timeout=15m -v ./internal/library ./internal/jobs ./internal/app ./internal/runstats
# 50万真实临时文件标记，以及53万文件/111目录扫描；顺序执行
go test -tags=scale_large -run '^TestScaleLarge' -count=1 -timeout=15m -v ./internal/library
# 完整历史筛选接口基准（夹具生成不计时）
go test -tags=scale -run '^$' -bench '^BenchmarkHistoryModelSource$' -benchtime=1x -count=1 -benchmem ./internal/app
# 11000条缓存账目保留检查（不含真实媒体和磁盘删除）
go test -run '^$' -bench '^BenchmarkCacheSweepRetained$' -benchtime=1x -count=1 -benchmem ./internal/framecache
```

普通规模测试预留至少 2 GB 临时空间，超大规模建议至少 5 GB。规模测试检查精确计数、候选清理及 SQLite 完整性/外键，不纳入默认快速测试。避免并行运行其他重负载后再作速度对比；本机合成数据结果不代表 SMB/NFS 实际吞吐。

## 依赖维护与构建版本

前端使用 Vite 8、plugin-vue 6、vue-tsc 3。TypeScript 固定 **6.0.3**：当前发布的 vue-tsc 3.3.12 实测无法加载 TypeScript 7.0.2 的 `typescript/lib/tsc`，不使用私有 API 补丁或关闭类型检查；更新类型工具后再验证 TypeScript 7。构建使用 `vite/client` 声明导入的样式类型。

YAML 使用 YAML 团队维护的 `go.yaml.in/yaml/v3`，不再依赖已归档的 `gopkg.in/yaml.v3`。SQLite 升级时沿用其要求的 libc 版本，并重新执行索引、持久化、竞态和规模测试。cron v3.0.1 虽发布时间较早，仍是当前稳定版；发布时间不是单独替换依据。

```bash
npm --prefix web outdated
npm --prefix web audit
go list -m -u all
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

版本与安全检查需要联网，结果仅代表执行时的公告数据库。依据：[YAML 维护声明](https://github.com/yaml/go-yaml)、[Vite 支持范围](https://vite.dev/releases)、[Vue 类型工具](https://github.com/vuejs/language-tools/tree/master/packages/tsc)。

Go 最低版本为 **1.27.2**。这是 1.27 系列的补丁升级，包含工具链与标准库安全修复；部署时需要重新构建并替换旧二进制，单独修改 `go.mod` 不会更新已经运行的程序。版本背景见 [Go 发布记录](https://go.dev/doc/devel/release#go1.27.0)。

构建版本按以下优先级确定：有效的 `-ldflags '-X main.version=…'` 显式注入 > Go 构建信息中的主模块版本 > 12 位提交号 > `dev`。覆盖值和模块版本先去除首尾空白，空值、`dev`、`(devel)` 不覆盖可用信息。版本输出与启动日志保留可取得的提交号和 `modified` 标记；缺少 VCS 信息不代表已核验干净状态。运行时不调用 Git，`--version` 不读取配置、不打开业务数据库或启动服务。

普通 Git 构建直接使用 Go 自 [1.24](https://go.dev/doc/go1.24#go-command) 起写入的主模块版本，不通过额外脚本解析 tag。新发布 tag 使用 `v主.次.修订`，例如 `v1.2.0`，预发布使用 `v1.2.0-rc.1`；现有 `1.0`、`1.1` 不重命名、删除或重打，也不作为 Go 模块版本。精确有效 tag 显示对应版本，其余提交保留 Go 的伪版本，未提交修改保留 `+dirty`。模块路径的主版本与子目录前缀须满足 [Go 模块版本规则](https://go.dev/ref/mod#vcs-version)。

在带 Git 元数据的检出中，先完成上述 Web 构建，再以包路径编译；`-buildvcs=true` 要求 Go 在无法取得所需 VCS 信息时报告错误：

```bash
go build -buildvcs=true -o bin/video-rename ./cmd/video-rename
./bin/video-rename --version
# 交叉编译只验证构建，不证明 Linux 实机运行
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=true -o bin/video-rename-linux-amd64 ./cmd/video-rename
```

Docker 上下文或源码包缺少 `.git` 时允许显式注入。先设置与实际源码对应的 `release_version`；不硬编码正式版本作为默认值，仍须先安装并构建 Web：

```bash
go build -ldflags "-X main.version=${release_version:?请先设置实际源码版本}" -o bin/video-rename ./cmd/video-rename
./bin/video-rename --version
```

无 Git 且未注入时显示 `dev`。普通 Git 构建不使用 `-buildvcs=false`，否则会丢失 Go 自动版本和 VCS 信息。版本只基于本地可见的提交历史与 tag；发布检出应带完整历史与标签，浅克隆或缺少标签时先补全，不能把伪版本误当作正式发布。构建不自动拉取或创建标签。

构建版本集成测试使用临时 Git 仓库和源码包，执行真实 `go build`，不改动本仓库标签。需要 Go、Git 和已构建的 Web 资源：

```bash
go test -tags=integration -run '^TestBuildVersion$' -count=1 -v ./cmd/video-rename
```

Web 私有包不独立发布。数据库结构号、缓存命名空间与编号快照版本有独立存储语义，不随应用版本变化。`web/public/favicon.ico` 是由 `web/public/logo.png` 生成并提交的静态资源（16–256px 多尺寸，圆角比例与工作台品牌标记一致）；更换 logo 时需同步重新生成。

仓库目前没有 CI 平台配置；CI 可依次执行上述原生命令。最新实测及未覆盖环境见 [验收记录](ACCEPTANCE.md)，历史方案和旧测量在 `archive/`。
