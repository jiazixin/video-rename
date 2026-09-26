# 开发与实现

当前应用为 Go 1.27.1 + Vue / TypeScript + SQLite 单实例 Web 服务。原始 CLI 设计保留在 `docs/plans/` 用于追溯，当前使用协议见根 README 与 [运行说明](operations.md)。

## 边界

- `internal/app`：HTTP 路由、中间件装配、静态资源和生命周期。
- `internal/auth`：随机令牌、哈希会话、12 小时滑动过期（活跃自动续期）、CSRF、同源校验、登录限流。
- `internal/storage`：SQLite schema、WAL、FULL 同步、单连接短写事务、实例锁。
- `internal/library`：根约束、文件身份、目录索引、统一改名与恢复。
- `internal/models`：模型配置、服务预设、密钥编辑、模型检测、不可变运行快照。
- `internal/jobs`：磁盘候选、运行状态、动态并发调度、取消及去重。
- `internal/scheduler`：五段 Cron、显式时区、预览、唯一触发与重启不补跑。
- `internal/pipeline/analyze.go`：复用既有探测、抽帧、模型处理，不执行旧 JSONL 提交。
- `web/src/features`：auth、library、history、runs、models 五个业务域；目录选择器通过 library 的公开入口供运行页面复用。

数据库 schema 由 `internal/storage/` 维护：`store.go` 创建基础表，`migrations.go` 按版本事务升级。schema v4 将原提示词表扩展为 model_configs，保留原 ID 与正文并新增服务设置和加密密钥；任务、计划绑定 model_config_id。运行创建时保存加密的整套配置快照；API 只返回脱敏摘要。schema v5 为失败事件增加按需读取的请求 / 响应诊断，密钥脱敏、图片负载省略，每个字段最多 128 KiB。模型配置行为见 [模型配置](models.md)。

## 构建顺序

```bash
npm --prefix web ci
npm --prefix web run build
go build -o bin/video-rename ./cmd/video-rename
```

`web/embed.go` 直接嵌入 `web/dist`。不复制资源到后端目录，不提交占位 HTML 绕过嵌入检查。`web/dist`、`bin` 和测试产物均忽略。

## 验证命令

```bash
npm --prefix web run check
npm --prefix web run build
go test ./...
go test -race ./...
go vet ./...
go test -tags=integration ./internal/media/
go test -tags=scale ./internal/library -run TestScaleDirectoryPagination -v
go build -o bin/video-rename ./cmd/video-rename
npm --prefix web run test:e2e
```

浏览器首次运行先在 `web` 内执行 `npx playwright install chromium`。浏览器测试的 fixture 负责创建临时配置、数据库、合成视频及本地假模型；自动命名用真实 FFmpeg，模型响应是可控测试响应，不代表用户真实模型联调通过。

## 已覆盖的风险

认证 / CSRF / 退出失效；根和符号链接约束；目标冲突；手动版本校验与请求幂等；目录改名后的历史；重启恢复 prepared；逐次还原与防重复；硬链接候选去重；取消及分析期间源变化；计划预览、时区、重复触发和重叠；分层分页及大规模索引；实际浏览器登录、手动改名、还原、日志、分页、计划和真实抽帧链路。

旧 pipeline / vision / media 的回归测试继续保留，确保沿用能力不回退。旧命令参数测试已替换为 Web 服务参数协议测试。

## 目录多选与播放

目录页多选按当前目录保存跨页 ID；本页全选 / 反选不加载其他页。自动命名复用运行 API，每批最多 100 个目标，服务端继续执行范围和重复校验。批量还原复用逐项恢复 API，保留失败项，不提供跨文件事务回滚。

多选期间，后台目录刷新在响应到达时检查当前选择，按 ID 原位更新已有行，仅在本页有空位时追加新条目，避免迟到的索引结果移动勾选行或把它挤出当前页；主动刷新、翻页和筛选仍使用最新列表。

`GET /api/entries/{id}/video`、`/audio` 与 `/image` 预览经过会话认证；library 以受根目录约束的文件描述符打开、核对文件身份和祖先路径，HTTP 层使用 ServeContent 提供 Range / seek，不把整段媒体文件读入内存。音频按扩展名固定 Content-Type，避免各平台探测不一致。不提供转码。播放器关闭时暂停、移除 src 并释放资源。共享 Modal 使用引用计数锁定页面滚动，最后一个弹窗关闭后恢复原有样式。

## 原位更新、处理标记与目录汇总

schema v6 将 marks.operation 改为可空，手动状态标记使用 source=marked、operation=NULL；不创建伪改名历史。取消标记删除对应文件身份/大小/修改时间的保护，不撤销操作历史。升级在事务内保留所有已有标记。

POST /api/entries/processed 接收 targets（1–100）和 marked，递归扫描现有目录，逐视频核对路径与快照，使用 library 的改名互斥锁修改标记。重复/重叠目标去重；返回 changed、unchanged、failed 和最多 100 条错误详情。活动任务中的文件不允许修改标记。新视频与内容变化后的文件不继承标记，硬链接共享身份标记。

GET /api/entries/status?ids=... 返回最多 300 个条目的当前状态（不存在的条目省略），不会启动目录刷新。前端按 ID 合并，不重排；运行从排队到终态持续查询，避免扫描前尚无 run_items 时停止轮询。导航及更新请求序号阻止迟到响应覆盖新页面。成功改名原位维护 directory_items 名称，不清空分页索引。

目录 progress 汇总 SQLite 中当前可达的完整子树：total、processed、unknown（未完整索引/扫描失败目录）、active。手动与自动命名、手动状态标记均计入 processed。unknown 非零时不宣称全部已处理；汇总不触发全库磁盘扫描，外部变更在目录扫描后反映。

目录内容列与递归处理进度分别计算：只读取 `directory_state` 当前完整 generation 对应的 `directory_items`，统计该目录的直接子目录、视频、图片和音频（`contents.audio`）；未索引或扫描报错时返回空统计状态，前端显示“待统计”。扫描进行中保留上一次完整索引的数字，完成后由目录列表刷新更新。`DirectoryContents.vue` 统一呈现目录行与刷新按钮前的两行两列紧凑统计，仅显示图标及等宽数字，类型名称由悬停提示与无障碍标签提供，不使用横向长条边框和分隔线；统计不受分页、搜索或类型筛选影响。

## 递归目录编号

目录顶部编号通过 `POST /api/directories/{id}/numberings/tree-preview` 刷新并遍历真实目录树，忽略符号链接；按文件夹创建包含图片和音频的 `mixed` 编号预览，沿用既有日志、冲突检测、两阶段改名及还原。图片和音频分别使用保存的规则，共用序号时音频规划使用图片的目标名称预留号码。前端按父目录在前的顺序调用既有 apply 接口，避免子目录内改名造成父目录预览指纹失效；失败停止后续目录并显示已完成数量。`GET /api/directories/{id}/numberings/tree` 汇总当前可达子树最近 100 次编号操作，供刷新后还原及异常恢复。

## 固定命名失败协议

固定提示词默认要求标题不少于 25 个中文字符、建议 30–50 个中文字符，仅输出一个完整的单行中文标题。默认文本不再包含命名失败输出协议；已有自定义文本保留，恢复默认并保存后采用新值。模型请求除固定提示词外不追加页面不可见的硬编码文本。

最终标题文本包含“命名失败”时，vision 返回非全局 NAMING_FAILED，不重试、不进入文件名清洗或改名。复用任务失败处理：保留原文件名、不写已处理标记，写入任务历史失败记录及脱敏请求/响应；其他视频继续处理。

## 运行页面分区

批次记录通过 `GET /api/runs?category=all|manual|automatic&task=rename|extract&page=1&size=50` 获取分页，返回 `items/total/page/size/profiles`。task 留空表示全部任务。附加筛选为 `status`（原批次状态值）、`profile`（模型配置 ID，0 为无配置）、`from/to`（RFC3339，开始包含、结束不含）、`duration`（under1 / 1to5 / 5to30 / 30to60 / over60）。时间取实际开始时间，缺失回退提交时间；耗时按秒计算，不含排队，进行中取当前时间，缺少开始时间不匹配耗时区间。所有条件在同一读事务内先筛选、统计再取页；profiles 返回历史批次中的配置 ID 和名称，保留已删除配置的筛选能力。每页支持 50 / 100 / 200 条，页码越界收敛至最后一页。无 category 的列表接口继续供既有游标调用使用。页面显示总批次、页码和当前页条数；全部、手动、自动分别保存页码与条数设置，其他筛选切换重置页码，轮询和刷新保留当前页，切页与筛选请求通过序号防止迟到响应覆盖。

侧栏「运行管理」下的「计划管理」和「批次记录」分别使用 `#plans` 与 `#runs`。Runs.vue 按 view 分别展示计划管理或批次列表，切换时重新挂载，仅请求所属页面的数据。计划管理上半部分为手动计划（创建后立即提交一次批次），下半部分为持久化自动计划；两个创建弹窗均提供命名 / 抽帧类型。schema v21 为 schedules 增加 task，旧计划默认 rename；抽帧计划保存 model_config_id=0，跳过模型校验，定时触发与立即运行均传递 task。批次页顶部统一提供组合筛选。source=schedule 或 plan>0 的运行归计划触发，包含计划卡片上的立即运行；其余归手动运行。记录按批次编号倒序分页。

运行中批次的预计剩余时间是纯前端估算（Runs.vue 的 trackEta），不新增接口或轮询频率：每次既有 3 秒轮询用已处理增量（done+skipped+failed）计算瞬时速率并做 EMA 平滑（α=0.3），无增量的轮询保留上次有效速率；首次看到 running 状态的轮询作为基线，回退平均速率与预热均基于该基线的差值，避免扫描期瞬时跳过项抬高速度。预热条件为处理差值达到 warmup 阈值（总数≤2 恒不显示；3–10 取 max(2, ⌈总数/2⌉)；>10 取 10）、running 超 5 秒且存在有效速率，满足前显示“预计时间计算中…”。批次离开 running 状态即停止估算并清理状态；移动端窄屏隐藏剩余时间片段。

目录选择器与文件目录页共用 library/DirectoryStatus.vue，显示递归处理状态、已处理/总数及活动数量；保留待统计状态与原有多选、层级、分页行为。

## 批次通知与诊断扩展

schema v7 新增 notification_channels、notification_deliveries 与 runs.started。独立 notifications worker 每 3 秒发现新终态批次、持久化通知并发送；每渠道/批次唯一键去重，启动恢复 sending 为 pending，失败最多三次（30 秒、60 秒退避）。关闭配置取消待发送项；重新启用从最新批次 ID 开始。通知失败不修改任务状态。HTTP 禁止重定向，限制响应 64 KiB，校验渠道业务码，错误文本不回显远端正文或含密钥 URL。

schema v8 为 operations 添加脱敏请求/响应，并为 run_items 添加 original_path。成功模型结果经 pipeline 捕获后与 prepared 操作一起持久化，完成或崩溃恢复时写入成功事件，诊断接口支持成功与失败记录。图片负载省略、凭据脱敏、每项 128 KiB 限制沿用既有实现；旧记录不补录。GET /api/runs/{id} 返回目标显示路径，items 返回原始与当前路径，详情支持每页 100 项翻页。

协议参考：[企业微信机器人](https://developer.work.weixin.qq.com/document/path/91770)、[Telegram sendMessage](https://core.telegram.org/bots/api#sendmessage)、[钉钉加签](https://open.dingtalk.com/document/robots/customize-robot-security-settings)、[Bark](https://github.com/Finb/Bark/blob/master/README.zh.md)。渠道真实凭据未联调，自动测试使用本地模拟服务。

## 目录排序与时间

名称使用 SQLite NATURAL_NAME 排序规则，逐段比较数字，支持超长数字和确定性的同值排序；目录始终在前。大小、文件修改时间、重命名时间支持升降序，同值按自然名称与 ID 排序，游标采用同一排序组合，切换排序回到第一页。目录大小显示“—”，大小排序下目录按名称排列。

重命名时间来自该条目最近一次 committed 操作的 created 时间，包含手动、自动命名和还原；处理标记、失败操作与外部改名不计入，没有记录显示“—”。操作记录与展示历史分开，因此清空任务历史不会清除此时间。原文件 mtime 继续显示为“文件修改时间”。

## Bark 通知正文

Bark 使用标准 `/push` JSON 接口：从以设备 Key 结尾的配置地址提取 device_key，保留自建服务的路径前缀，显式发送 title、body、group 和 isArchive。标题显示批次成功/失败/取消，编号、成功/失败/跳过/未完成数量、开始/完成时间、耗时、来源和目录置于正文。测试覆盖末尾斜杠与路径前缀，并从批次记录经队列到本地 HTTP 接收端校验完整正文；真实手机显示仍需运行新版服务后验证。

## 排队范围占用

创建批次在 Manager 的创建互斥锁内检查全部 queued/scanning/running/cancelling 范围，拒绝不同批次的相同文件或重叠目录，并在错误中给出已存在的批次编号；相同请求键仍返回原批次。递归标志参与范围判断，不重叠的批次仍可排队。

条目状态在 run_items 尚未生成时回查持久化 runs.targets，覆盖直接目标、递归后代及非递归目录的直接文件，刷新不会丢失排队状态。目录状态同步显示批次状态；取消或结束批次会释放占用。已结束的文件明细不再被范围回查显示为排队。


## Logo 与并发更新

左上角与登录页统一使用 B 款折带 V（墨绿底、米白 V、薄荷绿折面），由内置图像生成工具根据已选方案提取并定稿。唯一图像源为 `web/public/logo.png`；`web/public/favicon.ico` 是同源派生文件，包含 16/32/48/64/128/256 像素尺寸。修改源图后执行 `node scripts/generate-favicon.mjs`（需要 FFmpeg）再构建。

批次处理采用有界动态调度：派发前及等待期间每 500 毫秒读取原模型配置的 workers，每个视频使用独立配置副本，其他请求参数维持创建时快照。降低上限不会取消在途处理；删除配置保留最近已知并发值。取消时停止派发并等待在途处理结束，再写批次终态。

## 通知时区与上下文诊断

schema v9 保存 notification_preferences.timezone。认证完成后浏览器提交 IANA 时区，后台生成新通知时按此时区转换，包含 UTC 偏移和时区名称；关闭浏览器或服务重启后沿用已保存设置。未同步前默认为 UTC，已经生成的通知正文保留原值。程序内嵌 tzdata，容器缺少系统时区文件时仍可转换。

新请求诊断在本地脱敏副本的 _local_context 中保存请求字节数、图片张数、输出上限及文字 Token 粗估（ASCII 字符数 / 4，其他字符数 × 1.5）。该元数据不发送给模型，也不额外请求计数接口。文字估算不含图片与协议开销，不代表完整上下文；自定义模型需其 tokenizer 和图像处理规则才能准确计算。诊断面板优先展示响应 usage 的实际输入 Token，Anthropic 输入包含缓存读写；旧记录没有元数据时明确显示无法还原。1K = 1000 Token，KiB 为 1024 字节。

## Web 抽帧设置

schema v10 增加 sampling_settings 单行设置与 runs.sampling_snapshot。internal/sampling 读写全局参数，复用 config.FrameSettings 校验；GET/PUT /api/sampling 复用认证、CSRF 和严格 JSON 解码。创建批次读取设置并记录快照，执行时校验并恢复派生采样表，图片参数也进入快照。YAML sampling/images 字段移除，不导入、不兼容。前端 features/sampling 提供规则表及基础表单，沿用 SelectField。

## 图片与音频一键编号

系统设置的编号规则页使用图片 / 音频切换，分别编辑命名前缀、最小位数、编号方式与扩展名大小写，并实时显示文件名格式示例（实际号码以编号预览为准）。共用序号独立放在公共设置中；切换类型保留未保存草稿，底部统一保存两种类型并显示修改状态。详细匹配与保留号码语义收在“规则说明”，窄屏采用单列布局。

schema v11 增加 image_numberings 和 image_numbering_items；schema v16 增加 media 列区分图片与音频批次，两类共用同一套移动日志与崩溃恢复机制，编号记录按类型分开查询。schema v17 在 system_settings.numbering 以 JSON 保存编号规则（最小位数、前缀、扩展名小写、紧凑重排、图片音频共用序号），空值回退到与历史行为一致的默认；`GET/PUT /api/numbering/settings` 读写，预览时读取当前规则并把最终文件名冻结进批次条目，应用与整批还原不依赖之后的规则修改。接口为 `POST /api/directories/{id}/numberings/preview`（body 指定 media）、`GET /api/directories/{id}/numberings?media=` 与 `/api/numberings/{id}` 下的 apply / restore-preview / recover。服务端复用目录排序表达式，读取完整当前目录索引，按搜索过滤所选类型文件，冻结预览中的文件身份、版本、原名、新名及目录指纹；确认时重新校验。编号为正整数，前导零规范化，0 和非纯数字名称进入续排；使用大整数避免长数字溢出。不同格式共用序号，未参与的编号文件保留其号码。硬链接重复参与时拒绝操作。

修改项先全部转唯一临时名，再转最终名。每次移动前写入 pending_name，使用平台 no-replace 改名并同步目录后记录 current_name。最终在单一数据库事务中提交索引、原名到最终名的历史以及批次状态；临时名不出现在历史中。数据库提交结果不确定时先查持久状态，不盲目回滚。中断批次在启动时依据文件身份与 current/pending 证据做两阶段回滚；证据不一致或原名被外部文件占用时标记 review，阻止相关范围改名和扫描，用户可在编号记录中重试恢复。

编号记录显示当前目录最近 50 次已执行操作。整批还原先预览，要求每张改动图片仍对应原批次操作且未发生后续改名或内容变化，恢复原名及扩展名原始大小写；使用同一持久化移动机制。预览无文件修改，过期预览按日清理；已执行日志保留。接口统一走登录和 CSRF 校验，应用请求只提交服务端预览 ID，不接受客户端指定任意目标文件或名字。

## 系统设置与抽帧缓存

系统设置集中提供处理与缓存、模型配置、抽帧设置和通知设置四个页签。schema v12 的 system_settings 保存抽帧并发（1–32，默认 8）与缓存上限（1–10000 GB，默认 10，十进制）。GET/PUT `/api/system/settings` 沿用登录、同源和 CSRF 校验。目录由 YAML `tmp-storage` 指定，默认 `../.cache`，相对配置文件解析。

jobs 同时运行最多 20 个活动批次，各自完成扫描与稳定性观察（默认 30 秒，可配置为 0 禁用等待）。抽帧调度在取得全局抽帧名额后派发，截图准备完成即释放抽帧名额，不再限制每批等待模型的视频数量；等待模型的截图持有缓存租约，以临时目录容量限制预取，容量不足等待释放后继续。全局抽帧限额与按模型配置 ID 共享的模型限额独立，排队不占用另一个阶段的槽位。配置降低并发时不打断已开始工作；模型其他参数仍使用任务快照。取消只停止对应运行，普通单视频失败记录后继续其余视频；全局依赖故障停止对应批次。

framecache 在 `tmp-storage/video-rename-frames-v1/` 中维护 SHA-256 键到截图清单的映射，键包含缓存版本、源文件 dev/inode/size/mtime、抽帧与图片参数，不包含模型或提示词。清单保存相对路径和内容校验和，原子替换；缓存目录有实例锁。完整输出在失败或取消后保留，文件与参数匹配且校验通过时重用；AutoCommit 成功后才删除。排队、请求与改名期间用租约保护截图。中断抽帧的半成品清理，重启清理遗留 pending 工作目录并加载完整清单，原批次仍标记 interrupted，需要用户重新运行。

容量计算包含每项清单预留 64 KiB；抽帧预约最大帧数 × 1 MiB 加 32 MiB 中间文件空间。媒体提取及时删除失败和重压缩原图。容量不足优先淘汰最久未使用的闲置缓存，全部占用时暂停生产；降低容量可能暂时低于正在使用的空间，后续释放后收敛。每分钟清理连续 72 小时未使用的闲置缓存，启动时也执行清理。只管理专属目录中生成的缓存，媒体、配置和数据库不参与清理。

验证覆盖模型阻塞时单批 140 个视频全部提前抽帧（跨分页）、容量满后暂停及提交释放后的补位、等待容量时取消、跨批次预取与共享模型上限、失败/取消后复用、提交后删除、重启加载、缓存键失效、同尺寸内容损坏、容量等待取消、占用期间不清理、闲置淘汰、配置路径与鉴权，以及设置页桌面/窄屏和刷新持久化。

### 独立抽帧与统一记录

schema v20 为 `runs` 增加 `task`（`rename` / `extract`，旧批次及省略参数默认 `rename`），新增独立 `extraction_records` 表与状态分页、批次索引。`POST /api/runs` 的 `task: "extract"` 使用既有目标、递归、请求键、扫描、稳定性检查、全局抽帧并发和取消机制；忽略模型配置 ID、不读取模型或提示词，保存创建时的抽帧参数快照。相同请求键不能切换任务类型，抽帧和重命名的重叠范围统一互斥。

抽帧扫描包括已处理视频，完成后只释放缓存租约，保留截图，不创建改名操作或处理标记。手动抽帧和自动命名均调用 `jobs.prepareFrames`，按文件身份及参数取得缓存、校验抽帧后的源快照并记录结果。日志在准备阶段进入时写开始，在该阶段返回时写结束与毫秒耗时（包含缓存容量等待，不含批次扫描、稳定性观察、抽帧槽位排队和模型推理）；缓存命中保存 `cached=true` 及帧数。工具缺失、媒体失败等保存摘要与最多 128 KiB 诊断；取消记录失败原因，重启时将遗留 running 记录收尾为“服务重启，抽帧已中断”。后续推理或改名失败不会回写已经成功的抽帧记录。旧历史不补录。

- `GET /api/extractions`：`q` 搜索路径或失败原因，`run` 筛选批次，`from` / `to` 为 RFC3339 日期，`limit` 为 50 / 100 / 200；`success_before`、`failure_before` 是独立 ID 游标。单事务返回 `{success:{items,total,next},failure:{items,total,next},running}`；列表不加载诊断正文。
- `GET /api/extractions/{id}`：按需返回 `processing_log`。列表与详情均登录保护、`no-store`。
- `GET /api/logs/batches?category=extraction`：列出抽帧记录中的非零批次。
- `POST /api/extractions/clear`：`{all:true}` 清除已结束记录；沿用 CSRF 与同源校验，保留进行中记录和缓存。

前端入口为当前目录刷新左侧、跨页多选工具栏、计划管理的“新建手动计划 → 抽帧”；抽帧弹窗无需模型，默认递归，自动计划也可选择抽帧。`features/history/ExtractionHistory.vue` 为侧栏“抽帧记录”独立页面（`#extractions`），成功/失败按钮沿用运行记录样式，一次显示一种结果，切换保留各自分页。展示日期、耗时、批次（0 显示“—”）、来源、文件名与错误明细；支持轮询、筛选、复制与清除。回归覆盖无模型运行、请求幂等、标记保持、抽帧后命名复用、手动 / 自动失败记录和取消中断；`web/e2e/extraction.spec.ts` 使用真实 FFmpeg 与本地假模型验证三个入口、抽帧计划持久化与立即运行、缓存复用，并检查窄屏布局与无批次显示。

### 可配置的稳定性等待

schema v13 为 system_settings 新增 stability_seconds，默认 30，范围 0–3600。升级保留既有并发与容量；0 明确禁用观察等待。每个批次在扫描前读取设置，扫描后有候选才开始计时；等待可取消，设置变更不会修改已开始批次的等待。原来的文件快照核对与提交前核对保持执行。设置页将它作为“处理与缓存”的第三张卡片。测试覆盖默认与升级、0 持久化、非法值、禁用等待后立即执行、等待期间文件变化跳过、等待中取消及修改设置不缩短已有等待。

## 本页选择、批次还原与媒体诊断

运行列表从创建时的 config_summary 显示模型 ID；任务历史通过事件的批次编号关联同一快照，手动操作不关联模型。运行批次的 token 数写入独立的 run_token_usage 表，每个视频记录一次可用的模型诊断。优先取 usage.total_tokens，其次取输入与输出 usage 之和；缺失时回退到请求中的文字粗估。旧批次不补算；清除任务历史不影响新批次已保存的累计值。图片 Token 与无法还原的请求重试不包含在文字粗估中。

目录选择器的全选、反选与取消仅修改当前分页的子目录 ID，保留当前目录和其他分页选择。运行记录统一显示还原批次、取消、详情；活动批次不能还原。POST `/api/runs/{id}/restore` 读取该批次所有成功自动改名的 operation，逐项调用原有 Restore 校验并提交，返回成功、已还原跳过、失败数量及最多 100 条失败详情。不会撤销后续改名或覆盖冲突文件；非跨文件事务，重复请求跳过已还原项，失败后可重新尝试。

schema v14 增加 events.processing_log。抽帧失败保存每个时间点首次及补试错误、工具路径、视频流/尺寸、预算与最终摘要，日志有容量限制；ffprobe 的 stderr 同样持久化。诊断接口沿用登录与 no-store，详情默认折叠，按需加载并支持复制。旧历史不补录。设置页移除与页签重复的大标题。

## 用量分析

schema v15 新增 token_usage_records 请求级用量账本：每条记录批次、run item、毫秒时间戳、token 数与估算标记；item 唯一键使写入幂等。批次累计仍写入 run_token_usage，两者在同一事务内推进，重复记录不会重复累计；无可计数的交换两端都不写。写入失败记录服务端错误日志，不影响改名结果。升级时把已有非空批次累计各回填一条 legacy=1 记录，时间取实际开始（缺失用提交时间），计数原样迁移、重开不重复；更早未记录的批次保持"未记录"，不从可清除的事件表猜测恢复。

GET /api/usage/analytics 在登录保护下聚合账本：`[from,to)` 半开区间（RFC3339，from 可省略表示全部），timezone 为 IANA 名称，model/profile/source/run 过滤，sort 支持 tokens/recent，分页最大 100。聚合在单个读事务内完成，总量、趋势、模型、配置、来源和批次排行共用同一过滤口径；模型身份来自运行创建时的 config_summary 快照，不随当前配置修改变化。趋势按区间跨度自动选 15 分钟 / 小时 / 天 / 月 / 年分桶，天及以上按请求时区的本地日历对齐（含夏令时）；"全部"从首条记录起算，避免空桶跨越数十年。批次行带区间内用量与来自累计表的全期用量两个口径。

前端 `web/src/features/usage/` 提供筛选行（预设区间、自定义日期时刻、模型 / 配置 / 来源 / 批次）、总览卡片、SVG 趋势图（悬停与方向键取值、完整数据表替代）、模型对比条、高消耗批次排行与分页明细；请求带中止与序号防迟到覆盖，错误提供重试。图表色 #0e8a5c / #0c7f52 已通过配色校验（浅色表面、色度与对比达标）。浏览器测试覆盖导航与直达、筛选参数往返、空态、错误重试与窄屏。

## 运行分析

schema v19 为 run_items 增加 started_ms / finished_ms（毫秒，0 表示未记录）：jobs 在置 running 时写 started_ms，finish()、扫描期跳过与取消批量置状态时写 finished_ms；扫描前跳过的项只有完成时刻、不进耗时统计。旧数据不回填。

`internal/runstats` 聚合批次性能（结构与 tokenusage 平行：单读事务 + Go 内聚合 + 相同分桶粒度）：批次按实际开始（缺失用提交时间）归属 `[from,to)` 半开区间；批次耗时 = finished − started；逐项耗时 = finished_ms − started_ms（仅 done / failed 计入，cancelled 排除）；条目完成时刻优先 finished_ms，缺失回退批次 finished 并计入 legacy_items；P50 / P95 为 nearest-rank 分位；失败原因按首行截断 100 字符归组。`GET /api/runs/analytics`（字面路由优先于 `/api/runs/{id}` 通配）提供 from / to / timezone / profile / source / sort（recent / duration / size）/ page / size 参数，风格与 usage 接口一致。

侧栏「统计分析」分组提供 `#usage/tokens|analysis` 独立入口，由 App.vue 解析（默认 tokens）。`Usage.vue` 为页面标题和内容壳组件，按路由展示模型用量或运行分析，不再提供重复的页内导航；`UsageAnalysis.vue` 提供 10 格指标卡、批次耗时趋势、处理结果趋势（成功 #0e8a5c / 跳过 #2468af / 失败 #b14545，CVD 与对比校验通过）、吞吐趋势 + 耗时分布（单色顺序色带 ordinal 校验通过）并排、模型配置性能比较、`AnalysisScatter` 散点（最近点拾取 + 方向键）、失败分析与分页批次明细；图表组件 `AnalysisLine` / `AnalysisHistogram` / `AnalysisScatter` 沿用 UsageTrend 的悬停 / 键盘 / 数据表惯例，样式在 `analysis.css`。浏览器测试覆盖侧栏切换与 hash 直达、指标渲染、图表交互、排序筛选参数、空态、错误重试与窄屏截图。

## Prompt配置与提示词库

schema v18 重建 `prompts` 表（v4 已把旧表改名 `model_configs`，名称可复用）：提示词成为独立实体（名称 1–80 字、正文 ≤16000 字节、上限 200 条、版本校验），`internal/prompts` 提供增删改查；删除前检查 `model_configs.prompt_id` 引用，被引用时返回 409 与数量。`model_configs` 新增 `prompt_id` 并删除 `body` 列，正文与提示词名称在读取时 LEFT JOIN 解析（API 的 `body`/`prompt_title` 为只读视图）；迁移把旧正文按内容去重回填为提示词条目，标题取同正文的最低 ID 配置，配置自动关联，历史运行快照不受影响。全局固定提示词存 `system_settings.fixed_prompt`（空值回退代码默认），`internal/systemsettings` 提供 `FixedPrompt`/`SaveFixedPrompt`，接口为 `GET /api/prompts`（返回 fixed_prompt、内置默认值 default_fixed_prompt 与列表，页面据此提供「恢复默认」填入）、`PUT /api/prompts/fixed` 与提示词的 POST/PATCH/DELETE；`/api/model-presets` 不再返回 default_prompt。

运行创建时在 jobs 里把所选提示词正文与固定提示词拼为完整 system 文本，写入 `runs.prompt_body`、加密快照与 prompt_hash；编辑提示词库正文影响之后创建的运行，进行中批次保持快照。vision 保留 NAMING_FAILED 检测，不追加硬编码提示文本。默认字数要求作为提示词发送，不新增本地字数校验。`web/src/features/prompts/` 提供固定提示词卡片、提示词库与编辑弹窗，页面说明采用简短文案；模型配置通过下拉框选择提示词。浏览器测试覆盖提示词增删改、引用保护、持久化与恢复默认。
