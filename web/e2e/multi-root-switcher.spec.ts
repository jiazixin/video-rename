import AxeBuilder from '@axe-core/playwright'
import { test, expect, type Page } from '@playwright/test'
import { settleAnimations } from './support'

function makeEntry(id: number, name: string, kind = 'directory', parent = 0) {
  return {
    id,
    parent,
    name,
    kind,
    size_bytes: 0,
    mtime: '2026-11-20T00:00:00.000Z',
    etag: `"v-${id}"`,
    version: 1,
    processed: '',
    restore_id: 0,
    task_status: '',
    progress: kind === 'directory' ? { total: 0, processed: 0, unknown: 1, active: 0 } : undefined,
    contents: kind === 'directory' ? { directories: 1, videos: 0, images: 0, audio: 0 } : undefined
  }
}

async function login(page: Page) {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
}

async function expectNoSeriousViolations(page: Page, where: string, selectors: string[]) {
  let builder = new AxeBuilder({ page })
  for (const selector of selectors) builder = builder.include(selector)
  const audit = await builder.analyze()
  expect(
    audit.violations
      .filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')
      .map((violation) => ({
        id: violation.id,
        nodes: violation.nodes.map(
          (node) => `${node.target.join(' ')}: ${node.failureSummary ?? ''}`
        )
      })),
    where
  ).toEqual([])
}

test('2-5 个媒体根目录使用清晰边框的直达切换条并支持桌面与窄屏', async ({ page }) => {
  const roots = [
    { id: 1, name: 'color1' },
    { id: 2, name: 'color2' }
  ]
  const subDir = makeEntry(101, '2026-旅行素材', 'directory', 1)
  const color1File = makeEntry(102, 'color1-clip.mp4', 'video', 1)
  const color2File = makeEntry(201, 'color2-clip.mp4', 'video', 2)

  await page.route('**/api/directories/*/entries?*', (route) => {
    const match = /\/api\/directories\/(\d+)\/entries/.exec(route.request().url())
    const id = Number(match?.[1] ?? 1)
    if (id === 2) {
      return route.fulfill({
        json: {
          media_roots: roots,
          offset: 0,
          limit: 100,
          total: 1,
          items: [color2File],
          generation: '1',
          refreshing: false,
          refreshed: '2026-11-20T00:00:00.000Z',
          directory: makeEntry(2, 'color2'),
          breadcrumbs: [makeEntry(2, 'color2')]
        }
      })
    }
    if (id === 101) {
      return route.fulfill({
        json: {
          media_roots: roots,
          offset: 0,
          limit: 100,
          total: 0,
          items: [],
          generation: '1',
          refreshing: false,
          refreshed: '2026-11-20T00:00:00.000Z',
          directory: subDir,
          breadcrumbs: [makeEntry(1, 'color1'), subDir]
        }
      })
    }
    return route.fulfill({
      json: {
        media_roots: roots,
        offset: 0,
        limit: 100,
        total: 2,
        items: [subDir, color1File],
        generation: '1',
        refreshing: false,
        refreshed: '2026-11-20T00:00:00.000Z',
        directory: makeEntry(1, 'color1'),
        breadcrumbs: [makeEntry(1, 'color1')]
      }
    })
  })
  await page.route('**/api/entries/status?*', (route) => route.fulfill({ json: { items: [] } }))

  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page)
  await settleAnimations(page)

  const group = page.getByRole('group', { name: '切换媒体根目录', exact: true })
  await expect(group).toBeVisible()
  const color1Tab = group.getByRole('button', { name: 'color1', exact: true })
  const color2Tab = group.getByRole('button', { name: 'color2', exact: true })
  await expect(color1Tab).toHaveAttribute('aria-pressed', 'true')
  await expect(color2Tab).toHaveAttribute('aria-pressed', 'false')

  // Verify the switcher frame and active tab keep visible solid 1px borders.
  const borders = await group.evaluate((el) => {
    const active = el.querySelector<HTMLElement>('.root-tab.active')
    const groupStyle = getComputedStyle(el)
    const activeStyle = active ? getComputedStyle(active) : null
    return {
      groupBorderStyle: groupStyle.borderTopStyle,
      groupBorderWidth: groupStyle.borderTopWidth,
      activeBorderStyle: activeStyle?.borderTopStyle,
      activeBorderWidth: activeStyle?.borderTopWidth
    }
  })
  expect(borders).toEqual({
    groupBorderStyle: 'solid',
    groupBorderWidth: '1px',
    activeBorderStyle: 'solid',
    activeBorderWidth: '1px'
  })

  await page
    .locator('.library-directory-overview')
    .screenshot({ path: 'test-results/multi-root-inline-desktop.png' })

  await expectNoSeriousViolations(page, 'multi-root-inline-desktop', ['nav[aria-label="目录路径"]'])

  // One-click switch to color2.
  await color2Tab.click()
  await expect(color2Tab).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByText('color2-clip.mp4', { exact: true })).toBeVisible()

  // Switch back to color1, enter subdirectory, then click color1 tab to return to root.
  await color1Tab.click()
  await expect(page.getByRole('button', { name: '2026-旅行素材', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '2026-旅行素材', exact: true }).click()
  await expect(
    page
      .getByRole('navigation', { name: '目录路径', exact: true })
      .getByRole('button', { name: '2026-旅行素材', exact: true })
  ).toHaveAttribute('aria-current', 'location')

  await page
    .locator('.library-directory-overview')
    .screenshot({ path: 'test-results/multi-root-subdir-desktop.png' })

  await color1Tab.click()
  await expect(page.getByText('color1-clip.mp4', { exact: true })).toBeVisible()

  // Narrow viewport check: tabs stay usable with >= 24px target size, never overlap refresh, and pass axe.
  await page.setViewportSize({ width: 390, height: 844 })
  await settleAnimations(page)
  const tabBox = await color1Tab.boundingBox()
  const navBox = await page.getByRole('navigation', { name: '目录路径', exact: true }).boundingBox()
  const refreshBox = await page.getByRole('button', { name: '刷新', exact: true }).boundingBox()
  expect(tabBox).not.toBeNull()
  expect(navBox).not.toBeNull()
  expect(refreshBox).not.toBeNull()
  expect(tabBox?.height ?? 0).toBeGreaterThanOrEqual(24)
  expect((navBox?.x ?? 0) + (navBox?.width ?? 0)).toBeLessThanOrEqual((refreshBox?.x ?? 0) + 1)
  await page
    .locator('.library-directory-overview')
    .screenshot({ path: 'test-results/multi-root-inline-mobile.png' })
  await expectNoSeriousViolations(page, 'multi-root-inline-mobile', ['nav[aria-label="目录路径"]'])
})

test('超过 5 个媒体根目录使用清晰边框的下拉切换器', async ({ page }) => {
  const roots = Array.from({ length: 6 }, (_, i) => ({
    id: i + 1,
    name: i === 0 ? 'color1-主媒体库' : `color${i + 1}`
  }))
  await page.route('**/api/directories/*/entries?*', (route) => {
    const match = /\/api\/directories\/(\d+)\/entries/.exec(route.request().url())
    const id = Number(match?.[1] ?? 1)
    const active = roots.find((r) => r.id === id) ?? roots[0]
    if (!active) return route.fulfill({ status: 404 })
    return route.fulfill({
      json: {
        media_roots: roots,
        offset: 0,
        limit: 100,
        total: 1,
        items: [makeEntry(active.id * 100, `${active.name}.mp4`, 'video', active.id)],
        generation: '1',
        refreshing: false,
        refreshed: '2026-11-20T00:00:00.000Z',
        directory: makeEntry(active.id, active.name),
        breadcrumbs: [makeEntry(active.id, active.name)]
      }
    })
  })
  await page.route('**/api/entries/status?*', (route) => route.fulfill({ json: { items: [] } }))

  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page)
  await settleAnimations(page)

  const combobox = page.getByRole('combobox', { name: '切换媒体根目录', exact: true })
  await expect(combobox).toBeVisible()
  await expect(combobox).toContainText('color1-主媒体库')
  await expect(combobox).toContainText('1/6')

  const triggerBorder = await combobox.evaluate((el) => {
    const style = getComputedStyle(el)
    return { style: style.borderTopStyle, width: style.borderTopWidth }
  })
  expect(triggerBorder).toEqual({ style: 'solid', width: '1px' })

  await combobox.click()
  const listbox = page.getByRole('listbox', { name: '媒体目录列表', exact: true })
  await expect(listbox).toBeVisible()
  await page.screenshot({
    path: 'test-results/multi-root-dropdown-open.png',
    clip: { x: 200, y: 120, width: 720, height: 420 }
  })
  await expectNoSeriousViolations(page, 'multi-root-dropdown-open', [
    'nav[aria-label="目录路径"]',
    '.root-dropdown-menu'
  ])

  await listbox.getByRole('option', { name: 'color4', exact: true }).click()
  await expect(combobox).toContainText('color4')
  await expect(combobox).toContainText('4/6')
  await expect(page.getByText('color4.mp4', { exact: true })).toBeVisible()
})
