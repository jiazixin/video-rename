# 项目文档

当前版本为 Web 管理服务，启动与配置以根 [README](../README.md) 为准。

1. [整体设计](DESIGN.md)：需求、页面、数据模型和行为设计。
2. [配置参考](configuration.md)：当前 YAML 服务协议。
3. [运行说明](operations.md)：改名、还原、去重、调度与恢复。
4. [媒体处理](processing.md)：沿用的采样、图片和视觉模型协议。
5. [开发说明](IMPLEMENTATION.md)：目录边界、构建和测试。
6. [验收记录](ACCEPTANCE.md)：本轮实际执行结果与限制。

7. [模型配置](models.md)：服务预设、模型检测、密钥保存、任务快照与上下文隔离。

8. [模型请求排查](model-request.md)：提示词角色、图片组织、采样参数与标题创作。

`plans/` 保存当前实施计划。旧 CLI 方案及数据库升级说明已移除，历史可通过 Git 查看。
