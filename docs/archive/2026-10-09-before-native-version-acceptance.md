# 历史验收：构建版本与 Go 1.27.2（2026-10-09）

> 历史状态：此记录保留此前脚本注入方案及当时的验证结果。该方案已被 Go 原生版本方案替代，不作为当前构建命令或验收依据；当前记录见 [最新验收](../ACCEPTANCE.md)。

范围：基线 `0a1d64dcfaa17c437844ae5c7f9112832fc51a25` 加本轮工作区变更。更新最低 Go 版本、构建版本读取与注入、双语使用说明和开发文档；未修改业务流程、数据库结构、用户配置或媒体，也未提交、打 tag 或发布。上一轮结果已移入 [历史验收](2026-10-08-release-safety-acceptance.md)，不用于证明本轮通过。

## 核实与实现

- 原实现只显示显式注入的版本（默认 `dev`）、12 位提交号和 `modified`，没有读取 `debug.BuildInfo.Main.Version`。
- Go 自 1.24 起可将符合模块版本规则的 tag 或伪版本写入构建信息，但本仓库现有 `1.0`、`1.1` 不属于其识别的 `v1.2.3` 格式。本次没有改写现有标签。
- 原生 `go build` 命令通过 `scripts/build-version.sh` 计算并注入 Git 版本，支持轻量/附注标签；保留 tag 之后的提交距离，无 tag 回退到短 ID，无 `.git` 源码包回退到 `dev`。脚本从自身位置定位项目，Git 错误非零退出；文档使用 `&&` 避免计算失败后继续构建。
- 应用依次使用显式注入、Go 主模块版本、`dev`，保留提交号和修改状态。运行二进制无需 Git。
- `go.mod` 最低版本更新为 `1.27.2`，README、英文 README、设计和开发说明已同步。本机实际工具链也是 `go1.27.2 darwin/arm64`，无需另行安装。

依据：[Go 1.24 构建版本说明](https://go.dev/doc/go1.24#go-command)、[Go 1.27.2 官方发布记录](https://go.dev/doc/devel/release#go1.27.0)，以及本机 Go 源码、真实编译二进制和隔离 Git 仓库验证。

## 依赖与工具检查

- `go list -m -u all` 查询完成：直接依赖及当前二进制使用的间接依赖未显示更新，不需要为此次补丁升级调整版本；`go.sum` 无变更。
- 模块图中的 `golang.org/x/tools`、`github.com/google/pprof`、`modernc.org/cc/v4`、`modernc.org/ccgo/v4`、`modernc.org/gc/v3` 有新版本。`go mod why -m` 核实这些路径来自 SQLite/libc 的依赖测试工具链，未链接进应用二进制，本轮不单独提升这些间接约束。
- 当前 `govulncheck@latest` 解析为 **v1.8.0**，可使用 Go 1.27.2 运行。实际扫描固定使用该版本，结果为 `No vulnerabilities found.`，只代表本次可见漏洞数据库与分析结果。
- Go 1.27.2 包含工具链和标准库安全修复，需要重新编译、替换部署二进制才能生效。本轮已构建 macOS arm64 与 Linux amd64 产物，没有替换正在运行的服务。
- 初次联网命令受沙箱 DNS 限制而失败，取得网络执行权限后重新查询成功；未修改用户的 Go 代理配置。

## 已执行验证

本机：macOS / arm64，Go **1.27.2**、Node **26.11.0**、npm **11.20.0**。Go 使用 `GOCACHE=/tmp/video-rename-go-cache`，数据库、文件、模型、通知和 HTTP 测试均使用临时夹具或本地假服务。

| 命令或检查 | 结果 |
| --- | --- |
| `npm --prefix web run build` | 严格类型检查与 Vite 构建通过 |
| `go mod verify` | 全部模块校验通过 |
| `go mod tidy -diff` | 通过，无依赖或校验文件差异 |
| `go test -count=1 ./...` | 全仓通过，包括扩展后的构建版本格式测试 |
| `go test -race -count=1 ./...` | 全仓竞态检查通过 |
| `go vet ./...` | 通过 |
| `go test -tags=integration -count=1 ./internal/media` | 通过，真实 FFmpeg/ffprobe |
| `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` | 未发现漏洞 |
| `sh -n scripts/build-version.sh` | Shell 语法检查通过 |
| `gofmt`、文档命令 Shell 语法、本地链接、`git diff --check` | 全部通过 |
| 隔离 Git 仓库与源码包，真实 `go build` | 13 项场景通过，详见下文 |
| macOS arm64 应用构建及 `--version` | 通过，二进制确认为 Go 1.27.2 |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build`，带同一版本注入 | 通过，确认为 Go 1.27.2 的静态 ELF；未在 Linux 执行 |

当前 macOS 工作区二进制输出：

```text
video-rename 1.1-102-g0a1d64dcfaa1 (revision=0a1d64dcfaa1, modified)
```

该版本表示从本地最近标签 `1.1` 起的 102 个提交，不是新发布的版本；`modified` 如实反映尚未提交的改动。

13 项构建验证包括：无 tag、现有格式的轻量 tag、tag 后提交、修改状态、显式版本覆盖、附注 tag、原生 Go tag、原生 Go dirty tag、禁用 VCS、Git worktree、无 Git 源码包、不继承外层仓库 tag、损坏 Git 检出非零退出。夹具只复制版本代码和脚本，使用临时提交与标签，未操作本项目标签。日志见 [构建夹具结果](../../test-results/build-version/build-fixtures.log)。

## 验证边界

本轮未重跑浏览器 E2E、数据库规模/超大规模测试、全应用无缓存依赖安装或真实外部模型/通知服务。Linux 仅交叉编译，未实机运行；NAS、SMB/NFS 和物理故障不在本次验证范围内。前端与业务实现没有变更，不沿用旧验收数字作为当前测试结果。
