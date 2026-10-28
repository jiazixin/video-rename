/*
 * Text for codes sent by the Go server. The server sends stable codes with
 * English developer messages and the details each text needs; the interface
 * shows the text mapped here, never the server's message. Stored records
 * written before codes were introduced have no code and are shown as is.
 * details.detail is raw tool or system output and is appended after the text.
 * See docs/API.md. Only errors.ts reads this catalog.
 */

import type { ServerRecord } from './contracts'
import { displayPath } from '../lib/format/path'

/** One invalid field of an invalid_argument error. */
type FieldError = {
  path: string
  reason: string
}

const mediaLabels: Record<string, string> = { image: '图片', audio: '音频', mixed: '图片和音频' }

const codeTexts: Record<string, string> = {
  precondition_failed: '内容已被修改，请刷新后重试',
  idempotency_key_reused: '请求键已用于其他操作，请重新提交',
  idempotency_pending: '上次操作的结果尚未确定，请先核对记录与文件',
  idempotency_capacity: '请求记录已达到保留上限，请稍后重试',
  // Requests, sessions and the service
  internal: '服务暂时无法完成请求',
  not_found: '请求的记录不存在或已删除，请刷新页面后重试',
  unauthenticated: '请先登录',
  permission_denied: '请求来源无效，请从当前页面提交',
  unsupported_media_type: '请求格式无效，请刷新页面后重试',
  payload_too_large: '请求内容过大',
  method_not_allowed: '接口不支持该操作，请刷新页面后重试',
  invalid_credentials: '账号或密码错误',
  rate_limited: '登录尝试过于频繁，请稍后重试',
  shutting_down: '服务正在关闭，请稍后重试',
  secret_invalid: '保存的密钥数据无效',
  secret_unreadable: '保存的密钥无法解密，请核对数据库 .key 文件',
  no_ready_model: '请先在模型配置页面完善一个模型',
  cache_clear_incomplete: '已清理 {deleted} 项，后续清理失败',
  temp_capacity_too_small: '临时目录容量不足以容纳单个视频，请在系统设置中调大',
  frames_over_reservation: '截图超出预留容量',
  // Files and directories
  entry_busy: '该文件或目录有活动任务，请等待任务完成',
  descendant_busy: '目录内有活动任务，请等待任务完成',
  entry_changed: '文件已变化，请刷新后重试',
  identity_changed: '文件身份已变化，请刷新目录',
  invalid_hierarchy: '目录关系无效',
  not_directory: '不是目录',
  not_regular_file: '不是普通文件',
  not_image: '当前文件不是图片',
  illegal_path: '非法路径',
  symlink_not_allowed: '不允许操作符号链接或特殊文件',
  protected_entry: '不能修改媒体根目录、符号链接或特殊文件',
  root_unavailable: '媒体根目录不可用或挂载已变化',
  directory_refreshed: '目录已刷新，请返回第一页',
  directory_changed: '目录已变化，请重新预览',
  entry_not_indexed:
    '文件或上级目录已不在索引中，请到文件目录刷新并重新提交命名；若文件已删除，可清除对应记录',
  entry_missing:
    '文件或上级目录不存在，请检查媒体挂载并刷新所在目录后重试；若文件已删除，请清除对应记录',
  library_halted: '文件操作已停止，需要重启恢复',
  review_pending: '该范围存在待核对改名，不能继续自动或手动处理',
  delete_incomplete: '删除未完成，目录可能已部分删除，请刷新后核对',
  delete_index_failed: '文件已删除，但索引更新失败，请重启并刷新目录',
  delete_listing_failed: '文件已删除，列表更新失败，请刷新目录',
  mark_batch_failed: '标记批次未提交',
  mark_running: '已有标记任务正在后台执行，请等待完成后重试',
  mark_result_gone:
    '标记任务结果不可查询，可能已回收、过期或服务已重启；已完成的标记仍保留，请重新选择目标后重试',
  scan_changed: '目录扫描校验不一致，请稍后重试',
  scan_closed: '目录扫描已关闭',
  scan_hierarchy_changed: '扫描目录关系已变化，请刷新',
  scan_settings_unreadable: '读取扫描设置失败',
  scan_target_unavailable: '扫描目录不可用（{path}）',
  scan_too_deep: '扫描目录层级超过 1024 层',
  // Renames and restores
  operation_key_conflict: '该请求键已用于其他操作或操作未成功',
  operation_not_committed: '上次操作状态为 {status}',
  restore_not_latest: '只能还原最近一次尚未撤销的改名',
  restore_source_changed: '文件身份或内容发生变化，不能还原',
  already_processed: '文件已处理',
  source_unreadable: '分析后读取源文件失败',
  too_many_conflicts: '名称冲突过多',
  journal_source_changed: '日志写入后源文件已变化，需重启核对',
  rename_sync_failed: '文件已改名，但目录同步失败，需重启核对',
  rename_parent_changed: '文件已改名，但父目录已变化，需重启核对',
  rename_result_changed: '改名结果身份或内容已变化，需重启核对',
  rename_unconfirmed: '文件可能已改名，结果未确认，需重启恢复',
  parent_unverifiable: '父目录无法核对',
  rename_evidence_mismatch: '改名前后路径证据不一致，需要核对',
  rename_not_started: '改名尚未发生，运行已中断',
  retry_operation_missing: '该成功记录没有有效的自动命名操作，无法重试',
  retry_operation_stale:
    '只可重试当前有效的自动命名结果；文件已变化、已还原、另行改名或显式标记时请先核对',
  retry_scope_invalid: '成功记录重试只能用于明确选中的视频文件',
  retry_target_stale: '重试目标的索引已失效，请到文件目录刷新并重新提交命名',
  record_not_retryable: '记录不存在或不支持自动重试',
  // Numbering
  numbering_running: '上一编号任务仍在处理或安全停止，请稍后重试',
  numbering_jobs_full: '近期编号任务过多，请稍后重试',
  numbering_job_gone: '编号任务已过期或服务已重启，请重新预览；已完成的编号可在记录中查看',
  numbering_pending: '该目录有未完成的编号，请先在编号记录中恢复',
  preview_stale: '预览已过期或不属于当前目录，请重新预览',
  preview_not_ready: '预览尚未完成或已过期',
  nothing_to_number: '没有需要编号的文件',
  numbering_not_restorable: '该批次不可还原',
  numbering_not_preview: '该批次状态为 {status}，请查看编号记录',
  numbering_original_unavailable: '原批次已还原或不可用',
  numbering_item_marked: '{media} {name} 已标记为跳过编号，请重新预览',
  numbering_item_changed: '{media} {name} 已变化，请重新预览',
  numbering_item_changed_since: '{media} {name} 已变化或有后续改名，不能整批还原',
  numbering_item_busy: '{media}有活动任务，请等待完成',
  numbering_item_renamed: '{media}有后续改名，不能整批还原',
  numbering_item_ambiguous: '无法唯一确定{media} {name} 的位置，需要人工核对',
  numbering_item_modified: '{media} {name} 已被外部修改，需要人工核对',
  numbering_nothing_to_restore: '该批次没有需要还原的{media}',
  numbering_media_changed: '{media}已变化，请刷新目录后重试',
  numbering_index_incomplete: '{media}索引不完整，请刷新目录后重试',
  numbering_nothing_selected: '当前筛选下没有可编号的{media}',
  numbering_hard_link: '{media}包含硬链接，无法安全整批编号',
  numbering_temp_conflict: '临时名称冲突，请重新预览',
  numbering_unconfirmed: '编号结果待核对，请重启恢复',
  numbering_evidence_unsaved: '保存编号失败现场失败，需重启核对',
  numbering_rolled_back: '操作失败，已恢复原名',
  numbering_recovery_incomplete: '{media}编号恢复未完成',
  numbering_interrupted_restored: '中断的{media}编号已恢复原名',
  // Client codes of a numbering task that stopped; details.cause is the server record.
  numbering_preview_stopped: '编号预览已停止',
  numbering_apply_stopped: '编号已停止，已完成 {done} 个目录、{files} 个文件，其余目录未继续执行',
  // Runs, items and extraction records
  run_active: '请先取消运行并等待批次结束，再执行此操作',
  run_not_pausable: '该批次已结束或正在取消，无法暂停',
  run_not_paused: '该批次未暂停，无法继续运行',
  run_overlap: '所选范围与批次 #{run} 重叠，该批次尚未结束，请勿重复提交',
  run_targets_unavailable: '批次 #{run} 的目标状态不可用，请等待结束或取消后重试',
  waiting_runs_full: '等待批次已达上限（{limit} 个），请稍后重试或在系统设置中调整',
  target_not_runnable: '只能运行目录或视频文件',
  model_snapshot_missing: '任务没有模型配置快照，请重新创建任务',
  model_snapshot_invalid: '模型快照无效',
  sampling_snapshot_invalid: '抽帧快照无效',
  fallback_model_unavailable: '兜底模型不可用',
  fallback_model_invalid: '兜底模型配置无效',
  skip_processed: '已处理，跳过',
  skip_unstable: '文件尚不稳定，留待下次运行',
  cancelled: '已取消',
  interrupted_by_restart: '服务重启，处理已中断',
  run_interrupted_by_restart: '服务重启，前次运行已中断',
  run_failed: '批次失败，处理已停止',
  extraction_cancelled: '抽帧已取消',
  extraction_interrupted_by_restart: '服务重启，抽帧已中断',
  // Models, prompts and schedules
  model_config_not_found: '所选模型配置不存在或已删除，请重新选择模型配置',
  model_config_incomplete: '请完善模型配置',
  model_config_changed: '模型配置已修改或删除，请刷新后重试',
  model_configs_full: '最多保存 200 条模型配置',
  model_config_is_default: '请先将其他模型配置设为全局默认，再删除此配置',
  model_config_is_fallback: '请先在处理与缓存中更换或取消兜底模型，再删除此配置',
  model_config_in_use: '有 {plans} 个运行计划使用此配置，请先更换计划中的模型配置',
  discovery_request_invalid: '模型检测请求无效',
  discovery_timeout: '模型列表检测超时（{timeout}），请重试或检查服务',
  discovery_unreachable: '无法连接模型服务，请检查地址、网络和超时',
  discovery_read_failed: '读取模型列表失败',
  discovery_http_error: '模型列表接口返回 HTTP {status}，请检查 URL、密钥及服务是否支持 /models',
  discovery_too_large: '模型列表响应超过 1 MiB 上限',
  discovery_invalid: '服务未返回有效的模型列表；可手动填写模型名称',
  discovery_cursor_invalid: '模型列表分页游标无效',
  prompt_not_found: '提示词不存在',
  prompt_changed: '提示词已修改或不存在，请刷新后重试',
  prompts_full: '最多保存 200 条提示词',
  prompt_in_use: '有 {configs} 个模型配置使用此提示词，请先更换模型配置中的提示词',
  schedules_full: '最多支持 100 条计划',
  schedule_changed: '计划已被修改，请刷新',
  schedule_triggered: '已触发',
  schedule_skipped_active: '前次任务未结束，本次跳过',
  // Notifications
  unsupported_channel: '不支持的通知渠道',
  notification_not_configured: '请先保存此渠道配置',
  notification_credentials_unreadable: '通知凭据无法解密，请核对数据库密钥',
  notification_config_invalid: '通知配置无效',
  notification_unconfirmed: '服务重启，上次发送结果未确认',
  // Job errors keep their historical upper-case codes; lookups are lower case.
  tool_not_found: '找不到 {tool}，请检查安装和 PATH',
  probe_exec_failed: 'ffprobe 执行失败',
  probe_timeout: 'ffprobe 探测超时',
  probe_output_limit: 'ffprobe 输出超过上限',
  probe_json_invalid: 'ffprobe 输出无法解析',
  probe_no_video: '没有可用的视频流（纯音频、仅封面或无视频轨）',
  probe_duration_unknown: '无法获得有效的视频时长',
  extract_failed: '抽帧失败',
  extract_timeout: '抽帧总预算耗尽且没有成功帧',
  extract_insufficient_frames: '计划 {planned} 帧全部失败',
  image_too_large: '截图重新编码后仍超过单图上限',
  request_too_large: '单帧请求体 {bytes} 字节仍超过 {limit} 字节上限',
  api_timeout: '模型请求或响应读取超时',
  api_unauthorized: '模型服务鉴权失败，已停止新任务',
  api_rate_limited: '模型服务限流，重试后仍未成功',
  api_http_error: '模型服务返回 HTTP {http_status}',
  api_response_invalid: '模型响应格式无效',
  api_response_too_large: '模型响应超过 {limit} 字节上限',
  title_invalid: '清洗后的标题无效',
  naming_failed: '命名失败：模型标题包含失败词句“{keyword}”，已保留原文件名',
  model_output_truncated: '模型输出因长度截断，不作为标题',
  source_changed: '源文件已变化',
  rename_conflict: '目标已存在',
  rename_conflict_exhausted: '同名文件过多，没有可用的名称',
  rename_unsupported: '内核或文件系统不支持安全改名，已保留原文件',
  rename_permission_denied: '没有改名权限',
  rename_failed: '改名失败'
}

// Variants of a code chosen by details.reason, keyed code:reason.
const reasonTexts: Record<string, string> = {
  'tool_not_found:vanished': '{tool} 在运行中消失（PATH 中不再存在）',
  'probe_json_invalid:sample_aspect_ratio': '无法解释的像素宽高比 {value}',
  'probe_json_invalid:rotation': '无法解释的旋转元信息 {value} 度',
  'probe_json_invalid:coded_size': '视频流编码尺寸无效',
  'source_changed:recheck_failed': '重新核验文件失败',
  'source_changed:modified_during_probe': '文件在探测期间被外部修改',
  'source_changed:preview_invalid': '目录 {path} 的预览格式无效，请重新预览',
  'source_changed:preview_mismatch': '目录 {path} 的预览校验不一致，请查看诊断详情并重新预览',
  'extract_failed:degenerate_size': '视频尺寸 {width}×{height} 过小，无法抽帧',
  'extract_failed:temp_dir': '创建临时目录失败',
  'extract_failed:frame_unreadable': '第 {frame} 张截图无法读取或为空，请重新抽帧',
  'extract_insufficient_frames:empty_plan': '采样计划为空',
  'request_too_large:no_frames': '没有截图，未调用模型',
  'api_http_error:check_config':
    '模型服务返回 HTTP {http_status}，请核对 base_url、模型及请求规模配置',
  'api_http_error:request_build': '构造模型请求失败',
  'api_response_invalid:json': '模型响应 JSON 格式或字段类型无效',
  'api_response_invalid:empty_choices': '模型响应没有候选回答',
  'api_response_invalid:tool_calls': '模型响应包含工具调用，不是文本标题',
  'api_response_invalid:empty_answer': '模型回答为空',
  'api_response_invalid:not_completed': '模型未正常完成文本回答',
  'api_response_invalid:non_text': '模型响应包含非文本标题内容',
  'api_response_invalid:request_build': '组装模型请求失败',
  'title_invalid:empty': '模型输出为空标题',
  'title_invalid:multiline': '模型输出包含多行内容',
  'title_invalid:code_fence': '模型输出包含代码围栏',
  'title_invalid:nul': '模型输出包含 NUL 字符',
  'title_invalid:blank': '模型输出仅含空白或控制字符',
  'title_invalid:byte_budget': '字节预算内无法容纳有效标题',
  'rename_conflict:target_occupied': '目标 {to} 被未参与本次改名的文件或目录占用（原文件：{from}）',
  'notification_failed:encode': '通知内容编码失败',
  'notification_failed:invalid_endpoint': '通知地址无效',
  'notification_failed:unreachable': '通知连接失败或超时，请检查地址和网络',
  'notification_failed:response_unreadable': '通知响应无法读取或过大',
  'notification_failed:http_status': '通知服务返回 HTTP {status}',
  'notification_failed:response_invalid': '通知服务响应格式无效',
  'notification_failed:rejected': '通知服务拒绝发送，请检查机器人权限、目标或安全设置'
}

const sourceStages: Record<string, string> = {
  before_rename: '源文件在分析期间发生变化',
  path_identity: '文件身份已变化，请刷新目录',
  after_extract: '视频在抽帧期间发生变化，请重新抽帧',
  before_request: '视频在等待模型期间发生变化'
}

const numberingPhases: Record<string, string> = {
  final_rename: '最终编号',
  temporary_rename: '临时改名',
  rollback_rename: '恢复原名'
}

// Field texts keyed path:reason. List indexes in paths are written as *;
// image and audio numbering fields as media.
const fieldTexts: Record<string, string> = {
  ':not_object': '请求必须为 JSON 对象',
  ':invalid_json': '请求字段或格式无效',
  ':trailing_data': '请求只能包含一个 JSON 对象',
  ':invalid_numbering_request': '编号任务参数无效',
  ':invalid_retry_request':
    '请选择要重试的记录，并提供有效模型配置和请求键；成功记录仅支持明确选择后重试',
  'media.padding:out_of_range': '{media}编号位数须在 1–9 之间',
  'media.prefix:too_long': '{media}编号前缀最长 32 个字符',
  'media.prefix:invalid_character': '{media}编号前缀不能包含路径分隔符或控制字符',
  'media.prefix:untrimmed': '{media}编号前缀不能以空白开头或结尾',
  'provider:invalid_choice': '请选择有效的服务类型',
  'protocol:invalid_choice': '协议必须为 OpenAI 或 Anthropic',
  'protocol:provider_mismatch': '协议与服务类型不匹配',
  'base_url:invalid_url': '模型 URL 必须是无账号、查询参数和片段的 HTTP(S) 地址',
  'base_url:too_long': '模型 URL 过长',
  'api_key:invalid': '密钥格式无效',
  'api_key:required': '请填写 API 密钥',
  'api_key:reenter_after_endpoint_change': '服务地址或类型已改变，请重新填写密钥，或明确清除旧密钥',
  'model:required': '请填写模型名称（最长 256 个字符）',
  'timeout:out_of_range': '请求超时须在 1 秒至 30 分钟之间',
  'timeout:invalid_choice': '超时只能选择 5s、10s 或 15s',
  'temperature:out_of_range': '温度须在 0–2 之间',
  'temperature:out_of_range_anthropic': 'Anthropic 协议的温度须在 0–1 之间',
  'max_tokens:out_of_range': '输出上限须在 1–32768 之间',
  'workers:out_of_range': '并发任务数须在 1–32 之间',
  'thinking:invalid_choice': '思考模式必须为默认、开启或关闭',
  'thinking:unsupported_provider': '该服务不支持思考控制',
  'thinking:unsupported_model': '该型号尚未适配思考控制，请选择跟随模型默认',
  'thinking:disable_unsupported': '该型号无法关闭思考',
  'thinking:disable_unsupported_use_low': '该型号不支持关闭思考，请使用默认或开启低强度思考',
  'thinking_adapter:required': '自定义服务开启或关闭思考前，请选择服务支持的思考参数类型',
  'thinking_adapter:invalid_choice': '未知思考参数类型',
  'thinking_adapter:anthropic_needs_claude_adapter': 'Anthropic 协议请选择自适应或固定预算思考参数',
  'thinking_adapter:claude_adapter_needs_anthropic': 'Claude 思考参数需要 Anthropic 协议',
  'thinking_effort:invalid_choice': '思考强度必须为低、中或高',
  'thinking_budget:llama_out_of_range': 'llama.cpp 思考预算须至少 1，且小于最大输出 token',
  'thinking_budget:claude_out_of_range': 'Claude 思考预算须至少 1024，且小于最大输出 token',
  'title:invalid_length': '名称须为 1–80 字',
  'body:invalid_length': '提示词内容不能为空且不得超过 16000 字节',
  'prompt_id:required': '请选择提示词',
  'fixed_prompt:invalid_length': '固定提示词不能为空且不得超过 16000 字节',
  'failure_keywords:required': '请提供失败词句数组；空数组表示关闭匹配',
  'failure_keywords:too_many': '最多保存 100 条失败词句',
  'failure_keywords:invalid_item': '每条失败词句须为 1–200 字，不能只包含空白',
  'sampling.strategy:invalid_choice': '抽帧策略必须是 hybrid、size 或 duration',
  'sampling.max_frames:out_of_range': '最大帧数须在 1–32 之间',
  'sampling.skip_start_ratio:not_finite': '跳过比例不是有限数值',
  'sampling.skip_start_ratio:negative': '跳过比例必须为非负数',
  'sampling.skip_end_ratio:sum_too_large': '开头与结尾跳过比例之和必须小于 1',
  'sampling.duration_rules:required': '时长规则不能为空',
  'sampling.size_rules:required': '大小规则不能为空',
  'sampling.duration_rules:last_not_null': '时长规则最后一档的上限必须为空（兜底档）',
  'sampling.size_rules:last_not_null': '大小规则最后一档的上限必须为空（兜底档）',
  'sampling.duration_rules.*.max_duration:null_not_last':
    '时长规则第 {tier} 档：只有最后一档的上限可以为空',
  'sampling.duration_rules.*.max_duration:invalid_duration':
    '时长规则第 {tier} 档：上限不是有效时长',
  'sampling.duration_rules.*.max_duration:not_positive': '时长规则第 {tier} 档：上限必须为正数',
  'sampling.duration_rules.*.max_duration:not_increasing':
    '时长规则第 {tier} 档：上限必须大于前一档',
  'sampling.duration_rules.*.frames:out_of_range': '时长规则第 {tier} 档：帧数须在 1–32 之间',
  'sampling.size_rules.*.max_mib:null_not_last': '大小规则第 {tier} 档：只有最后一档的上限可以为空',
  'sampling.size_rules.*.max_mib:out_of_range':
    '大小规则第 {tier} 档：上限必须为有效的正整数且换算字节后不能溢出',
  'sampling.size_rules.*.max_mib:not_increasing': '大小规则第 {tier} 档：上限必须大于前一档',
  'sampling.size_rules.*.frames:out_of_range': '大小规则第 {tier} 档：帧数须在 1–32 之间',
  'images.max_dimension:out_of_range': '图片最大边长须在 128–2048 之间',
  'images.jpeg_quality:out_of_range': 'JPEG 质量须在 2–15 之间',
  'running_batch_limit:out_of_range': '同时运行批次上限须在 1–256 之间',
  'waiting_batch_limit:out_of_range': '等待批次上限须在 1–100000 之间',
  'retry_count:out_of_range': '重试次数须在 1–10 次之间',
  'fallback_model_id:invalid': '兜底模型配置无效',
  'fallback_model_id:model_not_ready': '请选择已配置完成的兜底模型',
  'success_cache_mode:invalid_choice': '缓存处理方式无效',
  'success_cache_hours:out_of_range': '成功后保留时长须在 1–8760 小时之间',
  'idle_cache_hours:out_of_range': '闲置清理时长须在 0–8760 小时之间，0 表示关闭',
  'extract_workers:out_of_range': '并发抽帧数须在 1–32 之间',
  'cache_gb:out_of_range': '临时目录容量须在 1–10000 GB 之间',
  'stability_seconds:out_of_range': '稳定性等待时长须在 0–3600 秒之间，0 表示禁用等待',
  'interval_minutes:out_of_range': '扫描间隔须在 5–1440 分钟之间',
  'targets:selection_required': '请选择 1–100 个目标',
  'targets:invalid_or_duplicate': '扫描目录编号无效或重复',
  'targets:not_directory': '目标必须为现有目录',
  'excluded_targets:too_many': '最多选择 100 个排除目录',
  'excluded_targets:invalid_or_duplicate': '排除目录编号无效或重复',
  'name:required': '请填写名称（最长 80 个字符）',
  'name:invalid_name': '名称不能为空、首尾空白或句点，且不能超过 240 字节',
  'name:invalid_character': '名称包含不允许的字符',
  'expression:cron_fields': 'Cron 必须为五段：分 时 日 月 周',
  'expression:invalid_cron': 'Cron 表达式无效',
  'expression:never_runs': '该规则没有可执行时间',
  'model_config_id:invalid': '请选择有效的模型配置',
  'token:telegram_incomplete': '请填写有效的 Bot Token 和 Chat ID',
  'endpoint:invalid_url': '请填写有效的 HTTP/HTTPS 推送地址',
  'endpoint:bark_key_required': 'Bark 地址须以设备 Key 结尾',
  'endpoint:wecom_webhook_required': '请填写企业微信群机器人的完整 Webhook 地址',
  'endpoint:dingtalk_webhook_required': '请填写钉钉群机器人的完整 Webhook 地址',
  'from:invalid_time': '起始时间无效，请使用 RFC3339 格式',
  'to:invalid_time': '结束时间无效，请使用 RFC3339 格式',
  'to:not_after_from': '结束时间必须晚于起始时间',
  'timezone:invalid_timezone': '时区无效',
  'ids:too_many': '最多查询 300 个条目',
  'confirm:confirmation_required': '请先确认此操作',
  'confirmed:confirmation_required': '请先确认永久删除',
  'all:confirmation_required': '请指定清除全部已结束抽帧记录',
  'ids:selection_required': '请选择本页记录或清空当前分类'
}

// Field labels for reasons without a dedicated text.
const fieldLabels: Record<string, string> = {
  id: '记录编号',
  ids: '记录编号',
  key: '请求键',
  status: '状态',
  category: '分类',
  task: '任务类型',
  media: '媒体类型',
  direction: '切换方向',
  model_source: '模型来源',
  source: '运行来源',
  sort: '排序方式',
  profile: '模型配置',
  run: '运行批次',
  page: '页码',
  size: '每页数量',
  offset: '分页位置',
  duration: '耗时范围',
  operation: '操作记录',
  kind: '计划类型'
}

const genericReasons: Record<string, string> = {
  invalid: '无效',
  invalid_choice: '无效',
  invalid_number: '无效',
  invalid_date: '日期格式无效',
  invalid_time: '时间格式无效',
  out_of_range: '超出允许范围',
  required: '不能为空'
}

function fill(template: string, details: Record<string, unknown>): string {
  return template.replace(/\{(\w+)\}/g, (_, key: string) => {
    const value = details[key]
    if (key === 'media') return mediaLabels[String(value)] ?? '文件'
    const text = typeof value === 'string' || typeof value === 'number' ? String(value) : ''
    if (key === 'path' || key === 'from' || key === 'to') return displayPath(text)
    return text
  })
}

function fieldText(field: FieldError): string {
  let path = field.path
  const extra: Record<string, unknown> = {}
  const media = /^(image|audio)\./.exec(path)
  if (media) {
    extra.media = media[1]
    path = 'media.' + path.slice(media[0].length)
  }
  const tier = /\.(\d+)\./.exec(path)
  if (tier) {
    extra.tier = Number(tier[1]) + 1
    path = path.replace(tier[0], '.*.')
  }
  const text = fieldTexts[`${path}:${field.reason}`]
  if (text) return fill(text, extra)
  const label = fieldLabels[field.path] ?? field.path
  return `${label}${genericReasons[field.reason] ?? '无效'}`
}

export function isRecord(value: unknown): value is ServerRecord {
  return typeof value === 'object' && value !== null && ('code' in value || 'message' in value)
}

/** Reports whether code has its own text, so callers can fall back by status. */
export function hasCodeText(code: string): boolean {
  const lower = code.toLowerCase()
  return lower === 'invalid_argument' || lower in codeTexts
}

/** Returns the text of an error or outcome record; '' when it has no code. */
export function recordText(record: ServerRecord | null | undefined): string {
  if (!record) return ''
  const code = record.code
  if (!code) return ''
  const details = record.details ?? {}
  if (code === 'invalid_argument') {
    const fields = Array.isArray(details.fields) ? (details.fields as FieldError[]) : []
    return fields.length ? fields.map(fieldText).join('；') : '请求参数无效'
  }
  const reason = typeof details.reason === 'string' ? details.reason : ''
  let text: string | undefined = reasonTexts[`${code}:${reason}`]
  if (!text && code === 'source_changed') text = sourceStages[String(details.stage)]
  text = fill(text ?? codeTexts[code] ?? '服务暂时无法完成请求', details)
  const phase = numberingPhases[String(details.phase)]
  if (phase) text = `${phase}失败：${text}`
  if (typeof details.directory === 'string') text = `${displayPath(details.directory)}：${text}`
  if (isRecord(details.cause)) text += `：${recordText(details.cause)}`
  if (isRecord(details.recovery)) text += `；恢复失败：${recordText(details.recovery)}`
  if (typeof details.detail === 'string' && details.detail) text += `（${details.detail}）`
  return text
}
