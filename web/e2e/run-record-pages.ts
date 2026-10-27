import type { Locator } from '@playwright/test'
import { defined } from './support'
import type { RunItem } from '../src/features/runs/use-run-records'

// Fixtures give an item message as its error code; it is sent as a stored record.
type FixtureItem = Omit<Partial<RunItem>, 'message'> & { message?: string }

const finished = ['done', 'skipped', 'failed', 'cancelled']

export function recordPage(items: FixtureItem[], query: URLSearchParams) {
  const records = items
    .map((item) => {
      const path = item.original_path || ''
      const separator = path.lastIndexOf(' / ')
      return {
        new_name: '',
        operation_status: '',
        undone: false,
        message: '',
        updated_ms: 0,
        attempts: [],
        ...item,
        directory: item.directory ?? (separator < 0 ? '' : path.slice(0, separator)),
        original_name: item.original_name ?? (separator < 0 ? path : path.slice(separator + 3))
      }
    })
    .sort((a, b) => {
      if (a.directory !== b.directory) return a.directory < b.directory ? -1 : 1
      return defined(a.id) - defined(b.id)
    })
  const directories = [...new Set(records.map((r) => r.directory))].map((path) => {
    const selected = records.filter((r) => r.directory === path)
    return {
      path,
      total: selected.length,
      ...Object.fromEntries(
        ['done', 'failed', 'running', 'queued', 'skipped', 'cancelled'].map((status) => [
          status,
          selected.filter((r) => r.status === status).length
        ])
      )
    }
  })
  const status = query.get('status')
  const scope = records.filter(
    (r) => !query.has('directory') || r.directory === query.get('directory')
  )
  const selected = scope.filter((r) => !status || r.status === status)
  // The server lists running files longest-running first.
  if (status === 'running')
    selected.sort((a, b) => a.updated_ms - b.updated_ms || defined(a.id) - defined(b.id))
  const page = Math.min(
    Math.max(1, Number(query.get('page')) || 1),
    Math.max(1, Math.ceil(selected.length / 100))
  )
  const count = (s: string) => records.filter((r) => r.status === s).length
  const extra: Record<string, unknown> = {}
  if (status === 'running')
    extra.recent = scope
      .filter((r) => finished.includes(defined(r.status)))
      .sort((a, b) => b.updated_ms - a.updated_ms || defined(b.id) - defined(a.id))
      .slice(0, 20)
      .map((r) => ({
        id: r.id,
        status: r.status,
        message: r.message ? { code: r.message } : undefined,
        directory: r.directory,
        original_name: r.original_name,
        new_name: r.new_name,
        operation_status: r.operation_status,
        undone: r.undone,
        started_ms: r.updated_ms ? r.updated_ms - 24000 : 0,
        finished_ms: r.updated_ms,
        attempts: r.attempts.length
      }))
  if (status === 'skipped') {
    const reasons = new Map<string, number>()
    for (const r of selected) reasons.set(r.message, (reasons.get(r.message) || 0) + 1)
    extra.skip_reasons = [...reasons]
      .map(([code, n]) => ({ reason: { code }, count: n }))
      .sort((a, b) => b.count - a.count)
  }
  return {
    directories,
    all_total: records.length,
    done_total: count('done'),
    running_total: count('running'),
    skipped_total: count('skipped'),
    failed_total: count('failed'),
    total: selected.length,
    page,
    items: selected
      .slice((page - 1) * 100, page * 100)
      .map((r) => ({ ...r, message: r.message ? { code: r.message } : undefined })),
    ...extra
  }
}

/** File records in a run detail, excluding the live view of files in progress. */
export const fileRecords = (scope: Locator) => scope.getByRole('article', { name: /^文件记录 / })

/** Visible request attempts; collapsed attempt lists are hidden. */
export const attempts = (scope: Locator) =>
  scope.getByRole('list', { name: '请求尝试' }).getByRole('listitem')

/** Directory groups of run records. */
export const directories = (scope: Locator) => scope.getByRole('region', { name: /^目录 / })

/** The expand toggle of each directory group, named after its path and counts. */
export const directoryToggles = (scope: Locator) =>
  directories(scope).getByRole('button', { name: /\d+ 个文件/ })

/** Files that finished recently, newest first. */
export const recentFiles = (scope: Locator) =>
  scope.getByRole('region', { name: /^最近结束/ }).getByRole('listitem')
