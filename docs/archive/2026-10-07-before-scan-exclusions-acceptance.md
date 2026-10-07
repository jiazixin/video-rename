> 历史验收归档：以下为扫描排除功能之前的检查结果，不代表当前版本。最新结果见 [当前验收](../ACCEPTANCE.md)。

# 最新验收：批次详情目录分组（2026-10-07）

基于干净工作区 `123bc0096dbe`，实现用户选定的 A 方案。上一轮检查保留在 [历史验收](archive/2026-10-07-before-directory-layout-acceptance.md)，历史全量和规模结果不代表本次重新执行。

## 本次行为与证据

- 统一运行记录按提交时目录分组，包含排队文件；服务端目录/最终失败筛选在每页100文件的分页前生效，目录统计覆盖整批，跨页目录显示本页/目录数量。
- 每项显示原名、本批次新名及状态，按实际次数展开首次/重试/兜底请求。对应 run/item 操作日志区分成功名称、未提交拟用名称和待核对；后续改名不会冒充历史结果。
- 自动刷新保持分页、目录折叠、请求展开和滚动位置；筛选变化取消旧查询，迟到响应被丢弃。终态最后读取后停止，收起/关闭停止；错误保留旧内容并提供重试。
- 保留已有概览和批次控制，提交范围放入默认折叠的配置区，删除废弃列表及样式。没有改变数据库 schema、处理链或重试策略。

## 已执行验证

| 命令 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` | 通过 |
| `npm --prefix web run build` | 通过，包含严格类型检查 |
| `GOCACHE=/tmp/video-rename-go-cache go build -o bin/video-rename ./cmd/video-rename` | 通过 |
| `GOCACHE=/tmp/video-rename-go-cache go test ./internal/app ./internal/jobs` | app 通过；jobs 首轮因沙箱不能监听 httptest 端口中断，获准后单独执行 `go test ./internal/jobs` 通过 |
| `GOCACHE=/tmp/video-rename-go-cache go test -race ./internal/app -run 'TestRunDirectoryRecords\|TestNumberedHistoryExtractionAndItemPages\|TestReadHandlersCancel\|TestRunReads' -count=1` | 通过：目录筛选、边界分页、请求取消与读取并发 |
| `GOCACHE=/tmp/video-rename-go-cache go vet ./internal/app` | 通过 |
| `npm --prefix web run test:e2e -- run-detail.spec.ts run-detail-records.spec.ts page-navigation.spec.ts run-pause.spec.ts run-pagination.spec.ts` | 当时已有的12项全部通过，54.5秒 |
| `npm --prefix web run test:e2e -- run-detail-records.spec.ts workspace.spec.ts --grep '多目录同名\|运行详情显示目录文件' --output=test-results/run-directory-followup` | 新增同名/历史名称场景与已有历史诊断流程，2/2通过，2.6秒；合计14个不同场景 |
| `GOCACHE=/tmp/video-rename-go-cache go test ./internal/app -run '^TestRunDirectoryRecords$' -count=1` | 新增实时失败汇总断言后通过 |
| `git diff --check` | 通过 |

服务端回归使用205文件、交错目录、另一个批次、根目录、精确路径/特殊字符筛选、四次请求和后续改名日志，核验无丢失、无重复和统计更新。新增测试在旧实现上先失败，随后修复通过。首次浏览器回归发现配置标题未更新，修复产品文案后通过；补充测试夹具有一处括号笔误，修复后2项通过，没有修改断言掩盖产品失败。

已查看1440px桌面、390px和320px窄屏截图：原/新名称可读、长文件名换行、请求历史完整、没有水平溢出。键盘选择目录、弹窗焦点、长提示词、加载/空/错误及复制路径均有浏览器覆盖。截图保存在忽略目录 `web/test-results/`，包括 `run-detail-timeline-desktop.png`、`run-directory-names-mobile.png`、`run-directory-failures-320.png`。

## 验证边界

所有验证均为本机隔离夹具，浏览器服务使用18790端口、临时数据库与合成媒体。未访问用户的配置、数据库、媒体或真实模型/通知服务；未部署或重启用户服务。没有重跑全仓Go测试、全量浏览器套件、规模测试或Linux目标实测；本次目录汇总和排序的超大批次成本尚未专项测量。
