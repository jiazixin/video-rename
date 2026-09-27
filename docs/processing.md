# 媒体处理与模型接口

## 1. 探测

Go 通过参数数组启动 ffprobe，不拼接 shell 命令。输入先转为绝对本地路径，避免文件名被当成选项；命令中的路径始终作为独立参数。

探测参数设计为 `-v error -show_format -show_streams -of json <absolute-path>`。使用 JSON 中的 format 与 streams 获取文件字节数、时长、视频流索引、编码尺寸、sample_aspect_ratio、旋转元信息、色彩标记及 attached_pic disposition。ffprobe 支持可供程序解析的 JSON 输出，见[官方文档](https://ffmpeg.org/ffprobe.html)。

选择首个 default 且不是 attached_pic 的视频流；没有 default 则选择第一个非封面视频流。只有音轨、只有封面、无有效视频流均报 `PROBE_NO_VIDEO`。后续 FFmpeg 显式 map 该绝对流索引，不能假定一定是 `0:v:0`。

时长优先采用所选视频流的有效 duration，缺失时使用 format.duration。只接受有限且 > 0 的秒数；都不可用则报 `PROBE_DURATION_UNKNOWN`。首版不自动全量解码推算时长，避免对损坏或超大文件不可控扫描。

文件字节数用 Go stat 的 size，并与 ffprobe format.size 对照；缺失 format.size 时仍可继续，明显不一致则重新核验文件是否变化，变化报 `SOURCE_CHANGED`。不依靠读取整个视频或计算全文件哈希获取大小。所有 MiB 阈值按 1024 × 1024 字节换算后比较。

探测受内置 30 秒超时和 4 MiB stdout 大小上限控制；stderr 只保留最后 8 KiB 用于诊断。捕获退出码、超时及 JSON 解析错误，不将它们合并成一个不明错误。

## 2. 帧数算法

设 D 为视频时长（秒），S 为字节数。Nd、Ns 分别来自 sampling.duration_rules、sampling.size_rules；以下为默认分档，可在 Web「抽帧设置」中调整。

| 时长（取首个命中的上界） | 基础帧数 Nd |
| --- | --- |
| ≤30 秒 | 3 |
| ≤2 分钟 | 5 |
| ≤10 分钟 | 8 |
| ≤30 分钟 | 10 |
| >30 分钟 | 12 |

大小对应帧数 Ns：≤50 MiB 为 3，≤200 MiB 为 5，≤1024 MiB 为 8，更大为 12。hybrid 策略的目标 N0 = Nd + min(2, max(0, Ns − Nd))；size 策略取 N0 = Ns，duration 策略取 N0 = Nd。

hybrid 策略中，小文件不会减少长视频的时间覆盖；大文件最多补 2 帧，防止高码率短视频占用过多预算。这是启发式，不假定文件越大内容越复杂。

采样有效窗口：A = D × sampling.skip_start_ratio，Z = D × (1 − sampling.skip_end_ratio)，L = Z − A。

最终目标帧数 N = min(N0, sampling.max_frames, max(1, floor(L / 1秒)))。至少一帧成功就可继续，允许极短视频只取一帧。

时间点取每段中点：t[i] = A + (i + 0.5) × L / N，i 从 0 到 N−1。它们严格位于窗口内部，避免直接读取 EOF。记录原始时长、基础帧数、最终目标和时间点，便于复盘。浮点非有限值或无法生成有效时间点直接失败。

使用默认值的例子：

| 视频 | Nd | Ns | N0 | 最终 N |
| --- | --- | --- | --- | --- |
| 8 MiB、20 秒、约 500p | 3 | 3 | 3 | 3 |
| 120 MiB、90 秒、1080p | 5 | 5 | 5 | 5 |
| 3 GiB、60 秒、4K | 5 | 12 | 7 | 7 |
| 80 MiB、1 小时、低码率 | 12 | 5 | 12 | 12 |
| 5 MiB、0.4 秒 | 3 | 3 | 3 | 1 |

分辨率不直接增加帧数，而是决定截图缩放，避免 4K 视频被同时增加帧数和单图体积。均匀采样可能漏掉短事件、片头片尾；默认跳过首尾各 5%，可分别调整为 0；首版不增加场景识别流程。

## 3. 截图与资源边界

每个视频创建私有临时目录。同一 worker 内依次调用 FFmpeg 提取图片，最多一个 FFmpeg 活跃，不再为每张图片派生 goroutine。对于长视频，每个目标点进行输入侧 seek，避免从头解码到每个采样点。seek 不保证零成本，长 GOP 或索引不良的文件仍可能较慢。

参数约定：`-nostdin -hide_banner -loglevel error`；输入侧 `-ss <seconds>`；`-i <absolute-path>`；显式 `-map 0:<stream-index>`；`-frames:v 1`；缩放后使用 JPEG 编码、`-q:v <images.jpeg_quality>`、`-update 1` 输出至唯一临时文件。解码端、编码端各设置 2 个线程，并将简单滤镜线程设为 1。原视频永不作为输出。

FFmpeg 输入侧 seek 配合默认 accurate_seek 会从可定位点解码并丢弃到指定时间附近的内容，参见[seek 文档](https://ffmpeg.org/ffmpeg.html)。验收时覆盖非零 start_time 和可变帧率，不以名义 FPS 推算时间。

### 图片尺寸

- 保留默认自动旋转，按实际显示方向输出，竖屏不横置。
- 先根据旋转和 sample_aspect_ratio 计算显示宽高 W、H。SAR 缺失按 1；异常或无法解释的变换在测试中明确报错，不能猜测后静默拉伸。
- s = min(1, images.max_dimension / max(W, H))；目标宽高按 W×s、H×s 向下取可编码偶数，至少 2 像素。正常尺寸不放大，极端小于 2 像素的病态输入拒绝。
- FFmpeg 执行目标尺寸的 scale，并输出方形像素；自动旋转后的输入与计算目标必须一致。避免直接把非方形像素视频 `setsar=1` 而不补偿宽高比。
- 每张图片再次解码检查 JPEG、实际宽高和字节数，不只检查 FFmpeg 退出码。

例如：3840×2160 → 768×432；2160×3840 → 432×768；720×480 且 SAR=1 → 720×480。scale/SAR 语义依据 [FFmpeg filters](https://ffmpeg.org/ffmpeg-filters.html)，具体滤镜表达式在实施阶段通过横竖屏与非方形像素夹具验证。

缩图限制的是发送图像成本，不是源视频解码内存的硬上限。FFmpeg 仍需解码原分辨率帧；4K 并发解码会占用内存。首版使用软件解码，暂不做 GPU 解码和 HDR 色调映射；HDR 来源会标记 `HDR_NOT_TONEMAPPED`，颜色判断可能有偏差，需作为实际样片验收项。

### 失败与预算降级

每个截图点首次失败时，只补试一次：在该分段内从中点移至 1/3 处，仍保留原始点与实际请求点。补试、重新压缩都共享该视频内置的 120 秒抽帧预算，不额外增加总预算。若超时已经耗尽，不再尝试剩余点。

图片超过内置 1 MiB 上限时，在临时目录中最多做一次重新编码：尺寸缩到当前的 75%，质量参数变为 min(15, 当前 q + 3)。依然超限则视该帧失败。不能无限缩小或不受控地反复转码。

成功帧数至少达到 1 就允许继续；成功日志记录 warnings 和失败采样点，并在控制台提示。低于该数量则 `EXTRACT_INSUFFICIENT_FRAMES`。零帧绝不调用模型。

组装 JSON 前估算 base64 大小为每图 4×ceil(bytes/3)，再计入 prompt 和协议开销；组装后必须按真正序列化字节数复核内置 16 MiB 请求体上限。超限时均匀删除部分帧，保持时间顺序，直至满足预算；仍不足最低有效帧数则 `REQUEST_TOO_LARGE`。重试复用已缓存的帧集，不重新抽帧；同模型使用冻结的请求参数，兜底按其独立配置组装请求。

Web 运行的完整截图保存到 tmp-storage 的专属缓存目录。改名成功默认立即清理，可设置延时或不因改名删除；失败或取消保留完整结果。闲置清理默认 3 天，可修改或关闭；容量不足可提前淘汰闲置旧缓存。正在等待或调用模型的截图受租约保护。未完成的抽帧输出清理，异常退出遗留的专属 pending 工作目录在下次启动清理；缓存目录实例锁防止多进程误删。磁盘满或写图片失败属于抽帧失败。

## 4. llama.cpp 协议

服务应已加载支持图片的模型以及必要的多模态组件；具体启动方法由用户的模型决定。前提依据 [llama.cpp 多模态文档](https://github.com/ggml-org/llama.cpp/blob/master/docs/multimodal.md)。

程序使用 `POST {base_url}/chat/completions`，`Content-Type: application/json`，有 key 时设置 Bearer 鉴权。共享 HTTP client 和连接池。首版禁用自动重定向，避免路径配置错误和鉴权意外转发。

模型参数来自页面所选配置的任务快照。OpenAI 兼容请求使用 system（所选提示词 + 固定提示词）及一条 user（截图说明、时间与图片），图片用 data:image/jpeg;base64 内联，按时间递增排列。OpenAI 官方预设使用 max_completion_tokens，其余兼容服务使用 max_tokens；temperature 可关闭发送。Claude 使用顶层 system 与一条 user，图片为 base64 source。仅发送截图，不传原视频或本机路径。详细结构见 [模型请求](model-request.md)。

响应必须是有效 HTTP 2xx + 合法 JSON。读取 `choices[0].message.content`，要求非空字符串。忽略 reasoning 字段，拒绝 tool_calls 或空 choices；`finish_reason=length` 标记为 `MODEL_OUTPUT_TRUNCATED`，不将截断内容当标题。

只移除首尾空白及成对包裹引号，然后要求仅一个非空逻辑行。代码围栏、多标题列表、解释段落、空标题均 `TITLE_INVALID`；不随意从长答案中猜第一行。通过后再交给文件名清洗。

## 5. 超时与重试

全局取消约束所有阶段。探测和抽帧分别执行自己的预算，单次 HTTP 使用当前模型配置的 timeout；每次兜底使用兜底配置的超时。整批重试由 jobs 调度，响应体固定上限 1 MiB。所有等待，包括退避和请求排队，都可被 context 取消。

| 场景 | 策略 |
| --- | --- |
| 临时网络错误、连接重置、HTTP 超时（含收到响应头后的响应体读取） | 有限重试；运行 context 取消时不重试 |
| HTTP 408、429、500、502、503、504 | 有限重试 |
| HTTP 401/403 | 不重复请求该配置，有兜底则直接切换；最终鉴权故障停止该批次 |
| HTTP 400/404/413/422 | 不重复请求该配置，有兜底则直接切换；记录摘要与配置提示 |
| 无多图能力、上下文不足、无效参数 | 不盲目重试相同请求，不自动删改用户配置 |
| 响应体超限、坏 JSON、无效标题、输出截断、“命名失败” | 按批次规则重试，最终失败保留错误码 |
| ffprobe/FFmpeg 失败 | 不走 HTTP 重试逻辑 |

默认首次请求后最多额外重试 3 次，总计 4 次，可关闭或设置 1–10 次额外重试。选定兜底时最后一次使用兜底配置，兜底不递归重试。第 k 次失败后的退避基数为 min(15秒, 2秒 × 2^(k−1))，加入 0.5–1 倍 jitter。可解析的 Retry-After 是最小等待要求，与本地退避取较大值，等待可取消。jobs 每次调用 vision.TitleOnce，不叠加客户端自身的三次尝试。

HTTP 超时不保证服务器立刻终止推理；重试可能使服务端重复计算。workers 限制的是客户端活跃视频数，不是服务器残留计算数。模型忙时优先降低 workers 或加长合理的 timeout，不不断叠加重试。
