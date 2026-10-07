> 历史记录：已完成或被后续实现取代；当前使用见 [文档索引](../README.md)，最新验证见 [验收记录](../ACCEPTANCE.md)。

# 批次容量配置 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将固定 20 个活动批次改为系统设置中独立的运行容量和等待容量。

**Architecture:** 在现有 system_settings.policy 中保存批次容量，不修改数据库结构。调度器动态读取运行上限，入队在写事务中检查等待容量。运行中暂停仍占运行名额，未启动暂停占等待容量；降低上限保留已创建批次。

**Tech Stack:** Go 1.27.1、SQLite、Vue / TypeScript、Playwright。

---

## 设计决定

默认同时运行 32 个、等待 1000 个。可配置运行范围 1–256，等待范围 1–100000；这些是容量约束，不是吞吐保证。等待包括 queued 和未开始的 paused；执行名额包括扫描、运行、已开始暂停及取消收尾。设置缺少新字段时使用默认值，明确的非法值拒绝。保存扫描配置和处理配置时保留对方字段。完成通知在关闭时响应 context，避免扩容后等待工作退出卡住。

### Task 1: 持久化及校验

修改 internal/systemsettings/store.go、scan.go，增加独立 BatchLimits 与事务可用的读取函数。更新 store_test.go 和 scan_test.go，验证默认值、边界、重启及扫描设置合并。

### Task 2: 入队和调度

修改 internal/jobs/create.go、manager.go；在入队事务内检查最新等待上限，按运行上限调度最早 queued 批次。新增 internal/jobs/batch_limits_test.go，覆盖超过原20个、暂停分类、幂等提交、并发上限动态调整、取消/关闭。

### Task 3: 设置界面和 API

修改 web/src/features/settings/Processing.vue 与 internal/app/system_settings_test.go，添加数字输入和明确生效语义。扩展 web/e2e/settings.spec.ts 验证保存、刷新和窄屏布局。

### Task 4: 文档和验证

更新 docs/configuration.md、operations.md。执行 npm --prefix web run format:check、check、build；go test -count=1 ./...、go test -race -count=1 ./internal/jobs ./internal/systemsettings ./internal/app、go vet ./...、go build -o bin/video-rename ./cmd/video-rename；最后执行 settings.spec.ts 浏览器测试，隔离端口18790及临时数据。
