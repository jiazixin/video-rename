# 开发与实现

应用为 Go 1.27.1 + Vue / TypeScript + SQLite 单实例服务。使用与部署见根 [README](../README.md)，行为约定见 [整体设计](DESIGN.md) 和 [运行说明](operations.md)。

## 模块入口

| 模块 | 职责 |
| --- | --- |
| cmd/video-rename、internal/app | 启动、HTTP装配、API参数与应用生命周期 |
| auth、config、storage | 登录/CSRF、部署配置、SQLite连接/完整建表/密钥/实例锁 |
| library、rename、naming、domain、discovery | 惰性索引、身份快照、无覆盖改名/编号/删除/恢复、标题与媒体类型 |
| jobs、scheduler、pipeline | 持久批次、动态并发、暂停取消与重试、Cron、媒体准备及模型推理 |
| libraryscan | 独立后台索引扫描、目录范围规范化、单轮去重、定时调度与停止 |
| media、framecache、sampling | ffprobe/FFmpeg、采样策略、截图租约及容量/保留管理 |
| models、vision、prompts、systemsettings | 模型配置/协议、提示词、资源和编号设置 |
| notifications、tokenusage、runstats | 通知队列、逐次请求用量、批次及条目性能 |
| web/src/app、layouts、components、features、lib、styles | Vue装配、布局、复用组件、业务域、共享技术能力及样式 |

命令入口分为 `main.go`（启动/退出）、`cli.go`（参数与帮助）、`version.go`（构建身份）。HTTP 应用的 `app.go` 仅管理依赖和生命周期，`routes.go` 统一装配，`http.go` 负责共同的请求/响应处理，`assets.go` 提供嵌入资源；目录、运行、计划、历史分别位于 `library.go`、`runs.go`、`schedules.go`、`history.go`。这些文件仍在同一 app 包内，沿用原有业务包边界。

部署 `config.Config` 与运行 `config.TaskConfig` 分离：任务链只接收从持久化模型/抽帧设置生成的快照，不复制监听、路径或登录字段。采样默认值和校验集中于 `config/sampling.go`。部署 YAML、模型/采样快照的字段协议不变。

批次的创建冻结、执行调度、逐项处理与查询分别位于 `jobs/create.go`、`execute.go`、`processing.go`、`runs.go`；编号名称规划、预览、查询、文件提交和恢复位于 `library/numbering_*.go`。拆文件不改变锁、事务、身份校验及恢复顺序。启动仅中断活动批次，不修正已结束批次的历史分类。

前端 `app/api.ts` 管理请求/会话，`app/contracts.ts` 声明 API 类型，`app/labels.ts` 管理跨页面标签；日期/大小展示来自 `lib/date` 和 `lib/bytes`。目录页的预览、多选和内联编辑在 library 域内各自拥有状态，页面协调目录加载与激活/停用。登录专属样式位于 auth 域。图标使用锁定的 `@lucide/vue`，不再导入废弃包。

## 持久化与文件安全

`internal/storage/schema.sql` 是唯一建表来源，空数据库内事务创建全部表与索引，当前结构标识 **20261005**。已有不支持结构拒绝启动且不修改；项目不提供旧库升级。实体 ID 使用 AUTOINCREMENT。写池单连接，读池最多4连接且设置 mode=ro/query_only；每次连接建立启用外键、FULL同步和busy_timeout。相关分页计数与数据读取共用只读事务，结束后才读取实时条目状态，避免嵌套占用读池。

会话校验、模型配置及并发数、处理设置、编号规则和固定提示词的独立只读查询使用读池，扫描写事务占用连接时仍可读取已提交数据。会话查询传播请求取消；登录、会话续期、退出和配置保存继续使用写池，保存中的策略合并仍在同一写事务内完成。

已校验有效的会话采用最多 100 毫秒的续期写入预算，并传播请求取消；写连接忙或续期失败时不重发 Cookie，继续使用已提交且尚未到期的会话，下次请求再尝试续期。成功提交后仍将期限滑动到 12 小时并保持原 Cookie 安全属性。续期不会异步执行，过期 / 撤销 / 指纹不符与 CSRF 校验仍在每次请求中进行。

数据库路径先解析符号链接，再选择实例锁、主密钥和SQLite辅助文件。已有数据库硬链接数大于1时拒绝打开，避免同一inode使用不同lock/key/WAL路径；拒绝时保留数据库和密钥。

library 构造受媒体根约束的父子路径，核验设备/inode；文件额外核验size/mtime。普通改名、还原与编号逐层openat禁止跟随符号链接，使用同一目录句柄核验源、无覆盖移动并同步：Linux为renameat2(RENAME_NOREPLACE)，macOS为renameatx_np(RENAME_EXCL)。提交后再次检查父路径；证据不一致保留待核对状态。

改名前先持久化prepared，再移动文件，最后提交索引、历史及标记。编号采用冻结预览、唯一临时名和两阶段移动；失败/取消恢复当前目录，已完成目录保留。启动恢复按实际位置、身份和内容判断；证据不足进入review并阻止相关范围。诊断保留失败现场，不通过重读挂载路径猜测原因。

## 任务与请求

运行创建检查索引目标、范围去重、请求键及重叠；请求内轻量祖先缓存避免重复查库。真实路径和祖先身份在worker扫描前核验，扫描页最多128项短事务写候选。重试规则、主/兜底模型及提示词保存不可变快照；模型名额按配置ID共享，只有并发数动态更新。

prepare受全局抽帧并发与缓存容量限制；等待/调用模型的截图持有租约。暂停停止新步骤、允许在途收尾并释放待推理租约；恢复优先复用缓存。取消向媒体、模型及退避传播。全局工具或鉴权故障停止批次，普通视频失败继续其他条目。重启将活动批次和未结束条目在同一事务内收尾为中断/取消。

缓存使用量随条目加入、重新校验、删除及抽帧预留 / 归还维护，不在每个候选的清理判断中重复汇总全部条目。`Cache.Stats` 读取独立原子快照，设置接口无需等待截图校验、manifest 持久化或删除持有的缓存工作锁；工作进行时返回最近完成的账目变化。快照不参与租约或容量决策，实际操作继续在原锁下核验文件、保护在用截图并控制预留容量。

目录标记使用应用生命周期的后台任务，相同键复用进度，断连继续执行，终态内存保留1小时。标记每批最多128个文件：在Library.mu下逐文件核验活动/待核对状态、当前索引快照及磁盘身份/内容，然后才打开短事务，复用预编译语句并保持WAL + FULL同步。遍历页读取基础字段；校验复用刚读取的文件快照，祖先仍逐次查库和核验磁盘，不跨批缓存路径证据。文件校验失败独立报告，其余文件可提交；SQL或提交失败回滚本批有效文件，取消时不统计未提交文件，进度只在成功提交后推进。已有提交不受后续失败影响。

目录按需刷新与后台扫描复用同一实现：全局锁外分两遍读取，每页最多128项、最多4个属性worker；通过固定目录句柄核验身份并暂存到本地 SQLite，只有完整观测成功后才短锁发布。失败或取消保留上一代完整索引，不提前提交可见条目的部分属性。未变化文件不重写observed，完整目录的观测时间使用directory_state.refreshed；身份替换和内容变化仍遵循原有ID/version规则。取消停止新读取并等待在途系统调用退出。

标记结束输出一条`mark task finished`日志，含总耗时、目录刷新/文件校验/标记写入耗时及已提交标记批次数。刷新细分为`refresh_readdir_duration`（目录枚举）、`refresh_metadata_duration`（并发属性读取的墙钟时间）和`refresh_index_duration`（其余协调、暂存、发布及错误记录）。这三项属于`refresh_duration`，不能重复相加；Go可能在ReadDir中获取属性，文件系统开销需同时看前两项。`write_duration`仅指标记事务，不包括索引写入。阶段耗时不包含全部锁等待、遍历查询和进度回报，不应将阶段之和视为完整总耗时。

目录编号任务通过查询续租，超过20秒未查询或显式取消后安全停止；服务关闭等待两类任务退出。

API使用Go方法路由、严格单JSON对象、未知字段拒绝及64KiB请求体限制。业务/媒体/诊断接口经过登录，写入校验同源与CSRF；秘密只存加密快照，诊断脱敏、图片负载省略。

后台索引扫描独立于 jobs 和 scheduler，默认开启、间隔 60 分钟，启动与每轮结束后等待间隔。设置保存于 system_settings.policy 的 library_scan 字段，扫描与处理设置在同一写事务内合并。应用生命周期管理扫描取消和退出，客户端断连不停止任务。定时和手动扫描共用单轮状态；目录轻量状态接口只读完整 generation，不触发按需扫描。

后台扫描与按需刷新共用每目录互斥。扫描在全局锁外以最多 4 路读取两遍元数据，使用固定目录句柄和 Fstatat 的不跟随符号链接选项；结果分批暂存到本地临时 SQLite，再校验目录身份后短锁发布。全库 indexEpoch 只提示可能存在索引变更：计数变化时，在最终发布事务内核对祖先的 parent/name/kind/identity/present，以及当前目录直接子项的 id/name/kind/identity/size/mtime/version；双向按 ID 连接比较，检查新增、移除和属性差异，仅执行一次，不在每页反复全量检查。祖先大小、修改时间和版本不参与路径判定；直接子项版本仍阻止改名后还原的旧扫描发布。其他目录的刷新或操作不再直接使当前扫描失效。当前目录 generation 始终独立核对。两遍观测或发布校验不一致时，后台只完整重扫一次；取消、普通读取错误及媒体根异常不重试，计数与错误只采用最终结果，持续失败保留两次诊断。诊断使用已捕获属性，包含具体阶段、条目及身份/大小/修改时间差异，不额外读取 NAS 来推测原因。

未变目录保留 generation；按需刷新与后台扫描采用相同的完整基线和两遍观测。失败保留旧完整代，成功重试回收异常退出留下的未发布候选。取消停止后续工作并等待在途系统调用退出。页面可见时每 5 秒只读索引状态，有新版本时静默更新当前页；勾选、编辑、播放及弹窗期间以固定定位的琥珀色通知提示手动应用，保留用户操作和列表布局。自动读取期间开始交互时延后应用，通知持续到新列表应用完成，离页隐藏。

单轮后台扫描复用一份本地暂存 SQLite，每目录先清除上一个目录的暂存记录；目录身份和索引基线仍重新读取。第二遍枚举从固定目录句柄打开独立偏移，发布前仍独立重开并核验完整祖先链。索引条目 ID 回写暂存也采用最多128项事务与预编译语句；子目录遍历每次读取最多128个ID，减少逐目录初始化和逐子目录查询。

发布新增候选按暂存 ID 查找主库条目；旧代清理从当前目录索引中分批取得 ID，在同一短事务内删除过期索引行与无引用的隐形候选。主库页提交后暂存 ID 回写失败也可据此回收。历史进程留下的无引用候选只在每轮建立暂存时通过读池捕获一次，按暂存父目录索引逐目录回收；删除前重新核对父目录、隐形状态、捕获的标记及索引引用，已发布代仍独立保护。单目录扫描单独建立暂存并执行一次捕获。仍使用 WAL 与 FULL 同步。

数据库查询使用对应索引：外键的独立反向索引避免清理候选时扫描引用表；活动批次查询只遍历run_active，排队调度使用run_status；批次明细按run/id、日志及失败按run/最大起止时间排序；历史批次条件动态构造以使用category/run索引。候选捕获使用只覆盖隐形扫描条目的部分索引，未完成操作与编号的空集检查使用部分索引。

HTTP目录和条目状态、批次与重试记录、统计和诊断读取传播请求Context，独立读取使用只读池。目录条目、展示路径的祖先集合、重试记录均批量加载；后台刷新使用应用生命周期Context，请求结束不会取消已经启动的后台刷新。

## 前端与统计

文件目录采用页码查询，页码先收敛再计算OFFSET；旧generation+游标协议用于遍历。后台按ID原位更新，保留行序、勾选和滚动；主动刷新/翻页重新排序。目录会话保存分页/筛选/位置，最近30个列表快照用于返回定位。KeepAlive离页清理弹窗与媒体，异步加载通过序号/中止避免迟到覆盖。

目录进度缓存只保存直接子项计数，最多8192个目录。entries、marks、run_items、runs及已发布directory_items的相关变更通过事务触发器更新directory_revision；读取在同一快照中获取目录树、修订号、完整generation和计数，只重新汇总变化目录，随后按目录树求和。未知状态实时读取，取消和回滚不泄漏未提交计数；缓存不保存磁盘身份校验结果。状态筛选仍按精确状态执行现有快照查询。

处理状态筛选在分页只读事务中批量汇总候选目录的递归视频状态，再将匹配目录应用于同一快照的计数和页查询；复用批量统计逻辑，避免为每行重复遍历子树。未索引或扫描有错误的子树视为待统计，不误判全部已处理。前端静默保序更新对目录使用本轮服务端分页的匹配ID，不将已过滤目录重新插入。

`LibraryTableHeader.vue` 在sticky控制区显示表头，列表中不可交互表头只负责列宽；ResizeObserver同步宽度，两个横向容器同步scrollLeft。共享Modal引用计数锁定页面滚动，长文字按容器换行；PageNavigation处理跳页与窄屏，DateTime统一两行日期时间。图表按容器实际宽度绘制。

诊断复制统一使用 `web/src/lib/clipboard/clipboard.ts`：安全上下文优先 Clipboard API，缺失或拒绝时尝试原生选区复制；恢复焦点、选区与滚动，失败保留诊断原文并提示手动选取。窄屏Prompt卡片的标题和操作分行，长标题完整换行。

token_usage_records以(item,attempt)幂等，单事务推进请求账本和run_token_usage；按实际尝试配置归属，缺usage时只粗估文字。model_attempts保存各次请求诊断。runstats按实际开始/完成计算耗时，排队取消不计耗时；P50/P95为nearest-rank。独立抽帧计入整体运行统计、趋势和批次明细，明细按记录的task标注抽帧且不使用模型；模型配置性能比较仅统计带模型配置的命名批次，不计入抽帧的结果和耗时。统计区间为[from,to)，按IANA时区分桶；历史与抽帧日期筛选比较时间值，SQLite日期函数按毫秒精度处理，亚毫秒边界可能舍入。

统计按SQL筛选时间、批次、来源及配置；运行分析的analysis_start_ms生成列采用与Go一致的RFC3339解析及UnixMilli边界并建立索引。明细流式汇总，精确P50/P95仅保留数值样本。报告与选项缓存共用最多8MiB/32项的LRU，版本来自同一读事务的report_revision，所有相关写入通过触发器失效；超出容量的报告不缓存。缓存保存完整排序批次行，分页复用汇总并返回独立副本。前端翻页固定当前时间区间，手动刷新或筛选变化才更新区间；终态批次日志完成最后一次读取后停止定时轮询，重新展开仍可读取。

运行记录的模型来源计数和分页关联通过 `INDEXED BY item_entry` 按文件条目查找请求，避免缺少统计信息时 SQLite 按 `UNIQUE(run,identity)` 为每条记录扫描整批任务。每行 Token 汇总同样按条目和现有 `UNIQUE(item,attempt)` 索引查找；该隐式索引名为 `sqlite_autoindex_token_usage_records_1`，修改唯一约束时需同步查询及计划回归。成功仍选生成标题的成功请求，失败仍选最后请求，Token 仍汇总同批次、同条目的全部尝试。当前完整结构同时包含批次、日志排序、外键与候选清理索引；旧结构不提供迁移。

## 构建与验证

所有命令从仓库根执行。编译任何依赖web/embed.go的Go目标前，必须先生成web/dist；它是唯一资源输出，不复制或提交产物。

```bash
npm --prefix web ci
npm --prefix web run format:check
npm --prefix web run check
npm --prefix web run build
go mod verify
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o bin/video-rename ./cmd/video-rename
```

前端格式由锁定版本的Prettier及 `web/.prettierrc.json` 统一管理，执行 `npm --prefix web run format` 整理Vue/CSS/TS。Vue保留多语句事件分号，模板保留空白语义；SVG固定文字的局部忽略用于保持原始文本。`web/tsconfig.json` 开启 `noUnusedLocals` 与 `noUnusedParameters`，既有 `check` 和 `build` 会拒绝闲置导入、局部变量与参数；这不等于验证所有导出符号都有调用方。

### 构建版本

`--version` 和启动日志共用 `cmd/video-rename/version.go`。普通构建明确显示 `dev`，读取 Go 内嵌的 `vcs.revision`（前 12 位）与 `vcs.modified`；源码副本无 VCS 信息、或指定 `-buildvcs=false` 时只显示 `dev`，不会猜测发布版本。运行二进制不需要 Git，也不读取运行目录中的仓库。语义依据见 [Go BuildSetting](https://pkg.go.dev/runtime/debug#BuildSetting)。

发布构建从已确定版本的干净标签检出执行，先完成 Web 构建，再用标签注入版本。以下命令拒绝未提交变更或没有指向当前提交的标签：

```bash
test -z "$(git status --porcelain)" &&
release_version="$(git describe --tags --exact-match)" &&
go build -buildvcs=true -ldflags "-X main.version=$release_version" -o bin/video-rename ./cmd/video-rename &&
./bin/video-rename --version
```

私有 Web 包不独立发布，不维护另一份应用版本；依赖版本仍由 npm 清单与锁文件控制。数据库结构号、缓存命名空间与编号快照版本各有存储语义，不与应用版本联动。构建身份不替代 Web 预构建检查，必须遵守上述资源生成顺序。

### 专项验证

默认Go测试包含隔离SQLite、文件操作、任务调度、认证及本地模型/通知HTTP夹具，需要临时本地监听权限。临时文件和端口与真实服务分开。

```bash
# 真实FFmpeg：横屏、竖屏4K、非方形像素、短视频及损坏文件
go test -tags=integration -count=1 ./internal/media
# 规模测试：30万索引/约1GB库、5万文件分页、2万编号预览/标记及多选提交
go test -tags=scale -run '^TestScale' -count=1 -v ./internal/library ./internal/jobs ./internal/app
# 10万请求/条目统计，数据库筛选与跨页缓存
go test -tags=scale -run '^TestScaleAnalytics' -count=1 -v ./internal/runstats
# 扫描查询对比：50万合成索引、100目录清理与候选发布，隔离数据库开销
go test -tags=scale -run '^TestScaleDirectoryScanCleanupWithLargeIndex$' -count=1 -v ./internal/library
# 运行记录来源筛选：同批11000条、600条兜底，精确计数、首末页和Token汇总
go test -tags=scale -run '^TestScaleHistoryModelSource$' -count=1 -v ./internal/app
# 同一隔离夹具的完整接口耗时，不含记录生成
go test -tags=scale -run '^$' -bench '^BenchmarkHistoryModelSource$' -benchtime=1x -count=1 -benchmem ./internal/app
# 截图缓存保留检查：11000条内存账目，隔离排序与容量判断开销，不涉及磁盘或媒体
go test -run '^$' -bench '^BenchmarkCacheSweepRetained$' -benchtime=1x -count=1 -benchmem ./internal/framecache
# 超大规模：50万真实临时文件递归标记、精确计数及SQLite完整性，独立于常规scale
go test -tags=scale_large -run '^TestScaleLargeProcessedMarks$' -count=1 -timeout=15m -v ./internal/library
# 超大规模扫描：多层目录内53万真实临时文件，首次索引/未变复扫/候选清理及SQLite完整性
go test -tags=scale_large -run '^TestScaleLargeDirectoryScan$' -count=1 -timeout=15m -v ./internal/library
# 属性延迟实验：512临时文件，每次Info注入2ms延迟；用于对比并发，不能代表真实SMB吞吐
go test -tags=scale -run '^TestScaleRefreshDelayedMetadata$' -count=1 -v ./internal/library
# Linux可执行文件交叉编译（不等于Linux运行验证）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/video-rename-linux-amd64 ./cmd/video-rename
# 浏览器：先构建bin/video-rename，需要真实FFmpeg和Chromium
npm --prefix web exec -- playwright install chromium
npm --prefix web run test:e2e
```

常规规模测试需要约2GB以上临时空间，`scale_large`建议预留5GB以上临时空间；两者均不纳入默认快速测试。浏览器夹具使用18790、本地假模型、临时媒体/配置/数据库；失败截图和trace位于忽略的web/test-results。真实模型质量、通知送达、Linux/CIFS性能需要相应环境实测。最新结果只记录在[验收记录](ACCEPTANCE.md)，历史计划与测量在archive中追溯。

仓库无CI平台配置；CI直接依次调用上述原生命令。开发模式先启动Go，再运行`npm --prefix web run dev`。图标生成命令是`node scripts/generate-favicon.mjs`，来源为web/public/logo.png，需要FFmpeg。
