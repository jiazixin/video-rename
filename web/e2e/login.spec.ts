import { expect, test } from '@playwright/test'

test('登录失败只在表单内提示一次', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('wrong-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('alert')).toHaveText('账号或密码错误')
  // The form owns this message; a toast repeating it would be announced twice.
  await expect(page.getByText('账号或密码错误')).toHaveCount(1)
  await expect(page.getByRole('heading', { name: '登录工作台' })).toBeVisible()
})
