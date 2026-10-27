import { test, expect } from '@playwright/test'
import { bodyRows } from './support'

test('文件名双击进入行内编辑，保存与取消保留扩展名', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '文件目录', exact: true }).click()

  const filename = page.getByRole('button', { name: '双击或按 Enter 重命名 VID_20260920_001.mp4' })
  await filename.click()
  await expect(page.getByRole('textbox', { name: '编辑 VID_20260920_001.mp4' })).toHaveCount(0)
  await filename.dblclick()
  const editor = page.getByRole('textbox', { name: '编辑 VID_20260920_001.mp4' })
  await expect(editor).toBeFocused()
  await expect(editor).toHaveValue('VID_20260920_001')
  await expect(page.getByRole('button', { name: '修改后缀名', exact: true })).toHaveText('.mp4')
  await page.screenshot({ path: 'test-results/inline-rename.png' })
  await editor.fill('快速重命名测试')
  await editor.press('Enter')
  const renamed = page.getByRole('button', { name: '双击或按 Enter 重命名 快速重命名测试.mp4' })
  await expect(renamed).toBeVisible()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  await renamed.focus()
  await renamed.press('Enter')
  const again = page.getByRole('textbox', { name: '编辑 快速重命名测试.mp4' })
  await again.fill('不会保存')
  await again.press('Escape')
  await expect(renamed).toBeVisible()
  await expect(
    page.getByRole('button', { name: '双击或按 Enter 重命名 不会保存.mp4' })
  ).toHaveCount(0)

  await renamed.dblclick()
  await again.fill('已占用名称')
  await again.press('Enter')
  await expect(bodyRows(page).getByRole('alert')).toBeVisible()
  await again.press('Escape')
  await expect(renamed).toBeVisible()

  // Restore the shared fixture name: renaming again would leave new rename history,
  // while restoring returns to the original state for later VID_20260920_001.mp4 assertions.
  await page.getByRole('button', { name: '还原 快速重命名测试.mp4', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('VID_20260920_001.mp4')
  await page.getByRole('button', { name: '确认还原', exact: true }).click()
  await expect(filename).toBeVisible()
})

test('点击后缀进入编辑，支持取消、冲突、保存及还原', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '文件目录', exact: true }).click()
  const filename = page.getByRole('button', { name: '双击或按 Enter 重命名 VID_20260920_001.mp4' })
  await filename.dblclick()
  const editor = page.getByRole('textbox', { name: '编辑 VID_20260920_001.mp4' })
  await page.getByRole('button', { name: '修改后缀名', exact: true }).click()
  await expect(editor).toBeFocused()
  await expect(editor).toHaveValue('VID_20260920_001.mp4')
  expect(
    await editor.evaluate((e) =>
      (e as HTMLInputElement).value.slice(
        (e as HTMLInputElement).selectionStart ?? -1,
        (e as HTMLInputElement).selectionEnd ?? -1
      )
    )
  ).toBe('mp4')
  await editor.fill('VID_20260920_001.mkv')
  await editor.press('Escape')
  await expect(filename).toBeVisible()
  await filename.dblclick()
  await editor.press('Tab')
  await expect(page.getByRole('button', { name: '修改后缀名', exact: true })).toBeFocused()
  await page.getByRole('button', { name: '修改后缀名', exact: true }).press('Enter')
  await editor.fill('已占用名称.mp4')
  await editor.press('Enter')
  await expect(bodyRows(page).getByRole('alert')).toBeVisible()
  await editor.fill('VID_20260920_001.MKV')
  await page.screenshot({ path: 'test-results/edit-extension.png' })
  await editor.press('Enter')
  await expect(
    page.getByRole('button', { name: '双击或按 Enter 重命名 VID_20260920_001.mkv' })
  ).toBeVisible()
  await page.getByRole('button', { name: '还原 VID_20260920_001.mkv', exact: true }).click()
  await page.getByRole('button', { name: '确认还原', exact: true }).click()
  await expect(filename).toBeVisible()
})

test('手动改名时 JPEG 统一为 jpg，并可还原原始后缀', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '文件目录', exact: true }).click()
  await page.getByRole('button', { name: '后缀编辑测试', exact: true }).click()
  const filename = page.getByRole('button', { name: '双击或按 Enter 重命名 photo.JPEG' })
  await filename.dblclick()
  await expect(page.getByRole('button', { name: '修改后缀名', exact: true })).toHaveText('.jpg')
  await page.getByRole('textbox', { name: '编辑 photo.JPEG' }).press('Enter')
  await expect(page.getByRole('button', { name: '双击或按 Enter 重命名 photo.jpg' })).toBeVisible()
  await page.getByRole('button', { name: '还原 photo.jpg', exact: true }).click()
  await page.getByRole('button', { name: '确认还原', exact: true }).click()
  await expect(filename).toBeVisible()
})
