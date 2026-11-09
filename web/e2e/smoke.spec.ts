import { expect, test, type Page } from '@playwright/test'

const username = process.env.PG_E2E_USER || 'admin'
const password = process.env.PG_E2E_PASSWORD || ''

async function login(page: Page) {
  await page.goto('/login')
  await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible()
  // The TOTP field is hidden unless the account has 2FA enabled.
  await expect(page.getByLabel('动态验证码')).toHaveCount(0)
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码').fill(password)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).toHaveURL(/\/dashboard/, { timeout: 15_000 })
}

test.describe('management UI smoke', () => {
  test('login, dashboard, core navigation', async ({ page }) => {
    await login(page)
    await expect(page.getByText('站点管理', { exact: true }).first()).toBeVisible()

    await page.getByRole('menuitem', { name: '站点管理' }).click()
    await expect(page).toHaveURL(/\/sites/)
    await expect(page.locator('.header-title')).toHaveText('站点管理')

    await page.getByRole('menuitem', { name: '应用服务' }).click()
    await expect(page).toHaveURL(/\/applications/)

    await page.getByRole('menuitem', { name: 'SSL 证书' }).click()
    await expect(page).toHaveURL(/\/certificates/)
    await expect(page.locator('.header-title')).toHaveText('SSL 证书')

    await page.getByRole('menuitem', { name: '业务拓扑' }).click()
    await expect(page).toHaveURL(/\/topology/)
    await expect(page.locator('.topo-card')).toBeVisible()

    await page.getByRole('menuitem', { name: '系统设置' }).click()
    await expect(page).toHaveURL(/\/settings/)
    await page.getByText('安全策略', { exact: true }).click()
    await expect(page.getByText('两步验证（TOTP）')).toBeVisible()
    await expect(page.getByText('安全与登录策略')).toBeVisible()
    await expect(page.getByText('登录保护')).toBeVisible()
    await expect(page.getByText('空闲超时（分钟）')).toBeVisible()

    await page.locator('.header-right .el-dropdown').click()
    await page.getByText('修改密码', { exact: true }).click()
    await expect(page.getByRole('dialog')).toContainText('当前密码')
    await page.getByRole('button', { name: '取消', exact: true }).click()
  })

  test('certificate detail and download actions exist', async ({ page }) => {
    await login(page)
    await page.getByRole('menuitem', { name: 'SSL 证书' }).click()
    await expect(page).toHaveURL(/\/certificates/)
    const view = page.getByRole('button', { name: '查看' }).first()
    if (await view.count()) {
      await view.click()
      await expect(page.getByText('证书 PEM（含证书链）')).toBeVisible()
      await expect(page.getByRole('button', { name: '下载 PEM' })).toBeVisible()
      await expect(page.getByRole('button', { name: '导出证书+私钥' })).toBeVisible()
      await page.getByRole('button', { name: '关闭', exact: true }).click()
    }
  })
})
