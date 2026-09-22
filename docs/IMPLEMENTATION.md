# 开发与实现

当前应用为 Go 1.27.1 + Vue / TypeScript + SQLite 单实例 Web 服务。原始 CLI 设计保留在 `docs/plans/` 用于追溯，当前使用协议见根 README 与 [运行说明](operations.md)。

## 边界

- `internal/app`：HTTP 路由、中间件装配、静态资源和生命周期。
- `internal/auth`：随机令牌、哈希会话、12 小时过期、CSRF、同源校验、登录限流。
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

`GET /api/entries/{id}/video` 经过会话认证；library 以受根目录约束的文件描述符打开、核对文件身份和祖先路径，HTTP 层使用 ServeContent 提供 Range / seek，不把整段视频读入内存。不提供转码。播放器关闭时暂停、移除 src 并释放资源。共享 Modal 使用引用计数锁定页面滚动，最后一个弹窗关闭后恢复原有样式。

## 原位更新、处理标记与目录汇总

schema v6 将 marks.operation 改为可空，手动状态标记使用 source=marked、operation=NULL；不创建伪改名历史。取消标记删除对应文件身份/大小/修改时间的保护，不撤销操作历史。升级在事务内保留所有已有标记。

POST /api/entries/processed 接收 targets（1–100）和 marked，递归扫描现有目录，逐视频核对路径与快照，使用 library 的改名互斥锁修改标记。重复/重叠目标去重；返回 changed、unchanged、failed 和最多 100 条错误详情。活动任务中的文件不允许修改标记。新视频与内容变化后的文件不继承标记，硬链接共享身份标记。

GET /api/entries/status?ids=... 返回最多 300 个条目的当前状态（不存在的条目省略），不会启动目录刷新。前端按 ID 合并，不重排；运行从排队到终态持续查询，避免扫描前尚无 run_items 时停止轮询。导航及更新请求序号阻止迟到响应覆盖新页面。成功改名原位维护 directory_items 名称，不清空分页索引。

目录 progress 汇总 SQLite 中当前可达的完整子树：total、processed、unknown（未完整索引/扫描失败目录）、active。手动与自动命名、手动状态标记均计入 processed。unknown 非零时不宣称全部已处理；汇总不触发全库磁盘扫描，外部变更在目录扫描后反映。

## 固定命名失败协议

模型请求统一在用户提示词后追加不可在页面编辑的输出协议（OpenAI 兼容和 Anthropic 均适用）：无法因安全限制或其他原因生成符合要求的标题时，仅返回“命名失败”。模型配置正文与已保存的用户提示词不改写。

最终标题文本包含“命名失败”时，vision 返回非全局 NAMING_FAILED，不重试、不进入文件名清洗或改名。复用任务失败处理：保留原文件名、不写已处理标记，写入任务历史失败记录及脱敏请求/响应；其他视频继续处理。

## 运行页面分区

运行计划页沿用任务历史的按钮切换样式：左侧“手动计划和运行记录”（默认选中，一次性运行入口及手动运行记录），右侧“自动计划和运行记录”（新建/编辑计划、计划卡片及计划运行记录）；每次仅展示选中分类。source=schedule 或 plan>0 的运行归自动区，包含计划卡片上的立即运行；其余归手动区。仍展示 API 返回的最近运行，不改变调度行为。

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

## 图片一键编号

schema v11 增加 image_numberings 和 image_numbering_items。服务端复用目录排序表达式，读取完整当前目录索引，按搜索过滤图片，冻结预览中的文件身份、版本、原名、新名及目录指纹；确认时重新校验。编号为正整数，前导零规范化，0 和非纯数字名称进入续排；使用大整数避免长数字溢出。不同格式共用序号，未参与的编号图片保留其号码。硬链接重复参与时拒绝操作。

修改项先全部转唯一临时名，再转最终名。每次移动前写入 pending_name，使用平台 no-replace 改名并同步目录后记录 current_name。最终在单一数据库事务中提交索引、原名到最终名的历史以及批次状态；临时名不出现在历史中。数据库提交结果不确定时先查持久状态，不盲目回滚。中断批次在启动时依据文件身份与 current/pending 证据做两阶段回滚；证据不一致或原名被外部文件占用时标记 review，阻止相关范围改名和扫描，用户可在编号记录中重试恢复。

编号记录显示当前目录最近 50 次已执行操作。整批还原先预览，要求每张改动图片仍对应原批次操作且未发生后续改名或内容变化，恢复原名及扩展名原始大小写；使用同一持久化移动机制。预览无文件修改，过期预览按日清理；已执行日志保留。接口统一走登录和 CSRF 校验，应用请求只提交服务端预览 ID，不接受客户端指定任意目标文件或名字。
