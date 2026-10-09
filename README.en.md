# video-rename

[中文](README.md)

Organize local videos, images, and audio from your browser. Browse and play media, rename files manually, or use a vision model to generate Chinese video filenames from sampled frames. Batch frame extraction, image and audio numbering, scheduled jobs, history, and model usage reports are included.

The application combines a Go server, a Vue interface, and SQLite in one executable. It supports macOS and Linux and is intended for personal use or a trusted team with a single administrator account.

## Quick start

Building from source requires **Go 1.27.2**, **Node.js 22.12 or newer**, and npm; building a Git checkout also requires Git. Frame extraction and automatic naming also require `ffmpeg` and `ffprobe` on PATH. Automatic naming needs an image-capable OpenAI-compatible or Anthropic service. Browsing and manual renaming do not require a model service.

Run from the repository root:

```bash
npm --prefix web ci
npm --prefix web run build
go build -o bin/video-rename ./cmd/video-rename
cp configs/config.example.yaml configs/config.local.yaml
chmod 600 configs/config.local.yaml
```

Edit `configs/config.local.yaml` with your media directory and a strong, unique password:

```yaml
listen: "127.0.0.1"
port: 8790
media-path: "/srv/videos"
storage: "../data"
tmp-storage: "../.cache"
auth:
  username: "admin"
  password: "" # Set a strong, unique password.
```

The media directory must already exist. Keep configuration, storage, and cache outside that directory. Also keep media, configuration, and persistent data out of `tmp-storage/video-rename-frames-v1/`, which the application cleans automatically. Unsafe path overlaps are rejected before startup. Relative paths in YAML resolve against the configuration file's directory. Environment variables do not override configuration. Replace the example password before starting.

```bash
./bin/video-rename --config configs/config.local.yaml
```

Open [http://127.0.0.1:8790](http://127.0.0.1:8790) and sign in with your configured credentials. The default configuration path is `configs/config.local.yaml`. Use `--help` for CLI options and `--version` for build information. The application interface is in Chinese.

Ordinary Git builds use Go's embedded main-module version: a valid standard tag produces its version, other commits produce pseudo-versions, and uncommitted changes retain `+dirty` and `modified`. New release tags use `vMAJOR.MINOR.PATCH` (for example, `v1.2.0`). Existing `1.0` and `1.1` tags remain intact but are not Go module versions. Version queries need neither runtime configuration nor Git.

Source archives without `.git` default to `dev`. To identify such a build, first set `release_version` to the version matching the actual source, then inject it explicitly (build the Web assets first):

```bash
go build -ldflags "-X main.version=${release_version:?Set the actual source version first}" -o bin/video-rename ./cmd/video-rename
./bin/video-rename --version
```

Version precedence is valid explicit override → main-module version → 12-character commit ID → `dev`. Empty values, `dev`, and `(devel)` never hide an available version. Available VCS metadata retains the commit ID and modification marker; missing metadata does not establish a clean checkout. Shallow clones and missing tags limit version detection. See [build versions](docs/IMPLEMENTATION.md#依赖维护与构建版本) for details.

## Using the application

1. Open **文件目录** (media library). Opening a directory starts indexing it; the complete listing appears when indexing finishes.
2. Check your selection before renaming or numbering. Numbering includes a preview. Renaming never overwrites an existing target; eligible renames can be undone from history.
3. For automatic naming, create a prompt under **系统设置 → Prompt配置**, then configure the endpoint, model, and API key under **模型配置**. Try a small sample before processing a large library.
4. Select videos or directories and start extraction or naming. **批次记录** shows progress and pause/cancel controls. Batch details group files by directory, with original/new names, every request attempt, and a final-failure filter. **运行记录** shows rename history and failure diagnostics.

| Feature | Behavior |
| --- | --- |
| Media preview | Native browser video/audio decoding and seeking; image browsing; no server-side transcoding |
| Processed marks | Protect existing names: marked images/audio skip numbering, and marked videos skip normal automatic naming |
| Frame extraction | Does not rename files; cached frames can be reused for naming |
| Schedules | Naming or extraction with an explicit time zone; missed runs are not replayed after downtime |
| Usage and analysis | Request usage, durations, and failures; when model usage is unavailable, estimates cover text only |

Background indexing is enabled by default. It waits one hour after startup and after each completed scan. Change its scope, interval, or enabled state in settings. Scan scopes and excluded directories share a multi-select dialog; add or remove directories and save. Both manual and background scans skip excluded directories and their descendants, starting with the next scan. Exclusions preserve existing indexes and do not restrict browsing or media processing. Indexing only updates listings; processing is triggered by your actions or enabled schedules.

## Data, security, and deployment

- **Deleting a file or directory is permanent. It does not use the trash and cannot be undone through rename history.** Undoing a rename also requires matching file identity, content, and history. Keep independent backups of valuable media.
- Store SQLite on a local disk, never SMB/NFS, and run only one instance per database. Media may reside on a network mount; verify throughput and file identity behavior on your deployment.
- Stop the service and back up the entire `storage` directory. Keep the database and `data.sqlite.key` together. Cache files are rebuildable. Reconcile file changes before resuming work after restoring an older backup.
- Keep the default listener for local use. Set `listen` to `0.0.0.0` for LAN access. For remote access, use an HTTPS reverse proxy, restrict access, and preserve the original Host header. This administrator workspace should not be exposed directly to an untrusted network.
- Automatic naming sends sampled frames and prompts to your configured model endpoint and may incur charges. Original videos are not uploaded. Stored credentials are encrypted; configure only trusted endpoints.

This project has not launched. It does not provide legacy configuration support or database migrations. Unsupported databases are rejected without being reset; explicitly select a new `storage` directory when rebuilding a development instance.

## Development and reference

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

Browser tests use port **18790**, temporary data, synthetic media, real FFmpeg, and a local mock model. Keep that port free. For development, start the Go server and run `npm --prefix web run dev`; Vite proxies `/api` to `127.0.0.1:8790`.

Further documentation is in Chinese: [configuration](docs/configuration.md), [operation and recovery](docs/operations.md), [model setup](docs/models.md), [development and scale tests](docs/IMPLEMENTATION.md), and [latest verification results](docs/ACCEPTANCE.md).
