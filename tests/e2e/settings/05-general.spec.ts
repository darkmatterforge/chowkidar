import { test, expect } from '@playwright/test'
import { gotoSettings } from '../helpers/nav'

async function openGeneralTab(page: Parameters<Parameters<typeof test>[1]>[0]['page']) {
  await gotoSettings(page)
  await expect(page.locator('#tab-general')).toBeVisible()
}

test.describe('Settings — General tab', () => {
  test('Settings page opens on General tab by default', async ({ page }) => {
    await gotoSettings(page)
    await expect(page.locator('.tab-btn[data-tab="general"]')).toHaveClass(/active/)
    await expect(page.locator('#tab-general')).toBeVisible()
  })

  test('change default action and save persists the value', async ({ page }) => {
    await openGeneralTab(page)

    const select = page.locator('#action')
    await select.selectOption('stop')
    await page.locator('#saveSettingsBtn').click()
    await expect(page.locator('#settingsStatus')).toBeVisible()

    // Reload and confirm the value stuck
    await gotoSettings(page)
    await expect(page.locator('#action')).toHaveValue('stop')

    // Restore default
    await page.locator('#action').selectOption('restart')
    await page.locator('#saveSettingsBtn').click()
  })

  test('Auto Detect button populates the Primary Base URL field', async ({ page }) => {
    await openGeneralTab(page)

    const urlInput = page.locator('#primaryBaseURL')
    await urlInput.fill('')
    await page.locator('#autoDetectBaseURLBtn').click()
    // After detection the field should contain a non-empty URL
    await expect(urlInput).not.toHaveValue('')
  })

  test('display timezone dropdown has selectable options', async ({ page }) => {
    await openGeneralTab(page)

    const sel = page.locator('#displayTimezone')
    await expect(sel).toBeVisible()
    // Should have populated option list (loaded from server)
    const count = await sel.locator('option').count()
    expect(count).toBeGreaterThan(1)
  })

  test('log retention days change saves and persists', async ({ page }) => {
    await openGeneralTab(page)

    const input = page.locator('#logRetentionDays')
    await input.fill('14')
    await page.locator('#saveSettingsBtn').click()
    await expect(page.locator('#settingsStatus')).toBeVisible()

    await gotoSettings(page)
    await expect(page.locator('#logRetentionDays')).toHaveValue('14')

    // Restore
    await page.locator('#logRetentionDays').fill('7')
    await page.locator('#saveSettingsBtn').click()
  })

  test('selecting a layout option marks it as selected', async ({ page }) => {
    await openGeneralTab(page)

    const tableLayout = page.locator('#layoutPicker .layout-option[data-layout="table"]')
    await tableLayout.click()
    await expect(tableLayout).toHaveClass(/selected/)

    // Also verify the other options are NOT selected
    await expect(page.locator('#layoutPicker .layout-option[data-layout="cards"]')).not.toHaveClass(/selected/)

    // Save settings
    await page.locator('#saveSettingsBtn').click()
    await expect(page.locator('#settingsStatus')).toBeVisible()

    // The layout class on #serviceGroups is only applied when containers render.
    // With no monitored containers the empty-state shows without the class.
    // Verify persistence via the picker itself instead.
    await expect(page.locator('#layoutPicker .layout-option[data-layout="table"]')).toHaveClass(/selected/)

    // Navigate to dashboard and back to confirm the setting survived the round-trip
    const containersLoaded = page.waitForResponse(r => r.url().includes('/api/containers'))
    await page.locator('[data-page="dashboard"]').click()
    await containersLoaded
    await gotoSettings(page)
    await expect(page.locator('#layoutPicker .layout-option[data-layout="table"]')).toHaveClass(/selected/)

    // Restore cards layout
    await page.locator('#layoutPicker .layout-option[data-layout="cards"]').click()
    await page.locator('#saveSettingsBtn').click()
  })
})
