import { bodyRows, defined, pageReadout } from './support'
import { test, expect, type Page } from '@playwright/test'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

type Entry = {
  id: number
  name: string
  version: number
  etag: string
  processed: string
  restore_id: number
}

async function fixture(page: Page, name: string, files: string[], directories: string[] = []) {
  const { mediaPath } = JSON.parse(readFileSync('test-results/fixture.json', 'utf8'))
  const path = join(mediaPath, name)
  mkdirSync(path, { recursive: true })
  for (const directory of directories) mkdirSync(join(path, directory), { recursive: true })
  for (const file of files) writeFileSync(join(path, file), 'isolated processing-status fixture')
  const login = await page.request.post('/api/auth/login', {
    data: { username: 'admin', password: 'test-password-123' }
  })
  expect(login.ok()).toBe(true)
  expect((await page.request.post('/api/directories/1/refresh', { data: {} })).ok()).toBe(true)
  let directory: Entry | undefined
  await expect
    .poll(async () => {
      const result = await (
        await page.request.get('/api/directories/1/entries?page=1&limit=200')
      ).json()
      directory = result.items.find((entry: Entry) => entry.name === name)
      return !!directory && !result.refreshing
    })
    .toBe(true)
  const id = defined(directory).id
  expect((await page.request.post(`/api/directories/${id}/refresh`, { data: {} })).ok()).toBe(true)
  await expect
    .poll(async () => {
      const result = await (
        await page.request.get(`/api/directories/${id}/entries?page=1&limit=200`)
      ).json()
      return !result.refreshing && result.total === files.length + directories.length
    })
    .toBe(true)
  const entries = new Map<string, Entry>()
  for (let number = 1; number <= Math.ceil((files.length + directories.length) / 200); number++) {
    const result = await (
      await page.request.get(`/api/directories/${id}/entries?page=${number}&limit=200`)
    ).json()
    for (const entry of result.items as Entry[]) entries.set(entry.name, entry)
  }
  async function mark(names: string[]) {
    for (let offset = 0; offset < names.length; offset += 100) {
      const targets = names.slice(offset, offset + 100).map((file) => defined(entries.get(file)).id)
      const request = await page.request.post('/api/entries/processed', {
        headers: { 'Idempotency-Key': `status-${id}-${offset}` },
        data: { targets, marked: true }
      })
      expect(request.ok()).toBe(true)
      await expect
        .poll(async () => {
          const current = (
            await (await page.request.get(`/api/entries/status?ids=${targets.join(',')}`)).json()
          ).items
          return (
            current.length === targets.length &&
            current.every((entry: Entry) => entry.processed === 'marked')
          )
        })
        .toBe(true)
    }
  }
  async function rename(oldName: string, nextName: string) {
    const entry = defined(entries.get(oldName))
    const result = await page.request.post(`/api/entries/${entry.id}/rename`, {
      headers: { 'If-Match': entry.etag, 'Idempotency-Key': `status-rename-${entry.id}` },
      data: { name: nextName }
    })
    expect(result.ok()).toBe(true)
    const current = (await (await page.request.get(`/api/entries/status?ids=${entry.id}`)).json())
      .items
    entries.delete(oldName)
    entries.set(nextName, current[0])
  }
  async function open() {
    await page.goto('/')
    await page.getByRole('button', { name, exact: true }).click()
    await expect(bodyRows(page)).toHaveCount(Math.min(100, files.length + directories.length))
  }
  async function populateDirectory(name: string, files: string[]) {
    for (const file of files) writeFileSync(join(path, name, file), 'directory status fixture')
    const child = defined(entries.get(name))
    expect(
      (
        await page.request.post(`/api/directories/${child.id}/refresh`, {
          data: {}
        })
      ).ok()
    ).toBe(true)
    let items: Entry[] = []
    await expect
      .poll(async () => {
        const result = await (
          await page.request.get(`/api/directories/${child.id}/entries?page=1&limit=200`)
        ).json()
        items = result.items
        return !result.refreshing && result.total === files.length
      })
      .toBe(true)
    return items
  }
  return { id, entries, mark, rename, open, populateDirectory }
}

const rows = (page: Page) => bodyRows(page)
const names = (page: Page) => rows(page).getByRole('rowheader')

async function select(page: Page, label: string, option: string) {
  await page.getByRole('combobox', { name: label, exact: true }).click()
  await page.getByRole('option', { name: option, exact: true }).click()
}

test('处理状态与搜索、类型共同筛选，翻页只显示匹配文件并在切换后回第一页', async ({ page }) => {
  const clips = Array.from(
    { length: 230 },
    (_, index) => `clip${String(index).padStart(3, '0')}.mp4`
  )
  const setup = await fixture(
    page,
    '状态筛选-分页',
    [...clips, 'marked.png', 'pending.png'],
    ['clip-folder']
  )
  await setup.mark([...clips.slice(0, 125), 'marked.png'])
  await setup.open()
  await select(page, '类型筛选', '视频')
  await select(page, '处理状态筛选', '已标记')
  await expect(page.getByText('共 125 项 · 100 项 / 当前页', { exact: true })).toBeVisible()
  await expect(names(page)).toHaveText(clips.slice(0, 100))
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await expect(names(page)).toHaveText(clips.slice(100, 125))
  await expect(rows(page)).toHaveCount(25)
  await expect(rows(page).filter({ hasText: '手动标记已处理' })).toHaveCount(25)
  await expect(
    pageReadout(page.getByRole('navigation', { name: '分页导航', exact: true }))
  ).toHaveText(/^2\//)

  await select(page, '处理状态筛选', '未处理')
  await expect(
    pageReadout(page.getByRole('navigation', { name: '分页导航', exact: true }))
  ).toHaveText(/^1\//)
  await expect(names(page)).toHaveText(['clip-folder', ...clips.slice(125, 224)])
  await expect(page.getByText('共 106 项 · 100 项 / 当前页', { exact: true })).toBeVisible()
  await page.getByLabel('搜索当前目录', { exact: true }).fill('clip22')
  await expect(names(page)).toHaveText(clips.slice(220))
  await expect(rows(page)).toHaveCount(10)
  await expect(rows(page).filter({ hasText: '未处理' })).toHaveCount(10)
  await select(page, '处理状态筛选', '已处理')
  await expect(rows(page)).toHaveCount(0)
  await page.getByLabel('搜索当前目录', { exact: true }).fill('')
  await select(page, '类型筛选', '图片')
  await expect(names(page)).toHaveText(['marked.png'])
  await select(page, '处理状态筛选', '未处理')
  await expect(names(page)).toHaveText(['clip-folder', 'pending.png'])
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('处理状态表头按状态分组及自然名称双向排序，进入目录后返回保留筛选和排序', async ({ page }) => {
  const setup = await fixture(
    page,
    '状态筛选-排序',
    ['a-z2.mp4', 'a-z10.mp4', 'a-m2.mp4', 'a-m10.mp4', 'raw2.mp4', 'raw10.mp4'],
    ['a-folder2', 'a-folder10']
  )
  await setup.mark(['a-m2.mp4', 'a-m10.mp4'])
  await setup.rename('raw2.mp4', 'a-a2.mp4')
  await setup.rename('raw10.mp4', 'a-a10.mp4')
  for (const name of ['a-folder2', 'a-folder10']) {
    const nested = defined((await setup.populateDirectory(name, ['raw.mp4']))[0])
    expect(
      (
        await page.request.post(`/api/entries/${nested.id}/rename`, {
          headers: { 'If-Match': nested.etag, 'Idempotency-Key': `nested-status-${nested.id}` },
          data: { name: 'manual.mp4' }
        })
      ).ok()
    ).toBe(true)
  }
  await setup.open()
  const sort = page.getByRole('button', { name: '切换处理状态排序', exact: true })
  const header = page.getByRole('columnheader').filter({ has: sort })
  await expect(header).toHaveAttribute('aria-sort', 'none')
  await sort.click()
  await expect(header).toHaveAttribute('aria-sort', 'ascending')
  await expect(names(page)).toHaveText([
    'a-folder2',
    'a-folder10',
    'a-z2.mp4',
    'a-z10.mp4',
    'a-m2.mp4',
    'a-m10.mp4',
    'a-a2.mp4',
    'a-a10.mp4'
  ])
  await sort.click()
  await expect(header).toHaveAttribute('aria-sort', 'descending')
  await expect(names(page)).toHaveText([
    'a-folder10',
    'a-folder2',
    'a-a10.mp4',
    'a-a2.mp4',
    'a-m10.mp4',
    'a-m2.mp4',
    'a-z10.mp4',
    'a-z2.mp4'
  ])
  await page.screenshot({ path: 'test-results/library-status-desktop.png' })
  await select(page, '处理状态筛选', '已处理')
  await expect(names(page)).toHaveText([
    'a-folder10',
    'a-folder2',
    'a-a10.mp4',
    'a-a2.mp4',
    'a-m10.mp4',
    'a-m2.mp4'
  ])
  await select(page, '处理状态筛选', '手动命名')
  await select(page, '类型筛选', '视频')
  await page.getByLabel('搜索当前目录', { exact: true }).fill('a-')
  await expect(names(page)).toHaveText(['a-folder10', 'a-folder2', 'a-a10.mp4', 'a-a2.mp4'])
  await page.getByRole('button', { name: 'a-folder10', exact: true }).click()
  await expect(page.getByRole('combobox', { name: '处理状态筛选', exact: true })).toHaveText(
    '全部状态'
  )
  await expect(page.getByRole('combobox', { name: '类型筛选', exact: true })).toHaveText('全部类型')
  await expect(page.getByLabel('搜索当前目录', { exact: true })).toHaveValue('')
  await page
    .getByRole('navigation', { name: '目录路径', exact: true })
    .getByRole('button', { name: '状态筛选-排序', exact: true })
    .click()
  await expect(page.getByRole('combobox', { name: '处理状态筛选', exact: true })).toHaveText(
    '手动命名'
  )
  await expect(page.getByRole('combobox', { name: '类型筛选', exact: true })).toHaveText('视频')
  await expect(page.getByLabel('搜索当前目录', { exact: true })).toHaveValue('a-')
  await expect(header).toHaveAttribute('aria-sort', 'descending')
  await expect(names(page)).toHaveText(['a-folder10', 'a-folder2', 'a-a10.mp4', 'a-a2.mp4'])

  await page.setViewportSize({ width: 390, height: 844 })
  const filter = page.getByRole('combobox', { name: '处理状态筛选', exact: true })
  await filter.focus()
  await filter.press('Enter')
  await filter.press('Home')
  await filter.press('ArrowDown')
  await filter.press('Enter')
  await expect(filter).toHaveText('未处理')
  await expect(names(page)).toHaveText(['a-z10.mp4', 'a-z2.mp4'])
  const filterBounds = await filter.boundingBox()
  expect(defined(filterBounds).x).toBeGreaterThanOrEqual(0)
  expect(defined(filterBounds).x + defined(filterBounds).width).toBeLessThanOrEqual(390)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: 'test-results/library-status-mobile.png' })
})

test('图片和音频一键编号预览遵循处理状态筛选，还原后的文件重新归入未处理', async ({ page }) => {
  const setup = await fixture(page, '状态筛选-编号', [
    'marked.png',
    'pending2.png',
    'pending10.png',
    'raw.png',
    'marked.mp3',
    'pending2.mp3',
    'pending10.mp3',
    'raw.mp3'
  ])
  await setup.mark(['marked.png', 'marked.mp3'])
  await setup.rename('raw.png', 'manual.png')
  await setup.rename('raw.mp3', 'manual.mp3')
  await setup.open()
  for (const [kind, extension, unit] of [
    ['图片', 'png', '张'],
    ['音频', 'mp3', '个']
  ] as const) {
    await select(page, '类型筛选', kind)
    await select(page, '处理状态筛选', '手动命名')
    await expect(names(page)).toHaveText([`manual.${extension}`])
    await page.getByRole('button', { name: '一键编号', exact: true }).click()
    let dialog = page.getByRole('dialog', { name: `${kind}编号预览`, exact: true })
    await expect(dialog).toContainText(`共 1 ${unit}${kind}`)
    await expect(bodyRows(dialog)).toHaveCount(1)
    await expect(bodyRows(dialog)).toContainText(`manual.${extension}`)
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await select(page, '处理状态筛选', '未处理')
    await expect(names(page)).toHaveText([`pending2.${extension}`, `pending10.${extension}`])
    await page.getByRole('button', { name: '一键编号', exact: true }).click()
    dialog = page.getByRole('dialog', { name: `${kind}编号预览`, exact: true })
    await expect(dialog).toContainText(`共 2 ${unit}${kind}`)
    await expect(bodyRows(dialog)).toHaveCount(2)
    await expect(bodyRows(dialog).first()).toContainText(`pending2.${extension}`)
    await expect(bodyRows(dialog).last()).toContainText(`pending10.${extension}`)
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
  }
  const restored = defined(setup.entries.get('manual.mp3'))
  expect(
    (
      await page.request.post(`/api/operations/${restored.restore_id}/restore`, {
        headers: { 'Idempotency-Key': `status-restore-${restored.id}` },
        data: {}
      })
    ).ok()
  ).toBe(true)
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(names(page)).toHaveText(['pending2.mp3', 'pending10.mp3', 'raw.mp3'])
  await select(page, '处理状态筛选', '已处理')
  await expect(names(page)).toHaveText(['marked.mp3'])
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('未处理筛选中的标记和手动改名立即移除行，并保留其他勾选', async ({ page }) => {
  const setup = await fixture(page, '状态筛选-变更', ['clip1.mp4', 'clip2.mp4', 'clip3.mp4'])
  await setup.open()
  await select(page, '处理状态筛选', '未处理')
  await page.getByRole('button', { name: '多选', exact: true }).click()
  await page.getByLabel('选择 clip2.mp4', { exact: true }).check()
  await page.getByRole('button', { name: '标记已处理 clip1.mp4', exact: true }).click()
  await page.getByRole('button', { name: '确认标记', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(names(page)).toHaveText(['clip2.mp4', 'clip3.mp4'])
  await expect(page.getByText('共 2 项 · 2 项 / 当前页', { exact: true })).toBeVisible()
  await expect(page.getByLabel('选择 clip2.mp4', { exact: true })).toBeChecked()
  await expect(page.getByRole('group', { name: '批量操作' }).getByText(/^已选 /)).toHaveText(
    '已选 1 项'
  )

  await page
    .getByRole('button', { name: '双击或按 Enter 重命名 clip2.mp4', exact: true })
    .dblclick()
  const editor = page.getByRole('textbox', { name: '编辑 clip2.mp4', exact: true })
  await editor.fill('renamed2')
  await editor.press('Enter')
  await expect(names(page)).toHaveText(['clip3.mp4'])
  await expect(page.getByText('共 1 项 · 1 项 / 当前页', { exact: true })).toBeVisible()
  await expect(page.getByRole('group', { name: '批量操作' }).getByText(/^已选 /)).toHaveText(
    '已选 1 项'
  )
  await select(page, '处理状态筛选', '全部状态')
  await expect(names(page)).toHaveText(['clip1.mp4', 'clip3.mp4', 'renamed2.mp4'])
  await expect(page.getByLabel('选择 renamed2.mp4', { exact: true })).toBeChecked()
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('未处理隐藏全部处理目录，递归标记后即时移除目录并保留其他选择', async ({ page }) => {
  const setup = await fixture(
    page,
    '目录状态真实筛选',
    [],
    ['已完成', '部分完成', '待处理', '待统计', '空目录']
  )
  const completed = await setup.populateDirectory('已完成', ['done.mp4'])
  const partial = await setup.populateDirectory('部分完成', ['done.mp4', 'pending.mp4'])
  await setup.populateDirectory('待处理', ['pending.mp4'])
  await setup.populateDirectory('空目录', [])
  const ids = [defined(completed[0]).id, defined(partial.find((e) => e.name === 'done.mp4')).id]
  expect(
    (
      await page.request.post('/api/entries/processed', {
        headers: { 'Idempotency-Key': `directory-status-seed-${setup.id}` },
        data: { targets: ids, marked: true }
      })
    ).ok()
  ).toBe(true)
  await expect
    .poll(async () => {
      const current = (
        await (await page.request.get(`/api/entries/status?ids=${ids.join(',')}`)).json()
      ).items
      return current.every((e: Entry) => e.processed === 'marked')
    })
    .toBe(true)
  await setup.open()
  await select(page, '处理状态筛选', '未处理')
  await expect(names(page)).toHaveText(['待处理', '待统计', '部分完成'])
  await expect(page.getByText('共 3 项 · 3 项 / 当前页', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '多选', exact: true }).click()
  await page.getByLabel('选择 待处理', { exact: true }).check()
  await page.getByRole('button', { name: '标记已处理 部分完成', exact: true }).click()
  await page.getByRole('button', { name: '确认标记', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(names(page)).toHaveText(['待处理', '待统计'])
  await expect(page.getByText('共 2 项 · 2 项 / 当前页', { exact: true })).toBeVisible()
  await expect(page.getByLabel('选择 待处理', { exact: true })).toBeChecked()
  await select(page, '处理状态筛选', '已处理')
  await expect(names(page)).toHaveText(['已完成', '部分完成'])
  await select(page, '处理状态筛选', '已标记')
  await expect(names(page)).toHaveText(['已完成', '部分完成'])
  await select(page, '处理状态筛选', '手动命名')
  await expect(rows(page)).toHaveCount(0)
  await expect(page.getByRole('heading', { name: '没有匹配的条目', exact: true })).toBeVisible()
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('保序更新不会保留改名后不匹配搜索或媒体类型的文件', async ({ page }) => {
  const setup = await fixture(page, '筛选保序-改名', ['clip.mp4', 'photo.png'])
  await setup.open()
  await page.getByLabel('搜索当前目录', { exact: true }).fill('clip')
  await expect(names(page)).toHaveText(['clip.mp4'])
  await page.getByRole('button', { name: '双击或按 Enter 重命名 clip.mp4', exact: true }).dblclick()
  const editor = page.getByRole('textbox', { name: '编辑 clip.mp4', exact: true })
  await editor.fill('renamed')
  await editor.press('Enter')
  await expect(rows(page)).toHaveCount(0)
  await expect(page.getByText('共 0 项 · 0 项 / 当前页', { exact: true })).toBeVisible()
  await page.getByLabel('搜索当前目录', { exact: true }).fill('')
  await select(page, '类型筛选', '视频')
  await expect(names(page)).toHaveText(['renamed.mp4'])
  await page
    .getByRole('button', { name: '双击或按 Enter 重命名 renamed.mp4', exact: true })
    .dblclick()
  await page.getByRole('button', { name: '修改后缀名', exact: true }).click()
  const fullName = page.getByRole('textbox', { name: '编辑 renamed.mp4', exact: true })
  await fullName.fill('renamed.jpg')
  await fullName.press('Enter')
  await expect(rows(page)).toHaveCount(0)
  await expect(page.getByText('共 0 项 · 0 项 / 当前页', { exact: true })).toBeVisible()
  await select(page, '类型筛选', '图片')
  await expect(names(page)).toHaveText(['photo.png', 'renamed.jpg'])
  await expect(page.getByRole('alert')).toHaveCount(0)
})

for (const width of [1440, 390]) {
  test(`目录处理状态排序按显示状态分组（${width}px）`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 })
    const setup = await fixture(page, `目录状态排序-${width}`, [], ['dir1', 'dir2', 'dir3', 'dir4'])
    const targets: number[] = []
    for (const name of ['dir1', 'dir2', 'dir3', 'dir4']) {
      const files = await setup.populateDirectory(name, ['a.mp4', 'b.mp4'])
      targets.push(
        ...files
          .filter((e) => name === 'dir1' || name === 'dir3' || e.name === 'a.mp4')
          .map((e) => e.id)
      )
    }
    const result = await page.request.post('/api/entries/processed', {
      headers: { 'Idempotency-Key': `sort-directories-${setup.id}` },
      data: { targets, marked: true }
    })
    expect(result.ok()).toBe(true)
    await expect
      .poll(async () => {
        const current = (
          await (await page.request.get(`/api/entries/status?ids=${targets.join(',')}`)).json()
        ).items
        return current.every((e: Entry) => e.processed === 'marked')
      })
      .toBe(true)
    await setup.open()
    const sort = page.getByRole('button', { name: '切换处理状态排序', exact: true })
    await sort.click()
    await expect(names(page)).toHaveText(['dir2', 'dir4', 'dir1', 'dir3'])
    await expect(rows(page).nth(0)).toContainText('部分已处理')
    await expect(rows(page).nth(2)).toContainText('全部已处理')
    await sort.click()
    await expect(names(page)).toHaveText(['dir3', 'dir1', 'dir4', 'dir2'])
    await expect(rows(page).nth(0)).toContainText('全部已处理')
    await expect(rows(page).nth(2)).toContainText('部分已处理')
  })
}
