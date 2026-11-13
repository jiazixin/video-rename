# 最新验收：递归标记性能优化（2026-11-13）

## 范围与实现

基线为 `d83185c`，验证对象为该提交上的本次工作区修改。改动限于 Go 的扫描暂存、标记复核、相关测试和文档；数据库结构、依赖、接口和前端源码未变。此前缺陷修复与扫描库复用的验收已移入 [历史快照](archive/2026-11-13-before-mark-performance-acceptance.md)，不能将旧通过记录当作本次验证。

- 可重建扫描暂存库由 WAL/FULL 改为 WAL/NORMAL；业务主库保持 WAL/FULL。暂存库关闭后删除，重启不读取它恢复业务状态。
- 每批标记先执行任务占用回调，再在一个短读快照中检查待恢复操作、读取文件和公共祖先；在现有库锁内使用该快照，读事务结束后仍逐文件复核根目录、祖先、文件身份、大小和 mtime。不跨批次缓存索引或磁盘证据。
- 批次上限仍为 128，扫描仍为两遍、最多 4 个属性 worker。部分文件失败、整批提交失败、取消和已提交进度规则保持。
- `mark_finished` 增加 6 个刷新子阶段和 3 个复核子阶段。子阶段互不重复，已包含在对应父阶段中；字段口径见 [运行与恢复](operations.md)。任务占用回调仍逐文件执行，其成本单独记录，未借此改动 job 的占用语义。

## 回归证据

新增日志字段断言在旧实现上失败（字段缺失）；暂存库同步策略测试在旧实现上失败（两次连接读取均为 FULL/2），改动后为 NORMAL/1。测试同时验证附加暂存库发布时、分离暂存库后主库仍为 FULL/2，暂存库删除后已发布数据可读。

批量快照回归覆盖缺失/循环祖先、祖先被替换但叶子硬链接身份未变、符号链接、批次间祖先改名、占用回调中新出现的待处理操作及错误优先级。原有标记/扫描测试继续覆盖逐项失败、文件内容与类型变化、硬链接、持久进度、取消、写入/提交回滚、扫描失败保留旧索引、临时库清理及任务占用。

## 本机性能对照

macOS / arm64，Apple M2 Max，Go 1.27.4；全部为新建临时目录、合成小文件和隔离数据库。library 夹具不装配 job 的真实 Busy 回调，不含 NAS 延迟或真实媒体大小分布。先前用户日志的 402.8 秒不能用本机结果直接换算；已有索引也不等于冷文件系统缓存。

### 分项基准

每目录 53 个文件，预建索引，计时外清空 marks，计时内递归刷新并重新标记。`-benchtime=3x -count=3`，下表为三组输出的中位数（每组 3 次迭代）。单独 NORMAL 版本来自相同基线，仅修改暂存同步策略；最终版本另外包含批量复核、可用性读快照合并及细分计时。

| 布局 | 改动前 | 仅暂存 NORMAL | 最终实现 |
| --- | ---: | ---: | ---: |
| 1 目录 × 53 文件 | 11.00 ms | 8.59 ms | 5.58 ms |
| 100 目录 × 53 文件 | 690.21 ms | 602.70 ms | 257.34 ms |

100 目录场景仅 NORMAL 缩短约 12.7%，全部改动缩短约 62.7%。最终该场景约 45.0 MB、77.1 万次分配/轮，基线约 89.5 MB、195.9 万次分配/轮。短基准会受本机缓存及背景负载影响，不代表所有目录布局。

### 530000 文件、10000 目录

`TestScaleLargeProcessedMarksManyDirectories` 在同一隔离夹具先执行首次索引标记，再清空 marks、保留索引进行第二轮标记。改动前/后各执行一次完整用例，分开顺序运行；下表仅为 `mark_finished` 操作耗时，不含夹具生成、完整性检查及清理时间。

| 阶段 | 改动前 | 最终实现 | 耗时减少 |
| --- | ---: | ---: | ---: |
| 首次建立索引并标记 | 92.290 s | 55.837 s | 39.5% |
| 已有索引后重新标记 | 64.971 s | 28.983 s | 55.4% |

| 分段 | 首次：前 → 后 | 已有索引：前 → 后 |
| --- | --- | --- |
| 刷新 | 46.330 → 43.915 s | 18.934 → 16.564 s |
| 复核 | 39.452 → 5.574 s | 39.082 → 5.623 s |
| 标记写入 | 5.207 → 5.088 s | 5.611 → 5.523 s |

每轮均精确标记 530000 条、刷新 10001 个目录（含根）、提交 10000 批，失败和中断为零。主库标记/索引精确计数、`integrity_check`、`foreign_key_check` 全部通过。新的子阶段显示首次索引构建/发布仍占约 31 秒；已有索引时该项约 2.1 秒。

复现命令：

```sh
go test -run '^$' -bench '^BenchmarkProcessedMarks$' -benchtime=3x -count=3 -benchmem ./internal/library
go test -tags=integration,scale -run '^TestScaleLargeProcessedMarksManyDirectories$' -count=1 -timeout=15m -v ./internal/library
```

本机原始输出保留于忽略的 `test-results/mark-performance/`。对照版本分别编译成独立测试二进制，后续工作区修改不会改变正在运行的基线。未使用用户配置、媒体、数据库或外部模型。

## 检查结果

| 检查 | 实际结果 |
| --- | --- |
| 改动文件 gofmt / goimports v0.50.0、`git diff --check` | 通过 |
| `npm --prefix web run build` | 通过，生成真实嵌入资源 |
| `go test -count=1 ./...` | 全部通过 |
| `go vet ./...`、`go vet -tags=integration,scale ./...` | 通过 |
| staticcheck v0.8.2：library、storage、job、app、libraryscan | 通过 |
| `go build -o bin/video-rename ./cmd/video-rename` | 通过，macOS arm64；`--version` 可独立运行 |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/video-rename-linux-amd64 ./cmd/video-rename` | 交叉编译通过，不代表 Linux 实机运行 |
| `CGO_ENABLED=1 go test -race -count=1 ./internal/library ./internal/storage ./internal/job ./internal/app ./internal/libraryscan` | 全部通过 |
| `go test -tags=scale -run '^TestScale' -count=1 -timeout=10m ./internal/library ./internal/app ./internal/job` | 全部通过，含实际 app/job 装配的后台标记、断连与占用检查 |

未执行：SMB/NFS 目标环境实测、断电/系统崩溃故障注入、Linux 实机运行、其他发布架构构建、Web lint/typecheck/单测/E2E、FFmpeg 集成、Fuzz 与漏洞扫描。前端源码、媒体处理和依赖未变；本次仅重建 Web 供 Go 嵌入。WAL/NORMAL 的断电语义依据 [SQLite 官方说明](https://www.sqlite.org/pragma.html#pragma_synchronous)，不能把普通回滚测试当作物理断电验证。
