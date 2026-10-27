import { bodyRows, defined } from './support'
import { test, expect, type Page } from '@playwright/test'
import type { Entry } from '../src/app/contracts'

const entry = (id: number): Entry => ({
  id,
  parent: 1,
  name: `retry-${id}.png`,
  kind: 'file',
  size: 1,
  mtime: '2026-10-01T00:00:00Z',
  version: 1,
  processed: '',
  restore_id: 0,
  task_status: ''
})
const result = (key: string, changed = 1, status = 'completed') => ({
  key,
  status,
  changed,
  unchanged: 0,
  failed: 0,
  interrupted: false,
  failures: []
})
const dialog = (page: Page) => page.getByRole('dialog', { name: '标记已处理', exact: true })
const confirm = (page: Page) => dialog(page).getByRole('button', { name: '确认标记', exact: true })

async function setup(page: Page, count = 1) {
  const entries = Array.from({ length: count }, (_, index) => entry(1000 + index))
  await page.route('**/api/directories/*/entries?*', (route) => {
    const limit = Number(new URL(route.request().url()).searchParams.get('limit'))
    return route.fulfill({
      json: {
        page: 1,
        total: entries.length,
        items: entries.slice(0, limit),
        next: 0,
        generation: '1',
        refreshing: false,
        refreshed: '',
        error: '',
        directory: { ...entry(1), name: '媒体库', kind: 'directory' },
        breadcrumbs: []
      }
    })
  })
  await page.route('**/api/entries/status?*', (route) =>
    route.fulfill({ json: { items: entries } })
  )
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
  await expect(bodyRows(page)).toHaveCount(Math.min(count, 100))
  return entries
}

async function openSingle(page: Page) {
  await page.getByRole('button', { name: '标记已处理 retry-1000.png', exact: true }).click()
  await expect(confirm(page)).toBeEnabled()
}

for (const failure of ['断连', '502 JSON', '502 HTML', '409 HTML']) {
  test(`首次提交${failure}后只查询原任务，不重新执行`, async ({ page }) => {
    await setup(page)
    const posts: string[] = [],
      queries: string[] = []
    await page.route('**/api/entries/processed', async (route) => {
      posts.push(route.request().postDataJSON().key)
      if (failure === '断连') await route.abort('connectionreset')
      else if (failure === '502 JSON')
        await route.fulfill({
          status: 502,
          json: {
            error: {
              code: 'internal',
              message: 'test failure',
              details: { detail: '代理返回错误' }
            }
          }
        })
      else
        await route.fulfill({
          status: failure === '502 HTML' ? 502 : 409,
          contentType: 'text/html',
          body: '<html>代理响应</html>'
        })
    })
    await page.route('**/api/entries/processed/status/*', async (route) => {
      const requestKey = decodeURIComponent(
        defined(new URL(route.request().url()).pathname.split('/').pop())
      )
      queries.push(requestKey)
      expect(route.request().method()).toBe('GET')
      await route.fulfill({ json: result(requestKey) })
    })
    await openSingle(page)
    await confirm(page).click()
    await expect(dialog(page).getByRole('alert')).toContainText('提交结果未确认')
    await expect(dialog(page).getByRole('alert')).toContainText('可再次确认查询原任务')
    await expect(confirm(page)).toBeEnabled()
    await confirm(page).click()
    await expect(dialog(page)).toHaveCount(0)
    expect(posts).toHaveLength(1)
    expect(queries).toEqual(posts)
    await expect(page.getByRole('status').filter({ hasText: '已标记 1 个文件' })).toBeVisible()
  })
}

test('明确409拒绝后使用新请求键提交，不声称后台仍在执行', async ({ page }) => {
  await setup(page)
  const posts: string[] = []
  let queries = 0
  await page.route('**/api/entries/processed', async (route) => {
    const requestKey = route.request().postDataJSON().key
    posts.push(requestKey)
    if (posts.length === 1)
      await route.fulfill({
        status: 409,
        json: {
          error: {
            code: 'internal',
            message: 'test failure',
            details: { detail: '已有标记任务正在执行' }
          }
        }
      })
    else await route.fulfill({ json: result(requestKey) })
  })
  await page.route('**/api/entries/processed/status/*', async (route) => {
    queries++
    await route.fulfill({
      status: 404,
      json: {
        error: {
          code: 'internal',
          message: 'test failure',
          details: { detail: '不应查询被拒绝任务' }
        }
      }
    })
  })
  await openSingle(page)
  await confirm(page).click()
  await expect(dialog(page).getByRole('alert')).toContainText('本批次未启动，可再次确认提交')
  await expect(dialog(page).getByRole('alert')).not.toContainText('后台任务可能仍在执行')
  await confirm(page).click()
  await expect(dialog(page)).toHaveCount(0)
  expect(posts).toHaveLength(2)
  expect(posts[0]).not.toBe(posts[1])
  expect(queries).toBe(0)
})

test('旧结果404后禁用原弹窗提交，关闭重新选择才创建新操作', async ({ page }) => {
  await setup(page)
  const posts: string[] = [],
    queries: string[] = []
  await page.route('**/api/entries/processed', async (route) => {
    const requestKey = route.request().postDataJSON().key
    posts.push(requestKey)
    if (posts.length === 1) await route.abort('connectionreset')
    else await route.fulfill({ json: result(requestKey) })
  })
  await page.route('**/api/entries/processed/status/*', async (route) => {
    queries.push(
      decodeURIComponent(defined(new URL(route.request().url()).pathname.split('/').pop()))
    )
    await route.fulfill({
      status: 404,
      json: {
        error: {
          code: 'internal',
          message: 'test failure',
          details: { detail: '标记任务不存在' }
        }
      }
    })
  })
  await openSingle(page)
  await confirm(page).click()
  await expect(dialog(page).getByRole('alert')).toContainText('提交结果未确认')
  await confirm(page).click()
  await expect(dialog(page).getByRole('alert')).toContainText('标记任务结果已不可查询')
  await expect(dialog(page).getByRole('alert')).toContainText('已完成的标记会保留')
  await expect(dialog(page).getByRole('alert')).toContainText('请关闭弹窗，重新选择目标')
  await expect(confirm(page)).toBeDisabled()
  await dialog(page)
    .getByRole('form')
    .evaluate((form: HTMLFormElement) => {
      form.requestSubmit()
    })
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => {
          resolve()
        })
      )
  )
  expect(posts).toHaveLength(1)
  expect(queries).toEqual(posts)
  // The footer button follows the header's icon button of the same name.
  await dialog(page).getByRole('button', { name: '关闭', exact: true }).last().click()
  await openSingle(page)
  await confirm(page).click()
  await expect(dialog(page)).toHaveCount(0)
  expect(posts).toHaveLength(2)
  expect(posts[0]).not.toBe(posts[1])
})

for (const failure of ['断连', '409拒绝']) {
  test(`跨100目标批次在第二批${failure}后不重放已完成第一批`, async ({ page }) => {
    const entries = await setup(page, 101)
    await page.getByRole('combobox', { name: '每页条目数', exact: true }).click()
    await page.getByRole('option', { name: '200 项 / 页', exact: true }).click()
    await expect(bodyRows(page)).toHaveCount(101)
    await page.getByRole('button', { name: '多选', exact: true }).click()
    await page
      .getByRole('group', { name: '批量操作' })
      .getByRole('button', { name: '全选', exact: true })
      .click()
    const posts: { key: string; targets: number[] }[] = [],
      queries: string[] = []
    await page.route('**/api/entries/processed', async (route) => {
      const body = route.request().postDataJSON()
      posts.push(body)
      if (posts.length === 2) {
        if (failure === '断连') await route.abort('connectionreset')
        else
          await route.fulfill({
            status: 409,
            json: {
              error: {
                code: 'internal',
                message: 'test failure',
                details: { detail: '已有标记任务正在执行' }
              }
            }
          })
      } else await route.fulfill({ json: result(body.key, body.targets.length) })
    })
    await page.route('**/api/entries/processed/status/*', async (route) => {
      const requestKey = decodeURIComponent(
        defined(new URL(route.request().url()).pathname.split('/').pop())
      )
      queries.push(requestKey)
      const submitted = defined(posts.find((post) => post.key === requestKey))
      expect(submitted).toBeTruthy()
      await route.fulfill({ json: result(requestKey, submitted.targets.length) })
    })
    await page
      .getByRole('group', { name: '批量操作' })
      .getByRole('button', { name: '标记已处理', exact: true })
      .click()
    await confirm(page).click()
    await expect(dialog(page).getByRole('alert')).toContainText('已确认变更 100 个文件')
    await confirm(page).click()
    await expect(dialog(page)).toHaveCount(0)
    await expect(page.getByRole('status').filter({ hasText: '已标记 101 个文件' })).toBeVisible()
    expect(defined(posts[0]).targets).toEqual(entries.slice(0, 100).map((item) => item.id))
    expect(defined(posts[1]).targets).toEqual([defined(entries[100]).id])
    if (failure === '断连') {
      expect(posts).toHaveLength(2)
      expect(queries).toEqual(posts.map((post) => post.key))
    } else {
      expect(posts).toHaveLength(3)
      expect(queries).toEqual([defined(posts[0]).key])
      expect(defined(posts[2]).key).not.toBe(defined(posts[1]).key)
      expect(defined(posts[2]).targets).toEqual(defined(posts[1]).targets)
    }
  })
}

for (const interrupted of [false, true]) {
  test(`终态${interrupted ? '中断' : '部分失败'}提示已提交结果保留并禁止直接重放`, async ({
    page
  }) => {
    await setup(page)
    let posts = 0
    await page.route('**/api/entries/processed', async (route) => {
      posts++
      await route.fulfill({
        json: {
          ...result(route.request().postDataJSON().key, 0),
          interrupted,
          failed: interrupted ? 0 : 1,
          failures: interrupted ? [] : [{ name: 'retry-1000.png', message: '文件正在处理' }]
        }
      })
    })
    await openSingle(page)
    await confirm(page).click()
    await expect(dialog(page).getByRole('alert')).toContainText(
      interrupted ? '标记任务已中断' : '失败 1 项'
    )
    await expect(dialog(page).getByRole('alert')).toContainText('已完成的标记会保留')
    await expect(dialog(page).getByRole('alert')).not.toContainText('后台任务可能仍在执行')
    await expect(confirm(page)).toBeDisabled()
    expect(posts).toBe(1)
  })
}
