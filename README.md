<p align="center">
  <img src="web/public/logo.png" width="96" alt="video-rename logo">
</p>

<h1 align="center">video-rename · 视频命名工作台</h1>

<p align="center">
  自托管的本地媒体整理工作台：在浏览器里浏览、播放、改名和编号视频 / 图片 / 音频，<br>
  并可让视觉模型根据视频截图自动生成中文文件名。
</p>

<p align="center">
  <a href="https://github.com/jiazixin/video-rename/actions/workflows/ci.yml"><img src="https://github.com/jiazixin/video-rename/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="https://github.com/jiazixin/video-rename/releases/latest"><img src="https://img.shields.io/github/v/release/jiazixin/video-rename?sort=semver" alt="Release"></a>
  <img src="https://img.shields.io/badge/Go-1.27.2-00ADD8?logo=go&logoColor=white" alt="Go 1.27.2">
  <img src="https://img.shields.io/badge/Vue-3-42b883?logo=vuedotjs&logoColor=white" alt="Vue 3">
  <img src="https://img.shields.io/badge/platform-Linux%20%7C%20macOS-4c8b3f" alt="Linux | macOS">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License: Apache-2.0"></a>
</p>

<p align="center">
  <b>简体中文</b> · <a href="README.en.md">English</a>
</p>

---

video-rename 由 Go 服务、内嵌的 Vue 3 页面和 SQLite 组成，构建后只有一个可执行文件。它直接管理你指定的本地（或已挂载的）媒体目录：浏览与播放、手动改名、批量抽帧、图片和音频编号、按计划自动命名，并记录每一次改名、模型请求和用量。适合个人或受信任的小团队使用，只有一个管理员账号。

> [!IMPORTANT]
> - **删除文件或目录是永久删除**，不进回收站，也不能通过改名历史恢复。重要媒体请保留独立备份。
> - **SQLite 数据目录必须在本机磁盘**，不能放在 SMB/NFS 上；同一个数据库只运行一个实例。
> - **自动命名会把视频截图和提示词发送到你配置的模型地址**，可能产生费用；不会上传原视频。

## 功能特性

- **媒体浏览与预览**：目录索引与分页浏览；视频、音频使用浏览器原生解码，支持进度跳转和倍速；视频与图片在全屏暗场中查看，图片可缩放、旋转并带同目录缩略图；音频旁列出同目录曲目。不做服务端转码。
- **安全改名**：手动改名从不覆盖已有文件；先持久化操作日志再移动，跨平台使用原子的“不替换”重命名；符合条件的改名可在记录中一键还原。
- **AI 自动命名**：按规则从视频采样截图，交给支持图片输入的模型生成中文文件名。内置智谱、DeepSeek、OpenAI、Claude、Ollama 预设，也可自定义 OpenAI 兼容或 Anthropic Messages 服务；支持思考参数与图片识别测试。
- **批量处理**：独立抽帧（截图可复用于后续命名）、图片 / 音频编号（带冻结预览、两阶段移动）、处理标记保护已有名称。
- **队列与计划**：持久任务队列，支持暂停、重试、取消，服务重启后恢复；按目录和时区配置定时命名或抽帧；后台目录扫描可配置范围与排除目录。
- **记录与分析**：批次详情按目录汇总原名、新名和全部尝试历史；运行记录与失败诊断；模型请求用量、耗时和失败原因分析。
- **通知**：钉钉、企业微信、Bark、Telegram 与通用 Webhook，可只在失败时通知。
- **数据安全**：模型密钥加密存储；文件操作逐层校验目录、拒绝符号链接并核对文件身份；不支持的旧数据库拒绝打开并保留原样。

## 运行要求

| 项目 | 要求 |
| --- | --- |
| 操作系统 | Linux 或 macOS |
| FFmpeg | 抽帧与自动命名需要 `ffmpeg`、`ffprobe` 在 PATH 中；只浏览和手动改名可不安装 |
| 模型服务（可选） | 支持图片输入的 OpenAI 兼容或 Anthropic 服务；不使用自动命名时无需配置 |
| 数据目录 | 本机磁盘；媒体目录可以是网络挂载 |
| 从源码构建 | Go 1.27.2、Node.js ≥ 22.12、npm；从 Git 检出构建还需要 Git |

## 快速开始

### 1. 下载预编译版本

在 [Releases](https://github.com/jiazixin/video-rename/releases/latest) 下载对应平台的包。提供 linux amd64 / arm64 / ppc64le / s390x / riscv64 / loong64 与 darwin amd64 / arm64；其中 linux amd64、arm64 和 darwin 之外的架构只做过交叉编译，未在实机上运行过。

```sh
ver=1.0.0   # 替换为 Releases 页面上的实际版本（不带 v）
curl -LO https://github.com/jiazixin/video-rename/releases/download/v$ver/video-rename_${ver}_linux_amd64.tar.gz
curl -LO https://github.com/jiazixin/video-rename/releases/download/v$ver/checksums.txt
sha256sum --ignore-missing -c checksums.txt
mkdir video-rename && tar -xzf video-rename_${ver}_linux_amd64.tar.gz -C video-rename
cd video-rename && ./video-rename --version
```

包内包含二进制、LICENSE、中英文 README、[配置示例](configs/config.example.yaml)，以及[配置参考](docs/configuration.md)、[运行与恢复](docs/operations.md)、[模型配置](docs/models.md)三份文档。

> macOS 包只有 ad-hoc 签名、未经公证。用浏览器下载后如被 Gatekeeper 拦截，可执行 `xattr -d com.apple.quarantine video-rename`。

### 2. 编写配置并启动

```sh
cp configs/config.example.yaml configs/config.local.yaml
chmod 600 configs/config.local.yaml
```

编辑 `configs/config.local.yaml`，填写实际媒体目录和密码：

```yaml
listen: "127.0.0.1"
port: 8790
media-path: "/srv/videos"   # 已存在的媒体根目录
storage: "../data"          # SQLite 与密钥，相对于配置文件所在目录
tmp-storage: "../.cache"    # 可重建的截图缓存
auth:
  username: "admin"
  password: "请替换为你的密码"
```

```sh
./video-rename --config configs/config.local.yaml
```

打开 [http://127.0.0.1:8790](http://127.0.0.1:8790)，用配置中的账号登录。不带参数时读取工作目录下的 `configs/config.local.yaml`；其他参数见 `--help`。

### 3. 从源码构建

在仓库根目录执行。必须先构建 Web，因为 `web/embed.go` 会把 `web/dist/` 嵌入二进制：

```sh
npm --prefix web ci
npm --prefix web run build
go build -trimpath -o bin/video-rename ./cmd/video-rename
./bin/video-rename --version
```

交叉编译时 Web 产物不变，只需给 `go build` 加目标平台：`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o bin/video-rename-linux-amd64 ./cmd/video-rename`。之后按上一步复制配置并以 `./bin/video-rename --config configs/config.local.yaml` 启动。

## 第一次使用

1. 在“文件目录”浏览媒体。首次打开目录会建立索引，刷新后显示完整列表。
2. 手动改名或编号前先检查选择范围。编号提供预览；改名不覆盖已有文件，可在记录中还原符合条件的改名。
3. 自动命名前，在“系统设置 → Prompt配置”准备提示词，再到“模型配置”填写地址、模型和密钥。先用少量视频验证质量和费用。
4. 在文件目录选择视频或目录，启动抽帧或命名；在“批次记录”查看进度、暂停或取消，详情可筛选最终失败文件；在“运行记录”查看改名历史与失败诊断。

| 功能 | 说明 |
| --- | --- |
| 处理标记 | 保护已有名称；标记过的图片和音频跳过编号，标记过的视频跳过普通自动命名 |
| 独立抽帧 | 不改名；截图可供后续命名复用 |
| 计划管理 | 按目录和时区安排定时命名或抽帧；停机期间错过的计划不补跑 |
| 后台扫描 | 默认开启，启动一小时后首次运行、每轮结束再等一小时；可修改范围、排除目录、间隔或关闭。扫描只更新文件列表，不触发处理 |
| 用量与分析 | 查看请求用量、耗时、失败原因；缺少模型 usage 时只估算文字 token，不含图片 |

## 配置

完整字段、默认值和页面资源设置见 [配置参考](docs/configuration.md)。要点：

- `-f <文件>` 与 `--config <文件>` 等价；默认读取工作目录下的 `configs/config.local.yaml`。不支持用环境变量覆盖配置项。
- 配置中的相对路径以配置文件所在目录为基准。媒体目录必须已经存在；配置、数据和缓存目录必须位于媒体目录之外。
- 媒体、配置和持久数据不能放入 `tmp-storage/video-rename-frames-v1/`，该目录由程序自动清理；危险的路径重叠会在启动前被拒绝。
- 示例密码不能用于启动。模型、抽帧规则、提示词、通知和扫描都在 Web 页面设置。

## 部署与数据

- **监听**：仅本机使用时保留默认 `127.0.0.1`；局域网访问可改为 `0.0.0.0`。远程访问请使用 HTTPS 反向代理，限制入口访问并保留原始 Host。不要把管理员工作台直接暴露到不受信任网络。
- **备份**：停止服务后备份完整 `storage` 目录，数据库与 `data.sqlite.key` 必须一起保留；缓存可以重建。恢复旧备份后先核对期间发生的文件改动。
- **网络存储**：媒体目录可以是 SMB/NFS 挂载，但实际吞吐和文件身份行为需要在目标环境验证。
- **兼容性**：项目尚未发布稳定版，不提供旧数据库迁移或旧配置兼容。不受支持的数据库会拒绝打开并保留原样；需要重建时请明确选择新的 `storage` 目录。

批量操作、标记、重试、还原、通知与备份的细节见 [运行与恢复](docs/operations.md)。

## 开发

```sh
npm --prefix web ci
npm --prefix web run format:check
npm --prefix web run build
go vet ./...
go test -race -count=1 ./...
go test -tags=integration -count=1 ./internal/media ./cmd/video-rename
go build -o bin/video-rename ./cmd/video-rename
npm --prefix web exec -- playwright install chromium
npm --prefix web run test:e2e
```

开发时先启动 Go 服务，再运行 `npm --prefix web run dev`，Vite 会把 `/api` 代理到 `127.0.0.1:8790`。浏览器测试使用隔离端口 **18790**、临时数据库、合成媒体、真实 FFmpeg 和本地假模型，请保持该端口空闲。规模测试与依赖维护见 [开发与测试](docs/IMPLEMENTATION.md)。

推送到 `main` 和 Pull Request 时，[CI](.github/workflows/ci.yml) 会运行 Web 格式与类型检查、Go 检查与竞态测试、FFmpeg 集成测试、Playwright E2E，以及 GoReleaser 多目标快照构建。

## 版本与发布

- 版本号来自 Git tag，格式为 `v主版本.次版本.修订版本`（预发布如 `v1.2.0-rc.1`）。Go 构建时自动写入模块版本，非 tag 提交显示伪版本，有未提交修改时带 `+dirty`。
- 查询版本：`video-rename --version`。该命令不读取配置，也不启动服务。
- 源码包没有 `.git` 时默认显示 `dev`，可显式注入与源码一致的版本：`go build -trimpath -ldflags='-X main.version=v1.0.0' -o bin/video-rename ./cmd/video-rename`。
- 推送标准 tag 会触发 [Release 工作流](.github/workflows/release.yml)：先核对 `--version` 与 tag 一致，再由 GoReleaser 构建全部目标，并上传 tar.gz 与 `checksums.txt`。

版本优先级与验证方式见 [构建版本](docs/IMPLEMENTATION.md#依赖维护与构建版本)。

## 文档

| 文档 | 内容 |
| --- | --- |
| [docs/configuration.md](docs/configuration.md) | 配置字段、资源限制、扫描与抽帧设置 |
| [docs/operations.md](docs/operations.md) | 批量操作、标记、重试、还原、通知和备份 |
| [docs/models.md](docs/models.md) | 服务地址、图片识别测试、密钥及思考参数 |
| [docs/processing.md](docs/processing.md) | 探测、采样、截图预算和失败处理 |
| [docs/DESIGN.md](docs/DESIGN.md) | 当前架构、数据流、持久化与 UI 约定 |
| [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) | 模块入口、构建、回归、规模测试、发布与 CI |
| [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) | 最新验收结果与未验证范围 |
| [docs/README.md](docs/README.md) | 完整文档索引 |

## 项目结构

```text
cmd/video-rename/        CLI 入口与构建版本
internal/                Go 服务：app、storage、library、rename、jobs、media、models、notifications 等
web/                     唯一 npm/Vite 边界；Vue 源码、Playwright E2E、embed.go
configs/                 配置示例；本地实例 config.local.yaml 被忽略
docs/                    使用、设计、开发与验收文档；archive/ 为历史记录
.github/                 GitHub Actions CI 与 tag 发布
.goreleaser.yaml         GoReleaser 多目标发布配置
bin/ data/ .cache/       忽略的本机二进制（含 bin/release/ 发布预演）、状态与缓存
coverage/ test-results/  忽略的覆盖率与测试输出
```

目录基线只在 [目录规范.md](目录规范.md) 维护，协作规则见 [AGENTS.md](AGENTS.md)。本项目的目录例外：

| 基线规则 | 实际行为 | 原因与依据 | 适用范围 / 重新评估条件 |
| --- | --- | --- | --- |
| §3 根目录 README | 中文 `README.md` 为主入口，英文翻译为 `README.en.md` | 用户要求中英文 README | 两份同步维护；放弃英文版时评估 |
| §7.1 配置入口与覆盖 | `-f` / `--config` 选择配置，默认工作目录下的 `configs/config.local.yaml`；不读取 `CONFIG_FILE`，无环境变量覆盖 | 已有 CLI 配置协议 | 本程序；仅用户要求改变时评估 |
| §11 工具必需路径 | `web/embed.go` 嵌入 `web/dist` | Go embed 与 Vite 的实际构建边界 | Web 资源；交付方式改变时评估 |
| §4 `bin/` 只放可执行文件 | GoReleaser 输出（二进制、tar.gz、checksums、元数据）在 `bin/release/` | 默认根 `dist/` 未被忽略会让 Go 标记 dirty，且 `dist/` 保留给 Web 产物 | 发布构建；更换发布工具时评估 |

## 参与贡献

欢迎提交 Issue 和 Pull Request。提交前请：

1. 先阅读 [AGENTS.md](AGENTS.md) 与 [目录规范.md](目录规范.md)，新文件按其中的目录规则放置；
2. 在本地运行上文“开发”中的检查，前端改动还需运行相关 E2E；
3. 测试只使用临时目录、隔离端口、合成媒体和本地假模型，不要使用真实媒体库、配置或外部模型服务。

## 许可证

本项目基于 [Apache License 2.0](LICENSE) 开源。内嵌的前端依赖遵循其各自许可，例如 Vue（MIT）、Lucide 图标（ISC）和 Inter 字体（OFL-1.1）。
