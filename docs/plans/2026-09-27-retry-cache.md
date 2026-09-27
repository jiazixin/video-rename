# 重试、兜底模型与缓存管理实施计划

**目标：** 默认失败重试三次，最后一次可使用兜底配置；支持成功后立即/延时/不删除、闲置清理时长，以及确认后清空闲置缓存。

**架构：** jobs 统一调度每次模型调用，批次持久化加密兜底快照与重试规则；vision 提供单次调用，逐次用量独立记账。framecache 在租约锁内执行保留策略和清理，前端沿用设置页与 Modal。

**技术栈：** Go 1.27.1、SQLite、Vue/TypeScript。使用 use-modern-go CLI 输出的规范。沿用仓库仅维护当前数据库结构、不升级旧库的决定。

## 实施与验证

1. 修改 internal/systemsettings/store.go、internal/storage/schema.sql/schema.go：持久化策略、默认值和校验。添加默认及往返测试。
2. 修改 internal/framecache/cache.go：启动前加载策略、成功时记录时间、闲置/成功到期判断、清空闲置缓存。测试租约保护、重启、容量清理与改名/还原后的缓存命中。
3. 修改 internal/jobs、internal/vision、internal/pipeline：冻结规则与兜底快照，统一请求次数、可取消退避、分类重试、逐次模型名额。测试次数、最后一次兜底、配置错误直达兜底、取消及快照独立性。
4. 修改 internal/tokenusage：按 item/attempt 幂等记账，统计使用每次实际模型；批次详情提供尝试记录。
5. 修改 internal/app 与 web/src/features/settings/Processing.vue：设置与清空接口、模型选择、时间控件、红色按钮和确认框；批次详情展示尝试。
6. npm --prefix web run check && npm --prefix web run build；go test ./...、go test -race ./...、go vet ./...；执行相关 Playwright 与媒体集成测试。修复发现的问题。
7. 更新 README.md 与 docs/IMPLEMENTATION.md，记录验证及数据库结构变化。


## 完成与验证

2026-09-27 已完成上述实现；工作区原有改动为空，本次未操作实际媒体、配置或数据库。

- `npm --prefix web run check`、`npm --prefix web run build`：通过。
- `go test ./...`：通过（本机假模型服务需要沙箱外临时监听权限）。
- `go test -race ./...`：其余包通过，原并发测试的提交时序假设失败；改为等待真实提交后取消，随后 `go test -race ./internal/jobs ./internal/storage` 通过。最后的命名清洗预校验与清空去重计数变更，经 `go test -race ./internal/jobs ./internal/framecache ./internal/vision` 通过。
- `go vet ./...`、`go build -o bin/video-rename ./cmd/video-rename`：通过。Go 构建缓存使用临时目录 `/tmp/video-rename-go-cache`。
- `go test -tags=integration ./internal/media/`：通过。
- Playwright：抽帧 2 项、设置及编号设置 3 项通过。新测试最初模拟错误响应误用了 error 字段，修正为实际 message 协议后通过；最终设置页重新运行 3 项全部通过。
- 检查了桌面和 390px 窄屏截图；修正时间单位与输入框的底部对齐。`git diff --check` 通过。
- 未部署或重启用户服务。数据库结构标识变为 20260928；按既有仓库规则，旧库拒绝启动且不修改，需要新的 storage 目录。
