# 接口约定

本文是管理 API 以及运行、文件、抽帧、通知等记录中错误代码的唯一契约来源，适用于 Go 服务端（`internal/domain/errors.go`、`internal/app/http.go`、`internal/app/routes.go`、`internal/auth`）与 Vue 管理台（`web/src/app/api.ts`、`web/src/app/errors.ts`、`web/src/app/server-codes.ts`）。通用规则见 [代码规范.md](../代码规范.md) 第 5 节。

## 通用约定

- `GET /healthz` 无需登录，返回 `{"status": "ok"}`；`GET /api/info` 无需登录，返回 `{"version", "capabilities"}`（`image_numbering`、`notifications`、`schedules`）。
- 其余 `/api/` 接口需要会话 Cookie（`vr_session`）；写请求经 `http.CrossOriginProtection` 校验 `Sec-Fetch-Site` / `Origin`。
- 每个响应带 `X-Request-ID`（入站值匹配 `^[A-Za-z0-9-]{1,64}$` 时沿用），与服务端日志的 `request_id` 相同；`/api/` 响应带 `Cache-Control: no-store`。
- 请求体必须是 `application/json`（否则 415 `unsupported_media_type`），不超过 64 KiB（否则 413 `payload_too_large`），未知字段返回 400。整数查询参数（`before`、`limit`、`page`、`size`、`offset`、`run` 等）与数字路径 ID 格式错误返回 400 `invalid_argument`（`reason` 为 `invalid_number`）。
- 列表响应是对象：分页接口返回各自的分页字段（如 `{"items", "total", "page"}`），不分页的列表为 `{"items": [...]}`，不返回裸数组。
- 没有响应体的成功操作返回 204（如登出、删除、暂停与继续）；已接受的后台刷新返回 202。

服务端只返回英文说明和代码；管理台按代码显示中文，不展示服务端的英文说明。唯一例外是 `details.detail`：它是 FFmpeg、文件系统、模型或通知服务等外部来源的原始诊断，管理台把它附在中文说明之后。

## 错误响应

```json
{"error": {"code": "entry_busy", "message": "file or directory has an active job", "details": {}}}
```

- `code`：snake_case 错误码，是管理台分支和选择文案的唯一依据。
- `message`：英文开发者说明，包含包装错误的上下文，不展示给用户。
- `details`：可选，结构按错误码定义。
- 编号预览、执行和恢复失败时，`details.processing_log` 带英文诊断全文。

HTTP 状态由失败类别决定：参数无效 400、未登录 401、跨站 403、记录不存在 404、状态冲突 409、数量或频率超限 429、暂时不可用 503、服务端错误 500。未分类的错误统一为 500 `internal`，详情只写服务日志，不在响应中返回。

| 状态码 | `code` | 含义 | `details` |
| --- | --- | --- | --- |
| 400 | `invalid_argument` | 请求体、查询参数或设置字段无效 | `fields: [{path, reason}]`，见下节 |
| 401 | `unauthenticated`、`invalid_credentials` | 未登录；账号或密码错误 | — |
| 403 | `permission_denied` | 写请求跨站（`Sec-Fetch-Site` / `Origin` 不是本站） | — |
| 405 | `method_not_allowed` | 方法不支持，带 `Allow` | — |
| 413 | `payload_too_large` | 请求体超过 64 KiB | — |
| 415 | `unsupported_media_type` | 请求体不是 `application/json` | — |
| 404 | `not_found`、`mark_result_gone`、`numbering_job_gone`、`prompt_not_found`、`model_config_not_found`、`entry_not_indexed` | 记录或任务结果不存在 | — |
| 409 | 文件与目录：`entry_busy`、`descendant_busy`、`entry_changed`、`identity_changed`、`invalid_hierarchy`、`directory_refreshed`、`directory_changed`、`review_pending`、`scan_changed`、`scan_hierarchy_changed` | 当前文件状态不允许该操作 | — |
| 409 | 改名与还原：`operation_key_conflict`、`operation_not_committed`、`restore_not_latest`、`restore_source_changed`、`already_processed`、`source_unreadable`、`too_many_conflicts`、`retry_operation_missing`、`retry_operation_stale` | 改名日志或文件状态不允许该操作 | `operation_not_committed`：`status` |
| 409 | 编号：`numbering_*`、`preview_stale`、`preview_not_ready` | 编号批次状态不允许该操作 | 视代码带 `media`（image、audio、mixed）、`name`、`status`、`cause`、`recovery` |
| 409 | 运行：`run_active`、`run_not_pausable`、`run_not_paused`、`run_overlap`、`run_targets_unavailable` | 批次状态不允许该操作 | `run_overlap`、`run_targets_unavailable`：`run` |
| 409 | 配置：`request_key_reused`、`mark_running`、`prompt_changed`、`prompt_in_use`、`model_config_changed`、`model_config_is_default`、`model_config_is_fallback`、`model_config_in_use`、`schedule_changed`、`notification_not_configured`、`fallback_model_unavailable` | 资源已修改或仍被使用 | `prompt_in_use`：`configs`；`model_config_in_use`：`plans` |
| 409 | 小写的任务错误码，如 `source_changed`、`rename_conflict` | 由文件处理产生的失败（见“任务与记录错误”） | 同记录 |
| 400 | `nothing_to_number`、`numbering_nothing_selected`、`target_not_runnable`、`retry_scope_invalid`、`not_directory`、`not_regular_file`、`not_image`、`illegal_path`、`symlink_not_allowed`、`protected_entry`、`unsupported_channel`、`model_config_incomplete`、`fallback_model_invalid`、`model_snapshot_missing`、`model_snapshot_invalid`、`sampling_snapshot_invalid`、`scan_too_deep` | 请求对象不符合操作要求 | `model_config_incomplete`、`fallback_model_invalid` 以 `detail` 带出具体字段问题 |
| 429 | `rate_limited`、`numbering_jobs_full`、`waiting_runs_full`、`prompts_full`、`model_configs_full`、`schedules_full` | 频率或数量超过上限 | `waiting_runs_full`：`limit` |
| 500 | `internal`、`library_halted`、`secret_invalid`、`secret_unreadable`、`notification_credentials_unreadable`、`notification_config_invalid`、`scan_settings_unreadable`、`cache_clear_incomplete`、`delete_*`、`rename_*`（改名后核对失败）、`journal_source_changed`、`numbering_unconfirmed`、`numbering_evidence_unsaved`、`numbering_item_ambiguous`、`numbering_item_modified`、`mark_batch_failed` | 服务端错误或需要重启核对 | `cache_clear_incomplete`：`deleted` |
| 503 | `shutting_down`、`root_unavailable`、`scan_closed`、`scan_target_unavailable`、`temp_capacity_too_small`、`frames_over_reservation`、`discovery_*`、`notification_failed` | 服务或外部依赖暂时不可用 | `scan_target_unavailable`：`path`；`discovery_timeout`：`timeout`；`discovery_http_error`：`status`；`notification_failed`：`reason`（`encode`、`invalid_endpoint`、`unreachable`、`response_unreadable`、`http_status`、`response_invalid`、`rejected`），`http_status` 另带 `status` |

## 参数错误

`invalid_argument` 的 `details` 固定为 `{"fields": [{"path": "sampling.size_rules.0.frames", "reason": "out_of_range"}]}`：

- `path` 是请求或设置字段的 JSON 名，嵌套用点分，列表项用下标（`sampling.duration_rules.2.max_duration`）；图片和音频编号字段以 `image.`、`audio.` 开头；整个请求体无效时为空字符串。
- `reason` 是 snake_case 原因码，常用 `required`、`invalid`、`invalid_choice`、`invalid_number`、`invalid_time`、`invalid_date`、`invalid_timezone`、`out_of_range`、`too_long`、`too_many`、`selection_required`、`confirmation_required`；请求体另有 `not_object`、`invalid_json`、`trailing_data`，个别字段有专用原因（如 `temperature` 的 `out_of_range_anthropic`、`thinking` 的 `unsupported_model`、`endpoint` 的 `wecom_webhook_required`）。完整文案见 `web/src/app/server-codes.ts`，新增路径或原因码时两端同时修改。

## 任务与记录错误

文件处理失败使用 `domain.ErrorCode` 的 snake_case 代码（如 `source_changed`、`rename_conflict`）。`details.reason` 区分同一代码的不同原因：

| 代码 | `details` |
| --- | --- |
| `tool_not_found` | `tool`（ffprobe、ffmpeg）；运行中消失为 `reason: vanished` |
| `probe_json_invalid` | `reason`：`json`（另带 `detail`）、`sample_aspect_ratio`、`rotation`（另带 `value`）、`coded_size` |
| `probe_output_limit` | `limit` |
| `source_changed` | `stage`（`before_rename`、`path_identity`、`after_extract`、`before_request`）与 `changed`（device、inode、size、mtime）；探测期间为 `reason: recheck_failed`、`modified_during_probe`；编号预览校验为 `reason: preview_invalid`、`preview_mismatch` 与 `path` |
| `extract_failed` | `reason`：`degenerate_size`（`width`、`height`）、`temp_dir`、`frame_unreadable`（`frame`） |
| `extract_insufficient_frames` | `planned`；空计划为 `reason: empty_plan` |
| `request_too_large` | `bytes`、`limit`；没有截图为 `reason: no_frames` |
| `api_http_error` | 由 `http_status` 给出状态；需核对配置时 `reason: check_config` |
| `api_response_invalid` | `reason`：`json`、`empty_choices`、`tool_calls`、`empty_answer`、`not_completed`、`non_text`、`request_build` |
| `api_response_too_large` | `limit` |
| `title_invalid` | `title`，或 `reason`：`empty`、`multiline`、`code_fence`、`nul`、`blank`、`byte_budget` |
| `naming_failed` | `keyword` |
| `rename_conflict` | 编号目标被占用时 `reason: target_occupied`、`from`、`to` |
| 编号改名失败 | 原代码另带 `phase`（`final_rename`、`temporary_rename`、`rollback_rename`）、`from`、`to` |

所有任务错误另带可选的 `exit_code`、`http_status` 和 `detail`（stderr 或脱敏后的响应摘要）。

## 记录字段

运行（`error`）、文件（`message`）、模型尝试（`error`）、运行记录（`error`）、抽帧记录（`error`）、改名操作（`error`）、目录同步状态（`error`）、计划（`last`）、通知投递（`error`）、编号批次（`error`）、批量还原与标记失败（`error`）、重试失败（`error`）、扫描错误（`error`）以及统计中的失败分组（`error`）都是同一结构：

```json
{"code": "skip_processed", "message": "already processed; skipped", "details": {}}
```

- 记录以 JSON 保存在 TEXT 列中，读取时必须是有效的记录 JSON。
- `details.cause` 和 `details.recovery` 是嵌套记录，管理台把它们的文案接在主文案之后。
- 文件记录还会使用以下代码：`skip_processed`、`skip_unstable`、`cancelled`、`interrupted_by_restart`、`run_failed`（带 `cause`）；运行有 `run_interrupted_by_restart`；抽帧记录有 `extraction_cancelled`、`extraction_interrupted_by_restart`；计划有 `schedule_triggered`、`schedule_skipped_active`；改名操作有 `parent_unverifiable`、`rename_evidence_mismatch`、`rename_not_started`；通知投递有 `notification_unconfirmed`。
- 扫描错误为 `{path, scope, error}`；`scope` 为 `settings`（扫描设置读取失败）或 `targets`（扫描范围无效）时没有 `path`。

## 其他代码化字段

- 显示路径中的 `/` 表示整个媒体库，`/?` 表示目录已缺失；真实路径是相对路径，不会以 `/` 开头。
- `GET /api/system` 的 `problem` 是记录结构，说明为何不能开始运行（如 `no_ready_model` 或 `tool_not_found`）。
- 编号任务状态用 `directory`（预览中正在扫描的目录编号）或 `directory_done`、`directory_total`（执行中当前目录的进度）代替原来的中文 `path`。
- 运行统计的耗时分布 `label` 为档位代码：`under_5s`、`5s_15s`、`15s_30s`、`30s_60s`、`1m_2m`、`2m_5m`、`5m_10m`、`over_10m`。
- 图片识别测试结果的 `status` 为 `passed`、`mismatch` 或 `error`，失败原因在 `error`。
- 模型服务预设的 `name` 为英文名，管理台按 `id` 显示中文名称。
- 清除记录接口返回 `deleted` 条数，不再返回提示文案。
