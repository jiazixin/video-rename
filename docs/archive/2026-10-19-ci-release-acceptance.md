# 历史验收：GitHub CI、GoReleaser 发布与 README 重写（2026-10-19）

> 历史状态：此记录为 GitHub CI、发布配置与 README 重写一轮的验收，已被重命名输入框焦点、尝试记录展开按钮与标记日志调整的验收取代，不作为当前行为依据；当前记录见 [最新验收](../ACCEPTANCE.md)。

范围：基线 `86d592befdb6` 加本轮变更。本轮参照 MirrorDock 新增 GitHub Actions CI 与 tag 发布、GoReleaser 多目标配置和 Apache-2.0 LICENSE，并按开源项目结构重写中英文 README；未修改 Go 或前端源码、接口、数据库、配置协议或测试断言。上一轮记录见 [历史验收](2026-10-19-ui-buttons-media-player-acceptance.md)，不用于证明本轮通过。

## 实现要点

- `.github/workflows/ci.yml`：`web`（Prettier、严格类型检查与构建）、`go`（先构建 Web；`go mod verify`、gofmt、Linux 与 darwin/arm64 交叉 vet、竞态测试、安装 FFmpeg 后的 `integration` 媒体与构建版本测试）、`e2e`（构建 `bin/video-rename`、安装 FFmpeg 与 Chromium 后跑完整 Playwright，失败上传 `web/test-results/`）、`release-snapshot`（GoReleaser check 与快照）。
- `.github/workflows/release.yml`：标准 `v*` tag 触发，无缓存；先核对 `--version` 精确等于 tag 与 12 位提交号，再由 GoReleaser 发布。
- 所有 Action 按提交固定。checkout v7.0.1、setup-node v7.1.0、setup-go v7.0.0、goreleaser-action v7.3.0、upload-artifact v7.0.2 的 SHA 均经 GitHub API 核对与 tag 一致。
- `.goreleaser.yaml`：linux amd64/arm64/ppc64le/s390x/riscv64/loong64 与 darwin amd64/arm64，`CGO_ENABLED=0 -trimpath`，输出在被忽略的 `bin/release/`；本机实测 linux 386/arm 无法编译，故不提供 32 位目标。
- README 与 README.en.md 同步改为徽章、功能特性、运行要求、下载 / 配置 / 源码构建、部署与数据、开发与 CI、版本与发布、文档、项目结构与目录例外、贡献和许可证结构；目录例外补记 `--config` 配置协议与 `bin/release/`。
- 开发说明新增“CI 与发布”；不存在的 `1.0`、`1.1` 旧 tag 描述改为“不带 `v` 前缀的 tag 不是 Go 模块版本”。

## 已执行验证

本机 macOS / arm64，Go 1.27.2，Node.js 26.12.0。浏览器测试使用隔离服务器 18790、临时数据库、合成媒体、真实 FFmpeg 和本地假模型。

| 命令或检查 | 结果 |
| --- | --- |
| `actionlint` v1.7.7（关闭 shellcheck） | 两个 workflow 无问题 |
| `npm --prefix web run format:check`、`npm --prefix web run build` | 通过 |
| `go mod verify`、gofmt、`go vet ./...`、`GOOS=darwin GOARCH=arm64 go vet ./...`、`GOOS=linux GOARCH=amd64 go vet ./...` | 通过 |
| `go test -race -count=1 ./...` | 全部包通过 |
| `go test -tags=integration -count=1 ./internal/media ./cmd/video-rename` | 通过（真实 FFmpeg 抽帧、临时仓库 `go build` 版本） |
| `goreleaser check` 与 `goreleaser release --snapshot --clean`（v2.18.3，`go run` 安装） | 通过；8 个目标归档与 `checksums.txt`，归档内容为二进制、LICENSE、两份 README、配置示例和三份文档；linux/amd64 为静态 ELF |
| `go build -trimpath -o bin/video-rename ./cmd/video-rename` 后全量 `npm --prefix web run test:e2e` | 182 通过、0 失败（10.0 分钟） |

## 验证边界

workflow 只做了本机等价命令与静态检查，GitHub 托管的 ubuntu-24.04 运行结果以推送后的 Actions 页面为准。尚未推送 tag，Release 工作流未实际执行；tag 版本核对逻辑由构建版本集成测试间接覆盖。未在 Linux 实机或非 amd64/arm64 架构上运行二进制，未执行真实模型或通知服务测试。
