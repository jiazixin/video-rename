# video-rename

本地媒体命名工作台：Go 常驻服务、Vue / TypeScript 前端和 SQLite。支持目录浏览、视频/音频播放、图片预览、手动改名、视觉模型自动命名、独立抽帧、图片/音频编号、处理标记、历史还原、定时计划及统计。

服务启动不会立即扫描或改名。后台索引扫描默认开启，首次等待 1 小时，之后每轮结束再等待 1 小时；可在系统设置调整范围、间隔或关闭，也可手动递归扫描。扫描只同步文件列表，媒体处理由页面操作或已启用计划触发。文件操作核对身份与内容，并采用无覆盖改名；删除是永久删除，不能通过改名历史还原。

## 安装与启动

支持 macOS / Linux。需要 Go **1.27.1**、Node.js **22.12+** 和 npm；依赖版本由 `go.mod`、`go.sum` 与 `web/package-lock.json` 确定。抽帧需要 PATH 中的 `ffprobe` / `ffmpeg`，自动命名还需要支持图片输入的 OpenAI 兼容或 Anthropic 模型服务；缺少工具时仍可浏览和手动改名。

在仓库根执行，先构建 Web，再编译嵌入资源的 Go 程序：

```bash
npm --prefix web ci
npm --prefix web run build
go build -o bin/video-rename ./cmd/video-rename
cp configs/config.example.yaml configs/config.local.yaml
# 修改 media-path 和 auth 后启动
./bin/video-rename --config configs/config.local.yaml
```

默认访问 `http://127.0.0.1:8790`。局域网访问可将 listen 改为 `0.0.0.0`。支持 `--config/-f`、`--help` 和 `--version`。

## 配置约定

```yaml
listen: "127.0.0.1"
port: 8790
media-path: "/srv/videos"
storage: "../data"
tmp-storage: "../.cache"
auth:
  username: "admin"
  password: "请替换为你的密码"
```

默认读取 `configs/config.local.yaml`；配置文件路径相对于工作目录，YAML 中的路径相对于配置文件目录。优先级为 YAML > 内置默认值，不读取环境变量覆盖。配置只接受已声明字段和单个文档；示例密码必须替换。

媒体根目录必须存在。部署配置、storage 和缓存不能位于可操作的媒体根内。监听、路径及认证变更需重启；模型、提示词、抽帧、编号、通知和资源设置通过页面保存到 SQLite，不写回 YAML。详见 [配置参考](docs/configuration.md)。

项目尚未上线，只接受当前完整数据库结构，不提供旧库迁移。不匹配的已有库会被拒绝且保留原样，需显式使用新的 storage 目录。

## 使用

| 入口 | 操作 |
| --- | --- |
| 文件目录 | 搜索、排序、分页及跨页多选；刷新当前目录、递归扫描；改名、删除、标记；抽帧、自动命名、编号与还原 |
| 抽帧记录 / 运行记录 | 成功和失败记录、筛选、诊断、失败命名重试 |
| 计划管理 | 创建手动命名/抽帧任务，管理定时计划、Cron 与时区 |
| 批次记录 | 查看进度与明细、暂停调度、继续、取消及整批还原 |
| 模型用量 / 运行分析 | 请求用量、耗时、处理结果与失败原因 |
| 系统设置 | 处理与缓存、后台目录扫描、编号规则、Prompt配置、模型配置、抽帧设置及通知 |

首次自动命名前，在系统设置中填写服务 URL、模型、密钥并选择提示词。每个视频独立发送截图；创建批次时冻结请求参数、提示词和重试规则。模型并发数可随配置动态调整。独立抽帧不改名，截图可供后续命名复用。

详细行为与限制见 [运行说明](docs/operations.md)，模型设置见 [模型配置](docs/models.md)。

## 部署与备份

- SQLite 放本机磁盘，不能放 SMB / NFS。一个数据库只运行一个实例；数据库符号链接按真实路径使用同一锁和密钥，存在多个硬链接的数据库拒绝打开。媒体可以在外部挂载中，身份或挂载变化会阻止操作。
- 运行账号需要媒体操作权限、配置读取权限以及 storage / 缓存读写权限；部署时持久挂载 storage。
- 冷备份先停止服务，再完整备份 storage。数据库 `data.sqlite` 与主密钥 `data.sqlite.key` 必须一起保留；在线备份使用 SQLite Backup API。
- 恢复旧备份后暂停计划，核对备份之后的文件改动。清除页面日志不会删除还原历史或保护标记；删除数据库会丢失这些状态。
- 缓存只维护 tmp-storage 下的 `video-rename-frames-v1/`，可重建，无需备份；日志输出到标准输出。对外访问由反向代理启用 HTTPS，应用不信任客户端转发头。

当前数据库结构标识为 **20261005**。本轮直接调整表、索引及统计缓存失效机制，不提供旧库迁移或兼容分支；旧结构会被明确拒绝打开，已有数据不会自动删除。开发实例请使用新的 storage 目录，媒体根可保持原样。

## 开发与验证

```bash
npm --prefix web run format:check
npm --prefix web run check
npm --prefix web run build
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go mod verify
go test -tags=integration -count=1 ./internal/media
go build -o bin/video-rename ./cmd/video-rename
npm --prefix web exec -- playwright install chromium
npm --prefix web run test:e2e
```

浏览器测试使用端口 **18790**、临时数据库与合成媒体，并使用真实 FFmpeg 和本地假模型；该端口不要运行真实服务。规模测试与具体条件见 [开发说明](docs/IMPLEMENTATION.md)，最新结果见 [验收记录](docs/ACCEPTANCE.md)。仓库目前没有 CI 平台配置，CI 应按上述顺序直接执行原生命令。

开发前先启动 Go 服务，再运行 `npm --prefix web run dev`；Vite 将 `/api` 代理到 `127.0.0.1:8790`。

## 目录

```text
cmd/video-rename/  服务入口
internal/         HTTP装配、业务与基础模块；Go测试就近放置
web/              Vue源码、npm配置、e2e、dist及Go嵌入入口
configs/          部署配置示例与本地实例
docs/             当前设计、实现、操作与验收；archive保存历史
scripts/          开发维护脚本
bin/              本地可执行文件（忽略）
data/             默认持久状态（忽略）
.cache/           默认截图缓存（忽略）
```

目录基线见 [目录规范](目录规范.md)，目录例外：无。所有当前文档见 [文档索引](docs/README.md)。
