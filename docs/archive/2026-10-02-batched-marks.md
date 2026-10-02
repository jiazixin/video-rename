> 已完成实施记录，仅用于追溯；当前行为与验证命令见 docs/IMPLEMENTATION.md，实际结果见 docs/ACCEPTANCE.md。

# 大规模文件标记优化计划

目标：降低 50 万以上文件递归标记的提交开销，保留 FULL 同步、当前磁盘内容遍历、文件身份与内容校验、任务占用保护和已提交进度。

方案：每批最多 128 个媒体文件。先在 Library.mu 下完成逐文件校验，再打开短事务，复用预编译语句提交本批有效文件；文件系统读取不占用数据库写事务。目录遍历读取当前页的基础字段，避免逐文件额外读取标记和历史。当前批次取消或数据库失败时回滚，进度只统计成功提交；校验失败的文件独立报告。进入新目录前及目录结束时提交余量，避免跨目录扫描延长等待。

取舍：保留 WAL + FULL，不更换数据库、不增加写并发、不跨批缓存挂载/祖先身份。校验复用当前锁内刚读取的文件快照，并省略无用途的处理标记查询；对 SMB 的全部属性检查保留。大事务和降低同步级别不采用。

## 实施与验证

1. 记录现有 `TestScaleProcessedMarks` 的首次、重复和取消标记基线。
2. 修改 `internal/library/marks.go`：有界收集、短事务、预编译语句、提交后进度及分页基础字段读取。
3. 在标记测试中覆盖批次边界取消、校验失败与正常文件共存、SQL/提交失败回滚、硬链接、历史与媒体保护；现有后台测试覆盖断连、关闭和请求幂等。
4. 执行受影响包测试及竞态检查，随后运行 Go 默认测试、静态检查与本地/Linux构建。依赖嵌入 Web 的命令先运行原生 Web 构建。
5. 用相同规模命令对比 2 万文件，并增加可独立执行的更大规模验证。记录本机条件和真实 Linux/SMB 未测量的范围。
6. 同步开发、操作和验收说明，说明批次回滚与进度粒度。

命令：`go test -tags=scale -run '^TestScaleProcessedMarks$' -count=1 -v ./internal/library`；`go test -count=1 ./internal/library ./internal/app`；`go test -race -count=1 ./internal/library ./internal/app`；`npm --prefix web run build`；`go test -count=1 ./...`；`go vet ./...`；`go build -o bin/video-rename ./cmd/video-rename`；`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/video-rename-linux-amd64 ./cmd/video-rename`。
