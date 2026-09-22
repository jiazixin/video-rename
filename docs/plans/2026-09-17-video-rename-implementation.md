# video-rename Implementation Plan

**Goal:** 实现由 YAML 驱动、连接 llama.cpp 视觉模型的 Go 视频自动重命名 CLI。

**Architecture:** 固定数量 worker 串联探测、抽帧、模型调用与命名；最后通过串行协调器完成原子无覆盖重命名和两路日志。业务 prompt、采样规则、图片设置和日志目录放在配置中，底层保护参数内置，模块之间通过明确的数据类型隔离。

**Tech Stack:** Go、FFmpeg、ffprobe、llama.cpp Chat Completions、严格 YAML 解析、JSONL、平台专用 no-replace 文件操作。

---

状态:阶段 1–6 已于 2026-09-20 实施完成并通过验证,阶段 7 文档与交付准备完成;真实样片与真实模型验收待部署侧执行(见 [../ACCEPTANCE.md](../ACCEPTANCE.md))。下面保留原始阶段计划作为实施与验收依据。

所有未来路径相对于项目根目录。实施按阶段依赖顺序推进，每阶段先写关键行为测试，再完成最小实现，验证通过后更新文档。不要为与实现同构的内部包装函数堆测试。

## 阶段 1：工程入口与配置契约

未来文件：`go.mod`、`cmd/video-rename/main.go`、`internal/config/config.go`、`internal/config/config_test.go`、`config.example.yaml`。

1. 选择受维护的 Go 工具链及 YAML/CLI 依赖，锁定版本。
2. 将配置参考转为 example；实现配置参考中的默认值、严格解码和规则表校验。
3. 实现 `--config/-f`、`--path/-p`、`--workers/-w`、`--recursive/-r`、`--dry-run`、帮助与版本。
4. 明确记录“显式提供”状态，让 false、0 等合法值不被默认值覆盖。
5. 处理路径基准与 api_key，确保日志与错误不输出凭证。

验收：未知参数、未知字段、重复 key、非法截图尺寸、非递增或缺少兜底的规则表、超时为零、未支持字段均返回 2；`temperature: 0` 保留；CLI 显式值覆盖 YAML；help/version 在无配置环境下成功。

预期验证：`go test ./internal/config/...`；`go run ./cmd/video-rename --help`。本阶段不执行文件改名。

## 阶段 2：扫描、身份和 ffprobe

未来文件：`internal/domain/types.go`、`internal/discovery/discovery.go`、`internal/discovery/discovery_test.go`、`internal/media/probe.go`、`internal/media/probe_test.go`、`testdata/probe/*.json`。

1. 实现文件/目录输入、递归、过滤、快照排序、符号链接规则和清单上限。
2. 记录源文件身份、大小和修改时间；同身份硬链接去重。
3. 用 context 和参数数组启动 ffprobe，限制输出和 stderr 保留量。
4. 解析正常、缺字段、多流、封面、无时长、异常 JSON 等结果。
5. 在独立临时目录中验证空目录、不可读子目录、中文与包含空格的路径。

验收：选中实际视频流，封面不作为内容；时长回退一致；显式未知扩展名单文件仍探测；扫描冻结后不二次处理新文件名。

预期验证：`go test ./internal/discovery/... ./internal/media/...`。以假子进程验证超时、过大输出和异常退出。

## 阶段 3：采样与 JPEG 提取

未来文件：`internal/media/sampling.go`、`internal/media/sampling_test.go`、`internal/media/extract.go`、`internal/media/extract_test.go`、`testdata/media/`。

1. 实现 size/duration/hybrid 三种采样策略、配置分档及时间点公式，测试精确阈值前后、极短时长、NaN/Inf 拒绝。
2. 验证 processing.md 中五组默认样例，并覆盖自定义分档、帧数截断、跳过比例和规则表整体替换。
3. 实现逐点 seek、主视频流选择、自动旋转、显示比例换算、最长边缩放与 JPEG 输出。
4. 实现单点补试、单图超限一次重压缩、总抽帧超时和部分成功规则。
5. 验证每视频清理、取消、磁盘错误和所有子进程回收。

验收：横屏和竖屏均 ≤ 配置最长边；普通小图不放大；非方形像素比例正确；0.4 秒样片可抽一帧；zero frame 不进入模型阶段。长视频不采用逐目标点从头完整解码的实现。

真实媒体集覆盖 500p、1080p、4K、旋转标记、SAR≠1、VFR、非零 start_time、多视频轨和截断文件。大文件性能样片本机生成或由用户提供，不把 GB 文件提交仓库；HDR 标为已知首版限制并检查 warning。

预期验证：`go test ./internal/media/...`；将真实 FFmpeg 集成测试单独标记为 `integration`，缺失外部工具时明确跳过原因。

## 阶段 4：模型客户端和输出验证

未来文件：`internal/vision/client.go`、`internal/vision/retry.go`、`internal/vision/response.go` 及对应测试文件。

1. 使用本地假 HTTP 服务验证端点拼接、鉴权、图片时间顺序与 data URL。
2. 实现共享 client、单次和总预算、请求/响应大小限制及禁止重定向。
3. 实现单行文本标题输出约定，拒绝空值、多行、围栏、截断和 reasoning 冒充标题。
4. 实现有限重试、jitter、Retry-After 和可取消等待，注入时钟避免测试实际长等。
5. 与真实 llama.cpp 做单图、多图各一次联调，记录服务、模型、模板与工具版本。

验收：401 不重试，429/503 在预算内重试，400 不盲目重试；连接超时、慢响应、响应过大均有对应错误；重试不重复抽帧；无 key 时不发 Authorization；自定义 prompt 保持原文。

预期验证：`go test ./internal/vision/...`。真实模型测试为显式选择，不让普通单元测试访问网络或消耗推理资源。

## 阶段 5：文件名、安全提交和日志

未来文件：`internal/naming/title.go`、`internal/naming/conflict.go`、`internal/rename/rename_linux.go`、`internal/rename/rename_darwin.go`、`internal/runlog/writer.go`，以及对应测试。

1. 实现 Unicode 清洗、保留名称、扩展名保持和双重长度预算。
2. 实现同目录候选分配、冲突后缀、unchanged。
3. 核对并封装目标平台 no-replace 系统调用；明确不支持时的错误映射。
4. 实现 run 目录、success/failure JSONL、pending 与最终事件、sync 和脱敏。
5. 以故障注入验证提交前日志失败、rename 失败、rename 成功但最终日志失败。

验收：同名竞争绝不覆盖；中文/emoji 截断为有效 UTF-8；日志能关联原名、新名和身份；未完成 pending 可识别；普通失败保留源名；目标权限不足不降级为复制删除。

在 macOS 与 Linux 各执行真实文件操作测试，包括大小写不敏感卷、目标同身份、源文件在提交前变化、文件系统不支持 no-replace。单个平台编译通过不能代替另一平台文件安全验收。

预期验证：`go test ./internal/naming/... ./internal/rename/... ./internal/runlog/...`。

## 阶段 6：流水线、取消与端到端

未来文件：`internal/pipeline/runner.go`、`internal/pipeline/worker.go`、`internal/pipeline/runner_test.go`、`cmd/video-rename/main_test.go`。

1. 接入有界队列与 workers 生命周期，给每视频建立共享总 context。
2. 接入提交协调器和 dry-run 虚拟名称保留集合。
3. 明确视频级错误与全局故障，完成结果计数、摘要和退出码。
4. 实现 Ctrl+C/SIGTERM、队列取消、子进程回收及短提交临界区收尾。
5. 用混合目录做端到端测试：正常、损坏、同标题、超时和名称不变同时存在。

验收：客户端活跃视频数不超过 workers；失败视频不影响其他正常视频；任务最终结果恰好一次；dry-run 不改源文件或内容；日志失败停止后续提交；中断后没有本进程遗留的活动 FFmpeg。

预期验证：`go test ./...`、`go test -race ./...`、`go vet ./...`；显式的 `go test -tags=integration ./...` 在工具就绪环境运行。测试均使用临时目录中的样片副本。

## 阶段 7：真实样片验收与发布准备

未来文件：根目录 `README.md`、`docs/validation-report.md`、构建配置（按实际发布流程决定）。

1. 用 20–30 个有代表性的本地视频，分别试 workers=1、2；模型容量允许时再试 4。
2. 记录探测/抽帧/推理/总耗时、失败原因、客户端峰值内存、截图大小、请求大小、服务端显存和排队情况。
3. 人工评价标题是否符合可见主体、动作与场景，是否产生无依据名称；记录合格数和具体错误，不虚构准确率。
4. 对照实际模型调 prompt、sampling 规则、images.max_dimension、images.jpeg_quality、server.max_tokens 和 timeout，保存参数与结果。
5. 构建 macOS/Linux 二进制，写清 FFmpeg/ffprobe 安装前提、支持平台、日志恢复方法和 HDR/纯视觉限制。

交付标准：核心行为测试、并发 race 检查和支持平台的无覆盖改名测试通过；真实模型多图联调通过；用 dry-run 看过代表性名称，再在样片副本上完成正式改名。吞吐和标题质量目标在真实样片报告中据实确认，不在未测量时许诺。

## 后续候选能力

按实际反馈再评估：场景检测抽帧、黑帧/重复帧替换、HDR tone mapping、字幕/音频辅助、历史成功跳过、可审计的恢复/撤销命令、Windows 适配、分阶段并发限额。它们不阻塞首版，也不隐含包含在当前实施范围。
