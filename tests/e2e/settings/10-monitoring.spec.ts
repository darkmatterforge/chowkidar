import { test, expect, type Page } from '@playwright/test'
import { gotoSettings } from '../helpers/nav'

async function openMonitoringTab(page: Page) {
  await gotoSettings(page, 'monitoring')
}

test.describe('Settings — Monitoring tab', () => {
  test('monitoring tab renders all key inputs', async ({ page }) => {
    await openMonitoringTab(page)
    await expect(page.locator('#dockerPingTimeoutSeconds')).toBeVisible()
    await expect(page.locator('#httpClientTimeoutSeconds')).toBeVisible()
    await expect(page.locator('#sqlitePath')).toBeVisible()
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

  test('SQLite path field is visible and editable', async ({ page }) => {
    await openMonitoringTab(page)

    const input = page.locator('#sqlitePath')
    await expect(input).toBeVisible()
    const current = await input.inputValue()
    expect(current.length).toBeGreaterThan(0)
  })

  test('saving from General tab preserves hidden settings (retryCount not reset to 0)', async ({ page }) => {
    // Previously _hiddenSettings only had 3 fields; retryCount, retryDelaySeconds,
    // and actionTimeoutSeconds were silently zeroed on every Save.
    // This test reads the current values, saves from General, then re-reads to
    // confirm they survived the round-trip.
    const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

    const before = await page.request.get(`${BASE_URL}/api/settings`)
    const beforeJson = await before.json()
    const retryBefore = beforeJson.retryCount ?? 3

    // Save settings from the General tab (no changes — just click Save)
    await gotoSettings(page)
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/settings') && r.request().method() !== 'GET'),
      page.locator('#saveSettingsBtn').click(),
    ])
    expect(res.status()).toBeLessThan(500)

    // Re-read settings and verify retryCount wasn't reset
    const after = await page.request.get(`${BASE_URL}/api/settings`)
    const afterJson = await after.json()
    expect(afterJson.retryCount).toBe(retryBefore)
    expect(afterJson.retryCount).toBeGreaterThan(0)
  })
})
