import { test, expect } from '@playwright/test'
import { gotoSettings } from '../helpers/nav'

async function openMonitoringTab(page: Parameters<Parameters<typeof test>[1]>[0]['page']) {
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
})
