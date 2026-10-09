# 历史验收：1.0 发布安全修复（2026-10-08）

**历史状态：这是 2026-10-08 的验证记录，不作为当前版本验收依据；当前结果见 [最新验收](../ACCEPTANCE.md)。**

**发布结论：可以发布 1.0.0。** 两项 P1、一项 P2 及最终复核发现的取消边界均已修复，最终版本回归全部通过；本轮审查与验证范围内没有未解决的发版阻塞。此结论适用于下述修复源码与明确列出的环境范围，不是对所有环境或未知缺陷的保证。

范围：基线 `276a19409ff2a192adf9ee80d34001d2a41da7fd` 加本轮未提交修复。保留上一轮文档变更；不升级依赖、不迁移数据库、不修改用户配置或真实媒体。尚未提交、打标签或发布。

修复前的两项 P1、一项 P2 及原始复现见 [历史审查](2026-10-08-before-release-safety-fixes-acceptance.md)，实施记录见 [历史计划](2026-10-08-release-safety-fixes.md)。下面结果全部针对本轮修复后的源码，不使用历史通过记录替代。

## 问题关闭与回归

| 原问题 | 当前实现 | 新增验证 |
| --- | --- | --- |
| P1 缓存启动误删媒体 | 配置层统一定义缓存受管根；启动前解析真实路径，拒绝媒体、配置、storage、数据库落入受管根，并拒绝受管根符号链接；配置文件自身的符号链接也核对实际目标 | 危险媒体布局、storage 不存在子目录、配置目录、数据库链接、媒体/状态链接、受管根链接、配置文件链接均拒绝且数据保留；正常父目录/兄弟目录和相似前缀允许 |
| P1 源文件被替换仍报改名成功 | prepared 写入后复核源，移动后复核目标；成功日志写入后的事务提交前再次核对文件及父目录；异常回滚成功记录，保留 prepared 并停止提交 | 手动改名、同名自动命名、还原 × 日志准备阶段/成功事务阶段 × 换 inode/同 inode 内容变化，共 12 场景；无假成功、不覆盖外部内容，恢复进入 review，原文件保留 |
| P2 明细写库失败仍报告批次完成 | 明细终态与计数在一个事务中提交；工作线程错误向上返回并取消剩余工作；批次失败收尾及正常结束也处理持久化错误 | 拒绝明细终态、拒绝计数、拒绝批次完成、持续拒绝所有批次终态；取消收尾事务回滚、拒绝未完成明细直接结束、并发线程取消和重复启动恢复 |

正式回归位于 [配置](../../internal/config/cache_isolation_test.go)、[文件提交](../../internal/library/commit_identity_test.go)、[批次完成](../../internal/jobs/completion_test.go)。先在未修复实现上重现原始三项失败，再实现修复并增加边界场景；没有修改原有断言或关闭检查。

最终复核进一步发现：工作线程的局部取消没有覆盖使用批次调度上下文的名额等待。新增回归先在 5 秒超时处失败；修复为同时取消活动批次上下文后通过竞态检查。随后在最终源码上重跑全仓测试、竞态、完整浏览器、常规规模和构建；超大规模仅依赖未再变更的 library 代码，其源码哈希已核对。

数据库持续不可写时无法可靠保存新的失败状态，因此服务会输出明确错误日志，保留活动批次供存储恢复后重启收尾；不会伪造成功。文件操作日志仍保留已经提交的改名事实，不为修正批次计数再次盲目改名。相关使用说明已同步至双语 README、配置参考、运行恢复和开发文档。

## 已执行验证

本机：macOS / Apple M2 Max / arm64，Go 1.27.1、Node 26.8.2、FFmpeg 9.0.1、TypeScript 6.0.3、Vite 8.3.3。Go 使用独立缓存 `GOCACHE=/tmp/video-rename-go-cache`。

| 命令或检查 | 结果 |
| --- | --- |
| `go test -count=1 ./internal/config ./internal/framecache ./internal/library ./internal/jobs` | 相关包通过 |
| `go test -race -count=1 -run 'TestCompletion' -v ./internal/jobs` | 最终调度上下文修正后，故障与并发取消测试通过 |
| `npm --prefix web run format:check` / `check` / `build` | 全部通过 |
| `go mod verify` | 全部模块校验通过 |
| `go test -count=1 ./...` | 全仓通过 |
| `go test -race -count=1 ./...` | 全仓竞态检查通过；新增并发测试另行竞态验证通过 |
| `go vet ./...` | 通过 |
| `go test -tags=integration -count=1 ./internal/media` | 通过，真实 FFmpeg/ffprobe |
| `npm --prefix web run test:e2e -- --output=test-results/release-fixes-final` | **182/182 通过，8.9 分钟**；此前一轮也为 182/182，但发布判断以本轮最终源码结果为准 |
| `go test -tags=scale -run '^TestScale' -count=1 -timeout=15m -v ./internal/library ./internal/jobs ./internal/app ./internal/runstats` | 全部通过；30 万索引、5 万分页、2 万标记/编号、任务提交和统计 |
| `go test -tags=scale_large -run '^TestScaleLarge' -count=1 -timeout=15m -v ./internal/library` | 通过，372.742 秒；53 万文件扫描和 50 万文件标记，精确计数、完整性和外键检查通过 |
| macOS 发布版本构建 | 通过；`bin/video-rename --version` 为 `video-rename 1.0.0 (revision=276a19409ff2, modified)` |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build` | 通过，Linux amd64 静态二进制，未在 Linux 执行 |
| 当前源码干净副本：`npm --prefix web ci` → Web 构建 → Go 构建 | 通过，包含未提交修复及新测试，没有复制 node_modules、dist 或用户状态 |
| 干净副本二进制隔离冒烟 | 首页 200、登录 200 与 CSRF、SIGTERM 正常退出；危险配置非零退出，合成媒体保留且 storage 尚未创建 |
| `npm --prefix web audit --json` | 0 个公告漏洞 |
| `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` | `No vulnerabilities found.` |

构建时注入 `-ldflags '-X main.version=1.0.0'`。工作区构建带 `-buildvcs=true`，因尚未提交而如实显示 modified；干净副本没有 `.git`，其冒烟构建使用 `-buildvcs=false`。版本号不是已发布声明。

完整测试首轮被上一轮审查证据中的 `.go` 附件干扰，Go 将它们当作独立包编译；已将附件保留为 `.go.txt`，随后使用原始 `./...` 命令重跑通过，未排除业务包。部分测试需要沙箱外回环监听，获准后完成。干净安装按 README 在副本根运行；没有为安装失败修改锁文件。冒烟脚本曾使用错误登录路径，按源码修正为 `/api/auth/login` 后通过。上述均为测试组织/调用问题，不计作产品测试通过，也不掩盖为产品修复。

已查看本轮真实改名详情的桌面和 390px 窄屏截图，原名、新名与完成状态可读；完整浏览器套件覆盖加载/空/错误状态、焦点、跨页选择和滚动保持。

## 最终差异复核与证据

- OCR delegation 解析本轮差异和 Go 审查规则，人工复核全部 8 个生产 Go 变更文件：路径解析、调用入口、事务回滚、跨线程取消、恢复和资源关闭。`reviewed_files=8 / skipped_files=0 / coverage_rate=100%` 仅指这 8 个差异文件，不代表全仓无缺陷。
- 最终 `git diff --check` 通过，文档链接与源码 SHA-256 复核通过；正式发布构建校验和保存在 `binaries-sha256.json`。
- OCR 默认排除的新增/修改测试和 Markdown 另行检查，核对测试与真实写入流程及双语文档的一致性。
- 本地日志、失败前证据、结构化复核清单、干净构建源码哈希位于 [本轮证据](../../test-results/release-fixes/)。`source-manifest.json` 记录包括新增测试在内的源码与依赖清单 SHA-256；测试启动后核对源码没有再变化。
- 修复前原始复现附件仍在 [上轮证据](../../test-results/release-audit/)，使用 `.go.txt` 后缀防止干扰正常 Go 测试发现。

## 验证边界

所有文件、数据库、媒体、模型和通知测试均使用隔离夹具、合成数据或本地假服务，未使用用户数据或真实付费模型/消息接收端。Linux 仅交叉编译；本机 Docker 客户端存在，但没有可用 daemon，因此没有 Linux 容器实测。NAS、SMB/NFS、物理断电和真实磁盘故障均未实测。

规模测试检查正确性、精确计数、SQLite 完整性与外键。部分测试与浏览器/竞态套件同时运行，耗时不用于性能比较；本轮没有重复与修复无关的单次吞吐基准，也不引用上轮数字证明修复后的性能。漏洞检查只代表执行时可见公告及分析结果。

最终发布信号仅适用于本轮已验证的修复源码；提交后应从包含这些修复的提交构建发布包，不能使用修复前基线的旧二进制。
