import AxeBuilder from '@axe-core/playwright'
import { test, expect, type Page } from '@playwright/test'

async function expectNoSeriousViolations(page: Page, where: string) {
  const audit = await new AxeBuilder({ page }).analyze()
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

/** Waits until no region is busy and every finite CSS animation has finished. */
async function settle(page: Page) {
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0)
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((animation) => animation.effect?.getComputedTiming().endTime !== Infinity)
        .map((animation) => animation.finished)
    )
  )
}

const pages = [
  'library',
  'history',
  'extractions',
  'plans',
  'runs',
  'usage/tokens',
  'usage/analysis',
  ...['processing', 'scan', 'numbering', 'prompts', 'models', 'sampling', 'notifications'].map(
    (tab) => `settings/${tab}`
  )
]

test('登录页与各工作台页面没有严重无障碍问题', async ({ page }) => {
  // Audit settled pages, not colors halfway through a page or tab transition.
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('/')
  await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
  await expectNoSeriousViolations(page, 'login')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  for (const path of pages) {
    await page.goto(`/#/${path}`)
    await expect(page.locator('main h1, .workspace h1').first()).toBeVisible()
    await page.waitForLoadState('networkidle')
    await settle(page)
    await expectNoSeriousViolations(page, path)
  }
})
