/** An error or outcome record: a code, its English message and details. */
export type ServerRecord = {
  code?: string
  message?: string
  details?: Record<string, unknown>
}

export type Entry = {
  progress?: { total: number; processed: number; unknown: number; active: number }
  contents?: { directories: number; videos: number; images: number; audio: number } | null
  id: number
  parent: number
  name: string
  kind: string
  size: number
  mtime: string
  renamed_at?: string
  etag: string
  version: number
  processed: string
  restore_id: number
  task_status: string
}
export type Page = {
  page?: number
  total: number
  items: Entry[]
  next: number
  generation: string
  refreshing: boolean
  refreshed: string
  error?: ServerRecord
  directory: Entry
  breadcrumbs: Entry[]
}
export type DirectoryState = {
  generation: string
  refreshing: boolean
  refreshed: string
  error?: ServerRecord
}
export type LibraryScanSettings = {
  excluded_targets: number[]
  enabled: boolean
  interval_minutes: number
  targets: number[]
}
export type LibraryScanStatus = {
  id: string
  status:
    'idle' | 'scanning' | 'cancelling' | 'completed' | 'partial_failure' | 'failed' | 'cancelled'
  source: string
  targets: number[]
  started: string
  finished: string
  next_scan: string
  current_path: string
  directories: number
  files: number
  added: number
  changed: number
  removed: number
  /** scope replaces path for failures of the scan settings or targets. */
  errors: { path?: string; scope?: 'settings' | 'targets'; error: ServerRecord }[]
}
export type LibraryScanState = {
  directory_paths: Record<number, string>
  settings: LibraryScanSettings
  status: LibraryScanStatus
}
export type Run = {
  task?: 'rename' | 'extract'
  target_names?: string[]
  started?: string
  request_config?: {
    retry_enabled?: boolean
    retry_count?: number
    fallback_model_title?: string
    provider?: string
    protocol?: string
    base_url?: string
    omit_temperature?: boolean
    token_parameter?: string
    system_location?: string
    model: string
    temperature?: number
    max_tokens?: number
    stream?: boolean
    message_roles?: string[]
  }
  model_config_id: number
  model_config_title: string
  prompt_body: string
  id: number
  source: string
  targets: number[]
  recursive: boolean
  status: string
  created: string
  finished: string
  discovered: number
  done: number
  skipped: number
  failed: number
  token_count: number
  token_records: number
  token_estimated_records: number
  error?: ServerRecord
  plan: number
}
export type Plan = {
  task?: 'rename' | 'extract'
  model_config_id: number
  target_names?: string[]
  id: number
  name: string
  targets: number[]
  recursive: boolean
  kind: string
  expression: string
  timezone: string
  enabled: boolean
  next: string
  last?: ServerRecord
  etag: string
  version: number
}
export type ModelProfile = {
  workers: number
  thinking: string
  thinking_adapter: string
  thinking_effort: string
  thinking_budget: number
  provider: string
  protocol: string
  base_url: string
  model: string
  timeout: string
  temperature: number
  max_tokens: number
  omit_temperature: boolean
  has_api_key: boolean
  ready: boolean
  is_default: boolean
  id: number
  title: string
  prompt_id: number
  prompt_title: string
  body: string
  etag: string
  version: number
  created: string
  updated: string
}
export type PromptProfile = {
  id: number
  title: string
  body: string
  etag: string
  version: number
  created: string
  updated: string
}
export type UsageTotals = {
  tokens: number
  records: number
  estimated_records: number
  batches: number
}
export type UsageBucket = {
  from: string
  to: string
  tokens: number
  records: number
  estimated_records: number
}
export type UsageModel = {
  key: string
  model: string
  provider: string
  protocol: string
  tokens: number
  records: number
  estimated_records: number
  batches: number
}
export type UsageProfile = {
  id: number
  title: string
  tokens: number
  records: number
  estimated_records: number
  batches: number
}
export type UsageSource = {
  key: string
  tokens: number
  records: number
  estimated_records: number
  batches: number
}
export type UsageBatch = {
  id: number
  model: string
  provider: string
  protocol: string
  profile_id: number
  profile_title: string
  source: string
  status: string
  created: string
  started: string
  finished: string
  tokens: number
  records: number
  estimated_records: number
  lifetime_tokens: number
  lifetime_records: number
  last_recorded_at: string
}
export type UsageReport = {
  range: { from: string; to: string; timezone: string; interval: string }
  totals: UsageTotals
  trend: UsageBucket[]
  models: UsageModel[]
  profiles: UsageProfile[]
  sources: UsageSource[]
  top_batches: UsageBatch[]
  batches: { items: UsageBatch[]; total: number; page: number; size: number }
  options: {
    models: { key: string; model: string; provider: string; protocol: string }[]
    profiles: { id: number; title: string; model_keys: string[]; deleted: boolean }[]
  }
}
export type AnalysisTotals = {
  batches: number
  completed: number
  partial_failure: number
  failed_batches: number
  cancelled: number
  interrupted: number
  active: number
  done: number
  skipped: number
  failed: number
  timed_items: number
  avg_batch_ms: number
  avg_item_ms: number
  p50_item_ms: number
  p95_item_ms: number
  throughput_per_hour: number
}
export type AnalysisBucket = {
  from: string
  to: string
  finished_batches: number
  avg_batch_ms: number
  done: number
  skipped: number
  failed: number
}
export type AnalysisDist = {
  label: string
  count: number
}
export type AnalysisProfile = {
  id: number
  title: string
  model: string
  provider: string
  protocol: string
  batches: number
  done: number
  skipped: number
  failed: number
  avg_item_ms: number
  p50_item_ms: number
  p95_item_ms: number
  throughput_per_hour: number
}
export type AnalysisScatter = {
  task: 'rename' | 'extract'
  run: number
  items: number
  ms: number
  status: string
  source: string
  profile_title: string
}
export type AnalysisFailure = {
  error: ServerRecord
  count: number
  last_at: string
}
export type AnalysisBatch = {
  task: 'rename' | 'extract'
  id: number
  status: string
  source: string
  profile_id: number
  profile_title: string
  model: string
  provider: string
  protocol: string
  created: string
  started: string
  finished: string
  discovered: number
  done: number
  skipped: number
  failed: number
  duration_ms: number
  avg_item_ms: number
}
export type AnalysisReport = {
  range: { from: string; to: string; timezone: string; interval: string }
  totals: AnalysisTotals
  trend: AnalysisBucket[]
  distribution: AnalysisDist[]
  profiles: AnalysisProfile[]
  scatter: AnalysisScatter[]
  failures: AnalysisFailure[]
  batch_failures: AnalysisFailure[]
  batches: { items: AnalysisBatch[]; total: number; page: number; size: number }
  options: { profiles: { id: number; title: string }[] }
}

/** Run states that still own their targets; mirrors domain.ActiveRunStatus. */
const activeRunStatuses: readonly string[] = [
  'queued',
  'scanning',
  'running',
  'paused',
  'cancelling'
]
export const isActiveRunStatus = (status: string) => activeRunStatuses.includes(status)

/** One rename operation of an entry, as listed by /entries/{id}/history. */
export type Operation = {
  id: number
  entry: number
  parent: number
  old_name: string
  new_name: string
  kind: string
  source: string
  status: string
  undo_of: number
  undone: boolean
  created: string
  error?: ServerRecord
  run: number
}

/** One success or failure record of /logs. */
export type LogRecord = {
  id: number
  category: string
  action: string
  entry: number
  operation: number
  run: number
  old_path: string
  new_path: string
  error?: ServerRecord
  created: string
  model: string
  model_config_title: string
  /** Empty for non-model actions; unknown when no model attempt was recorded. */
  model_source: '' | 'unknown' | 'primary' | 'fallback'
  tokens: number
  token_estimated: boolean
}
export type LogPage = {
  items: LogRecord[]
  total: number
  next: number
  page: number
}
export type LogRetryResult = {
  accepted: number[]
  runs: number[]
  /** A record, or a group of files, that was not resubmitted. */
  errors: { record?: number; files?: number; error: ServerRecord }[]
  skipped: number
}

/** Result of the model configuration's image recognition test. */
export type ProbeResult = {
  status: 'passed' | 'mismatch' | 'error'
  answer: string
  expected: string
  error?: ServerRecord
  http_status?: number
  elapsed_ms: number
}
