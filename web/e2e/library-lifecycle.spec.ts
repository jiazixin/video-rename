import { test, expect, type Page } from '@playwright/test'

const entry = (id: number, name: string, kind = 'file') => ({
  id,
  parent: 1,
  name,
  kind,
  size: 1,
  mtime: '2026-10-01T00:00:00Z',
  version: 1,
  processed: '',
  restore_id: 0,
  task_status: ''
})
const rows = [
  entry(10, 'first.wav'),
  entry(11, 'second.wav'),
  entry(12, 'first.png'),
  entry(13, 'second.png'),
  entry(14, 'delete.txt')
]
function directoryPage(items = rows, id = 1) {
  return {
    page: 1,
    total: items.length,
    items,
    next: 0,
    generation: '1',
    refreshing: false,
    refreshed: '',
    error: '',
    directory: entry(id, '媒体库', 'directory'),
    breadcrumbs: []
  }
}
async function login(page: Page) {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
}
async function mockLibrary(page: Page) {
  await page.route('**/api/directories/*/entries?*', (route) =>
    route.fulfill({ json: directoryPage() })
  )
  await page.route('**/api/entries/status?*', (route) => route.fulfill({ json: { items: rows } }))
}
async function leaveLibrary(page: Page) {
  await page.evaluate(() => {
    location.hash = 'runs'
  })
  await expect(page.getByRole('heading', { name: '批次记录.' })).toBeVisible()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect(await page.evaluate(() => document.body.style.overflow)).not.toBe('hidden')
}
async function settleUI(page: Page) {
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() =>
          requestAnimationFrame(() => {
            resolve()
          })
        )
      )
  )
}

for (const failed of [false, true])
  test(`关闭后重开同一条目历史忽略旧的${failed ? '错误' : '成功'}响应`, async ({ page }) => {
    await mockLibrary(page)
    await login(page)
    let release!: () => void
    const delayed = new Promise<void>((resolve) => {
      release = resolve
    })
    let requests = 0
    const records = (id: number, name: string) => [
      {
        id,
        source: 'manual',
        status: 'completed',
        old_name: 'original.txt',
        new_name: name,
        created: '2026-10-01T00:00:00Z'
      }
    ]
    await page.route('**/api/entries/14/history', async (route) => {
      const request = ++requests
      if (request === 1) {
        await delayed
        await route.fulfill(
          failed
            ? {
                status: 500,
                json: {
                  error: {
                    code: 'internal',
                    message: 'test failure',
                    details: { detail: '旧历史读取失败' }
                  }
                }
              }
            : { json: { items: records(1, '旧历史.txt') } }
        )
      } else await route.fulfill({ json: { items: records(2, '新历史.txt') } })
    })
    try {
      await page.getByRole('button', { name: '历史 delete.txt', exact: true }).click()
      await expect.poll(() => requests).toBe(1)
      await page
        .getByRole('dialog', { name: '命名历史' })
        .getByRole('button', { name: '关闭', exact: true })
        .last()
        .click()
      await expect(page.getByRole('dialog')).toHaveCount(0)
      await page.getByRole('button', { name: '历史 delete.txt', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: '命名历史' })
      await expect(dialog).toContainText('新历史.txt')
      const response = page.waitForResponse((r) => r.url().endsWith('/api/entries/14/history'))
      release()
      await (await response).finished()
      await settleUI(page)
      await expect(dialog).toContainText('新历史.txt')
      await expect(dialog.getByText('旧历史.txt')).toHaveCount(0)
      await expect(dialog.getByRole('alert')).toHaveCount(0)
    } finally {
      release()
    }
  })

for (const batch of [false, true])
  test(`${batch ? '批量' : '单项'}删除确认框在离开文件目录时关闭`, async ({ page }) => {
    await mockLibrary(page)
    await login(page)
    if (batch) {
      await page.getByRole('button', { name: '多选', exact: true }).click()
      await page.getByLabel('选择 delete.txt', { exact: true }).check()
      await page
        .locator('.batch-toolbar')
        .getByRole('button', { name: '删除', exact: true })
        .click()
    } else await page.getByRole('button', { name: '删除 delete.txt', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await leaveLibrary(page)
    await page.getByRole('button', { name: '文件目录', exact: true }).click()
    await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
    await expect(page.getByRole('dialog')).toHaveCount(0)
  })

test('单类编号记录在离开文件目录时关闭', async ({ page }) => {
  await mockLibrary(page)
  await page.route('**/api/directories/*/numberings?*', (route) =>
    route.fulfill({ json: { items: [] } })
  )
  await login(page)
  await page.getByRole('combobox', { name: '类型筛选', exact: true }).click()
  await page.getByRole('option', { name: '图片', exact: true }).click()
  await page.getByRole('button', { name: '编号记录', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '图片编号记录' })).toBeVisible()
  await leaveLibrary(page)
})

test('关闭后重开音频不会被旧的下一首响应切换', async ({ page }) => {
  await mockLibrary(page)
  await login(page)
  let release!: () => void
  const delayed = new Promise<void>((resolve) => {
    release = resolve
  })
  let requested = false
  await page.route('**/api/directories/*/entries?*', async (route) => {
    if (new URL(route.request().url()).searchParams.get('kind') === 'audio') {
      requested = true
      await delayed
      await route.fulfill({ json: directoryPage(rows.slice(0, 2)) })
    } else await route.fulfill({ json: directoryPage() })
  })
  try {
    await page.getByRole('button', { name: '播放音频 first.wav', exact: true }).click()
    await page.getByRole('button', { name: '下一首', exact: true }).click()
    await expect.poll(() => requested).toBe(true)
    await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
    await page.getByRole('button', { name: '播放音频 first.wav', exact: true }).click()
    await expect(page.getByRole('button', { name: '下一首', exact: true })).toBeEnabled()
    const response = page.waitForResponse(
      (r) => new URL(r.url()).searchParams.get('kind') === 'audio'
    )
    release()
    await (await response).finished()
    await settleUI(page)
    await expect(page.getByRole('dialog', { name: 'first.wav', exact: true })).toBeVisible()
    await expect(page.getByRole('dialog').locator('audio')).toHaveAttribute(
      'src',
      '/api/entries/10/audio'
    )
  } finally {
    release()
  }
})

test('关闭后重开图片不会被旧的下一张响应切换', async ({ page }) => {
  await mockLibrary(page)
  await login(page)
  let release!: () => void
  const delayed = new Promise<void>((resolve) => {
    release = resolve
  })
  let requested = false
  await page.route('**/api/entries/*/image-neighbor?*', async (route) => {
    requested = true
    await delayed
    await route.fulfill({ json: rows[3] })
  })
  try {
    await page.getByRole('button', { name: '预览图片 first.png', exact: true }).click()
    await page.getByRole('button', { name: '下一张图片', exact: true }).click()
    await expect.poll(() => requested).toBe(true)
    await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
    await page.getByRole('button', { name: '预览图片 first.png', exact: true }).click()
    await expect(page.getByRole('button', { name: '下一张图片', exact: true })).toBeEnabled()
    const response = page.waitForResponse((r) => r.url().includes('/image-neighbor?'))
    release()
    await (await response).finished()
    await settleUI(page)
    await expect(page.getByRole('dialog', { name: 'first.png', exact: true })).toBeVisible()
  } finally {
    release()
  }
})

test('目录选择器忽略已离开目录的迟到错误', async ({ page }) => {
  let release!: () => void
  const delayed = new Promise<void>((resolve) => {
    release = resolve
  })
  let requested = false
  await page.route('**/api/directories/*/entries?*', async (route) => {
    if (route.request().url().includes('/directories/2/')) {
      requested = true
      await delayed
      await route.fulfill({
        status: 500,
        json: {
          error: {
            code: 'internal',
            message: 'test failure',
            details: { detail: '旧目录读取失败' }
          }
        }
      })
    } else await route.fulfill({ json: directoryPage([entry(2, '子目录', 'directory')]) })
  })
  try {
    await login(page)
    await page.getByRole('button', { name: '计划管理', exact: true }).click()
    await page.getByRole('button', { name: '新建手动计划', exact: true }).click()
    const picker = page.locator('.directory-picker')
    await picker.getByRole('button', { name: '子目录', exact: true }).click()
    await expect.poll(() => requested).toBe(true)
    await picker.getByRole('button', { name: '媒体库', exact: true }).click()
    await expect(picker.getByRole('button', { name: '全选本页', exact: true })).toBeEnabled()
    const response = page.waitForResponse((r) => r.url().includes('/directories/2/entries?'))
    release()
    await (await response).finished()
    await settleUI(page)
    await expect(picker.getByText('旧目录读取失败')).toHaveCount(0)
    await expect(picker.getByRole('button', { name: '全选本页', exact: true })).toBeEnabled()
  } finally {
    release()
  }
})

test('离开文件目录后批量删除的迟到失败不会重新弹框', async ({ page }) => {
  await mockLibrary(page)
  await login(page)
  let release!: () => void
  const delayed = new Promise<void>((resolve) => {
    release = resolve
  })
  let requested = false
  await page.route('**/api/entries/14', async (route) => {
    requested = true
    await delayed
    await route.fulfill({
      status: 409,
      json: {
        error: {
          code: 'internal',
          message: 'test failure',
          details: { detail: '模拟删除失败' }
        }
      }
    })
  })
  try {
    await page.getByRole('button', { name: '多选', exact: true }).click()
    await page.getByLabel('选择 delete.txt', { exact: true }).check()
    await page.locator('.batch-toolbar').getByRole('button', { name: '删除', exact: true }).click()
    await page.getByRole('button', { name: '确认永久删除', exact: true }).click()
    await expect.poll(() => requested).toBe(true)
    await leaveLibrary(page)
    const response = page.waitForResponse((r) => r.url().endsWith('/api/entries/14'))
    release()
    await (await response).finished()
    await settleUI(page)
    await expect(page.getByRole('dialog')).toHaveCount(0)
  } finally {
    release()
  }
})

test('离开文件目录后超过100个目标的标记仍完整提交', async ({ page }) => {
  const entries = Array.from({ length: 101 }, (_, i) => entry(100 + i, `image-${i}.png`))
  await page.route('**/api/directories/*/entries?*', (route) => {
    const limit = Number(new URL(route.request().url()).searchParams.get('limit'))
    return route.fulfill({
      json: { ...directoryPage(entries.slice(0, limit)), total: entries.length }
    })
  })
  await page.route('**/api/entries/status?*', (route) =>
    route.fulfill({ json: { items: entries } })
  )
  await login(page)
  await page.getByRole('combobox', { name: '每页条目数', exact: true }).click()
  await page.getByRole('option', { name: '200 项 / 页', exact: true }).click()
  await expect(page.locator('tbody tr')).toHaveCount(101)
  await page.getByRole('button', { name: '多选', exact: true }).click()
  await page.locator('.batch-toolbar').getByRole('button', { name: '全选', exact: true }).click()
  let release!: () => void
  const delayed = new Promise<void>((resolve) => {
      release = resolve
    }),
    groups: number[][] = []
  await page.route('**/api/entries/processed', async (route) => {
    const body = route.request().postDataJSON()
    groups.push(body.targets)
    expect(body.marked).toBe(true)
    if (groups.length === 1) await delayed
    await route.fulfill({
      json: {
        key: body.key,
        status: 'completed',
        changed: body.targets.length,
        unchanged: 0,
        failed: 0,
        interrupted: false,
        failures: []
      }
    })
  })
  try {
    await page
      .locator('.batch-toolbar')
      .getByRole('button', { name: '标记已处理', exact: true })
      .click()
    await page.getByRole('button', { name: '确认标记', exact: true }).click()
    await expect.poll(() => groups.length).toBe(1)
    await leaveLibrary(page)
    release()
    await expect.poll(() => groups.flat()).toEqual(entries.map((e) => e.id))
    expect(groups.map((group) => group.length)).toEqual([100, 1])
    await expect(page.getByRole('dialog')).toHaveCount(0)
  } finally {
    release()
  }
})
