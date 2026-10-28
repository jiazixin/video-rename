import { expect, test } from '@playwright/test'
import { toasts } from './support'

for (const width of [390, 1440]) {
  test(`字段错误逐项关联、焦点和修正重试（${width}px）`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 })
    await page.request.post('/api/auth/login', {
      data: { username: 'admin', password: 'test-password-123' }
    })
    let reject = true
    await page.route('**/api/sampling', async (route) => {
      if (route.request().method() !== 'PUT') {
        await route.continue()
        return
      }
      if (!reject) {
        // This UI regression owns its draft; leave the shared server settings unchanged.
        const body = route.request().postData()
        if (!body) throw new Error('missing sampling draft')
        await route.fulfill({ contentType: 'application/json', body })
        return
      }
      await route.fulfill({
        status: 400,
        headers: { 'X-Request-ID': 'field-validation-test' },
        json: {
          error: {
            code: 'invalid_argument',
            message: 'private database detail',
            details: {
              fields: [
                { path: 'sampling.max_frames', reason: 'out_of_range' },
                { path: 'images.jpeg_quality', reason: 'out_of_range' }
              ]
            }
          }
        }
      })
    })
    await page.goto('/settings/sampling')
    const frames = page.getByRole('spinbutton', { name: '每个视频最大帧数', exact: true })
    const quality = page.getByRole('spinbutton', { name: 'JPEG 质量档位', exact: true })
    const savedFrames = Number(await frames.inputValue())
    await frames.fill(savedFrames === 16 ? '17' : '16')
    await page.getByRole('button', { name: '保存抽帧设置', exact: true }).click()
    await expect(frames).toHaveAttribute('aria-invalid', 'true')
    await expect(quality).toHaveAttribute('aria-invalid', 'true')
    await expect(frames).toBeFocused()
    await expect(frames).toHaveAccessibleDescription(/1.*32/)
    await expect(quality).toHaveAccessibleDescription(/2.*15/)
    await expect(toasts(page).first()).toContainText('1–32')
    await expect(page.getByText('private database detail')).toHaveCount(0)
    await page.screenshot({ path: `test-results/form-errors-${width}.png`, fullPage: true })
    // Retrying must keep a changed value, rather than reverting to the saved draft.
    await frames.fill(savedFrames === 12 ? '13' : '12')
    await expect(frames).not.toHaveAttribute('aria-invalid')
    reject = false
    await page.getByRole('button', { name: '保存抽帧设置', exact: true }).click()
    await expect(toasts(page).first()).toContainText('设置已保存')
    await expect(quality).not.toHaveAttribute('aria-invalid')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}
