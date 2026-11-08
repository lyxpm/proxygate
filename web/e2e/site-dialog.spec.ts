import { expect, test } from '@playwright/test'

test('site dialog edit save', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('用户名').fill('admin')
  await page.getByLabel('密码').fill('test123456')
  await page.getByRole('button', { name: '登录' }).click()
  await page.waitForURL(/\/dashboard/)
  await page.getByRole('menuitem', { name: '站点管理' }).click()
  await page.getByRole('button', { name: '编辑' }).first().click()
  const dialog = page.locator('.site-editor-dialog')
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: '保存并应用' }).click()
  await expect(dialog).toBeHidden({ timeout: 10000 })
})
