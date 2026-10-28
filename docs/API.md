# 接口约定

本文是管理 API 以及运行、文件、抽帧、通知等记录中错误代码的唯一契约来源，适用于 Go 服务端（`internal/domain/errors.go`、`internal/app/http.go`、`internal/app/routes.go`、`internal/auth`）与 Vue 管理台（`web/src/app/api.ts`、`web/src/app/errors.ts`、`web/src/app/server-codes.ts`）。通用规则见 [代码规范.md](../代码规范.md) 第 5 节。

## 通用约定

- `GET /healthz` 无需登录，返回 `{"status": "ok"}`；`GET /api/info` 无需登录，返回 `{"version", "revision", "build_time", "modified", "capabilities"}`（`image_numbering`、`notifications`、`schedules`）。`revision` 与 `build_time` 在未知时省略，已知构建时间同样采用 UTC 固定毫秒格式。
- 其余 `/api/` 接口需要会话 Cookie（`vr_session`）；写请求经 `http.CrossOriginProtection` 校验 `Sec-Fetch-Site` / `Origin`。
- 每个响应带 `X-Request-ID`（入站值匹配 `^[A-Za-z0-9-]{1,64}$` 时沿用），与服务端日志的 `request_id` 相同；`/api/` 响应带 `Cache-Control: no-store`。
- JSON 请求使用 `application/json`，PATCH 也接受 `application/merge-patch+json`（其他媒体类型返回 415 `unsupported_media_type`），不超过 64 KiB（否则 413 `payload_too_large`），未知字段返回 400。整数查询参数（`limit`、`offset`、`success_offset`、`failure_offset`、`run` 等）与数字路径 ID 格式错误返回 400 `invalid_argument`（`reason` 为 `invalid_number`）；可选整数查询参数可以省略，显式空值、非整数和溢出值均拒绝。
- 列表响应是对象：分页接口返回各自的分页字段（如 `{"items": [], "total": 0, "offset": 0, "limit": 100}`），不分页的列表为 `{"items": [...]}`，不返回裸数组。
- 创建提示词、模型配置（含复制）、计划、改名/还原操作和编号预览返回 201；提示词、配置和计划返回 `Location`。创建后台运行、重试运行、扫描、标记和编号任务返回 202，使用响应中的 ID 或 key 查询进度。无需创建资源的计算、设置更新和动作结果返回 200，没有响应体的成功操作返回 204。
- 接口时间输出统一为 UTC 固定毫秒：`2026-10-28T00:00:00.000Z`。文件内容核验使用内部纳秒指纹，不向接口暴露。

## 列表分页

管理台需要总数和跳页，表格接口使用 `offset`、`limit`、`total`。`offset` 是从 0 开始的行偏移量，省略时取 0；超出总数时返回最后一个分页窗口，空列表返回 `offset: 0`。计数与条目来自同一数据库快照。表格 `limit` 只接受 50、100、200，省略时按下表取默认值；负偏移、非法大小、未知和重复查询字段返回 400。界面页码由偏移量换算，不是 API 字段。

| GET 路径 | 查询字段 | 返回字段与默认大小 |
| --- | --- | --- |
| `/api/directories/{id}/entries` | `offset`、`limit`、`q`、`kind`、`status`、`sort` | `items`、`total`、`offset`、`limit`、`generation`、`refreshing`、`refreshed`、`error`、`directory`、`breadcrumbs`；默认 100 |
| `/api/runs` | `category`、`task`、`offset`、`limit`、`status`、`profile`、`from`、`to`、`duration` | `items`、`total`、`offset`、`limit`、`profiles`；默认 50，缺省 category 为 `all` |
| `/api/logs` | `category`、`offset`、`limit`、`q`、`action`、`model_source`、`run`、`from`、`to` | `items`、`total`、`offset`、`limit`；默认 100，category 必填 `success` 或 `failure` |
| `/api/extractions` | `success_offset`、`failure_offset`、`limit`、`q`、`run`、`from`、`to` | `success` / `failure` 各含 `items`、`total`、`offset`、`limit`，另有 `running` 数量；每组默认 50 |
| `/api/notifications/history` | `offset`、`limit` | `items`、`total`、`offset`、`limit`；默认 50 |
| `/api/runs/{id}/items` | `offset`、`directory`、`status` | `items`、`total`、`offset`、`limit` 及整批统计；固定每页 100 |
| `/api/usage/analytics`、`/api/runs/analytics` | `offset`、`limit` 及时间、模型、批次筛选 | `batches` 含 `items`、`total`、`offset`、`limit`；默认 20，允许 1–100 |

以上接口不再接受 `page`、`size`、`before`、`paged`、`success_page`、`failure_page`、`success_before`、`failure_before` 或目录 `generation` 输入，不保留旧分页实现。目录 `generation` 是只读字符串，用于播放器跨页读取时识别目录变化。目录 `kind` 只接受空值、`file`、`directory`、`video`、`image`、`audio`、`other`；`sort` 为空或 `name` / `size` / `mtime` / `renamed` / `status` 加 `_asc` / `_desc`。

日期筛选传 RFC 3339 时间（管理台传 UTC 毫秒格式），不接收只有日期而没有时间的值。`GET /api/entries/{id}/history` 使用 `cursor` 返回 `{"items": [...], "next_cursor": "..."}`，每页最多 100 条，末页省略 `next_cursor`；游标为上一页最后一条操作的 ID 字符串，按 ID 降序定位，拒绝无效、非正和重复游标。编号任务结果的 `offset` 是当前预览切片参数。

## 数量和可选字段

- 文件大小为 `size_bytes`；目录的可选 `contents`、`progress` 以及空错误记录省略，不返回 `null`。
- 模型配置与运行快照的超时为整数 `timeout_ms`（1000–1800000，默认 180000）。不接受旧 `timeout` 字符串。
- 抽帧时长档位使用整数 `max_duration_ms`；大小档位使用整数 `max_mib`。最后一档省略上限，表示覆盖剩余范围；其余档位的上限为正且严格递增。不接受 `max_duration` 字符串。
- 持久化格式身份为 `2026102801`。旧身份或实际结构不匹配的数据库只读检查后拒绝打开，不自动迁移、重建或删除数据。

## 修改与并发

提示词、模型配置和计划的 PATCH 遵循 JSON Merge Patch（RFC 7396）：未提供的成员保持不变，`null` 清空成员，嵌套对象逐成员合并，数组整体替换。清空必填字段仍须通过领域校验，否则返回 400；未知或只读成员拒绝。模型密钥 `api_key` 缺省保留，`null` 或空字符串清除。

资源 GET、创建和修改响应带强 `ETag`，列表中的资源也带 `etag` 字段。客户端把该值原样放入 `If-Match`，用于上述三个资源的 PATCH/DELETE，以及文件改名/删除。缺失、过期或弱 validator 返回 412 `precondition_failed`；数据库写入仍原子检查内部版本。请求体不接受 `version`、只读 `id` 或响应元数据。读取接口为 `GET /api/prompts/{id}`、`GET /api/model-configs/{id}`、`GET /api/schedules/{id}`，文件可从目录或状态批量接口获取 `etag`。

## 幂等请求

所有已认证的 POST 支持 `Idempotency-Key` 请求头，取值匹配 `^[A-Za-z0-9_-]{8,128}$`。创建请求应提供此头；请求体不接受旧 `key` / `request_key` 字段。键在整个 API 中唯一，身份为请求方法、路径及查询、`If-Match` 与原始请求体的组合。同键同请求重放第一次已记录的 HTTP 状态、响应体及 `Content-Type` / `ETag` / `Location` / `Retry-After` / `Allow`，同键异请求返回 409 `idempotency_key_reused`。不带键的 POST 可以执行，但客户端不得自动重试。

记录持久保留至少 24 小时，重启后仍可重放。并发同键请求串行等待，取消等待不取消已经开始的业务动作。结果先持久化再发送；如果服务在业务完成与结果记录之间中断，未完成保留项返回 409 `idempotency_pending`，避免再次执行不确定的文件操作，应先核对资源与操作日志。结果记录最多 10,000 条、合计 64 MiB；未完成项按每项 512 KiB 预留，单次结果（含选定响应头）不超过该值，达到容量时新请求返回 409 `idempotency_capacity`，不会提前驱逐仍在保留期的结果。后台任务的进度资源有自己的生命周期；重放 202 表示原提交已接受，不会重新启动任务。

管理台为 POST 创建一次请求键，在网络错误、超时、429 或 503 后至多重试两次，同一逻辑请求始终沿用同一键；只读 GET 同样可重试。认证登录/登出不自动分配幂等键或重试。`Retry-After` 超过 10 秒时直接展示错误。

服务端只返回英文说明和代码；管理台按代码显示中文，不展示服务端的英文说明。`details.detail`、`processing_log` 等原始诊断仅在明确的诊断视图展示，不拼入普通错误提示或 toast。

## 错误响应

```json
{"error": {"code": "entry_busy", "message": "file or directory has an active job", "details": {}}}
```

- `code`：snake_case 错误码，是管理台分支和选择文案的唯一依据。
- `message`：英文开发者说明，包含包装错误的上下文，不展示给用户。
- `details`：可选，结构按错误码定义。
- 编号预览、执行和恢复失败时，`details.processing_log` 带英文诊断全文。

领域返回具型别的失败原因或字段问题，HTTP 层用明确登记的映射生成错误码。未知原因统一为 `internal`；记录中的 `validation_failed` 与 `unexpected_failure` 是持久化诊断代码，不作为 HTTP 通用码复用。

HTTP 状态由失败类别决定：参数无效 400、未登录 401、跨站 403、记录不存在 404、状态或容量冲突 409、前置条件失败 412、频率超限 429、暂时不可用 503、服务端错误 500。未分类的错误统一为 500 `internal`，详情只写服务日志，不在响应中返回。

| 状态码 | `code` | 含义 | `details` |
| --- | --- | --- | --- |
| 400 | `invalid_argument` | 请求体、查询参数或设置字段无效 | `fields: [{path, reason}]`，见下节 |
| 401 | `unauthenticated`、`invalid_credentials` | 未登录；账号或密码错误 | — |
| 403 | `permission_denied` | 写请求跨站（`Sec-Fetch-Site` / `Origin` 不是本站） | — |
| 405 | `method_not_allowed` | 方法不支持，带 `Allow` | — |
| 413 | `payload_too_large` | 请求体超过 64 KiB | — |
| 415 | `unsupported_media_type` | 请求媒体类型不受支持 | — |
| 404 | `not_found`、`mark_result_gone`、`numbering_job_gone`、`prompt_not_found`、`model_config_not_found`、`entry_not_indexed` | 记录或任务结果不存在 | — |
| 409 | 文件与目录：`entry_busy`、`descendant_busy`、`identity_changed`、`invalid_hierarchy`、`directory_changed`、`review_pending`、`scan_changed`、`scan_hierarchy_changed` | 当前文件状态不允许该操作 | — |
| 409 | 改名与还原：`operation_key_conflict`、`operation_not_committed`、`restore_not_latest`、`restore_source_changed`、`already_processed`、`source_unreadable`、`too_many_conflicts`、`retry_operation_missing`、`retry_operation_stale` | 改名日志或文件状态不允许该操作 | `operation_not_committed`：`status` |
| 409 | 编号：`numbering_*`、`preview_stale`、`preview_not_ready` | 编号批次状态不允许该操作 | 视代码带 `media`（image、audio、mixed）、`name`、`status`、`cause`、`recovery` |
| 409 | 运行：`run_active`、`run_not_pausable`、`run_not_paused`、`run_overlap`、`run_targets_unavailable` | 批次状态不允许该操作 | `run_overlap`、`run_targets_unavailable`：`run` |
| 409 | 配置：`mark_running`、`prompt_in_use`、`model_config_is_default`、`model_config_is_fallback`、`model_config_in_use`、`notification_not_configured`、`fallback_model_unavailable` | 资源已修改或仍被使用 | `prompt_in_use`：`configs`；`model_config_in_use`：`plans` |
| 409 | 小写的任务错误码，如 `source_changed`、`rename_conflict` | 由文件处理产生的失败（见“任务与记录错误”） | 同记录 |
| 400 | `nothing_to_number`、`numbering_nothing_selected`、`target_not_runnable`、`retry_scope_invalid`、`not_directory`、`not_regular_file`、`not_image`、`illegal_path`、`symlink_not_allowed`、`protected_entry`、`unsupported_channel`、`model_config_incomplete`、`fallback_model_invalid`、`model_snapshot_missing`、`model_snapshot_invalid`、`sampling_snapshot_invalid`、`scan_too_deep` | 请求对象不符合操作要求 | `model_config_incomplete`、`fallback_model_invalid` 以 `detail` 带出具体字段问题 |
| 409 | `idempotency_key_reused`、`idempotency_pending`、`idempotency_capacity` | 幂等请求冲突、结果未确定或保留容量已满 | — |
| 409 | `numbering_jobs_full`、`waiting_runs_full`、`prompts_full`、`model_configs_full`、`schedules_full` | 资源数量超过上限 | `waiting_runs_full`：`limit` |
| 412 | `precondition_failed` | `If-Match` 缺失、过期或不匹配 | — |
| 429 | `rate_limited` | 请求频率超过上限，带 `Retry-After` | — |
| 500 | `internal`、`library_halted`、`secret_invalid`、`secret_unreadable`、`notification_credentials_unreadable`、`notification_config_invalid`、`scan_settings_unreadable`、`cache_clear_incomplete`、`delete_*`、`rename_*`（改名后核对失败）、`journal_source_changed`、`numbering_unconfirmed`、`numbering_evidence_unsaved`、`numbering_item_ambiguous`、`numbering_item_modified`、`mark_batch_failed` | 服务端错误或需要重启核对 | `cache_clear_incomplete`：`deleted` |
| 503 | `shutting_down`、`root_unavailable`、`scan_closed`、`scan_target_unavailable`、`temp_capacity_too_small`、`frames_over_reservation`、`discovery_*`、`notification_failed` | 服务或外部依赖暂时不可用 | `scan_target_unavailable`：`path`；`discovery_timeout`：`timeout_ms`；`discovery_http_error`：`status`；`notification_failed`：`reason`（`encode`、`invalid_endpoint`、`unreachable`、`response_unreadable`、`http_status`、`response_invalid`、`rejected`），`http_status` 另带 `status` |

## 参数错误

`invalid_argument` 的 `details` 固定为 `{"fields": [{"path": "sampling.size_rules.0.frames", "reason": "out_of_range"}]}`：

- `path` 是请求或设置字段的 JSON 名，嵌套用点分，列表项用下标（`sampling.duration_rules.2.max_duration_ms`）；图片和音频编号字段以 `image.`、`audio.` 开头；整个请求体无效时为空字符串。
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
- 没有诊断时省略可选记录字段，包括运行明细的 `message` 和历史列表的 `error`；不发送空对象或旧的文本错误。
- 记录无法编码时保存明确的 `internal` 失败记录，`details.record_code` 保留原代码，`details.detail` 说明编码错误；不得用代表“无错误”的空字符串替代失败。
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
