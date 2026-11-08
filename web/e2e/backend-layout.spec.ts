import { expect, test } from '@playwright/test'

test('backend connection switches are 2x2', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('用户名').fill('admin')
  await page.getByLabel('密码').fill(process.env.PG_E2E_PASSWORD || '')
  await page.getByRole('button', { name: '登录' }).click()
  await page.waitForURL(/dashboard/, { timeout: 15000 })
  await page.getByRole('menuitem', { name: '站点管理' }).click()
  await page.getByRole('button', { name: '编辑' }).first().click()
  await expect(page.getByRole('dialog')).toBeVisible()
  const advanced = page.getByText('高级配置', { exact: true })
  if (await advanced.count()) await advanced.click()
  const group = page.locator('.advanced-group', { hasText: '后端连接' })
  await expect(group.getByText('校验后端 TLS')).toBeVisible()
  await expect(group.getByText('保持长连接')).toBeVisible()
  await expect(group.getByText('使用后端 Host 头')).toBeVisible()
  await expect(group.getByText('传递真实访问 IP')).toBeVisible()
  const labels = group.locator('.el-checkbox')
  await expect(labels).toHaveCount(4)
  const boxes = []
  for (let i = 0; i < 4; i++) {
    const box = await labels.nth(i).boundingBox()
    if (!box) throw new Error('missing checkbox box')
    boxes.push(box)
  }
  // First row: TLS + keepalive; second row: host + XFF.
  expect(Math.abs(boxes[0].y - boxes[1].y)).toBeLessThan(4)
  expect(boxes[2].y).toBeGreaterThan(boxes[0].y)
  expect(Math.abs(boxes[2].y - boxes[3].y)).toBeLessThan(4)
  await page.getByRole('button', { name: '取消', exact: true }).click()
})
