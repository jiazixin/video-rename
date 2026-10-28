import { expect, test } from '@playwright/test'
import { mockRunPages } from './run-pages'
import { fileRecords, recordPage } from './run-record-pages'
import { pageReadout } from './support'

test('批次文件详情从 URL 恢复筛选和页码，刷新后保持', async ({ page }) => {
  const run = {
    id: 975,
    task: 'rename',
    source: 'manual',
    status: 'completed',
    targets: [1],
    target_names: ['素材'],
    created: '2026-10-28T00:00:00.000Z',
    discovered: 206,
    done: 1,
    skipped: 0,
    failed: 205
  }
  const items = Array.from({ length: 206 }, (_, index) => ({
    id: index + 1,
    entry: index + 1,
    status: index === 205 ? 'done' : 'failed',
    original_path: `素材 / clip-${String(index + 1).padStart(3, '0')}.mp4`,
    message: index === 205 ? '' : 'rename_failed'
  }))
  const requests: URLSearchParams[] = []
  await mockRunPages(page, [run])
  await page.route('**/api/runs/975', (route) => route.fulfill({ json: run }))
  await page.route('**/api/runs/975/items?*', (route) => {
    const query = new URL(route.request().url()).searchParams
    requests.push(query)
    return route.fulfill({ json: recordPage(items, query) })
  })
  await page.request.post('/api/auth/login', {
    data: { username: 'admin', password: 'test-password-123' }
  })
  await page.goto(
    `/runs?detail=975&record_status=failed&record_page=3&record_directory=${encodeURIComponent('path:素材')}`
  )
  const body = page
    .getByRole('dialog', { name: '运行 #975' })
    .getByRole('region', { name: '运行记录', exact: true })
  const navigation = body.getByRole('navigation', { name: '运行记录翻页' })
  await expect(fileRecords(body)).toHaveCount(5)
  await expect(pageReadout(navigation)).toHaveText(/^3\//)
  await expect(body.getByRole('button', { name: /^最终失败/ })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await expect(body.getByRole('combobox', { name: '记录目录筛选' })).toContainText('素材')
  expect(requests[0]?.get('offset')).toBe('200')
  expect(requests[0]?.get('status')).toBe('failed')
  expect(requests[0]?.get('directory')).toBe('素材')
  await body.getByRole('button', { name: '上页文件' }).click()
  await expect(pageReadout(navigation)).toHaveText(/^2\//)
  await expect(page).toHaveURL(/record_page=2/)
  await page.reload()
  await expect(fileRecords(body)).toHaveCount(100)
  await expect(pageReadout(navigation)).toHaveText(/^2\//)
  await body.getByRole('button', { name: /^已完成/ }).click()
  await expect(fileRecords(body)).toHaveCount(1)
  await expect(pageReadout(navigation)).toHaveText(/^1\//)
  await expect(page).toHaveURL(/record_status=done/)
  expect(new URL(page.url()).searchParams.get('detail')).toBe('975')
})
