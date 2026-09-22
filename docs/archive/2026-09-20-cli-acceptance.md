> 历史 CLI 验收记录，仅供追溯，不作为当前 Web 版本的命令或结果依据。

# 验收记录:2026-09-20 首版实施

当前状态：本页末尾“复检缺陷修复验收”记录了最新结果；整体复检发现的 6 项代码问题已修复并通过回归。真实模型与批量性能验收仍未执行。

## 工作区范围

在 `video-rename` 仓库当前工作区(含既有文档与目录规范)完成实施计划阶段 1–6 的全部代码与测试,及阶段 7 的文档与交付准备。未改动任何既有设计文档的契约内容,仅更新状态行。

## 环境

- Ubuntu 22.04(aarch64)沙箱,Go 1.23.4,ffmpeg/ffprobe 4.4.2(apt 版)。
- 依赖:gopkg.in/yaml.v3 v3.0.1、golang.org/x/sys v0.28.0、golang.org/x/text v0.21.0,go.sum 校验记录已生成。

## 已执行验证

| 命令 | 结果 |
| --- | --- |
| `go vet ./...` | 通过 |
| `go test ./...` | 全部通过(10 个包,含配置校验、扫描去重、探测解析、采样公式五组样例、抽帧补试/重压缩/预算、HTTP 重试/鉴权/限流/超限、清洗/截断/冲突、no-replace、两路日志、pipeline 端到端 9 个场景) |
| `go test -race ./internal/pipeline/ ./internal/runlog/ ./internal/vision/` | 通过,无数据竞争 |
| `go test -tags=integration -run TestIntegration ./internal/media/` | 通过:真实 ffprobe 探测、横屏 640x360 不放大、竖屏 4K 2160x3840→432x768、SAR 2:1 显示 1280x360→768x216、0.4s 单帧、损坏文件 PROBE 失败 |
| `GOOS=darwin GOARCH=arm64/amd64 go build ./cmd/video-rename` | 交叉编译通过(renamex_np 封装经 x/sys) |
| `GOOS=linux GOARCH=amd64 go build ./cmd/video-rename` | 通过 |
| CLI 冒烟(Linux) | `--help`/`--version` 正常;真实视频 + 假 llama-server:dry-run 预览与虚拟保留集正确、正式运行真实改名(renameat2)、pending 审计 + success/failure 事件字段符合 operations.md 示意、连接被拒时按退避重试后正确记 API_HTTP_ERROR、混合失败退出码 1、二次运行同名分配 "(2)" 后缀 |

## 未执行项及原因

- **真实 llama.cpp 多图联调(单图、多图各一次)**:沙箱内无 llama.cpp 与视觉模型。需要用户在部署侧执行:`go build -o bin/video-rename ./cmd/video-rename` 后用真实 base_url/model 跑 `--dry-run`,再在样片副本上正式运行。联调时记录 llama.cpp 构建版本、模型标识与 ffmpeg 版本。
- **macOS 真实运行验收**:交叉编译通过,但 renamex_np 的运行时行为、大小写不敏感卷上的 unchanged 判定需在真实 mac 上执行 `go test ./internal/rename/` 与一次真实改名确认。
- **真实样片批量验收(阶段 7 的 20–30 个代表性视频、workers=1/2/4 对比、耗时与显存记录、标题质量人工评价)**:依赖用户的模型与视频集合,吞吐与标题质量目标在未测量前不作承诺。
- **Windows**:首发范围外。

## 与设计文档建议的差异(依目录规范)

- 配置示例放 `configs/config.example.yaml`(规范第 7.1 节),而非设计文档建议的根目录 `config.example.yaml`。
- 固定测试数据放各被测包 `testdata/`(规范第 6 节),而非根目录 `testdata/`。
- 其余模块划分、CLI 契约、错误码、日志 schema 与设计文档一致。

---

## 2026-09-20 整体复检（macOS 当前工作区）

结论：现有自动测试与正常使用链路通过，但补充边界验证发现缺陷，尚不满足完整验收。上文为首版历史记录，不能替代以下复检结果。

### 范围与环境

- 检查当前全部 Go 源码、测试、依赖清单、配置示例及使用/行为文档。当前目录没有 `.git`，无法验证提交状态、已跟踪文件和历史差异；以检查开始时的文件内容为基准。
- macOS arm64，Go 1.27.1，ffmpeg/ffprobe 9.0.1。
- 未修改业务源码、配置和原有测试；只追加本节并按 README 生成 `bin/video-rename`。合成视频、假模型服务、补充诊断测试均在系统临时目录运行，未使用用户视频或真实 API key。
- 首轮遇到依赖缓存缺失和沙箱禁止本机端口监听。下载锁定依赖至临时缓存，并在允许监听本机端口的环境重跑后，以下项目测试均通过；环境失败不计为产品缺陷。

### 验证结果

Go 命令使用 `GOCACHE=/tmp/video-rename-review-gocache`、`GOMODCACHE=/tmp/video-rename-review-modcache`，不修改项目依赖版本。

| 命令/场景 | 本轮结果 |
| --- | --- |
| `gofmt -l cmd internal` | 无输出，格式通过 |
| `go mod verify` | 全部依赖校验通过 |
| `go vet ./...` | 通过 |
| `go test -count=1 -coverprofile=/tmp/video-rename-review-cover.out ./...` | 通过，整体语句覆盖率 82.8% |
| `go test -race -count=1 ./...` | 通过，无检测到的数据竞争 |
| `go test -tags=integration -count=1 -v ./internal/media/` | 通过，真实横屏、4K 竖屏、SAR、极短视频及损坏文件 5 个集成场景均执行，未跳过 |
| `go build -o bin/video-rename ./cmd/video-rename` | macOS arm64 构建通过 |
| `GOOS=linux GOARCH=amd64 go build -o /tmp/video-rename-review-linux-amd64 ./cmd/video-rename` | Linux amd64 交叉编译通过（未运行） |
| CLI help/version/缺少 path | 退出码分别为 0/0/2 |
| 真实 FFmpeg + 本机假模型服务 + CLI dry-run | 发送 3 张实际截图，源文件保留，无目标文件产生 |
| 同名目标已存在时正式改名 | macOS 原子改名成功，使用 ` (2)` 后缀；已存在目标内容保持不变 |
| 再次处理已命名文件 | 返回 unchanged，退出码 0 |
| CLI 生成的两路 JSONL | 均可逐行解析；字段完整性缺陷见下文 |

覆盖率用于描述既有测试范围，不代表下面的边界场景已覆盖。

### 确认的问题（复检时状态，后续修复结果见文末）

1. **P1：HTTP 错误摘要未脱敏，可能把 API key 写入日志。**
   `internal/vision/client.go:318` 将错误响应直接裁剪为 `StderrTail`；`internal/pipeline/commit.go:212` 仅再次裁剪后写入 failure 日志。用合成 key 模拟服务器在 HTTP 400 错误中回显 Authorization，key 原文完整保留。与“不保存 API key/Authorization、错误摘要脱敏”的契约不符。应在进入错误对象和输出路径前过滤敏感内容；不能把截断当作脱敏。真实配置凭证未参与测试。

2. **P2：取消后，尚未入队任务没有最终结果和 cancelled 日志。**
   `internal/pipeline/runner.go:175`、`:187` 直接退出 feed 循环，只处理已入队任务，遗漏剩余清单。20 个候选、workers=1 的推理中取消场景只记 3 个 cancelled，另 17 个漏记；提前取消时为 discovered=20、cancelled=0。应为所有未处理任务补记取消，并保证每个 job 恰好一个最终结果。既有取消测试仅有 3 个任务，刚好能装入一个 worker 加两个队列位置，未覆盖这一边界。

3. **P2：鉴权全局故障后继续启动队列中的任务。**
   `internal/pipeline/runner.go:146` 的 worker 只检查 context，未检查全局 abort；feed 中正在等待的发送也可能在故障后完成。复现为 20 个候选、workers=1、服务全部返回 401，实际仍发出 4 次 HTTP 请求，产生 4 个失败和 16 个取消；预期首个失败后不再开始新任务。应在 worker 取出任务时检查全局故障，同时保留已进行中任务的实际结果。

4. **P2：rename_pending 没有源文件身份快照，无法按文档完成崩溃核对。**
   `internal/runlog/writer.go:32` 的 Event 没有 dev/ino/mtime 字段，`internal/pipeline/commit.go:172` 也未序列化 `job.Snapshot`。实际 pending 只有路径、大小和媒体信息，缺少能关联扫描时原文件的身份。应把源快照写入并同步持久化的 pending；目前不能依靠日志落实文档要求的身份核对。

5. **P2：收到响应头后发生 HTTP 超时，被误报为协议错误且不重试。**
   `internal/vision/client.go:281` 将所有响应体读取错误归为 `API_RESPONSE_INVALID`。假服务先返回 HTTP 200 响应头，再拖延响应体，60ms 请求超时后实际 attempts=1、错误为 API_RESPONSE_INVALID；文档承诺 HTTP 超时有限重试。应区分读取阶段的超时/连接错误、全局取消与真正的 JSON 协议错误，并保留对应重试规则。

6. **P2：日志故障未通知调度器取消剩余处理（静态代码确认）。**
   `internal/pipeline/commit.go:220` 只设置 `logFailed`；调度器直到 `wg.Wait()` 完成后才读取该状态。日志损坏后仍继续抽帧、请求模型，pending 写失败的任务还会直接返回而没有更新最终计数。Writer 的 broken 状态会阻止后续 pending 成功，因此并非继续无日志改名；问题是未立即停止处理和不完整统计。应把日志故障传播到运行控制层，停止新任务并取消剩余处理。本轮没有进行磁盘满故障注入。

前 5 项通过临时副本补充测试复现，诊断测试按预期行为断言而失败；这些失败与上表“原有测试通过”并不矛盾。复现副本位于 `/var/folders/_g/bgsmtp8s4rd_my_5d2_94dkh0000gn/T/video-rename-review-v2cucfyr`，测试为 `internal/pipeline/review_test.go` 和 `internal/vision/review_test.go`，命令为 `go test -count=1 -timeout=20s -run TestReview -v ./internal/pipeline ./internal/vision`。临时目录可能被系统清理，缺陷描述及实测结果以本记录为准。

### 文档与目录检查

- README 构建输出是 `bin/video-rename`，使用示例却写成 `./video-rename`；按现有构建方式应使用 `./bin/video-rename`。
- README 的配置约定已记录根目录 `config.yaml` 行为，但“目录例外：无”未解释文件日志使用 `./logs` 与基线要求 `data/logs/` 的差异；本轮不擅自迁移配置或日志。
- 现有总体设计放在 `docs/plans/`，与基线规定的当前架构说明 `docs/DESIGN.md` 有归属差异；本轮仅记录，未迁移。
- 未发现项目内 CI 配置；无法确认外部 CI 状态，也未新增 CI。

### 仍未验证

- 真实 llama.cpp/视觉模型单图、多图联调及标题质量：当前无可用的真实模型配置；本机假服务只能验证客户端请求和处理流程。
- 20–30 个代表性真实视频、workers=1/2/4 性能与显存验收。
- 本轮 Linux 运行时、其他文件系统、真实断电恢复、磁盘满；Windows 不在首发范围。
- 最低 Go 1.23.4 环境未在本轮重跑；当前结果基于 Go 1.27.1。

建议先修复上述 6 个问题并将复现转为永久回归测试，再进行真实模型与批量验收。原有测试通过不足以宣称完整交付。

## 2026-09-20 复检缺陷修复验收

本轮在用户授权下修复上述 6 项代码问题。修改范围为 `internal/pipeline`、`internal/vision`、`internal/runlog/writer.go` 及相应文档；新增测试就近放在两个包的 `regression_test.go`。未改动依赖版本和本地配置，未进行目录迁移。当前目录仍无 Git，使用修复前临时副本核对文件差异。

### 已完成修复与回归证据

| 问题 | 修复及实测结果 |
| --- | --- |
| 敏感错误摘要 | 仅提取结构化错误消息，丢弃额外的请求/头部字段；在裁剪前过滤已知 key、prompt、JSON 转义内容和敏感标记。HTTP 200/400/401 错误对象、超长回显、Authorization 和图片数据回显测试通过。普通错误摘要仍可用 |
| 取消漏记 | feed 不再直接丢弃剩余候选，worker 与提交锁内均检查取消。20 个候选的提前取消和推理中取消均得到 20 个最终结果，无重复 job_id，退出码 1（CLI 信号码仍由 main 映射） |
| 全局鉴权失败后继续处理 | 队列发送和 worker 取出任务均检查停止状态。workers=1 的 401 场景只请求一次，1 失败 + 19 取消；workers=2 的 403 场景保留另一个已开始任务的成功结果，1 失败 + 1 改名 + 18 取消 |
| pending 缺少身份 | 新增 `source_snapshot`，包含 dev/ino 十进制字符串、size_bytes 和 UTC mtime。回归逐字段核对扫描快照；真实 CLI 生成的 pending 也已验证纳秒时间戳与字段类型 |
| 响应体超时不重试 | 发送与读取阶段共享传输错误分类。响应头之后超时按 API_TIMEOUT 尝试 3 次；响应体断连后可重试成功；context 取消只尝试一次。400/401/403 不因响应体中断而误重试 |
| 日志故障未停止处理 | 提交协调器同步通知运行层，取消进行中 HTTP/媒体调用并停止新任务。关闭真实 Writer 底层文件注入 pending/success/failure 写失败，均退出 2、统计完整；pending 失败不改名，rename 后 success 失败保留 renamed 和明确 stderr 提示。并行场景确认日志故障取消另一条进行中 HTTP 请求 |

日志已经失效时，剩余取消项仍计入内存摘要，无法保证写入 failure.jsonl；这是故障本身造成的持久化限制，运维文档已明确。新增快照为 schema_version=1 的可选字段，旧日志没有快照时仍不能据此完整核对源身份。

### 完成检查

环境沿用上述 macOS arm64、Go 1.27.1、FFmpeg 9.0.1；Go 使用相同的临时依赖与构建缓存。测试仅使用隔离临时目录、合成媒体及本机假 HTTP 服务。

| 检查 | 结果 |
| --- | --- |
| `gofmt -l cmd internal` | 无输出 |
| `go vet ./...`、`go mod verify` | 通过 |
| `go test -count=1 -coverprofile=/tmp/video-rename-fixed-cover.out -timeout=90s ./...` | 全部通过，总语句覆盖率 83.9%，pipeline 89.1% |
| `go test -race -count=1 -timeout=90s ./...` | 全部通过，无检测到的数据竞争 |
| `go test -tags=integration -count=1 ./internal/media` | 通过，真实媒体工具可用 |
| `go build -o bin/video-rename ./cmd/video-rename` | 通过，已更新本地二进制 |
| `GOOS=linux GOARCH=amd64 go build -o /tmp/video-rename-fixed-linux-amd64 ./cmd/video-rename` | 通过，仅编译 |
| 修复后 CLI 冒烟 | help/version/缺少 path 的退出码正确；真实视频发送 3 张截图；dry-run 不改名；同名目标保留内容并分配后缀；再次运行 unchanged；JSONL 可解析且 pending 含源快照 |

README 的使用命令已修正为 `./bin/video-rename`，最低 Go 版本与 go.mod 对齐为 1.23.4。处理、运维和开发文档已同步本次行为。此前记录的目录归属差异未在此次缺陷修复中迁移。

真实 llama.cpp/视觉模型联调、标题质量、代表性视频批量性能、Linux 运行时与真实断电/磁盘满仍未验证；本轮日志故障为真实文件关闭后的写错误注入，不等同于物理断电或磁盘耗尽试验。
