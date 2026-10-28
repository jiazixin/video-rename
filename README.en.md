<p align="center">
  <img src="web/public/logo.png" width="96" alt="video-rename logo">
</p>

<h1 align="center">video-rename</h1>

<p align="center">
  A self-hosted workspace for organizing local media: browse, play, rename and number videos, images and audio in your browser,<br>
  and let a vision model generate Chinese video filenames from sampled frames.
</p>

<p align="center">
  <a href="https://github.com/jiazixin/video-rename/actions/workflows/ci.yml"><img src="https://github.com/jiazixin/video-rename/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="https://github.com/jiazixin/video-rename/releases/latest"><img src="https://img.shields.io/github/v/release/jiazixin/video-rename?sort=semver" alt="Release"></a>
  <img src="https://img.shields.io/badge/Go-1.27.3-00ADD8?logo=go&logoColor=white" alt="Go 1.27.3">
  <img src="https://img.shields.io/badge/Vue-3-42b883?logo=vuedotjs&logoColor=white" alt="Vue 3">
  <img src="https://img.shields.io/badge/platform-Linux%20%7C%20macOS-4c8b3f" alt="Linux | macOS">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License: Apache-2.0"></a>
</p>

<p align="center">
  <a href="README.md">简体中文</a> · <b>English</b>
</p>

---

video-rename combines a Go server, an embedded Vue 3 interface, and SQLite in a single executable. It works directly on the local (or mounted) media directory you configure: browse and play files, rename them manually, extract frames in batches, number images and audio, run scheduled automatic naming, and keep a record of every rename, model request, and token used. It is intended for personal use or a trusted small team with a single administrator account. The application interface is in Chinese.

> [!IMPORTANT]
> - **Deleting a file or directory is permanent.** It does not use the trash and cannot be undone through rename history. Keep independent backups of valuable media.
> - **Store the SQLite data directory on a local disk**, never on SMB/NFS, and run only one instance per database.
> - **Automatic naming sends sampled frames and prompts to the model endpoint you configure** and may incur charges. Original videos are never uploaded.

## Features

- **Media browsing and preview**: indexed, paginated directory listings; native browser video/audio decoding with seeking and playback speed; videos and images open in a dark full-screen viewer, images with zoom, rotation and a same-folder thumbnail strip; audio shows the same-folder track list. No server-side transcoding.
- **Safe renaming**: manual renames never overwrite an existing file. The operation is journaled before the move and uses the platform's atomic no-replace rename; eligible renames can be undone from history.
- **AI naming**: frames are sampled from each video and sent to an image-capable model to produce a Chinese filename. Presets cover Zhipu, DeepSeek, OpenAI, Claude and Ollama, plus custom OpenAI-compatible or Anthropic Messages endpoints, with thinking parameters and an image-recognition test.
- **Batch processing**: standalone frame extraction (frames are reused for later naming), image/audio numbering with a frozen preview and two-phase moves, and processed marks that protect existing names.
- **Queue and schedules**: a persistent job queue with pause, retry and cancel that resumes after restart; per-directory, time-zone-aware naming or extraction schedules; background directory scans with configurable scopes and exclusions.
- **History and analytics**: batch details grouped by directory with original/new names and every attempt, plus the live stage of files being processed; run history and failure diagnostics; model request usage, latency and failure analysis.
- **Notifications**: DingTalk, WeCom, Bark, Telegram and generic webhooks, optionally only on failure.
- **Data safety**: model credentials are encrypted at rest; file operations open each parent directory, reject symbolic links and verify file identity; unsupported databases are rejected and left untouched.

## Requirements

| Item | Requirement |
| --- | --- |
| Operating system | Linux or macOS |
| Browser | Chrome / Edge 124+, Firefox 124+, Safari 17.4+; native request cancellation APIs required |
| FFmpeg | `ffmpeg` and `ffprobe` on PATH for frame extraction and automatic naming; not needed for browsing and manual renaming |
| Model service (optional) | An image-capable OpenAI-compatible or Anthropic service; not needed without automatic naming |
| Data directory | Local disk; media may reside on a network mount |
| Building from source | Go 1.27.3, Node.js ≥ 26.0, npm; Git when building from a Git checkout |

Copy buttons use the browser Clipboard API, which requires HTTPS or localhost and the relevant permission. When unavailable, the text remains selectable for manual copying.

## Quick start

### 1. Download a prebuilt release

Download the archive for your platform from [Releases](https://github.com/jiazixin/video-rename/releases/latest). Builds are provided for linux amd64 / arm64 / ppc64le / s390x / riscv64 / loong64 and darwin amd64 / arm64. Linux targets other than amd64 and arm64 are cross-compiled only and have not been run on real hardware.

```sh
ver=1.0.0   # replace with the actual version on the Releases page (without the v)
curl -LO https://github.com/jiazixin/video-rename/releases/download/v$ver/video-rename_${ver}_linux_amd64.tar.gz
curl -LO https://github.com/jiazixin/video-rename/releases/download/v$ver/checksums.txt
sha256sum --ignore-missing -c checksums.txt
mkdir video-rename && tar -xzf video-rename_${ver}_linux_amd64.tar.gz -C video-rename
cd video-rename && ./video-rename --version
```

Each archive contains the binary, LICENSE, both READMEs, the [configuration example](configs/config.example.yaml), and the [configuration](docs/configuration.md), [operations](docs/operations.md) and [model setup](docs/models.md) references (in Chinese).

> macOS archives are ad-hoc signed and not notarized. If Gatekeeper blocks a browser download, run `xattr -d com.apple.quarantine video-rename`.

### 2. Configure and start

```sh
cp configs/config.example.yaml configs/config.local.yaml
chmod 600 configs/config.local.yaml
```

Edit `configs/config.local.yaml` with your media directory and a strong, unique password:

```yaml
listen: "127.0.0.1"
port: 8790
media_path: "/srv/videos"   # existing media root
storage: "../data"          # SQLite and key, relative to this file's directory
tmp_storage: "../.cache"    # rebuildable frame cache
auth:
  username: "admin"
  password: "" # Set a strong, unique password.
```

```sh
./video-rename --config configs/config.local.yaml
```

Open [http://127.0.0.1:8790](http://127.0.0.1:8790) and sign in with your configured credentials. Without arguments the binary reads the file named by the `CONFIG_FILE` environment variable, or else `configs/config.local.yaml` under the working directory; see `--help` for other options.

### 3. Build from source

Run from the repository root. Build the Web assets first, because `web/embed.go` embeds `web/dist/` into the binary:

```sh
npm --prefix web ci
npm --prefix web run build
go build -trimpath -o bin/video-rename ./cmd/video-rename
./bin/video-rename --version
```

To cross-compile, keep the same Web build and set the target for `go build`: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o bin/video-rename-linux-amd64 ./cmd/video-rename`. Then copy the configuration as in step 2 and start with `./bin/video-rename --config configs/config.local.yaml`.

## Using the application

1. Open **文件目录** (media library). Opening a directory starts indexing it; the complete listing appears after a refresh.
2. Check your selection before renaming or numbering. Numbering includes a preview. Renaming never overwrites an existing target; eligible renames can be undone from history.
3. For automatic naming, create a prompt under **系统设置 → Prompt配置**, then configure the endpoint, model, and API key under **模型配置**. Try a small sample to check quality and cost first.
4. Select videos or directories and start extraction or naming. **批次记录** shows progress with pause/cancel controls, and its details can filter files that are processing, done, skipped, or finally failed; processing files show a stage timeline with old and new names. **运行记录** shows rename history and failure diagnostics.

| Feature | Behavior |
| --- | --- |
| Processed marks | Protect existing names: marked images/audio skip numbering, and marked videos skip normal automatic naming |
| Frame extraction | Does not rename files; cached frames can be reused for naming |
| Schedules | Naming or extraction per directory with an explicit time zone; missed runs are not replayed after downtime |
| Background scans | On by default: first run one hour after startup, then one hour after each scan. Scope, exclusions, interval and the on/off switch are configurable. Scans only update listings and never start processing |
| Usage and analysis | Request usage, durations, and failures; when model usage is unavailable, estimates cover text tokens only, not images |

## Configuration

See the [configuration reference](docs/configuration.md) for every field, default, and in-app resource setting. Key points:

- `-c <file>` and `--config <file>` are equivalent; naming different files with both is an error. The file is chosen as `--config` > `CONFIG_FILE` > `configs/config.local.yaml` under the working directory. A file named by `--config` or `CONFIG_FILE` must exist, and an empty `CONFIG_FILE` is an error; when the default file is missing the built-in defaults are used, but `media_path` and `auth` have no defaults, so startup still fails and reports the missing field. Apart from `CONFIG_FILE`, environment variables do not override configuration.
- Relative paths resolve against the configuration file's directory. The media directory must already exist, and configuration, storage, and cache must live outside it.
- Keep media, configuration, and persistent data out of `tmp_storage/video-rename-frames-v1/`, which the application cleans automatically. Unsafe path overlaps are rejected before startup.
- The example password cannot be used to start. Models, frame rules, prompts, notifications, and scans are configured in the Web interface.

## Deployment and data

- **Listening**: keep the default `127.0.0.1` for local use; set `listen` to `0.0.0.0` for LAN access. For remote access, use an HTTPS reverse proxy, restrict access, and preserve the original Host header. Do not expose the administrator workspace directly to an untrusted network.
- **Backups**: stop the service and back up the entire `storage` directory, keeping the database and `data.sqlite.key` together. The cache is rebuildable. After restoring an older backup, reconcile file changes made in the meantime.
- **Network storage**: media may live on an SMB/NFS mount, but verify throughput and file-identity behavior on your deployment.
- **Compatibility**: there is no stable release yet, so legacy databases and configuration are not migrated. Unsupported databases are rejected and left untouched; choose a new `storage` directory when rebuilding.

See [operations](docs/operations.md) for batch actions, marks, retries, undo, notifications, and backups.

## Development

```sh
npm --prefix web ci
npm --prefix web run format:check
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
go vet ./...
go test -race -count=1 ./...
go test -tags=integration -count=1 ./internal/media ./cmd/video-rename
go build -o bin/video-rename ./cmd/video-rename
npm --prefix web exec -- playwright install chromium
npm --prefix web run test:e2e
```

For development, start the Go server and run `npm --prefix web run dev`; Vite proxies `/api` to `127.0.0.1:8790`. Browser tests use the isolated port **18790**, a temporary database, synthetic media, real FFmpeg, and a local mock model; keep that port free. Scale tests and dependency maintenance are covered in [development and testing](docs/IMPLEMENTATION.md).

On pushes to `main` and on pull requests, [CI](.github/workflows/ci.yml) runs Web formatting and type checks, Go checks and race tests, FFmpeg integration tests, Playwright E2E, and a multi-target GoReleaser snapshot build.

## Versions and releases

- Versions come from Git tags in the form `vMAJOR.MINOR.PATCH` (pre-releases such as `v1.2.0-rc.1`). Go stamps the module version at build time; untagged commits show a pseudo-version, and uncommitted changes add `+dirty`.
- Query the version with `video-rename --version`, which prints e.g. `video-rename v1.2.0 (revision=0123456789ab)`. It and `--help` (in English) print to stdout, read no configuration and start no service.
- Exit codes: 0 success (including a clean shutdown after SIGINT/SIGTERM), 2 usage error, 1 configuration, startup or runtime failure. Usage errors are written to stderr as plain text `video-rename: <error>` followed by `Run 'video-rename --help' for usage.`; configuration errors are one English line `video-rename: config: <error>`, with field paths for validation errors, e.g. `video-rename: config: port: must be between 1 and 65535`.
- Logs: once the configuration loads, service logs are written to stdout as JSON lines. Startup and shutdown events are `service_starting` (with `version`), `service_listening` (with `listen`), `service_stopping` and `service_stopped`; later startup or runtime failures are logged as `service_failed` and not repeated on stderr. The first SIGINT/SIGTERM starts a graceful shutdown (up to 10 seconds); a second one terminates the process.
- Source archives without `.git` report `dev`; inject a version matching the source explicitly: `go build -trimpath -ldflags='-X main.version=v1.0.0' -o bin/video-rename ./cmd/video-rename`.
- Pushing a standard tag triggers the [Release workflow](.github/workflows/release.yml): it checks that `--version` matches the tag, then GoReleaser builds every target and uploads the tar.gz archives and `checksums.txt`.

Version precedence and verification are described under [build versions](docs/IMPLEMENTATION.md#依赖维护与构建版本).

## Documentation

Detailed documentation is in Chinese.

| Document | Contents |
| --- | --- |
| [docs/configuration.md](docs/configuration.md) | Configuration fields, resource limits, scan and extraction settings |
| [docs/operations.md](docs/operations.md) | Batch actions, marks, retries, undo, notifications, and backups |
| [docs/models.md](docs/models.md) | Endpoints, image-recognition tests, keys, and thinking parameters |
| [docs/processing.md](docs/processing.md) | Probing, sampling, frame budgets, and failure handling |
| [docs/API.md](docs/API.md) | Contract for error responses, field errors, and job and record error codes |
| [docs/DESIGN.md](docs/DESIGN.md) | Current architecture, data flow, persistence, and UI conventions |
| [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) | Module map, builds, regression and scale tests, releases, and CI |
| [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) | Latest verification results and untested scope |
| [docs/README.md](docs/README.md) | Full documentation index |

## Project layout

```text
cmd/video-rename/        CLI entry point and build version
internal/                Go server: app, storage, library, rename, job, media, model, notification, ...
web/                     The only npm/Vite boundary: Vue sources, Playwright E2E, embed.go
configs/                 Configuration example; the local config.local.yaml is ignored
docs/                    Usage, design, development and acceptance docs; archive/ is historical
.github/                 GitHub Actions CI and tag releases
.goreleaser.yaml         GoReleaser multi-target release configuration
bin/ data/ .cache/       Ignored local binaries (including bin/release/ snapshots), state and cache
coverage/ test-results/  Ignored coverage and test output
```

The directory baseline is maintained only in [目录规范.md](目录规范.md), and collaboration rules are in [AGENTS.md](AGENTS.md). Directory exceptions for this project:

| Baseline rule | Actual behavior | Reason | Scope / re-evaluate when |
| --- | --- | --- | --- |
| §7.1 Config entry and overrides | `-c` / `--config` > `CONFIG_FILE` > `configs/config.local.yaml` under the working directory; no per-field environment overrides | Existing CLI configuration protocol | This binary; only when the user asks to change it |
| §11 Tool-required paths | `web/embed.go` embeds `web/dist` | The real build boundary between Go embed and Vite | Web assets; when the delivery model changes |
| §4 `bin/` holds executables only | GoReleaser output (binaries, tar.gz, checksums, metadata) goes to `bin/release/` | An unignored root `dist/` would mark Go builds dirty, and `dist/` is reserved for Web output | Release builds; when the release tool changes |

## Code exceptions

| Rule | Actual practice | Reason | Scope | Re-evaluate when |
| --- | --- | --- | --- | --- |
| 代码规范 §3.1 user text is mapped from codes by the client | The server generates Chinese for content sent to the model (default prompt, fixed prompt, frame captions, image-recognition test instruction), the default failure keywords, notification titles and bodies (including the test notification), and the seed names of the default prompt and model configuration plus the "副本" suffix of copied configurations | Prompt content and failure keywords are data sent to the model or matched against its output, notifications go to external channels, and seed names are user-editable data; none has a client that could map codes. The admin API and all records return only codes per the [error contract](docs/API.md) | `internal/model` (`DefaultBody`, seeding and copy, `probe.go`), `internal/systemsetting` (`DefaultFixedPrompt`), the frame captions in `internal/vision/client.go`, `internal/naming/failure.go`, notification content in `internal/notification` | Prompt content, notifications or seed data become multilingual |
| Web代码规范 §3 enable at least `strictTypeChecked` | `@typescript-eslint/restrict-template-expressions` uses `allowNumber: true`, so template literals may interpolate numbers directly | Interpolating a number cannot produce `[object Object]` or `undefined` text, and rewriting each site as `String(n)` hurts readability; all other types still fail | `web/eslint.config.js`, all web sources and E2E tests | The rule default or the standard changes |
| Go代码规范 §7 configuration is a value type; runtime editing requires an exception | Deployment Config loads at startup; model, prompt, sampling, numbering, scan and notification settings are stored in SQLite and edited through the UI. Each task freezes its processing settings | Runtime editing is a product feature; snapshots keep edits from changing already submitted tasks | Settings stores and task creation | Configuration lifecycle or snapshot policy changes |
| Go代码规范 §8 no file I/O during write transactions | Rename and numbering commits retain a final path and stat/fstat identity check; scan publication also checks the held directory handle | External processes do not honor application locks. Moving these checks outside the final transaction widens the window for committing success after replacement; concurrent mutation regressions cover it. Directory traversal, file content I/O, network requests and processes remain outside write transactions | Final identity checks in `internal/library` | Filesystem and database commit coordination changes |

## Contributing

Issues and pull requests are welcome. Before submitting:

1. Read [AGENTS.md](AGENTS.md) and [目录规范.md](目录规范.md), and place new files according to their directory rules;
2. Run the checks under “Development” locally, plus the relevant E2E tests for frontend changes;
3. Use only temporary directories, isolated ports, synthetic media and the local mock model in tests — never a real media library, configuration, or external model service.

## License

Licensed under the [Apache License 2.0](LICENSE). Embedded frontend dependencies keep their own licenses, for example Vue (MIT), Lucide icons (ISC), and the Inter font (OFL-1.1).
