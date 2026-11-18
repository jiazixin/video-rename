# 历史验收：CI 重型竞态测试拆分（2026-11-17）

> 历史记录。当前验证结果见 [最新验收](../ACCEPTANCE.md)。

## 范围与原因

只修改 `.github/workflows/ci.yml` 和配套文档。工作区开始时干净；Go 源码、测试断言、8,003 文件夹具、依赖和锁文件均未修改。此前目录改名修复的本机验证已移入 [历史验收](2026-11-17-before-ci-split-acceptance.md)。

[失败的 CI](https://github.com/jiazixin/video-rename/actions/runs/43494244688/job/132123053759) 对应提交 `ef63f53`：大目录编号用例约 322 秒后等待超时，随后 `internal/app` 达到 Go 默认的 10 分钟包级超时。该用例的等待截止来自整个测试包的截止前 30 秒，前置测试会消耗其预算。日志未报告竞态；其他作业通过，Go 作业后续的漏洞扫描、Fuzz 和集成测试被跳过。

## 修复

- 工作流只定义一次精确匹配的 `GO_HEAVY_TEST_PATTERN`。常规 `go` 作业用 `-skip`，独立 `go-heavy` 作业用 `-run`，合起来覆盖原有默认测试集。
- 两组均使用 `CGO_ENABLED=1 go test -race -count=1 -timeout=15m`。重型用例有独立测试进程与 runner，不再与其他 app 测试共享包级截止时间；不修改等待逻辑、测试数据和断言。
- 常规作业上限 40 分钟，包含环境准备、交叉 vet、静态检查、竞态、漏洞扫描、Fuzz 和集成测试；重型作业上限 25 分钟，给依赖安装与编译留出时间。新增作业也先构建真实 Web 嵌入资源，沿用固定版本的 Actions。
- 双语 README 与开发说明同步两条验证命令。新增作业带来额外环境准备与构建成本；任何一组失败仍使工作流失败。

## 实际验证

本机 macOS / arm64，Go 1.27.4、Node.js 26.13.1。所有测试使用原有临时文件和隔离数据库，不使用用户媒体、配置或真实外部模型。两组在本机顺序执行，避免彼此争用资源；CI 则由独立 runner 执行。

| 检查 | 结果 |
| --- | --- |
| `npm --prefix web ci --ignore-scripts`、`npm --prefix web run build` | 通过 |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` | 全部工作流通过语法、表达式与作业检查 |
| 常规组：`CGO_ENABLED=1 go test -race -count=1 -timeout=15m -skip '^TestLargeDirectoryNumberingPreviewIsPaged$' ./...` | 27 个有测试的包通过；app 97.662 秒、job 93.829 秒、library 117.315 秒，无竞态报告 |
| 重型组：`CGO_ENABLED=1 go test -race -count=1 -timeout=15m -run '^TestLargeDirectoryNumberingPreviewIsPaged$' -v ./internal/app` | 原用例通过，122.96 秒，无竞态报告 |
| 测试清单与两组执行记录比较 | 默认清单 601 个顶层测试 / Fuzz 目标 / 示例，常规组 600 个、重型组 1 个；并集完整、交集为空 |
| `npm --prefix web audit --audit-level=high --omit=dev` | 通过，生产依赖 0 漏洞；安装时全依赖审计仍提示 13 项 moderate，本次未升级依赖 |
| `git diff --check`、改动文档本地链接检查 | 通过 |
| Go 源码、测试与依赖锁文件差异检查 | 无改动，既有断言及竞态检查保留 |

竞态测试添加 `-json` 保存诊断输出，其他参数与工作流一致。日志位于被忽略的 `test-results/ci-regular-race.jsonl`、`test-results/ci-heavy-race.jsonl`，清单位于 `test-results/ci-test-inventory.jsonl`。

macOS 文件系统按既有条件跳过了 `TestAutoCommitAllocatesSuffixForNormalizationHardLink` 和 `TestAutoCommitAllocatesSuffixForHardLink/SOURCE.mp4`，原因分别是不支持独立的 Unicode 规范化同名文件和仅大小写不同的文件；没有新增跳过条件。三个无测试文件的包不计入 27 个测试包。

未提交或推送，也未运行修改后的 GitHub Actions。上述耗时仅为本机结果，不能视为 Ubuntu runner 实测或性能保证。未重新运行完整 Web 检查/E2E、交叉 vet/构建、Go 漏洞扫描、Fuzz、FFmpeg 集成或规模测试；相关工作流步骤保持不变。Linux 文件系统场景及远端时限仍需推送后验证。
