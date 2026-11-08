import { expect, test, type Page } from '@playwright/test'

const username = process.env.PG_E2E_USER || 'admin'
const password = process.env.PG_E2E_PASSWORD || 'test123456'

async function login(page: Page) {
  await page.goto('/login')
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码').fill(password)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).toHaveURL(/\/dashboard/, { timeout: 15_000 })
}

test.describe('ECharts 6 charts regression', () => {
  test('dashboard and statistics charts render', async ({ page }) => {
    const errors: string[] = []
    page.on('pageerror', (err) => errors.push(err.message))
    await login(page)

    await page.goto('/dashboard')
    await expect(page.getByText('请求趋势').first()).toBeVisible()
    await expect(page.getByText('流量趋势').first()).toBeVisible()
    await expect(page.getByText('HTTP 状态码分布').first()).toBeVisible()
    await expect(page.locator('.chart canvas').first()).toBeVisible({ timeout: 15_000 })
    const dashboardCharts = await page.locator('.chart canvas').count()
    expect(dashboardCharts).toBeGreaterThanOrEqual(3)

    await page.getByRole('menuitem', { name: '流量分析' }).click()
    await expect(page).toHaveURL(/\/statistics/)
    await expect(page.getByText('请求与流量趋势')).toBeVisible()
    await expect(page.locator('.chart canvas').first()).toBeVisible({ timeout: 15_000 })
    expect(await page.locator('.chart canvas').count()).toBeGreaterThanOrEqual(1)

    const metricSwitch = page.locator('.metric-switch')
    await metricSwitch.getByText('流量', { exact: true }).click()
    await expect(page.locator('.chart canvas').first()).toBeVisible()
    await metricSwitch.getByText('响应时间', { exact: true }).click()
    await expect(page.locator('.chart canvas').first()).toBeVisible()

    expect(errors).toEqual([])
  })
})
