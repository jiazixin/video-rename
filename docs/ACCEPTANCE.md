# 最新验收：Go 原生构建版本（2026-10-09）

范围：基线 `0a1d64dcfaa17c437844ae5c7f9112832fc51a25` 加当前工作区变更。本轮只调整版本选择、构建命令、测试与相关文档；保留此前 Go 1.27.2 等已有改动，未修改业务、配置、数据库或媒体。未提交、打 tag 或发布。此前脚本注入方案的记录见 [历史验收](archive/2026-10-09-before-native-version-acceptance.md)，不用于证明本轮通过。

## 实现与修复证据

- 普通 Git 构建直接使用 `go build` 的主模块版本，删除原有 tag 解析脚本及当前调用方；双语 README 和开发说明同步普通构建、无 Git 显式注入及查询命令。
- 版本优先级为有效显式覆盖 → 主模块版本 → 12 位提交号 → `dev`。空值、`dev`、`(devel)` 不再覆盖有效版本；仍保留可取得的提交号及修改状态，无 VCS 信息不声明干净。
- 新发布 tag 采用标准语义化格式；本仓库原有 `1.0`、`1.1` 标签未修改。Web 私有清单、数据库结构号和缓存版本不随应用版本调整。
- 先添加或修正回归断言：旧实现出现 7 处预期失败，涉及占位覆盖值和提交号回退；实现修复后测试通过。
- 新增 `cmd/video-rename/version_build_test.go`，以 `integration` 标签运行真实构建。所有提交和标签只存在于临时夹具仓库，不操作项目 Git 元数据，不使用用户配置、数据库、媒体或外部服务。

## 已执行验证

本机 macOS / arm64，Go 1.27.2；Go 命令使用 `GOCACHE=/tmp/video-rename-go-cache`。

| 命令或检查 | 结果 |
| --- | --- |
| `npm --prefix web run build` | 严格类型检查与 Vite 构建通过，输出为 `web/dist/` |
| `go test -race -count=1 ./cmd/video-rename` | 命令入口及版本格式测试通过，竞态检查通过 |
| `go vet -tags=integration ./cmd/video-rename` | 通过，包括构建夹具测试 |
| `go test -tags=integration -run '^TestBuildVersion$' -count=1 -v ./cmd/video-rename` | 13 个真实构建场景通过 |
| `go build -buildvcs=true -o bin/video-rename ./cmd/video-rename` | macOS arm64 应用构建通过 |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=true -o bin/video-rename-linux-amd64 ./cmd/video-rename` | Linux amd64 交叉编译通过；未实机执行 |
| 应用二进制在临时空目录、PATH 不含 Git 的环境执行 `--version` | 通过，不依赖源码目录或运行配置 |
| `gofmt -l`、`git diff --check`、双语 README 与开发文档中的 Bash 语法 | 通过；当前调用方已无旧版本脚本引用 |

13 个构建场景：无 tag、旧格式 tag 不作为模块版本、标准 tag、空覆盖值、`dev` 覆盖值、`(devel)` 覆盖值、有效显式覆盖、dirty tag、dirty 显式覆盖、tag 后提交、禁用 VCS、无 Git 源码包、源码包显式注入。夹具二进制在源码目录之外、PATH 不含 Git 的环境运行。

当前工作区应用的版本输出：

```text
video-rename v0.0.0-20261008133736-0a1d64dcfaa1+dirty (revision=0a1d64dcfaa1, modified)
```

这是 Go 生成的伪版本；`+dirty` 与 `modified` 如实反映未提交改动，不是新发布版本。

## 验证边界

本轮未重跑全仓业务/竞态测试、浏览器 E2E、FFmpeg 集成、数据库规模测试、依赖漏洞扫描或全应用干净依赖安装。Web 构建使用已有依赖，应用构建使用本地 Go 缓存；版本夹具从临时源码目录真实编译。未执行 Docker 构建、Linux 实机运行、NAS 或真实模型/通知服务测试。未替换运行中的服务，不把历史验证结果作为本轮通过依据。
