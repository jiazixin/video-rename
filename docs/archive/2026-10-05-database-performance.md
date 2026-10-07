> 历史记录：已完成或被后续实现取代；当前使用见 [文档索引](../README.md)，最新验证见 [验收记录](../ACCEPTANCE.md)。

# 数据库与性能修复实施计划

> 使用 writing-plans 和 executing-plans 的分批实施与验证流程；本次用户已授权完整实施及失败后继续修复，无需逐批再次确认。

**Goal:** 修复本次全仓审计确认的查询、连接等待、扫描锁、聚合和轮询问题，并完成仓库全部可在本机执行的验证。

**Architecture:** 保留 SQLite 单写池、只读池、WAL/FULL 和文件身份复核。查询按实际条件使用索引；请求读操作传播取消。慢目录观测统一使用已有锁外扫描与短锁发布路径。统计使用数据库筛选、流式汇总及有界缓存；目录汇总按受影响目录失效。用户明确要求无兼容代码：直接更新完整结构及结构版本，不提供旧库补索引或升级分支，不删除用户数据。

**Tech Stack:** Go 1.27.1、modernc SQLite、Vue 3 / TypeScript、Playwright、FFmpeg。

## 1. 索引与查询

- 修改 `internal/storage/schema.sql`、`schema.go`，补齐外键、活动批次、批次明细和历史批次索引；所有 SQL 定义只有一份。
- 修改 `internal/library/refresh.go` 中的部分索引谓词，以及 `internal/app/app.go` 中历史筛选条件构造。
- 扩展 `internal/storage/indexes_test.go`、`store_test.go` 与接口测试，验证实际执行计划、新库完整结构、重复打开及旧结构保持不变。
- 使用 `go test -count=1 ./internal/storage ./internal/library ./internal/app` 验证。

## 2. 只读连接与取消

- 修改 library 的批量条目与路径读取、jobs 的批次和尝试读取，避免分页逐条占用写池。
- HTTP 读取使用请求 Context；后台刷新独立保留应用 Context。
- 统计函数改为显式 Context，在 SQL 等待、行读取及聚合循环中响应取消。
- 用独占写连接、读池耗尽和已取消请求的回归测试验证。

## 3. 统计工作量

- 修改 `internal/runstats/analytics.go`、`internal/tokenusage/analytics.go`，将时间、配置、来源、批次筛选下推 SQL。
- 流式处理明细；精确分位数只保留必要数值，不保留全部原始消息。
- 将筛选选项与分页统计的缓存键、容量、失效、快照一致性和取消行为写入测试。
- 修改前端统计请求，翻页使用同一时间范围，避免重复触发请求。

## 4. 扫描并发与目录统计

- 复用 `internal/library/directory-scan.go` 的目录句柄、两遍观测、基线复核和短锁发布；刷新计时与错误语义继续可观测。
- 删除不再使用的长锁刷新实现，更新相应回归测试而不减少身份、取消、失败保留完整代的保护。
- 目录统计缓存只重算变化目录；进度、标记、文件替换、改名、删除和任务状态变化在事务提交后可见。
- 验证慢目录读取期间其他目录可播放和改名，缓存命中、失效和并发安全。

## 5. 前端终态轮询

- 修改 `web/src/features/runs/use-run-records.ts` 和 `RunDetail.vue`，活动批次才继续轮询；终态再读取一次后停止。
- 保留首次展开、切换批次、翻页和手动刷新，取消离页请求。
- 添加浏览器回归测试，并只格式化受影响文件。

## 6. 完整验收

依次执行依赖验证、Web 格式/类型/构建、Go 默认测试/race/vet、真实 FFmpeg integration、常规 scale、两组 scale_large、现有基准、原生与 Linux 交叉构建、Playwright。并发执行只适用于互不影响的检查；大规模数据库与文件测试避免争抢磁盘。所有数据为临时夹具，不使用用户媒体和运行库。

任何失败先定位原因并修复，再执行受影响检查。最终在 `docs/ACCEPTANCE.md` 记录命令、结果、性能对照及真实模型/通知/Linux运行/NAS的环境边界；同步 README 与开发说明，不以合成测试代表真实 NAS 性能。

## 实施状态

1–6 已完成。完整 Go、race、vet、构建、integration、scale、两组 scale_large 及基准均已通过。浏览器首轮四处旧断言已修正，定向4/4通过，最终整套162/162全部通过。实测与边界记录在 `docs/ACCEPTANCE.md` 的 2026-10-05 节。
