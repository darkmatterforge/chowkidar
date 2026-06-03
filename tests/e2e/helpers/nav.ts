import { Page, expect } from '@playwright/test'

/**
 * Navigate to the app and wait until the app is ready and interactive.
 *
 * Uses Playwright's built-in auto-wait:
 *  1. waitForResponse — waits for /api/auth/status so auth JS has completed
 *  2. expect().toBeHidden() — auto-retries until #loginPage is hidden
 *
 * Handles both "auth disabled" and "valid session" paths correctly.
 * If the session is invalid, #loginPage becomes visible and toBeHidden times out —
 * the test fails with a clear "expected hidden, received visible" message.
 */
export async function gotoApp(page: Page) {
  await Promise.all([
    page.goto('/'),
    // Wait for the auth check to complete before asserting DOM state.
    page.waitForResponse(
      r => r.url().includes('/api/auth/status'),
      { timeout: 10_000 },
    ),
  ])
  // Auth check is done. If session is valid (or auth disabled), login page is hidden.
  await expect(page.locator('#loginPage')).toBeHidden({ timeout: 5_000 })
}

/**
 * Navigate to the app and wait for the LOGIN page to be visible.
 *
 * When auth is enabled and no session exists, waitForResponse lets us know
 * the auth check completed before we assert the login panel is visible.
 * When auth is disabled, the app loads directly — detected via API check.
 */
export async function gotoLogin(page: Page) {
  await Promise.all([
    page.goto('/'),
    page.waitForResponse(
      r => r.url().includes('/api/auth/status'),
      { timeout: 10_000 },
    ).catch(() => null), // non-fatal — old browsers may not emit this
  ])

  // If auth is disabled, the app loads directly (no login page).
  const authRes = await page.request.get('/api/auth/status').catch(() => null)
  if (authRes?.ok()) {
    const auth = await authRes.json().catch(() => ({ enabled: true }))
    if (!auth.enabled) {
      await expect(page.locator('#themeToggleBtn')).toBeVisible({ timeout: 5_000 })
      return
    }
  }

  // Auth is enabled — login panel (or setup panel on first boot) must appear.
  // Both panels can exist in the DOM simultaneously (one hidden, one visible),
  // so check computed style rather than using toBeVisible with a multi-match locator.
  await page.waitForFunction(
    () => ['loginPanel', 'setupPanel'].some(id => {
      const el = document.getElementById(id)
      return el !== null && window.getComputedStyle(el).display !== 'none'
    }),
    { timeout: 10_000 },
  )
}

/**
 * Navigate to Settings and optionally activate a specific tab.
 */
export async function gotoSettings(page: Page, tab?: string) {
  await gotoApp(page)
  await page.locator('[data-page="settings"]').click()
  if (tab) {
    await page.locator(`.tab-btn[data-tab="${tab}"]`).click()
    await expect(page.locator(`#tab-${tab}`)).toBeVisible()
  } else {
    await expect(page.locator('#settingsPage')).toBeVisible()
  }
}

/**
 * Returns true when the running app has authentication enabled.
 */
export async function isAuthEnabled(page: Page): Promise<boolean> {
  const res = await page.request.get('/api/auth/status').catch(() => null)
  if (!res?.ok()) return false
  const data = await res.json().catch(() => ({ enabled: false }))
  return !!data.enabled
}
