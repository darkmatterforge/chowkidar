import { Page } from '@playwright/test'
import { exec } from 'child_process'
import { promisify } from 'util'

const execAsync = promisify(exec)

export const BASE_URL     = process.env.BASE_URL           ?? 'http://localhost:8080'
export const CONTAINER    = process.env.E2E_CONTAINER_NAME ?? 'chowkidar-e2e'
export const E2E_USER     = process.env.E2E_USERNAME        ?? 'testadmin'
export const E2E_PASS     = process.env.E2E_PASSWORD        ?? 'TestPassword1!'

/**
 * Restart the app container and re-authenticate via the login form.
 * Container restart drops all in-memory sessions — this helper waits for the
 * app to come back healthy and then logs in so subsequent page/request calls
 * use a valid session.
 */
export async function restartAndReAuth(page: Page): Promise<void> {
  await execAsync(`docker restart ${CONTAINER}`)

  // Wait for health endpoint (no auth required)
  const deadline = Date.now() + 45_000
  while (Date.now() < deadline) {
    try {
      const h = await page.request.get(`${BASE_URL}/api/health`, { timeout: 2_000 })
      if (h.ok()) break
    } catch { /* still starting */ }
    await page.waitForTimeout(1_000)
  }

  // Re-login via the form so the browser context gets a fresh session cookie
  await page.goto(`${BASE_URL}/`)
  await page.waitForSelector('#loginPage, #dashboardPage', { timeout: 15_000 })
  const needsLogin = await page.locator('#loginPage').isVisible()
  if (needsLogin) {
    await page.locator('#loginUsername').fill(E2E_USER)
    await page.locator('#loginPassword').fill(E2E_PASS)
    await page.locator('#loginBtn').click()
    await page.waitForSelector('#dashboardPage', { timeout: 10_000 })
  }
}
