> 已完成，2026-11-13 归档。以下为实施时的计划；最终实现、取舍与测试结果见 [当前验收](../ACCEPTANCE.md) 和 [开发说明](../IMPLEMENTATION.md)。不作为后续执行指令。

# 递归标记性能优化 Implementation Plan

**Goal:** 降低递归标记的扫描暂存成本和重复读事务，保留文件身份、占用、取消与已提交进度语义，并提供可定位耗时的汇总日志。

**Architecture:** 扫描器累计互不重复的阶段耗时，汇总到 `mark_finished`；可丢弃的暂存库使用 WAL/NORMAL，业务主库保持 WAL/FULL。标记在现有批次锁内完成占用检查和批量索引快照读取，磁盘复核仍逐文件执行，提交后才报告进度。

**Tech Stack:** Go 1.27.4、database/sql、modernc SQLite；不新增依赖或数据库结构。

## 1. 建立可复用对照

- 在 `internal/library` 增加多目录标记基准及隔离规模用例，覆盖首次索引与已有索引。
- 使用 `go test -run '^$' -bench ... -benchmem` 记录改动前基线；大规模用例使用 `integration,scale` 标签。
- 分项保留原始输出到忽略的 `test-results/`；测试结果仅在 `docs/ACCEPTANCE.md` 汇总。

## 2. 细分计时

- 修改 `internal/library/directory_scan.go`、`marks.go`，将刷新拆为准备、文件系统读取、暂存处理、索引发布、清理及锁等待；明确嵌套关系和未分类时间。
- 扩展日志测试，覆盖正常、取消、文件目标，以及阶段累计不重复；不开逐文件日志。

## 3. 暂存库同步策略

- `internal/storage/scan.go` 改为 WAL/NORMAL，主库保持 FULL；暂存库从不用于进程重启恢复。
- 增加实际连接配置与附加发布后的主库同步验证，复用已有回滚、取消、暂存库清理测试。
- 与第 1 步同一命令对照，再继续查询优化。

## 4. 批量复核读取

- 在 `internal/library` 内复用可用性查询，将每批文件和公共祖先在短读事务中读取；不持有读事务进行文件 I/O。
- 保留任务占用检查、逐文件根/祖先/叶子身份及大小/mtime 复核；不复用跨批次索引或磁盘证据，不改变 128 项上限。
- 回归缺失/循环祖先、文件替换/符号链接、占用、批次间变化、取消、混合成功/失败、硬链接和提交失败。

## 5. 验证与文档

- 依次运行受影响单测、竞态测试、Go vet/staticcheck、Go 构建、规模测试；性能测量不与其他重负载测试并行。
- 增加接近用户布局的 10000 目录 × 53 文件用例；本机结果与 NAS 实测分开，不推算生产收益。
- 更新 `docs/operations.md`、`docs/IMPLEMENTATION.md`、`docs/DESIGN.md` 与 `docs/ACCEPTANCE.md`；完成后将本计划归档并更新文档索引。
