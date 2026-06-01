import { test, expect } from '@playwright/test'

function getTheme(page: ReturnType<typeof test.info> extends never ? never : Parameters<Parameters<typeof test>[1]>[0]['page']) {
  return page.evaluate(() => document.documentElement.getAttribute('data-theme'))
}

test.describe('Theme', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await expect(page.locator('#dashboardPage')).toBeVisible()
  })

  test('theme toggle button is visible in the nav bar', async ({ page }) => {
    await expect(page.locator('#themeToggleBtn')).toBeVisible()
  })

  test('cycles light → dark → auto on repeated clicks', async ({ page }) => {
    const btn = page.locator('#themeToggleBtn')

    // Click until we reach a known starting point (light)
    // The app persists theme; we normalise by cycling through all three states
    // and asserting the full rotation.
    const states: (string | null)[] = []
    for (let i = 0; i < 3; i++) {
      await btn.click()
      states.push(await getTheme(page))
    }

    // After 3 clicks the theme must have visited dark, auto, and light
    // (cycle order: light → dark → auto → light)
    const set = new Set(states.map(s => s ?? 'auto'))
    expect(set.size).toBe(3)
    expect(set).toContain('dark')
  })

  test('dark theme applies data-theme="dark" to <html>', async ({ page }) => {
    const btn = page.locator('#themeToggleBtn')

    // Cycle until we hit dark
    for (let i = 0; i < 3; i++) {
      await btn.click()
      const theme = await getTheme(page)
      if (theme === 'dark') break
    }

    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  })

  test('light theme applies data-theme="light" to <html>', async ({ page }) => {
    const btn = page.locator('#themeToggleBtn')

    for (let i = 0; i < 3; i++) {
      await btn.click()
      const theme = await getTheme(page)
      if (theme === 'light') break
    }

    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  })

  test('selected theme persists after page reload', async ({ page }) => {
    const btn = page.locator('#themeToggleBtn')

    // Cycle to dark
    for (let i = 0; i < 3; i++) {
      await btn.click()
      if (await getTheme(page) === 'dark') break
    }

    await page.reload()
    await expect(page.locator('#dashboardPage')).toBeVisible()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  })
})
