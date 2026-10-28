# 全仓代码规范整改计划（已完成）

> 历史计划，2026-10-28 已完成。当前行为与测试结果以 [最新验收](../ACCEPTANCE.md) 为准，不以本计划中的中间设想为准。

**Goal:** 按当前目录、通用、Go、Web 与 Vue 规范修复已核实的差异，删除旧格式兼容实现。

**Architecture:** 保留领域边界，由私有存储依赖与事务回调管理数据库访问，将 HTTP 中的查询归还领域。服务端、Web 调用、测试及接口文档同步切换契约。保留用户数据，结构不符直接拒绝打开。

**Tech Stack:** go.mod 声明的 Go、modernc SQLite，Vue 3、TypeScript、Vite、Vitest、Playwright。

## 已核实与纠正

- SQLite 已通过启动语句开启 WAL；需统一连接 DSN、立即写事务及存储封装，不应称为完全未启用 WAL。
- 领域包持有 SQL 并非天然违规；连接泄露、HTTP 直接 SQL 和事务生命周期分散才是整改重点。
- 函数约 80 行、文件约 600 行是评审信号；按职责拆分，不为行数机械拆分。
- 当前旧编号错误解析分支应删除；OpenAI 兼容协议属于产品支持，不属于旧代码兼容层。

## 执行批次

1. 基础正确性与可验证性：internal/timefmt、秘密字符串类型、构建标签、错误处理、Fuzz、CLI 元数据；更新所有调用和测试。先验证秘密打印、时间往返与解析器边界，再运行相关 Go 测试。
2. 数据边界：internal/storage 私有连接、Write/View 生命周期、DSN 与只读池；领域私有依赖和构造函数；internal/app 的记录查询迁至对应领域。验证回滚、并发快照、取消、只读和拒绝旧库。
3. HTTP 契约：真实 Merge Patch、ETag/If-Match、创建 201、Idempotency-Key、容量冲突；同步 web/src/app、业务请求、E2E 以及 docs/API.md。验证部分更新、清空、陈旧版本、幂等重放与不同请求冲突。
4. Web 归属与生命周期：SystemSettingsPage → features/settings，ToastNotice → components；业务样式离开 styles；onScopeDispose；composable 和关键组件交互测试；color-scheme。桌面、窄屏、焦点与错误状态由浏览器回归。
5. 全仓复查：长参数改参数对象、英文诊断与注释、只保留有效路径和协议；README 双语、依赖决策、开发说明、CI 对齐。执行原生检查并将实际结果写入 docs/ACCEPTANCE.md，完成计划归档。

## 原生验收命令

```sh
npm --prefix web run format:check
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
go mod verify
go mod tidy -diff
go vet ./...
go vet -tags integration,scale ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
go test -race -count=1 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go test -tags integration -count=1 ./internal/media ./cmd/video-rename
go build -o bin/video-rename ./cmd/video-rename
npm --prefix web run test:e2e
```

各平台 vet/交叉构建、scale、短时 Fuzz、无已有产物的隔离副本验证另记录实际命令；不把交叉构建或本机合成测试写成目标设备验证。所有测试使用临时配置、数据库、媒体和本地假服务。
