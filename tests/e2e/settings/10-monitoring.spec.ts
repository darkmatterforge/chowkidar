import { test, expect, type Page } from '@playwright/test'
import { gotoSettings } from '../helpers/nav'

const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

async function openMonitoringTab(page: Page) {
  await gotoSettings(page, 'monitoring')
}

test.describe('Settings — Monitoring tab', () => {
  test('monitoring tab renders all key inputs', async ({ page }) => {
    await openMonitoringTab(page)
    await expect(page.locator('#dockerPingTimeoutSeconds')).toBeVisible()
    await expect(page.locator('#httpClientTimeoutSeconds')).toBeVisible()
  })

  test('docker ping timeout change saves and persists', async ({ page }) => {
    await openMonitoringTab(page)

    const input = page.locator('#dockerPingTimeoutSeconds')
    const original = await input.inputValue()

    await input.fill('10')
    await page.locator('#saveSettingsBtn').click()
    await expect(page.locator('#settingsStatus')).toBeVisible()

    await gotoSettings(page, 'monitoring')
    await expect(page.locator('#dockerPingTimeoutSeconds')).toHaveValue('10')

    // Restore original value
    await page.locator('#dockerPingTimeoutSeconds').fill(original)
    await page.locator('#saveSettingsBtn').click()
  })

  test('saving from General tab preserves workerCount and dockerPingTimeout', async ({ page }) => {
    const before = await page.request.get(`${BASE_URL}/api/settings`)
    const beforeJson = await before.json()
    const workersBefore = beforeJson.workerCount
    const pingBefore = beforeJson.dockerPingTimeoutSeconds

    await gotoSettings(page)
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/settings') && r.request().method() !== 'GET'),
      page.locator('#saveSettingsBtn').click(),
    ])
    expect(res.status()).toBeLessThan(500)

    const after = await page.request.get(`${BASE_URL}/api/settings`)
    const afterJson = await after.json()
    expect(afterJson.workerCount).toBe(workersBefore)
    expect(afterJson.dockerPingTimeoutSeconds).toBe(pingBefore)
    expect(afterJson.workerCount).toBeGreaterThan(0)
  })
})

// ── Default values ────────────────────────────────────────────────────────────

test.describe('Settings — default values', () => {
  test('all critical settings have sensible non-zero defaults', async ({ page }) => {
    const res = await page.request.get(`${BASE_URL}/api/settings`)
    const s = await res.json()

    // These must never be zero — zero would break monitoring or notifications.
    expect(s.actionTimeoutSeconds).toBeGreaterThanOrEqual(1)
    expect(s.workerCount).toBeGreaterThanOrEqual(1)
    expect(s.queueSize).toBeGreaterThanOrEqual(1)
    expect(s.dockerPingTimeoutSeconds).toBeGreaterThanOrEqual(1)
    expect(s.logRetentionDays).toBeGreaterThanOrEqual(1)
    expect(s.httpClientTimeoutSeconds).toBeGreaterThanOrEqual(1)
  })

  test('settings UI fields show non-zero values matching the API', async ({ page }) => {
    const res = await page.request.get(`${BASE_URL}/api/settings`)
    const s = await res.json()

    await gotoSettings(page, 'monitoring')
    await expect(page.locator('#dockerPingTimeoutSeconds')).toHaveValue(String(s.dockerPingTimeoutSeconds))
    await expect(page.locator('#httpClientTimeoutSeconds')).toHaveValue(String(s.httpClientTimeoutSeconds))
    await expect(page.locator('#dockerClientRetryCount')).toHaveValue(String(s.dockerClientRetryCount))
    await expect(page.locator('#dockerClientRetryDelaySeconds')).toHaveValue(String(s.dockerClientRetryDelaySeconds))
  })
})

// ── Persistence through page reload ──────────────────────────────────────────

test.describe('Settings — persist through page reload', () => {
  test('changed setting survives a page reload', async ({ page }) => {
    await gotoSettings(page, 'monitoring')

    const input = page.locator('#dockerPingTimeoutSeconds')
    const original = await input.inputValue()
    const newValue = original === '7' ? '8' : '7'

    await input.fill(newValue)
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/settings') && r.request().method() !== 'GET'),
      page.locator('#saveSettingsBtn').click(),
    ])
    expect(res.status()).toBeLessThan(500)

    // Reload the page and verify the value is still there
    await page.reload()
    await expect(page.locator('#themeToggleBtn')).toBeVisible()
    await gotoSettings(page, 'monitoring')
    await expect(page.locator('#dockerPingTimeoutSeconds')).toHaveValue(newValue)

    // Restore
    await page.locator('#dockerPingTimeoutSeconds').fill(original)
    await page.locator('#saveSettingsBtn').click()
  })
})

// Restart persistence tests moved to 99-restart-persistence.spec.ts
// to prevent session contamination of subsequent test files.
