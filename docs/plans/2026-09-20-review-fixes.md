# 整体复检缺陷修复 Implementation Plan

**Goal:** 修复 ACCEPTANCE.md 复检确认的 6 项缺陷，保留配置协议和原子无覆盖改名语义。

**Architecture:** 调度使用独立的“停止启动新任务”信号与可取消的处理 context；提交协调器同步传播日志故障，完整记录内存统计。HTTP 层统一安全摘要及传输错误分类；日志增加源快照字段。

**Tech Stack:** Go 1.23.4 模块、标准库 context/net/http/testing、现有 runlog 和 pipeline。

用户已授权修复；当前目录没有 Git，保留工作区内容，不创建工作树或提交。遵循 writing-plans 的逐项测试执行方式，在当前任务直接完成。

## 1. HTTP 安全与重试

- 修改 `internal/vision/client.go`，新增同包摘要实现及测试，扩展 `client_test.go`。
- 先验证错误响应回显 key、prompt、Authorization、图片数据和 2xx 错误对象均不泄漏；普通错误仍保留可用摘要。
- 响应体读取超时与连接中断按传输错误重试；context 取消不重试；已知非重试 HTTP 状态保持原有策略。
- 运行 `go test -run 'TestError|TestResponseBody' ./internal/vision`，先失败再修复至通过。

## 2. 取消、全局故障和审计快照

- 修改 `internal/pipeline/runner.go`、`commit.go`、`internal/runlog/writer.go`，扩展相邻测试。
- 20 个候选分别覆盖提前取消、推理中取消、401/403 故障；核验每个任务恰好一个最终结果。
- worker 开始处理前检查停止信号；feed 遍历剩余任务补记取消；鉴权故障不取消已经运行的任务。
- 提交锁内检查 context；已经开始的 pending/rename 临界区正常收尾。
- `source_snapshot` 保存 dev、ino、size、mtime（纳秒精度 UTC）；实际 pending 必须与扫描快照一致。保持 schema 1 的可选字段增补，并说明历史记录缺少快照时的限制。

## 3. 日志故障传播

- 以窄接口注入日志依赖，生产入口继续使用 runlog.Open，无新增 CLI 配置。
- 测试 pending、success、failure 写入故障及并行处理中取消；失败日志本身不可写时，以内存统计和 stderr 如实报告，不能声称取消事件均已落盘。
- pending 失败计为当前任务失败，rename 已成功而结果日志失败仍计 renamed，其余任务取消；禁止再次提交或自动回滚。

## 4. 全面验证与文档

- `gofmt -l cmd internal`、`go vet ./...`、`go test -count=1 ./...`、`go test -race -count=1 ./...`。
- `go test -tags=integration -count=1 ./internal/media`，macOS 构建及 Linux amd64 交叉编译。
- 使用合成视频和本机假模型做 CLI smoke；修正 README 二进制路径，同步日志/错误处理说明和验收记录。
- 真实模型质量、批量性能及真实断电仍明确列为未验证项，不扩大目录迁移范围。

## 执行结果

上述 4 个阶段已完成。6 项代码缺陷均有永久回归测试覆盖，全套单元/race、静态检查、真实媒体集成、macOS 构建、Linux 交叉编译和 CLI 冒烟均通过。详细结果见 `../ACCEPTANCE.md` 文末；总语句覆盖率 83.9%。当前目录无 Git，未创建提交。
