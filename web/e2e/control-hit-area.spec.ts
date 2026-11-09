import { test, expect, type Page } from '@playwright/test'
import { settleAnimations } from './support'

/** A spot inside a <label> but outside its control, where a click must do nothing. */
type BlankPoint = { kind: string; x: number; y: number; name: string }

const settingsTabs = [
  '处理与缓存',
  '后台目录扫描',
  '编号规则',
  'Prompt配置',
  '模型配置',
  '抽帧设置',
  '通知设置'
]

// Every kind of blank spot the sweep must have reached at least once.
const expectedKinds = [
  'below-control',
  'gap-control-text',
  'gap-text-control',
  'gap-title-control',
  'right-of-last-line',
  'right-of-text',
  'title-text'
]

/**
 * Runs in the page: measures a label and returns blank spots inside it. A field label's title
 * counts as blank; a checkbox, radio or switch label keeps its caption text clickable.
 */
function blankPointsIn(label: Element): BlankPoint[] {
  const controlSelector = 'input, select, textarea, button, a[href], [tabindex]'
  const box = label.getBoundingClientRect()
  const nested = [...label.querySelectorAll(controlSelector)]
  // A `for` label names a control elsewhere; only its own text and box can misfire.
  const named = label instanceof HTMLLabelElement ? label.control : null
  const external = !nested.length && named !== null
  const controls = (external ? [named] : nested)
    .map((control) => control.getBoundingClientRect())
    .filter((rect) => rect.width > 0 && rect.height > 0)
  const first = controls[0]
  if (!first || box.width === 0) return []
  const isToggle = (external ? named : label).matches(
    'input[type="checkbox"], input[type="radio"], :has(input[type="checkbox"], input[type="radio"])'
  )
  const texts: DOMRect[] = []
  const walker = document.createTreeWalker(label, NodeFilter.SHOW_TEXT)
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (!node.textContent?.trim() || node.parentElement?.closest(controlSelector)) continue
    const range = document.createRange()
    range.selectNodeContents(node)
    texts.push(...[...range.getClientRects()].filter((rect) => rect.width > 0))
  }
  const near = (rect: DOMRect, x: number, y: number, pad: number) =>
    x >= rect.left - pad && x <= rect.right + pad && y >= rect.top - pad && y <= rect.bottom + pad
  const name = label.textContent.replace(/\s+/g, ' ').trim().slice(0, 30)
  const points: BlankPoint[] = []
  const add = (kind: string, x: number, y: number) => {
    if (!near(box, x, y, -0.5) || controls.some((rect) => near(rect, x, y, 2))) return
    if (kind !== 'title-text' && texts.some((rect) => near(rect, x, y, 1))) return
    const hit = document.elementFromPoint(x, y)
    // Skip spots another layer (sticky header, dialog) covers. A label that ignores the pointer
    // passes the hit to its ancestors, which still means the spot is uncovered.
    if (hit && (label.contains(hit) || hit.contains(label))) points.push({ kind, x, y, name })
  }
  const title = texts[0]
  const last = texts.at(-1)
  const controlBottom = Math.max(...controls.map((rect) => rect.bottom))
  if (title && !isToggle)
    add('title-text', title.left + title.width / 2, title.y + title.height / 2)
  if (title) add('right-of-text', box.right - 2, title.y + title.height / 2)
  if (external) return points
  if (title && first.top - title.bottom >= 3)
    add('gap-title-control', first.left + 4, (title.bottom + first.top) / 2)
  if (box.bottom - controlBottom >= 3) add('below-control', first.left + 4, box.bottom - 1.5)
  if (title && title.left - first.right >= 3)
    add('gap-control-text', (first.right + title.left) / 2, first.y + first.height / 2)
  if (title && first.left - title.right >= 3)
    add('gap-text-control', (title.right + first.left) / 2, first.y + first.height / 2)
  if (last && last !== title && box.right - last.right >= 6)
    add('right-of-last-line', box.right - 2, last.y + last.height / 2)
  return points
}

/** Focus, toggle and popup state that a stray click on a label would change. */
function controlState(page: Page): Promise<string> {
  return page.evaluate(() => {
    const all = [...document.querySelectorAll('*')]
    const active = document.activeElement
    return JSON.stringify({
      focus: active && active !== document.body ? all.indexOf(active) : -1,
      checked: [...document.querySelectorAll('input')].map((input) => input.checked),
      expanded: [...document.querySelectorAll('[aria-expanded]')].map((element) =>
        element.getAttribute('aria-expanded')
      ),
      popups: document.querySelectorAll('[role="listbox"], [role="dialog"]').length
    })
  })
}

/** Waits for a newly opened dialog to move focus inside, so that move is not read as a misfire. */
async function dialogFocused(page: Page) {
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect
    .poll(() =>
      page.evaluate(() =>
        document.querySelector('[role="dialog"]')?.contains(document.activeElement)
      )
    )
    .toBe(true)
}

async function blur(page: Page) {
  await page.evaluate(() => {
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
  })
}

/** Clicks every blank spot in the visible labels and records the ones that changed anything. */
async function sweep(page: Page, where: string, found: { misfires: string[]; kinds: Set<string> }) {
  await settleAnimations(page)
  // CSS selectors here only drive geometry; the label count proves the sweep found fields.
  const labels = page.locator('label')
  let swept = 0
  // Recount each pass: a stray toggle can add or remove dependent fields.
  for (let index = 0; index < (await labels.count()); index++) {
    const label = labels.nth(index)
    if (!(await label.isVisible())) continue
    // Bordered radio cards are the control themselves; their whole area selects by design.
    if (await label.evaluate((element) => element.classList.contains('method-option'))) continue
    // Scroll instantly: a misfire in the unfixed layout can swap labels mid-animation.
    await label.evaluate((element) => {
      element.scrollIntoView({ block: 'center' })
    })
    await settleAnimations(page)
    const points = await label.evaluate(blankPointsIn)
    if (points.length) swept++
    for (const point of points) {
      found.kinds.add(point.kind)
      await blur(page)
      const before = await controlState(page)
      await page.mouse.click(point.x, point.y)
      if ((await controlState(page)) === before) continue
      found.misfires.push(
        `${where} · ${point.name} · ${point.kind} @${Math.round(point.x)},${Math.round(point.y)}`
      )
      // Undo a stray popup or toggle so later spots start from the same state. Escape goes to
      // the open combobox itself: elsewhere it would close the dialog under the sweep.
      const expanded = page.getByRole('combobox', { expanded: true })
      if (await expanded.count()) await expanded.press('Escape')
      else if (await page.evaluate(() => document.activeElement?.matches('input[type="checkbox"]')))
        await page.mouse.click(point.x, point.y)
    }
  }
  expect(swept, `${where} has labelled controls to sweep`).toBeGreaterThan(0)
}

test('字段与开关只在控件本身响应点击', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  const found = { misfires: [] as string[], kinds: new Set<string>() }
  await page.goto('/')
  await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
  await sweep(page, '登录', found)
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('button', { name: '系统设置', exact: true })).toBeVisible()

  await page.goto('/runs')
  await expect(page.getByRole('combobox', { name: '时间范围' })).toBeVisible()
  await sweep(page, '运行记录筛选', found)

  await page.goto('/settings')
  for (const tab of settingsTabs) {
    await page.getByRole('button', { name: tab, exact: true }).click()
    await expect(page.getByRole('button', { name: tab, exact: true })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
    // The old panel lingers for a frame or two while Vue runs the out-in transition.
    await settleAnimations(page)
    await page.waitForLoadState('networkidle')
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0)
    if (tab === '模型配置') {
      // The tab lists configurations; its fields live in the editor dialog.
      await page.getByRole('button', { name: '新增配置', exact: true }).click()
      await dialogFocused(page)
    } else {
      // Tabs swap with an out-in transition and then load; wait for the new tab's fields.
      await expect(page.getByRole('main').locator('label').first()).toBeVisible()
    }
    await sweep(page, tab, found)
    if (tab === '模型配置') {
      await page.keyboard.press('Escape')
      await expect(page.getByRole('dialog')).toHaveCount(0)
    }
  }

  await page.goto('/plans')
  await page.getByRole('button', { name: '新建自动计划', exact: true }).click()
  await dialogFocused(page)
  await expect(page.getByRole('checkbox', { name: '选择当前目录' })).toBeEnabled()
  await sweep(page, '新建自动计划', found)
  // Weekly plans add the weekday checkboxes.
  await page.getByRole('combobox', { name: '执行频率' }).click()
  await page.getByRole('option', { name: '按周', exact: true }).click()
  await expect(page.getByRole('checkbox', { name: '周一' })).toBeVisible()
  await sweep(page, '按周计划', found)

  await page.goto('/library')
  await page
    .getByRole('group', { name: '当前目录操作' })
    .getByRole('button', { name: '抽帧当前目录', exact: true })
    .click()
  await dialogFocused(page)
  await sweep(page, '抽帧当前目录', found)
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: '多选', exact: true }).click()
  await page.getByLabel('选择 2026 · 旅行记录', { exact: true }).check()
  await page
    .getByRole('group', { name: '批量操作' })
    .getByRole('button', { name: '抽帧', exact: true })
    .click()
  await dialogFocused(page)
  await sweep(page, '批量抽帧', found)

  await page.goto('/history')
  await page.getByRole('button', { name: '清除记录', exact: true }).click()
  await dialogFocused(page)
  await sweep(page, '清除运行记录', found)

  expect(found.misfires).toEqual([])
  expect([...found.kinds].sort()).toEqual(expectedKinds)
})

test('控件本身、开关文字和单选卡片仍然响应点击', async ({ page }) => {
  await page.goto('/')
  const username = page.getByLabel('用户名', { exact: true })
  await username.click()
  await expect(username).toBeFocused()
  await username.fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '显示密码' }).click()
  await expect(page.getByLabel('密码', { exact: true })).toHaveAttribute('type', 'text')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('button', { name: '系统设置', exact: true })).toBeVisible()

  // The magnifier sits inside the search box, so clicking it still focuses the input.
  await page.goto('/library')
  const search = page.getByRole('textbox', { name: '搜索当前目录' })
  await expect(search).toBeVisible()
  const box = page.locator('.search-input').filter({ has: search })
  // Click by position: the magnifier lets the pointer through, so it is not a click target.
  const icon = await box.locator('svg').boundingBox()
  if (!icon) throw new Error('search icon has no layout')
  await page.mouse.click(icon.x + icon.width / 2, icon.y + icon.height / 2)
  await expect(search).toBeFocused()
  await blur(page)
  const frame = await box.boundingBox()
  if (!frame) throw new Error('search box has no layout')
  await page.mouse.click(frame.x + 3, frame.y + frame.height / 2)
  await expect(search).toBeFocused()

  await page.goto('/runs')
  await page.getByRole('combobox', { name: '时间范围' }).click()
  await expect(page.getByRole('listbox', { name: '时间范围' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('listbox')).toHaveCount(0)

  await page.goto('/settings/processing')
  const limit = page.getByLabel('同时运行批次上限', { exact: true })
  await limit.click()
  await expect(limit).toBeFocused()
  const retry = page.getByRole('switch', { name: '开启失败自动重试' })
  const retryOn = await retry.isChecked()
  await page.getByText('开启失败自动重试', { exact: true }).click()
  await expect(retry).toBeChecked({ checked: !retryOn })
  await retry.click()
  await expect(retry).toBeChecked({ checked: retryOn })
  // The label still names the switch and the keyboard still toggles it.
  await retry.focus()
  await page.keyboard.press('Space')
  await expect(retry).toBeChecked({ checked: !retryOn })
  await page.keyboard.press('Space')
  await expect(retry).toBeChecked({ checked: retryOn })

  await page.goto('/settings/numbering')
  const lowercase = page.getByRole('switch', { name: '图片扩展名转小写' })
  const lowercaseOn = await lowercase.isChecked()
  await page.getByText('扩展名转小写', { exact: true }).first().click()
  await expect(lowercase).toBeChecked({ checked: !lowercaseOn })
  await page
    .getByText(/^开启后将扩展名转为小写/)
    .first()
    .click()
  await expect(lowercase).toBeChecked({ checked: lowercaseOn })
  const compact = page.getByRole('radio', { name: /^连续重新编号/ }).first()
  const card = page.locator('.method-option').filter({ has: compact })
  const cardBox = await card.boundingBox()
  if (!cardBox) throw new Error('numbering method card has no layout')
  // A bordered radio card is the control: its blank corner still selects it.
  await page.mouse.click(cardBox.x + cardBox.width - 4, cardBox.y + 4)
  await expect(compact).toBeChecked()

  await page.goto('/settings/notifications')
  const failureOnly = page.getByRole('switch', { name: '仅失败时通知' }).first()
  const failureOnlyOn = await failureOnly.isChecked()
  await page.getByText('仅失败时通知', { exact: true }).first().click()
  await expect(failureOnly).toBeChecked({ checked: !failureOnlyOn })
})
