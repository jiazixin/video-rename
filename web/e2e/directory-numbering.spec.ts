import { bodyRows, defined, toasts } from './support'
import { test, expect } from '@playwright/test'

test('顶部编号不依赖视图，递归索引并按文件夹编号，刷新后可还原', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '递归编号测试', exact: true }).click()
  await expect(page.getByText('photo.JPG', { exact: true })).toBeVisible()
  const actions = page.getByRole('group', { name: '当前目录操作' })
  await expect(actions.getByRole('button')).toHaveText([
    '抽帧',
    '重命名',
    '编号',
    '标记',
    '取消标记'
  ])
  await expect(page.getByText('作用于当前目录及所有子目录', { exact: true })).toHaveCount(0)
  await page.getByLabel('搜索当前目录').fill('clip')
  await expect(bodyRows(page)).toHaveCount(1)
  await actions.getByRole('button', { name: '编号', exact: true }).click()
  let dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('共 4 个目录、8 个文件，8 个需要修改')
  await expect(bodyRows(dialog)).toHaveCount(8)
  await expect(dialog.getByRole('table')).toContainText('子目录/孙目录')
  await expect(bodyRows(dialog).nth(0)).toContainText('photo.JPG1.jpg')
  await expect(bodyRows(dialog).nth(1)).toContainText('song.MP31.mp3')
  await page.screenshot({ path: 'test-results/directory-numbering-preview.png' })
  await dialog.getByRole('button', { name: '确认编号', exact: true }).click()
  await expect(toasts(page).first()).toContainText('已编号 8 个文件，完成 4 个目录。')
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
  await page.reload()
  await expect(page.getByLabel('搜索当前目录')).toHaveValue('clip')
  await page.getByLabel('搜索当前目录').fill('')
  await actions.getByRole('button', { name: '编号', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('0 个需要修改，8 个保持不变')
  await page.getByRole('dialog').getByRole('button', { name: '编号记录', exact: true }).click()
  dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('article')).toHaveCount(4)
  const nested = dialog.getByRole('article').filter({ hasText: '递归编号测试/子目录/孙目录' })
  await nested.getByRole('button', { name: '还原本次编号', exact: true }).click()
  await dialog.getByRole('button', { name: '确认整批还原', exact: true }).click()
  await expect(toasts(page).first()).toContainText('已还原 2 个文件，完成 1 个目录。')
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
  await page.setViewportSize({ width: 390, height: 844 })
  const title = defined(await page.getByRole('heading', { name: '文件目录.' }).boundingBox())
  const bounds = await actions.getByRole('button').evaluateAll((buttons) =>
    buttons.map((button) => {
      const r = button.getBoundingClientRect()
      return { left: r.left, right: r.right, top: r.top, height: r.height }
    })
  )
  for (const bound of bounds) {
    expect(bound.top).toBeGreaterThanOrEqual(title.y + title.height)
    expect(bound.left).toBeGreaterThanOrEqual(0)
    expect(bound.right).toBeLessThanOrEqual(390)
    expect(bound.height).toBeGreaterThanOrEqual(44)
  }
  await page.screenshot({ path: 'test-results/directory-actions-mobile.png' })
  await page.getByRole('button', { name: '子目录', exact: true }).click()
  await page.getByRole('button', { name: '孙目录', exact: true }).click()
  await expect(page.getByText('photo.JPG', { exact: true })).toBeVisible()
  await expect(page.getByText('song.MP3', { exact: true })).toBeVisible()
  await expect(page.getByText('clip.mp4', { exact: true })).toBeVisible()
})

test('编号冲突直接显示原名、占用类型和可复制诊断', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '编号冲突测试', exact: true }).click()
  await expect(page.getByRole('button', { name: '子目录', exact: true })).toBeVisible()
  await page
    .getByRole('group', { name: '当前目录操作' })
    .getByRole('button', { name: '编号', exact: true })
    .click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('alert')).toContainText('原文件：photo.jpg')
  await expect(dialog.getByRole('alert')).toContainText('编号冲突测试/子目录')
  await dialog.getByText('查看诊断详情', { exact: true }).click()
  await expect(dialog.getByText(/target kind: directory/)).toBeVisible()
  await expect(dialog.getByText(/occupying entry: "1\.jpg"/)).toBeVisible()
  await expect(dialog.getByRole('button', { name: '复制诊断', exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: '确认编号', exact: true })).toHaveCount(0)
  await dialog.getByRole('button', { name: '编号记录', exact: true }).click()
  await expect(dialog).toContainText('暂无编号记录')
  await expect(dialog.getByText('查看诊断详情', { exact: true })).toHaveCount(0)
})

test('目录编号持续反馈进度，关闭取消，预览完成仍须确认', async ({ page }) => {
  let current = '',
    action = '',
    ready = false
  const submissions: string[] = [],
    cancelled: string[] = []
  await page.route('**/api/numbering/jobs', async (route) => {
    const body = route.request().postDataJSON()
    current = defined(route.request().headers()['idempotency-key'])
    action = body.action
    submissions.push(action)
    await route.fulfill({
      status: 202,
      json: {
        key: current,
        status: 'running',
        phase: action,
        directories: 0,
        files: 0,
        done: 0,
        total: 1
      }
    })
  })
  await page.route('**/api/numbering/jobs/*', async (route) => {
    await route.fulfill({
      json: {
        key: current,
        status: ready && action === 'preview' ? 'completed' : 'running',
        phase: action,
        directories: 12,
        files: 3500,
        done: 0,
        total: 1,
        path: '测试目录'
      }
    })
  })
  await page.route('**/api/numbering/jobs/*/cancel', async (route) => {
    cancelled.push(route.request().url())
    await route.fulfill({ json: { status: 'cancelling' } })
  })
  await page.route('**/api/numbering/jobs/*/result?*', async (route) => {
    await route.fulfill({
      json: {
        batches: [
          {
            id: 'preview-batch',
            path: '测试目录',
            parent: 1,
            total: 1,
            changed: 1,
            status: 'preview'
          }
        ],
        items: Array.from({ length: 100 }, (_, i) => ({
          entry: i + 1,
          path: '很长的目录名称/多层目录/用于验证布局的图片文件夹',
          old_name: 'photo.jpg',
          new_name: '1.jpg'
        })),
        total: 100
      }
    })
  })
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  const button = page
    .getByRole('group', { name: '当前目录操作' })
    .getByRole('button', { name: '编号', exact: true })
  await button.click()
  await expect(page.getByRole('dialog')).toContainText('已扫描 12 个目录、3500 个文件')
  // The point lies on the backdrop, outside the dialog.
  await page.mouse.click(5, 5)
  await expect(page.getByRole('dialog')).toContainText('确定停止并关闭编号窗口吗')
  expect(cancelled).toHaveLength(0)
  await expect(page.getByRole('button', { name: '继续处理', exact: true })).toBeFocused()
  await page.getByRole('button', { name: '继续处理', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('已扫描 12 个目录')
  await page.keyboard.press('Escape')
  expect(cancelled).toHaveLength(0)
  await page.getByRole('button', { name: '继续处理', exact: true }).click()
  await page.getByRole('button', { name: '停止并关闭', exact: true }).click()
  await page.getByRole('button', { name: '确定停止并关闭', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect.poll(() => cancelled.length).toBe(1)
  expect(submissions).toEqual(['preview'])
  ready = true
  await button.click()
  await expect(page.getByRole('dialog')).toContainText('photo.jpg1.jpg')
  expect(submissions).toEqual(['preview', 'preview'])
  for (const viewport of [
    { width: 1440, height: 760 },
    { width: 390, height: 844 }
  ]) {
    await page.setViewportSize(viewport)
    const layout = await page.getByRole('dialog').evaluate((el) => {
      const content = el.querySelector('.modal-content'),
        table = el.querySelector('.numbering-preview'),
        footer = el.querySelector('.modal-footer')
      return {
        outer: content ? content.scrollHeight - content.clientHeight : NaN,
        table: table ? table.scrollHeight - table.clientHeight : NaN,
        bottom: footer?.getBoundingClientRect().bottom ?? NaN
      }
    })
    expect(layout.outer).toBeLessThanOrEqual(1)
    expect(layout.table).toBeGreaterThan(0)
    expect(layout.bottom).toBeLessThanOrEqual(viewport.height)
  }
  await page.screenshot({ path: 'test-results/numbering-fixed-footer-mobile.png' })
  await page.setViewportSize({ width: 1440, height: 760 })
  await page.screenshot({ path: 'test-results/numbering-fixed-footer-desktop.png' })
  await page.getByRole('button', { name: '确认编号', exact: true }).click()
  await expect.poll(() => submissions).toEqual(['preview', 'preview', 'apply'])
  await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('确定停止并关闭编号窗口吗')
  expect(cancelled).toHaveLength(1)
  await page.getByRole('button', { name: '确定停止并关闭', exact: true }).click()
  await expect.poll(() => cancelled.length).toBe(2)
  await expect(page.getByRole('dialog')).toHaveCount(0)
})

test('编号历史刷新后可展开并复制执行失败现场', async ({ page, context }) => {
  const diagnostic =
    '编号执行失败诊断\n阶段：最终编号\n原文件名："2.JPG"\n目标文件名："2.jpg"\n目标同名或大小写匹配条目总数：1'
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.route('**/api/directories/*/numberings/tree', (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: 'failed-batch',
            parent: 1,
            path: '测试目录',
            status: 'rolled_back',
            created: '2026-09-30T12:00:00.000Z',
            undo_of: '',
            error: {
              code: 'numbering_rolled_back',
              details: { cause: { code: 'rename_conflict' } }
            },
            processing_log: diagnostic,
            total: 3,
            changed: 3
          }
        ]
      }
    })
  )
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '编号冲突测试', exact: true }).click()
  for (let attempt = 0; attempt < 2; attempt++) {
    if (attempt) await page.reload()
    await page
      .getByRole('group', { name: '当前目录操作' })
      .getByRole('button', { name: '编号', exact: true })
      .click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('button', { name: '编号记录', exact: true })).toBeEnabled()
    await dialog.getByRole('button', { name: '编号记录', exact: true }).click()
    const record = dialog.getByRole('article')
    await record.getByText('查看诊断详情', { exact: true }).click()
    await expect(record.getByText(diagnostic, { exact: true })).toBeVisible()
    await record.getByRole('button', { name: '复制诊断', exact: true }).click()
    await expect(record.getByRole('status')).toHaveText('已复制诊断')
    expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(diagnostic)
  }
})
