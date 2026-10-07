> 历史验收归档：以下为批次新文件名查询修复前的结果，不代表当前版本。最新结果见 [当前验收](../ACCEPTANCE.md)。

# 最新验收：扫描排除目录与统一选择弹窗（2026-10-07）

基于干净工作区 `14449628d14c`。上一轮验收已移至 [历史记录](2026-10-07-before-scan-exclusions-acceptance.md)，不作为本次验证依据。

## 本次行为

- 系统设置新增“排除目录”，支持最多100个目录、多选、取消、逐项移除与保存；扫描范围复用同一列表及模态选择组件。确认弹窗只更新草稿，保存后重开保留。
- 手动与后台递归索引扫描使用同一排除规则；排除优先于扫描范围，显式选择排除目录或其子目录也跳过。父子重叠排除项合并，清空后恢复扫描。
- 每轮冻结启动时的排除列表，保存不改变运行中的轮次。已有索引保留，浏览时按需索引及命名、抽帧不受限制；排除不是访问权限。
- 复用现有设置JSON存储，不更改数据库结构。目录展示路径只读索引，保存验证目录真实可用，拒绝文件、无效ID、重复ID和超量列表。
- 列表显示完整路径标题，长路径最多三行，目录较多时列表内部滚动；复用原有模态的焦点限制、Escape关闭与焦点恢复。

## 已执行验证

| 命令 | 结果 |
| --- | --- |
| `npm --prefix web run format:check` | 通过 |
| `npm --prefix web run build` | 通过，含严格类型检查 |
| `GOCACHE=/tmp/video-rename-go-cache go build -o bin/video-rename ./cmd/video-rename` | 通过 |
| `GOCACHE=/tmp/video-rename-go-cache go test ./internal/libraryscan ./internal/systemsettings ./internal/app` | 通过 |
| `GOCACHE=/tmp/video-rename-go-cache go test -race -count=1 ./internal/libraryscan ./internal/systemsettings` | 通过 |
| `GOCACHE=/tmp/video-rename-go-cache go test -race -count=1 ./internal/app -run 'TestScanExcludedTargetsAPI\|TestLibraryScanAPISettingsAndDiscovery'` | 通过 |
| `GOCACHE=/tmp/video-rename-go-cache go vet ./internal/libraryscan ./internal/systemsettings ./internal/app` | 通过 |
| `npm --prefix web run test:e2e -- library-scan.spec.ts` | 15/15通过，最终样式调整后重跑通过（18.8秒） |
| `git diff --check` | 通过 |

服务端覆盖两种扫描来源、排除子树、显式子目录绕过、保留已有索引、移除后恢复、运行中设置快照、保存重开和独立设置互不覆盖。接口补测覆盖目录路径回读、父子范围合并、非目录拒绝和手动扫描排除。

浏览器覆盖保存重开、多选、取消与Escape不改草稿、移除后保存空列表、键盘焦点恢复与循环、加载失败重试、长路径、320/390px窄屏，以及已有扫描状态、目录更新与交互保留场景。截图在忽略目录 `web/test-results/`，包括 `scan-exclusions-desktop.png`、`scan-exclusions-modal-mobile.png`、`scan-exclusions-long-320.png`。

首次Go编译发现基准测试需同步新参数，已更新调用；空排除列表的JSON数组语义已明确，并同步测试夹具。首次浏览器启动因沙箱不能监听本地端口中断，授权执行隔离服务后通过，没有修改断言掩盖失败。

## 验证边界

全部为本机临时数据库和合成媒体，浏览器使用18790隔离端口。未读取用户配置或媒体，未调用真实模型或通知服务；未部署、未重启用户服务。本次未重跑全仓Go测试、完整浏览器套件、规模测试、Linux目标或真实NAS实测。
