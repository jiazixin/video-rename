# 最新验收：子项变化后的目录改名（2026-11-14）

## 范围与原因

本次只修改 Go 目录扫描发布时的条目版本判断，新增 HTTP 和文件系统回归，并同步设计与接口说明。工作区开始时干净；数据库结构、依赖、前端源码未改。此前的处理标记优化、并发补验和默认端口验收已移入 [历史快照](archive/2026-11-14-before-directory-rename-acceptance.md)。

删除子文件后，目录的大小或 mtime 会变化。返回上一级时页面先显示已有列表，后台扫描随后更新元数据并递增目录版本；若改名框仍持有原 ETag，提交会返回 412 `precondition_failed`。在隔离夹具中固定“读列表 → 子项变化 → 读父级旧列表 → 扫描完成 → 用原 ETag 改名”的顺序，旧实现的删除、新增和子文件改名三个场景均复现该错误，不依赖固定等待。

扫描现在继续更新目录元数据和列表 generation，仅在目录自身类型变化时递增其版本；子项变化引起的目录大小/mtime 更新不递增。目录自身改名、还原仍递增版本，目录身份与路径仍在操作时复核，普通文件的大小/mtime 变化仍递增版本。

## 回归证据

- 新增 HTTP 回归的三个场景全部通过，并核对扫描后元数据、磁盘实际目录名和子项名称；真正改过目录名后，继续使用旧 ETag 仍返回 412。
- 新增目录身份回归覆盖“目录被替换，尚未扫描”和“目录被替换，已经扫描”；旧条目均不能改名，原目录及替换目录中的文件保持原样。
- 沿用现有 library/app 测试风格与隔离夹具，无新增规范例外。

## 实际检查

本机 macOS / arm64，Go 1.27.4、Node.js 26.13.1。测试使用临时媒体与数据库；浏览器用现有 18790 隔离服务、合成媒体与本地假模型，不使用用户配置或业务数据。

| 检查 | 结果 |
| --- | --- |
| 旧实现 `go test -run '^TestDirectoryRenameAfterChildMutationAndScan$' -count=1 ./internal/app` | 三个场景均按预期失败：412，期望 201 |
| 修复后 `go test -run '^TestDirectoryRename' -count=1 ./internal/app ./internal/library` | 全部通过 |
| `CGO_ENABLED=1 go test -race -count=1 ./...` | 全部通过，无竞态报告 |
| `go vet ./...` | 通过 |
| `go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./internal/library ./internal/app` | 通过 |
| 改动 Go 文件 gofmt / goimports v0.50.0 | 通过 |
| `git diff --check`、改动文档的本地文件链接检查 | 通过 |
| `npm --prefix web run build`、`go build -o bin/video-rename ./cmd/video-rename` | 通过 |
| Chromium 定向 E2E（下列命令） | 3 个用例通过 |

```sh
npm --prefix web run test:e2e -- delete.spec.ts workspace.spec.ts --grep '登录、目录、手动改名、冲突保护和还原|文件和非空目录确认后真实删除|返回目录恢复分页并居中定位'
```

未执行远端 CI、Linux/NAS 实机验证、交叉编译、规模测试、完整 Web 检查/E2E、FFmpeg 集成与漏洞扫描；本次未修改前端、媒体处理或依赖。浏览器回归覆盖现有改名/删除/返回目录流程，精确的扫描交错由 HTTP 测试控制。已生成本机构建，未重启或替换运行中的服务。
