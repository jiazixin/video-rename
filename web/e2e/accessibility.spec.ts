import AxeBuilder from '@axe-core/playwright'
import { test, expect, type Page } from '@playwright/test'
import { settleAnimations } from './support'

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
  await settleAnimations(page)
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
    await page.goto(`/${path}`)
    await expect(page.getByRole('heading', { level: 1 }).first()).toBeVisible()
    await page.waitForLoadState('networkidle')
    await settle(page)
    await expectNoSeriousViolations(page, path)
  }
})

// WCAG 2.5.8: pointer targets measure at least 24×24 CSS px. Checkboxes and switch inputs are
// left out: their caption text belongs to the same target (see DESIGN.md, 点击范围).
const smallTargets = (minimum: number) =>
  [...document.querySelectorAll('button, a[href], summary, [role="tab"], [role="option"]')]
    .map((element) => ({ element, box: element.getBoundingClientRect() }))
    .filter(({ box }) => box.width > 0 && box.height > 0)
    .filter(({ box }) => box.width < minimum - 0.5 || box.height < minimum - 0.5)
    .map(({ element, box }) => {
      const name = (element.getAttribute('aria-label') ?? element.textContent).trim()
      return `${Math.round(box.width)}×${Math.round(box.height)} ${element.tagName} ${name.slice(0, 24)}`
    })

for (const viewport of [
  { width: 1440, height: 1000 },
  { width: 390, height: 844 }
])
  test(`可点击元素不小于 24×24（${viewport.width}px）`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.goto('/')
    await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    expect(await page.evaluate(smallTargets, 24), 'login').toEqual([])
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('密码', { exact: true }).fill('test-password-123')
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
    for (const path of pages) {
      await page.goto(`/${path}`)
      await expect(page.getByRole('heading', { level: 1 }).first()).toBeVisible()
      await page.waitForLoadState('networkidle')
      await settle(page)
      expect(await page.evaluate(smallTargets, 24), path).toEqual([])
    }
  })
