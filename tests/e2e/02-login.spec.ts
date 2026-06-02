import { test, expect, BrowserContext } from '@playwright/test'
import { AUTH_STATE, TEST_USER, TEST_PASS } from './playwright.config'

async function goToLogin(page: ReturnType<BrowserContext['newPage'] extends (...args: unknown[]) => infer R ? () => R : never>) {
  await page.goto('/')
  await expect(page.locator('#loginPanel')).toBeVisible()
}

test.describe.serial('Login / Session', () => {
  test('login with valid credentials lands on dashboard', async ({ page }) => {
    await goToLogin(page)
    await page.locator('#loginUsername').fill(TEST_USER)
    await page.locator('#loginPassword').fill(TEST_PASS)
    await page.locator('#loginBtn').click()
    await expect(page.locator('#dashboardPage')).toBeVisible()
    await expect(page.locator('#loginPage')).toBeHidden()
  })

  test('login with wrong password shows error', async ({ page }) => {
    await goToLogin(page)
    await page.locator('#loginUsername').fill(TEST_USER)
    await page.locator('#loginPassword').fill('WrongPassword!')
    await page.locator('#loginBtn').click()
    await expect(page.locator('#loginError')).toBeVisible()
    await expect(page.locator('#loginError')).not.toBeEmpty()
    await expect(page.locator('#loginPage')).toBeVisible()
  })

  test('login with unknown username shows error', async ({ page }) => {
    await goToLogin(page)
    await page.locator('#loginUsername').fill('nobody')
    await page.locator('#loginPassword').fill(TEST_PASS)
    await page.locator('#loginBtn').click()
    await expect(page.locator('#loginError')).toBeVisible()
    await expect(page.locator('#loginPage')).toBeVisible()
  })

  test('login with empty fields shows validation error', async ({ page }) => {
    await goToLogin(page)
    await page.locator('#loginBtn').click()
    await expect(page.locator('#loginError')).toBeVisible()
    await expect(page.locator('#loginError')).toContainText('Username and password required')
  })

  test('Enter key in password field submits the form', async ({ page }) => {
    await goToLogin(page)
    await page.locator('#loginUsername').fill(TEST_USER)
    await page.locator('#loginPassword').fill(TEST_PASS)
    await page.locator('#loginPassword').press('Enter')
    await expect(page.locator('#dashboardPage')).toBeVisible()
  })

  test('logout redirects to login page', async ({ page }) => {
    await goToLogin(page)
    await page.locator('#loginUsername').fill(TEST_USER)
    await page.locator('#loginPassword').fill(TEST_PASS)
    await page.locator('#loginBtn').click()
    await expect(page.locator('#dashboardPage')).toBeVisible()

    // Logout via API (no dedicated logout button visible in nav — uses JS doLogout())
    await page.evaluate(() => (window as unknown as { doLogout: () => void }).doLogout())
    await expect(page.locator('#loginPage')).toBeVisible()
    await expect(page.locator('#loginPanel')).toBeVisible()
  })

  test('accessing app while logged out shows login page', async ({ page, context }) => {
    // Start fresh (no session)
    await context.clearCookies()
    await page.goto('/')
    await expect(page.locator('#loginPage')).toBeVisible()
  })

  // Must run last — saves session state consumed by all subsequent test files
  test('save authenticated session state', async ({ page, context }) => {
    await goToLogin(page)
    await page.locator('#loginUsername').fill(TEST_USER)
    await page.locator('#loginPassword').fill(TEST_PASS)
    await page.locator('#loginRemember').check()

    // Wait for the login API response first, then confirm the overlay dismisses.
    // (#dashboardPage is always display:block so it can't be used as a login gate)
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/auth/login')),
      page.locator('#loginBtn').click(),
    ])
    expect(res.status()).toBe(200)
    await expect(page.locator('#loginPage')).toBeHidden()
    await context.storageState({ path: AUTH_STATE })
  })
})
