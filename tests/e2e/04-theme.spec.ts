import { test, expect } from '@playwright/test'
import { gotoSettings } from './helpers/nav'

type Page = Parameters<Parameters<typeof test>[1]>[0]['page']

function getTheme(page: Page) {
  return page.evaluate(() => document.documentElement.getAttribute('data-theme'))
}

async function cycleToTheme(page: Page, target: string | null) {
  const btn = page.locator('#themeToggleBtn')
  for (let i = 0; i < 4; i++) {
    if (await getTheme(page) === target) return
    await btn.click()
  }
}

async function saveTheme(page: Page) {
  // Theme is only persisted across page reloads when saved to the server.
  // loadSettings() overwrites localStorage with the server-side theme on every load.
  await page.locator('[data-page="settings"]').click()
  const [res] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/settings') && r.request().method() !== 'GET'),
    page.locator('#saveSettingsBtn').click(),
  ])
  expect(res.status()).toBeLessThan(500)
}

test.describe('Theme', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await expect(page.locator('#dashboardPage')).toBeVisible()
  })

  test('theme toggle button is visible in the nav bar', async ({ page }) => {
    await expect(page.locator('#themeToggleBtn')).toBeVisible()
  })

  test('cycles auto → light → dark on repeated clicks', async ({ page }) => {
    const states: (string | null)[] = []
    for (let i = 0; i < 3; i++) {
      await page.locator('#themeToggleBtn').click()
      states.push(await getTheme(page))
    }
    // Cycle order is auto → light → dark; all 3 states must be visited
    const set = new Set(states.map(s => s ?? 'auto'))
    expect(set.size).toBe(3)
    expect(set).toContain('dark')
  })

  test('dark theme applies data-theme="dark" to <html>', async ({ page }) => {
    await cycleToTheme(page, 'dark')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  })

  test('light theme applies data-theme="light" to <html>', async ({ page }) => {
    await cycleToTheme(page, 'light')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  })

  test('selected theme persists after page reload', async ({ page }) => {
    await cycleToTheme(page, 'dark')
    // Save to server — loadSettings() overwrites localStorage from server settings on
    // every load, so the toggle alone does not survive a full reload.
    await saveTheme(page)

    await page.reload()
    await expect(page.locator('#themeToggleBtn')).toBeVisible()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')

    // Restore to auto so server-side theme is clean for subsequent test files
    await cycleToTheme(page, null)  // null = auto (no data-theme attribute)
    await saveTheme(page)
  })
})
