/**
 * Restart-persistence tests — kept in a separate file (99-) so they run last.
 * Container restart invalidates all in-memory sessions; restartAndReAuth()
 * re-authenticates via the login form after each restart so subsequent tests
 * in this file still work.
 */
import { test, expect } from '@playwright/test'
import { gotoApp, gotoSettings } from '../helpers/nav'
import { restartAndReAuth, BASE_URL } from '../helpers/restart'

// ── Monitoring setting survives restart ───────────────────────────────────────

test.describe('Restart persistence — Monitoring', () => {
  test('dockerPingTimeoutSeconds survives container restart', async ({ page }) => {
    await gotoSettings(page, 'monitoring')

    const input = page.locator('#dockerPingTimeoutSeconds')
    const original = await input.inputValue()
    const newValue = original === '9' ? '8' : '9'

    await input.fill(newValue)
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/settings') && r.request().method() !== 'GET'),
      page.locator('#saveSettingsBtn').click(),
    ])
    expect(res.status()).toBeLessThan(500)

    await restartAndReAuth(page)

    const s = await (await page.request.get(`${BASE_URL}/api/settings`)).json()
    expect(String(s.dockerPingTimeoutSeconds)).toBe(newValue)

    // Restore
    await page.request.put(`${BASE_URL}/api/settings`, {
      data: { ...s, dockerPingTimeoutSeconds: Number(original) },
    })
  })
})

// ── General settings survive restart ─────────────────────────────────────────

test.describe('Restart persistence — General settings', () => {
  test('primaryBaseURL, externalHostname, displayTimezone, serverTimezone survive restart', async ({ page }) => {
    const testURL      = 'http://restart-test.local'
    const testHostname = 'restart-host'
    const testTZ       = 'Pacific/Auckland'

    // The preceding restart test invalidates the storageState session cookie.
    // Re-authenticate before making any API calls.
    await gotoApp(page)

    // Read current settings to use as base for the PUT
    const current = await (await page.request.get(`${BASE_URL}/api/settings`)).json()

    await page.request.put(`${BASE_URL}/api/settings`, {
      data: {
        ...current,
        primaryBaseURL:   testURL,
        externalHostname: testHostname,
        displayTimezone:  testTZ,
        serverTimezone:   testTZ,
      },
    })

    await restartAndReAuth(page)

    const s = await (await page.request.get(`${BASE_URL}/api/settings`)).json()
    expect(s.primaryBaseURL).toBe(testURL)
    expect(s.externalHostname).toBe(testHostname)
    expect(s.displayTimezone).toBe(testTZ)
    expect(s.serverTimezone).toBe(testTZ)

    // Restore
    await page.request.put(`${BASE_URL}/api/settings`, {
      data: { ...s, primaryBaseURL: current.primaryBaseURL ?? '', externalHostname: current.externalHostname ?? '' },
    })
  })
})
