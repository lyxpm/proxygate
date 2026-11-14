import { expect, test, type Page } from '@playwright/test'

const username = process.env.PG_E2E_USER || 'admin'
const password = process.env.PG_E2E_PASSWORD || ''

async function login(page: Page) {
  await page.goto('/login')
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码').fill(password)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).toHaveURL(/\/dashboard/, { timeout: 15_000 })
}

test.describe('analytics states and interactions', () => {
  test('metric switch, crosshair tooltip and response percentiles', async ({ page }) => {
    await login(page)
    await page.getByRole('menuitem', { name: '流量分析' }).click()
    await expect(page.locator('.metric-switch-button.active')).toContainText('请求数')

    const canvas = page.locator('.chart canvas').first()
    await expect(canvas).toBeVisible({ timeout: 15_000 })
    const box = await canvas.boundingBox()
    expect(box).not.toBeNull()
    if (box) {
      await canvas.hover({ position: { x: Math.max(20, box.width * 0.55), y: Math.max(20, box.height * 0.45) } })
      await page.waitForTimeout(500)
    }
    await expect(page.locator('.chart-tooltip')).toBeVisible({ timeout: 5_000 })
    await expect(page.locator('.chart-tooltip')).toContainText('请求数')
    await expect(page.locator('.chart-tooltip')).toContainText('入站流量')
    await expect(page.locator('.chart-tooltip')).toContainText('出站流量')

    await page.locator('.metric-switch-button').filter({ hasText: '响应时间' }).click()
    await expect(page.locator('.metric-switch-button.active')).toContainText('响应时间')
    await expect(page.locator('.chart canvas').first()).toBeVisible()
    await expect(page.getByText('P95').first()).toBeVisible()
  })

  test('dashboard ranking navigates to filtered analysis', async ({ page }) => {
    await login(page)
    await expect(page.locator('.site-ranking .el-table__row').first()).toBeVisible({ timeout: 15_000 })
    await page.locator('.site-ranking .el-table__row').first().click()
    await expect(page).toHaveURL(/\/statistics\?site_code=[A-Z]+-[A-Z0-9]+/)
    await expect(page.getByText('站点流量明细').first()).toBeVisible()
  })

  test('empty analytics range shows empty state', async ({ page }) => {
    await login(page)
    await page.route('**/api/v1/statistics/overview?range=24h', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 0,
          message: 'ok',
          data: {
            totals: {
              requests: 0, inbound_bytes: 0, outbound_bytes: 0,
              status_2xx: 0, status_3xx: 0, status_4xx: 0, status_5xx: 0,
              avg_ms: 0, max_ms: 0, p95_ms: 0, p99_ms: 0,
            },
            series: [],
          },
        }),
      })
    })
    await page.goto('/statistics')
    await expect(page.locator('.chart-empty')).toBeVisible({ timeout: 10_000 })
  })

  test('analytics request failure keeps an error state', async ({ page }) => {
    await login(page)
    await page.route('**/api/v1/statistics/overview?range=24h', async (route) => {
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ code: 500, message: 'temporary failure' }),
      })
    })
    await page.goto('/statistics')
    await expect(page.locator('.chart-error')).toBeVisible({ timeout: 10_000 })
  })
})
