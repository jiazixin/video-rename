import type { RunItem } from '../src/features/runs/use-run-records'

export function recordPage(items: Partial<RunItem>[], query: URLSearchParams) {
  const records = items
    .map((item) => {
      const path = item.original_path || ''
      const separator = path.lastIndexOf(' / ')
      return {
        new_name: '',
        operation_status: '',
        undone: false,
        attempts: [],
        ...item,
        directory: item.directory ?? (separator < 0 ? '' : path.slice(0, separator)),
        original_name: item.original_name ?? (separator < 0 ? path : path.slice(separator + 3))
      }
    })
    .sort((a, b) =>
      a.directory < b.directory ? -1 : a.directory > b.directory ? 1 : a.id! - b.id!
    )
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
  const selected = records.filter(
    (r) =>
      (!query.has('directory') || r.directory === query.get('directory')) &&
      (!query.get('status') || r.status === query.get('status'))
  )
  const page = Math.min(
    Math.max(1, Number(query.get('page')) || 1),
    Math.max(1, Math.ceil(selected.length / 100))
  )
  return {
    directories,
    all_total: records.length,
    failed_total: records.filter((r) => r.status === 'failed').length,
    total: selected.length,
    page,
    items: selected.slice((page - 1) * 100, page * 100)
  }
}
