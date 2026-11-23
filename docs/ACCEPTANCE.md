# 持久化文件身份与重挂载兼容验收（2026-11-23）

本机环境为 macOS arm64、Go 1.27.4（`go version go1.27.4 darwin/arm64`）。本次仅修改 Go 后端存储结构、文件身份逻辑及文档，未改动 `web/` 前端代码。所有测试使用隔离临时目录与临时 SQLite 数据库。

## 变更要点

- **数据库结构号**：`internal/storage/schema.sql` 与 `internal/storage/schema.go` 提升为 `2026112301`，`library` 表新增 `mount_point INTEGER NOT NULL DEFAULT 0`；不提供旧库迁移，既有 `2026111801` 数据库只读核对后拒绝打开并保持原状。
- **持久化身份与运行期设备号分离**：`entries`、`marks`、`operations`、`run_items`、`image_numbering_items`、`numberingDirectorySnapshot`（第 5 版）与 `framecache.Key`（第 4 版）改用所属根范围的 `r<root_id>:<ino>`，不再持久化内核 `st_dev`；进程启动时将各媒体根当前的运行期 `st_dev` 与根 `ino` 保存在 `MediaRoot` 中，所有路径、扫描、播放与写操作仍严格核对运行期 `st_dev` 与根身份，跨设备子项归类为 `other`（`xdev:<dev>:<ino>`）且不深入。
- **启动核对与诊断**：启动时核对已绑定根目录存在、`mount_point` 状态未变化且根 `r<root_id>:<ino>` 一致；网络共享重挂载仅改变 `st_dev`（包括原设备号复用到其他共享）时允许启动，卸载后落回空本地目录、根目录被替换或服务端 inode 变化（如 `noserverino`）时明确报错并输出期望/实际身份与当前设备号。

## 本机执行结果

| 检查 | 命令与结果 |
| --- | --- |
| 模块与格式 | `go mod verify`、`go mod tidy -diff`、`gofmt -l`、`go run golang.org/x/tools/cmd/goimports@v0.50.0 -local video-rename -l` 通过，无未格式化文件。 |
| 全目标静态检查 | 8 个发布目标（`linux/amd64`、`linux/arm64`、`linux/ppc64le`、`linux/s390x`、`linux/riscv64`、`linux/loong64`、`darwin/amd64`、`darwin/arm64`）`go vet ./...`、`go vet -tags=integration,scale ./...` 与 `go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...` 全部通过。 |
| 新增与更新回归 | `internal/library/roots_test.go` 覆盖重挂载 `st_dev` 变化与设备号复用（`51→52`、`52→58`）、卸载丢失 `mount_point` 拒绝、根 inode 变化拒绝、移除已绑定根拒绝、物理重复根拒绝、同 inode 不同设备双根允许、未规范化/尾斜杠根路径拒绝、运行期切换 `root.dev` 阻断 `Path`/`ScanDirectory`/`OpenPlayback`/`Rename`、跨设备子项归类为 `other`/`xdev:`、重启替换根目录拒绝，以及扫描/标记/改名/编号后持久化表不含 `st_dev` 且重挂载后标记与还原仍可用；`internal/framecache/cache_test.go` 覆盖 `st_dev` 变化不影响缓存键以及空 `Identity` 兜底保留设备隔离。 |
| OCR 托管审查与复验 | `ocr delegate`（v1.14.1）审查全部 21 个可审查代码文件（覆盖率 100%，0 Critical/High，2 Medium）；修复 `inspectRoot` 尾斜杠路径校验与 `framecache.Key` 空 `Identity` 跨设备兜底后，复跑格式、`go vet`、`staticcheck`、`CGO_ENABLED=1 go test -race -count=1 ./internal/framecache ./internal/library ./internal/job` 及本机/Linux 交叉编译均通过。 |
| Go 竞态测试（常规组） | `CGO_ENABLED=1 go test -race -count=1 -timeout=15m -skip '^TestLargeDirectoryNumberingPreviewIsPaged$' ./...` 全部 27 个测试包通过。 |
| Go 竞态测试（重型组） | `CGO_ENABLED=1 go test -race -count=1 -timeout=15m -run '^TestLargeDirectoryNumberingPreviewIsPaged$' ./internal/app` 通过（113.13s）。 |
| 集成测试 | `go test -tags=integration -count=1 ./internal/media ./cmd/video-rename` 通过（真实 FFmpeg 抽帧与 CLI 构建版本集成测试）。 |
| 本机与交叉编译 | `go build -buildvcs=true -trimpath -o bin/video-rename ./cmd/video-rename`（`video-rename v1.1.13+dirty (revision=e81ece39f0ed, modified)`）及 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=true -trimpath -o bin/video-rename-linux-amd64 ./cmd/video-rename` 构建通过。 |

## 未验证范围

- **Linux / Samba 实机运行**：`bin/video-rename-linux-amd64` 仅完成了本机交叉编译，未在真实 AlmaLinux 10.2 + Samba/CIFS 重挂载环境中实机执行；内核 CIFS 客户端因外部客户端移动/重命名目录触发 `Autodisabling the use of server inode numbers` 属于内核行为，仍需通过部署与操作习惯规避。
- **前端与浏览器套件**：本次未修改 `web/` 代码，未重新执行 Web 单元/lint/Playwright E2E、`govulncheck`、Fuzz、规模测试或远端 CI。
