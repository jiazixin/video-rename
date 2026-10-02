# 开发与实现

应用为 Go 1.27.1 + Vue / TypeScript + SQLite 单实例服务。使用与部署见根 [README](../README.md)，行为约定见 [整体设计](DESIGN.md) 和 [运行说明](operations.md)。

## 模块入口

| 模块 | 职责 |
| --- | --- |
| cmd/video-rename、internal/app | 启动、HTTP装配、API参数与应用生命周期 |
| auth、config、storage | 登录/CSRF、部署配置、SQLite连接/完整建表/密钥/实例锁 |
| library、rename、naming、domain、discovery | 惰性索引、身份快照、无覆盖改名/编号/删除/恢复、标题与媒体类型 |
| jobs、scheduler、pipeline | 持久批次、动态并发、暂停取消与重试、Cron、媒体准备及模型推理 |
| media、framecache、sampling | ffprobe/FFmpeg、采样策略、截图租约及容量/保留管理 |
| models、vision、prompts、systemsettings | 模型配置/协议、提示词、资源和编号设置 |
| notifications、tokenusage、runstats | 通知队列、逐次请求用量、批次及条目性能 |
| web/src/app、layouts、components、features、lib、styles | Vue装配、布局、复用组件、业务域、共享技术能力及样式 |

## 持久化与文件安全

`internal/storage/schema.sql` 是唯一建表来源，空数据库内事务创建全部表与索引，当前结构标识 **20260929**。已有不支持结构拒绝启动且不修改；项目不提供旧库升级。实体 ID 使用 AUTOINCREMENT。写池单连接，读池最多4连接且设置 mode=ro/query_only；每次连接建立启用外键、FULL同步和busy_timeout。相关分页计数与数据读取共用只读事务，结束后才读取实时条目状态，避免嵌套占用读池。

数据库路径先解析符号链接，再选择实例锁、主密钥和SQLite辅助文件。已有数据库硬链接数大于1时拒绝打开，避免同一inode使用不同lock/key/WAL路径；拒绝时保留数据库和密钥。

library 构造受媒体根约束的父子路径，核验设备/inode；文件额外核验size/mtime。普通改名、还原与编号逐层openat禁止跟随符号链接，使用同一目录句柄核验源、无覆盖移动并同步：Linux为renameat2(RENAME_NOREPLACE)，macOS为renameatx_np(RENAME_EXCL)。提交后再次检查父路径；证据不一致保留待核对状态。

改名前先持久化prepared，再移动文件，最后提交索引、历史及标记。编号采用冻结预览、唯一临时名和两阶段移动；失败/取消恢复当前目录，已完成目录保留。启动恢复按实际位置、身份和内容判断；证据不足进入review并阻止相关范围。诊断保留失败现场，不通过重读挂载路径猜测原因。

## 任务与请求

运行创建检查索引目标、范围去重、请求键及重叠；请求内轻量祖先缓存避免重复查库。真实路径和祖先身份在worker扫描前核验，扫描页最多128项短事务写候选。重试规则、主/兜底模型及提示词保存不可变快照；模型名额按配置ID共享，只有并发数动态更新。

prepare受全局抽帧并发与缓存容量限制；等待/调用模型的截图持有租约。暂停停止新步骤、允许在途收尾并释放待推理租约；恢复优先复用缓存。取消向媒体、模型及退避传播。全局工具或鉴权故障停止批次，普通视频失败继续其他条目。重启将活动批次和未结束条目在同一事务内收尾为中断/取消。

目录标记使用应用生命周期的后台任务，相同键复用进度，断连继续执行，终态内存保留1小时。标记每批最多128个文件：在Library.mu下逐文件核验活动/待核对状态、当前索引快照及磁盘身份/内容，然后才打开短事务，复用预编译语句并保持WAL + FULL同步。遍历页读取基础字段；校验复用刚读取的文件快照，祖先仍逐次查库和核验磁盘，不跨批缓存路径证据。文件校验失败独立报告，其余文件可提交；SQL或提交失败回滚本批有效文件，取消时不统计未提交文件，进度只在成功提交后推进。已有提交不受后续失败影响。

标记结束输出一条`mark task finished`日志，含总耗时、目录刷新/文件校验/数据库写入耗时及已提交批次数。阶段耗时不包含全部锁等待、遍历查询和进度回报，不应将阶段之和视为完整总耗时。

目录编号任务通过查询续租，超过20秒未查询或显式取消后安全停止；服务关闭等待两类任务退出。

API使用Go方法路由、严格单JSON对象、未知字段拒绝及64KiB请求体限制。业务/媒体/诊断接口经过登录，写入校验同源与CSRF；秘密只存加密快照，诊断脱敏、图片负载省略。

## 前端与统计

文件目录采用页码查询，页码先收敛再计算OFFSET；旧generation+游标协议用于遍历。后台按ID原位更新，保留行序、勾选和滚动；主动刷新/翻页重新排序。目录会话保存分页/筛选/位置，最近30个列表快照用于返回定位。KeepAlive离页清理弹窗与媒体，异步加载通过序号/中止避免迟到覆盖。

`LibraryTableHeader.vue` 在sticky控制区显示表头，列表中不可交互表头只负责列宽；ResizeObserver同步宽度，两个横向容器同步scrollLeft。共享Modal引用计数锁定页面滚动，长文字按容器换行；PageNavigation处理跳页与窄屏，DateTime统一两行日期时间。图表按容器实际宽度绘制。

诊断复制统一使用 `web/src/lib/clipboard/clipboard.ts`：安全上下文优先 Clipboard API，缺失或拒绝时尝试原生选区复制；恢复焦点、选区与滚动，失败保留诊断原文并提示手动选取。窄屏Prompt卡片的标题和操作分行，长标题完整换行。

token_usage_records以(item,attempt)幂等，单事务推进请求账本和run_token_usage；按实际尝试配置归属，缺usage时只粗估文字。model_attempts保存各次请求诊断。runstats按实际开始/完成计算耗时，排队取消不计耗时；P50/P95为nearest-rank。统计区间为[from,to)，按IANA时区分桶；历史与抽帧日期筛选比较时间值，SQLite日期函数按毫秒精度处理，亚毫秒边界可能舍入。

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

前端格式由锁定版本的Prettier及 `web/.prettierrc.json` 统一管理，执行 `npm --prefix web run format` 整理Vue/CSS/TS。Vue保留多语句事件分号，模板保留空白语义；SVG固定文字的局部忽略用于保持原始文本。

默认Go测试包含隔离SQLite、文件操作、任务调度、认证及本地模型/通知HTTP夹具，需要临时本地监听权限。临时文件和端口与真实服务分开。

```bash
# 真实FFmpeg：横屏、竖屏4K、非方形像素、短视频及损坏文件
go test -tags=integration -count=1 ./internal/media
# 规模测试：30万索引/约1GB库、5万文件分页、2万编号预览/标记及多选提交
go test -tags=scale -run '^TestScale' -count=1 -v ./internal/library ./internal/jobs ./internal/app
# 超大规模：50万真实临时文件递归标记、精确计数及SQLite完整性，独立于常规scale
go test -tags=scale_large -run '^TestScaleLargeProcessedMarks$' -count=1 -timeout=15m -v ./internal/library
# Linux可执行文件交叉编译（不等于Linux运行验证）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/video-rename-linux-amd64 ./cmd/video-rename
# 浏览器：先构建bin/video-rename，需要真实FFmpeg和Chromium
npm --prefix web exec -- playwright install chromium
npm --prefix web run test:e2e
```

常规规模测试需要约2GB以上临时空间，`scale_large`建议预留5GB以上临时空间；两者均不纳入默认快速测试。浏览器夹具使用18790、本地假模型、临时媒体/配置/数据库；失败截图和trace位于忽略的web/test-results。真实模型质量、通知送达、Linux/CIFS性能需要相应环境实测。最新结果只记录在[验收记录](ACCEPTANCE.md)，历史计划与测量在archive中追溯。

仓库无CI平台配置；CI直接依次调用上述原生命令。开发模式先启动Go，再运行`npm --prefix web run dev`。图标生成命令是`node scripts/generate-favicon.mjs`，来源为web/public/logo.png，需要FFmpeg。
