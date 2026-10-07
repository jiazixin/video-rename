export interface Entry {
  progress?: { total: number; processed: number; unknown: number; active: number }
  contents?: { directories: number; videos: number; images: number; audio: number } | null
  id: number
  parent: number
  name: string
  kind: string
  size: number
  mtime: string
  renamed_at?: string
  version: number
  processed: string
  restore_id: number
  task_status: string
}
export interface Page {
  page?: number
  total: number
  items: Entry[]
  next: number
  generation: string
  refreshing: boolean
  refreshed: string
  error: string
  directory: Entry
  breadcrumbs: Entry[]
}
export interface DirectoryState {
  generation: string
  refreshing: boolean
  refreshed: string
  error: string
}
export interface LibraryScanSettings {
  excluded_targets: number[]
  enabled: boolean
  interval_minutes: number
  targets: number[]
}
export interface LibraryScanStatus {
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
  errors: { path: string; message: string }[]
}
export interface LibraryScanState {
  directory_paths: Record<number, string>
  settings: LibraryScanSettings
  status: LibraryScanStatus
}
export interface Run {
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
  error: string
  plan: number
}
export interface Plan {
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
  last: string
  version: number
}
export interface ModelProfile {
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
  version: number
  created: string
  updated: string
}
export interface PromptProfile {
  id: number
  title: string
  body: string
  version: number
  created: string
  updated: string
}
export interface UsageTotals {
  tokens: number
  records: number
  estimated_records: number
  batches: number
}
export interface UsageBucket {
  from: string
  to: string
  tokens: number
  records: number
  estimated_records: number
}
export interface UsageModel {
  key: string
  model: string
  provider: string
  protocol: string
  tokens: number
  records: number
  estimated_records: number
  batches: number
}
export interface UsageProfile {
  id: number
  title: string
  tokens: number
  records: number
  estimated_records: number
  batches: number
}
export interface UsageSource {
  key: string
  tokens: number
  records: number
  estimated_records: number
  batches: number
}
export interface UsageBatch {
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
export interface UsageReport {
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
export interface AnalysisTotals {
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
export interface AnalysisBucket {
  from: string
  to: string
  finished_batches: number
  avg_batch_ms: number
  done: number
  skipped: number
  failed: number
}
export interface AnalysisDist {
  label: string
  count: number
}
export interface AnalysisProfile {
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
export interface AnalysisScatter {
  task: 'rename' | 'extract'
  run: number
  items: number
  ms: number
  status: string
  source: string
  profile_title: string
}
export interface AnalysisFailure {
  message: string
  count: number
  last_at: string
}
export interface AnalysisBatch {
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
export interface AnalysisReport {
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
