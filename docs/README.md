# 文档索引

[中文使用说明](../README.md) · [English README](../README.en.md)

## 使用与维护

| 文档 | 用途 |
| --- | --- |
| [配置参考](configuration.md) | 部署 YAML、默认值和页面资源设置 |
| [运行与恢复](operations.md) | 浏览、处理、编号、删除、重试、调度、通知和备份 |
| [模型配置](models.md) | 地址、密钥、提示词、识别测试及思考参数 |
| [模型请求排查](model-request.md) | 请求示例、诊断和协议差异 |
| [媒体处理](processing.md) | 探测、采样、截图预算和失败处理 |

## 开发与验证

| 文档 | 用途 |
| --- | --- |
| [当前设计](DESIGN.md) | 架构、数据流、持久化与 UI 行为约定，含批次详情目录分组与扫描排除规则 |
| [接口错误约定](API.md) | 错误响应、参数错误、任务与记录错误代码，前后端共同遵守 |
| [开发与测试](IMPLEMENTATION.md) | 模块入口、构建、回归、规模测试、依赖维护、CI 与发布 |
| [最新验收](ACCEPTANCE.md) | 本次实际执行结果、修复证据和未验证范围 |
| [Vue Router 决策](decisions/2026-10-27-vue-router.md) | 多页面路由、直接页面地址与服务端 SPA 回退 |

## 历史验收索引

| 记录 | 状态与范围 |
| --- | --- |
| [全仓整改前的累计验收](archive/2026-10-27-before-full-compliance-acceptance.md) | 历史；包含 2026-10-22 至 10-27 的代码质量、接口、context 与 Web 整理结果，由 2026-10-28 [最新验收](ACCEPTANCE.md) 替代 |
| [预览窗口](archive/2026-10-20-preview-window-acceptance.md)、[改名焦点](archive/2026-10-20-rename-focus-acceptance.md)、[运行中记录](archive/2026-10-20-run-live-records-acceptance.md) | 2026-10-20 历史专项验收 |
| [CI 与发布](archive/2026-10-19-ci-release-acceptance.md)、[按钮与媒体播放器](archive/2026-10-19-ui-buttons-media-player-acceptance.md) | 2026-10-19 历史专项验收 |
| [原生构建版本](archive/2026-10-09-native-version-acceptance.md)、[发布安全](archive/2026-10-08-release-safety-acceptance.md)、[改名记录](archive/2026-10-07-rename-record-fix-acceptance.md) | 2026-10-07 至 10-09 历史专项验收 |
| [2026-10-05 验收](archive/2026-10-05-acceptance.md)、[2026-09-30 验收](archive/2026-09-30-acceptance.md) | 较早阶段的历史测量与验收 |
| [历史归档目录](archive/) | 其余旧方案、已完成计划、整改前快照与测量记录 |

已完成的本轮方案见 [全仓规范整改计划（历史）](archive/2026-10-27-code-compliance.md)。

`plans/` 仅保留正在执行的工作，完成后移入 `archive/`。`archive/` 中的计划与验收用于追溯，可能包含已废弃路径、版本和结论，不作为当前操作指南。

AI 协作入口是 [AGENTS.md](../AGENTS.md)，目录归属唯一依据是 [目录规范](../目录规范.md)。
