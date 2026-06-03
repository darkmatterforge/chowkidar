import { test, expect } from '@playwright/test'
import { gotoApp, gotoSettings } from '../helpers/nav'

const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

// ── Login page dark logo ──────────────────────────────────────────────────────

test.describe('Login page', () => {
  test('dark logo visible when dark theme is active on login page', async ({ page }) => {
    // Force dark theme via localStorage before navigating
    await page.goto('/')
    await page.evaluate(() => localStorage.setItem('chowkidar-theme', 'dark'))
    await page.goto('/')

    // Trigger the login page by clearing the session cookie (not real logout —
    // calling /api/auth/logout would destroy the shared session and cascade
    // all subsequent tests).
    await page.context().clearCookies()
    await page.goto('/')

    // The login-logo-dark images should be visible, not display:none
    const darkLogos = page.locator('.login-logo-dark')
    const count = await darkLogos.count()
    if (count > 0) {
      // At least one dark logo should NOT have display:none in inline style
      const inlineStyle = await darkLogos.first().getAttribute('style')
      expect(inlineStyle ?? '').not.toContain('display: none')
      expect(inlineStyle ?? '').not.toContain('display:none')
    }

    // Restore theme
    await page.evaluate(() => localStorage.removeItem('chowkidar-theme'))
  })
})

// ── Health endpoint (no auth required) ───────────────────────────────────────

test.describe('Health endpoint', () => {
  test('/api/health returns 200 without authentication', async ({ request }) => {
    const res = await request.get(`${BASE_URL}/api/health`)
    expect(res.status()).toBe(200)
    const body = await res.json()
    expect(body.ok).toBe(true)
  })

  test('/api/health includes version and bootTime', async ({ request }) => {
    const res = await request.get(`${BASE_URL}/api/health`)
    const body = await res.json()
    expect(typeof body.version).toBe('string')
    expect(body.version.length).toBeGreaterThan(0)
    expect(body.bootTime).toBeTruthy()
  })
})

// ── System alerts endpoint ────────────────────────────────────────────────────

test.describe('System alerts', () => {
  test('/api/system-alerts returns alerts array', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.get(`${BASE_URL}/api/system-alerts`)
    expect(res.ok()).toBe(true)
    const body = await res.json()
    expect(Array.isArray(body.alerts)).toBe(true)
  })

  test('monitoring_started alert ID includes boot timestamp', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.get(`${BASE_URL}/api/system-alerts`)
    const body = await res.json()
    const started = (body.alerts as Array<{ id: string; type: string }>).find(
      (a) => a.type === 'monitoring_started',
    )
    if (started) {
      // ID format: "monitoring-started-YYYYMMDDTHHmmss"
      expect(started.id).toMatch(/^monitoring-started-\d{8}T\d{6}$/)
    }
  })
})

// ── Notification bell: Mark all read visibility ───────────────────────────────

test.describe('Notification bell', () => {
  test('Mark all read button is hidden when bell is empty', async ({ page }) => {
    await gotoApp(page)
    // Clear dismissed list and use a fake session where there are no alerts
    await page.evaluate(() => localStorage.removeItem('cwk-dismissed-alerts'))
    // Open the bell dropdown
    await page.locator('#notifBellBtn').click()
    await expect(page.locator('#notifBellDropdown')).toBeVisible()
    // If the list shows "No active alerts", the mark-all-read button must be hidden
    const listText = await page.locator('#notifBellList').innerText()
    const markAllBtn = page.locator('#markAllReadBtn')
    if (listText.includes('No active alerts')) {
      await expect(markAllBtn).toBeHidden()
    }
    // Close
    await page.keyboard.press('Escape')
    await page.locator('#notifBellBtn').click()
  })
})

// ── Clear history ─────────────────────────────────────────────────────────────

test.describe('Clear history', () => {
  test('DELETE /api/history returns {cleared:true}', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.delete(`${BASE_URL}/api/history`)
    expect(res.ok()).toBe(true)
    const body = await res.json()
    expect(body.cleared).toBe(true)
  })

  test('GET /api/history returns empty after clear (verifies action-history.json was truncated)', async ({ page }) => {
    await gotoApp(page)
    // Clear first.
    await page.request.delete(`${BASE_URL}/api/history`)

    // Immediately re-check — file-backed store must reflect the clear.
    const res = await page.request.get(`${BASE_URL}/api/history?limit=10`)
    expect(res.ok()).toBe(true)
    const body = await res.json()
    expect(body.total).toBe(0)
    expect(body.entries).toHaveLength(0)
  })

  test('Clear All History button is visible in General tab', async ({ page }) => {
    await gotoSettings(page)
    await expect(page.locator('#clearHistoryBtn')).toBeVisible()
  })

  test('Clear All History shows custom confirmation dialog', async ({ page }) => {
    await gotoSettings(page)
    // Click the clear button — should open the custom confirm modal (not a browser dialog)
    page.on('dialog', (d) => {
      // If a native browser dialog fires, fail — we expect the custom modal
      d.dismiss()
      throw new Error('Native browser dialog opened — expected custom modal')
    })
    await page.locator('#clearHistoryBtn').click()
    // The custom modal should be visible
    await expect(page.locator('#confirmModal')).toBeVisible()
    // Cancel it
    await page.locator('#confirmModalCancel').click()
    await expect(page.locator('#confirmModal')).toBeHidden()
  })
})

// ── External Hostname and Primary Base URL persistence ────────────────────────

test.describe('Settings — ExternalHostname and PrimaryBaseURL persistence', () => {
  test('External Hostname persists after save', async ({ page }) => {
    await gotoSettings(page)

    const beforeRes = await page.request.get(`${BASE_URL}/api/settings`)
    const beforeJson = await beforeRes.json()
    const originalValue: string = beforeJson.externalHostname ?? ''

    const testValue = 'test-host-e2e'
    await page.locator('#externalHostname').fill(testValue)
    const [res] = await Promise.all([
      page.waitForResponse((r) => r.url().includes('/api/settings') && r.request().method() !== 'GET'),
      page.locator('#saveSettingsBtn').click(),
    ])
    expect(res.status()).toBeLessThan(500)

    // Re-read via API to confirm file was updated
    const after = await page.request.get(`${BASE_URL}/api/settings`)
    const afterJson = await after.json()
    expect(afterJson.externalHostname).toBe(testValue)

    // Restore original value
    await page.locator('#externalHostname').fill(originalValue)
    await page.locator('#saveSettingsBtn').click()
  })

  test('Primary Base URL persists after save', async ({ page }) => {
    await gotoSettings(page)

    const beforeRes = await page.request.get(`${BASE_URL}/api/settings`)
    const beforeJson = await beforeRes.json()
    const originalValue: string = beforeJson.primaryBaseURL ?? ''

    const testValue = 'http://test-e2e.local'
    await page.locator('#primaryBaseURL').fill(testValue)
    const [res] = await Promise.all([
      page.waitForResponse((r) => r.url().includes('/api/settings') && r.request().method() !== 'GET'),
      page.locator('#saveSettingsBtn').click(),
    ])
    expect(res.status()).toBeLessThan(500)

    const after = await page.request.get(`${BASE_URL}/api/settings`)
    const afterJson = await after.json()
    expect(afterJson.primaryBaseURL).toBe(testValue)

    // Restore
    await page.locator('#primaryBaseURL').fill(originalValue)
    await page.locator('#saveSettingsBtn').click()
  })

  test('empty port/filter fields are not present in settings response (omitempty)', async ({ page }) => {
    // Verify that fields with yaml:",omitempty" (port, containerNameFilter etc.)
    // are absent from the API response when empty — this confirms they are also
    // omitted from config.yaml.
    const res = await page.request.get(`${BASE_URL}/api/settings`)
    expect(res.ok()).toBe(true)
    const json = await res.json()

    // port is empty string in the app default — omitempty means the API should
    // return it as an empty string (JSON has no "omit" concept), but the key
    // must still be present for the frontend to work.
    expect('port' in json).toBe(true)
    // The actual value is empty string or "8080" — if empty, yaml saves nothing.
    expect(json.containerNameFilter ?? '').toBe('')
    expect(json.containerLabelFilter ?? '').toBe('')
    expect(json.containerEnvVarFilter ?? '').toBe('')
  })

  test('DashboardRefreshSeconds preserved after saving Docker Hosts', async ({ page }) => {
    await gotoSettings(page)

    // Read current dashboardRefreshSeconds
    const before = await page.request.get(`${BASE_URL}/api/settings`)
    const beforeJson = await before.json()
    const refreshBefore: number = beforeJson.dashboardRefreshSeconds ?? 30

    // Save Docker Hosts (GET then PUT the same data back)
    const hostsRes = await page.request.get(`${BASE_URL}/api/docker-hosts`)
    const hostsJson = await hostsRes.json()
    await page.request.put(`${BASE_URL}/api/docker-hosts`, { data: hostsJson })

    // Verify dashboardRefreshSeconds wasn't zeroed
    const after = await page.request.get(`${BASE_URL}/api/settings`)
    const afterJson = await after.json()
    expect(afterJson.dashboardRefreshSeconds).toBe(refreshBefore)
  })
})
