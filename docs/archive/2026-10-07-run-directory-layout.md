> 历史计划：已完成。当前行为见 [DESIGN](../DESIGN.md)，本次验证见 [ACCEPTANCE](../ACCEPTANCE.md)。

# 批次详情目录分组实施计划

**目标：** 落地用户选定的 A 方案，以目录、文件、尝试历史三级展示统一运行记录。

**架构：** 保留详情弹窗概览、控制和配置快照。单一 records 状态管理全批次目录筛选、最终失败筛选和文件分页；服务端在分页前按提交时目录排序，返回整批目录统计。名称使用对应 run/item 操作日志，不能用当前索引名称冒充历史结果。不修改数据库结构。

**技术：** Go / SQLite / Vue / TypeScript，复用 Modal、DateTime、SelectField、PageNavigation。

1. `internal/app/runs.go`、`run_records_test.go`：替换分离日志视图，覆盖排队文件、跨页目录统计、精确目录/失败筛选、批次隔离、四次请求、历史名称与空页；先运行回归再实现。
2. `web/src/features/runs/use-run-records.ts`、`RunDetail.vue`、`run-detail.css`、`Runs.vue`：统一请求与轮询，目录折叠、文件原新名称、全部请求历史；保留筛选/分页/展开状态，处理终态刷新和迟到响应。去掉废弃日志/失败区和父级重复文件轮询。
3. 更新相关 Playwright 用例及夹具，验证 100+ 文件、多目录同名、长文本、加载/空/错误、筛选竞态、终态停止、390/320px 和键盘操作。截图人工复核。
4. 更新 DESIGN、双语 README、文档索引与 ACCEPTANCE；完成计划归档。运行原生格式/构建、Go 测试、相关浏览器回归，不接触真实配置、数据库或媒体。

验证命令：`npm --prefix web run format:check`、`npm --prefix web run build`、`GOCACHE=/tmp/video-rename-go-cache go test ./internal/app ./internal/jobs`、`GOCACHE=/tmp/video-rename-go-cache go build -o bin/video-rename ./cmd/video-rename`、`npm --prefix web run test:e2e -- run-detail.spec.ts run-detail-records.spec.ts page-navigation.spec.ts run-pause.spec.ts run-pagination.spec.ts`。
