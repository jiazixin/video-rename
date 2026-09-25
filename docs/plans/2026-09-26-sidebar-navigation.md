# 侧边栏分组实施计划

目标：分组侧栏提供抽帧记录、运行记录、计划管理、批次记录、模型用量、运行分析独立入口。Vue hash 负责导航，Go 1.27.1 提供一致的来源/任务类型分页。

1. 修改 Workspace.vue、App.vue：三组导航、独立页面、标题与活动菜单，删除装饰文案。
2. 修改 History.vue、ExtractionHistory.vue：独立记录页，统一成功/失败切换。
3. 修改 Runs.vue、internal/jobs/pages.go、internal/app/app.go：分离计划和批次，补全部来源和任务类型筛选。
4. 修改 Usage.vue、关联跳转文案和 README。
5. 更新分页单元测试与受影响浏览器测试，验证导航、抽帧、计划、批次分页和窄屏。

验证：npm --prefix web run check；npm --prefix web run build；go test ./internal/jobs ./internal/app；go vet ./internal/jobs ./internal/app；go build -o bin/video-rename ./cmd/video-rename；npm --prefix web run test:e2e。
