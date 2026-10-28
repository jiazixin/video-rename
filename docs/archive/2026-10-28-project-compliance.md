# 全仓规范整改计划（已完成归档）

状态：2026-10-28 已完成归档。本文件保留实施范围，当前行为见设计与 API 文档，实际验证结果仅在 [最新验收](../ACCEPTANCE.md) 维护。

**Goal:** 完成本仓库前后端规范整改，删除未发布接口的旧协议分支，并以隔离测试验证。

**Architecture:** 在现有模块内统一领域错误、HTTP 分页与字段契约；前端通过共享路由状态和字段错误指令实现一致行为。保留现有设计与业务功能，直接修改所有调用方，不增加数据迁移或兼容适配。

**Tech Stack:** Go 1.27、SQLite、Vue 3、TypeScript、Vite、Vitest、Playwright。

---

### Task 1（已完成）: API pagination and resource contracts

Files: internal/app/{routes,library,history,extractions,runs,notifications,http}.go; internal/{library,record,job,notification}; web/src/app/contracts.ts; affected feature clients and web/e2e; docs/API.md.

1. 将契约测试改为要求 `{"items":[],"offset":0,"limit":100,"total":0}`，拒绝 `page`、旧游标和未知参数；运行 `go test ./internal/app -run Pagination` 观察失败。
2. 统一列表请求、查询、响应为行偏移量 `offset` 与 `limit`；页码 UI 仅在客户端换算。单次读取保持快照一致，空列表与超出范围均有明确结果。
3. 删除资源字段 `size`，改为 `size_bytes`；可选 `contents` 使用 `omitzero`，同步客户端与夹具。审查其他时长/大小字段。
4. 运行 `go test ./internal/app ./internal/library ./internal/record ./internal/job ./internal/notification` 与 `cd web && npm run typecheck`。

### Task 2（已完成）: Domain errors and test helper signatures

Files: internal/domain/errors.go; internal/app/http.go; domain users; affected *_test.go.

1. 增加领域验证错误通过包装后仍正确输出 HTTP `invalid_argument`/字段详情的测试；覆盖无意泄露内部错误的情形。
2. 领域仅提供具型别的失败与字段问题；HTTP 通用错误码在 HTTP 层映射，持久化业务诊断保持独立语义。
3. 将超过三个参数的测试辅助函数转为具名参数对象，同步全部调用点；用 Go AST 检查无遗漏。
4. 执行相关 Go 测试、goimports、go vet、staticcheck。

### Task 3（已完成）: Restorable route state

Files: web/src/app/use-route-state.ts; web/src/features/{library,history,runs,usage,notifications}; web/src/app/use-route-state.test.ts; web/e2e.

1. 增加直接 URL、刷新、浏览器后退、页面停用的行为测试。
2. 用共享 composable 同步可分享的过滤、页码、选中资源；解析非法 query 为明确默认值，避免 KeepAlive 组件污染别的路由。
3. 各页面恢复 query 并在变更后加载正确数据，保留滚动与选择；筛选变更重置分页，路由恢复保留原页码。
4. 执行 Vitest、类型检查及对应 Playwright 用例。

### Task 4（已完成）: Field validation and write feedback

Files: web/src/app/errors.ts; web/src/app/form-errors.ts; editable forms under web/src/features; relevant tests.

1. 复用单一 errorText 映射提取 `details.fields`，通过字段路径将 400 提示关联到对应控件。
2. 整改设置、模型、计划、通知、提示词、采样、登录与重命名表单的字段提示、焦点及 aria 关联。
3. 所有写入成功和失败通过共享 toast 给出结果；取消请求保持原生取消行为。
4. 验证多字段错误、未知字段/错误码、成功、取消、键盘焦点与重复提交。

### Task 5（已完成）: Design variables and remaining standards

Files: web/src/styles/theme.css; component styles; web/stylelint.config.js; repository static configs where needed.

1. 将颜色之外的字号、间距、圆角、阴影和动效字面值集中为 :root 变量，复用相同值，不改变布局。
2. 增加静态约束防止组件重新引入相关字面值；审查内联样式、嵌套三元、参数数量、错误吞没与取消生命周期。
3. 验证桌面和窄屏、长文本、加载/空/错误状态及键盘焦点。

### Task 6（已完成）: Final verification and documentation

Files: docs/{API,DESIGN,IMPLEMENTATION,ACCEPTANCE,README}.md; README.md and README.en.md where behavior changes.

1. 用原生命令执行 Go 格式/静态/race 测试及 Web 格式/lint/typecheck/unit/build/E2E。
2. 执行 integration、受影响的 scale、所有支持目标交叉构建与配置检查，区分本机测试和目标实测。
3. 核查无旧契约/兼容代码、无未解决的已知规范差异；维护文档与验证证据，归档完成计划。
4. 校验用户规范文件哈希、git diff --check 和文档链接；不擅自提交或推送。
