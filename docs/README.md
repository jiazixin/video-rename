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
| [接口约定](API.md) | 分页、并发、幂等与错误契约，前后端共同遵守 |
| [开发与测试](IMPLEMENTATION.md) | 模块入口、构建、回归、规模测试、依赖维护、CI 与发布 |
| [最新验收](ACCEPTANCE.md) | 本次实际执行结果、修复证据和未验证范围 |
| [Vue Router 决策](decisions/2026-10-27-vue-router.md) | 多页面路由、直接页面地址与服务端 SPA 回退 |

## 历史验收索引

| 记录 | 状态与范围 |
| --- | --- |
| [编号与通知复修前的验收](archive/2026-10-28-before-numbering-toast-fixes-acceptance.md) | 历史；后续发现编号扫描取消、通知关闭尺寸与组件类型依赖三项差异，修复及复验见最新验收 |
| [最终整改前的规范复查](archive/2026-10-28-before-final-compliance-acceptance.md) | 历史；保留六类待整改差异与当时的复现证据，已由最新验收替代 |
| [本次全仓规范整改计划](archive/2026-10-28-project-compliance.md) | 已完成归档；分页、错误边界、路由状态、字段提示、设计变量及全仓验证范围 |
| [本次全仓复查前的验收](archive/2026-10-28-before-project-audit-acceptance.md) | 2026-10-28 历史快照；本次继续发现并修正旧分页和结构校验问题，不再作为全仓已合规的依据 |
| [全仓整改前的累计验收](archive/2026-10-27-before-full-compliance-acceptance.md) | 历史；包含 2026-10-22 至 10-27 的代码质量、接口、context 与 Web 整理结果，由 2026-10-28 [最新验收](ACCEPTANCE.md) 替代 |
| [预览窗口](archive/2026-10-20-preview-window-acceptance.md)、[改名焦点](archive/2026-10-20-rename-focus-acceptance.md)、[运行中记录](archive/2026-10-20-run-live-records-acceptance.md) | 2026-10-20 历史专项验收 |
| [CI 与发布](archive/2026-10-19-ci-release-acceptance.md)、[按钮与媒体播放器](archive/2026-10-19-ui-buttons-media-player-acceptance.md) | 2026-10-19 历史专项验收 |
| [原生构建版本](archive/2026-10-09-native-version-acceptance.md)、[发布安全](archive/2026-10-08-release-safety-acceptance.md)、[改名记录](archive/2026-10-07-rename-record-fix-acceptance.md) | 2026-10-07 至 10-09 历史专项验收 |
| [2026-10-05 验收](archive/2026-10-05-acceptance.md)、[2026-09-30 验收](archive/2026-09-30-acceptance.md) | 较早阶段的历史测量与验收 |
| [历史归档目录](archive/) | 其余旧方案、已完成计划、整改前快照与测量记录 |

上一轮已归档的方案见 [全仓规范整改计划（历史）](archive/2026-10-27-code-compliance.md)。当前实现及验证边界以 [最新验收](ACCEPTANCE.md) 为准。

`plans/` 仅保留正在执行的工作，完成后移入 `archive/`。`archive/` 中的计划与验收用于追溯，可能包含已废弃路径、版本和结论，不作为当前操作指南。

AI 协作入口是 [AGENTS.md](../AGENTS.md)，目录归属唯一依据是 [目录规范](../目录规范.md)。
