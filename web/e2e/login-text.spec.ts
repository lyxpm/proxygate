import { expect, test } from '@playwright/test'

test('login page omits initial password instructions', async ({ page }) => {
  await page.goto('/login')
  await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible()
  await expect(page.getByText('initial-admin-password.txt')).toHaveCount(0)
  await expect(page.getByText('PG_ADMIN_PASSWORD')).toHaveCount(0)
})
