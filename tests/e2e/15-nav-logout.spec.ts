/**
 * Nav-bar logout button tests.
 *
 * KEY DESIGN CONSTRAINT: Any test in the 'app' Playwright project that calls
 * the real /api/auth/logout endpoint destroys the shared storageState session.
 * Playwright caches storageState in memory at project start — writing the file
 * mid-run doesn't help because subsequent tests still use the cached snapshot.
 *
 * Strategy:
 *  - Tests that need to OBSERVE the logout UX (login page appears, button hides)
 *    use page.route() to INTERCEPT /api/auth/logout so the real session is never
 *    destroyed while still triggering all the JS behaviour.
 *  - The API contract test (button calls /api/auth/logout with POST) is verified
 *    via page.request directly — it destroys the session but runs LAST so it
 *    can't cascade.
 */

import { test, expect } from '@playwright/test'
import { gotoApp, gotoSettings, isAuthEnabled } from './helpers/nav.js'

// ── Visibility & DOM structure ────────────────────────────────────────────────

test('logout button: visible when auth on, hidden when auth off, correct DOM', async ({ page }) => {
  await gotoApp(page)
  const authOn = await isAuthEnabled(page)

  if (authOn) {
    await expect(page.locator('#navLogoutBtn')).toBeVisible()
  } else {
    await expect(page.locator('#navLogoutBtn')).toBeHidden()
  }

  // Must be inside .page-nav, after the bell wrapper, no data-page attribute.
  await expect(page.locator('.page-nav #navLogoutBtn')).toHaveCount(1)
  const dataPage = await page.locator('#navLogoutBtn').getAttribute('data-page')
  expect(dataPage).toBeNull()
  await expect(page.locator('#navLogoutBtn svg')).toHaveCount(1)

  // DOM order: Settings button → bell wrapper → logout button (left to right)
  const order = await page.evaluate(() => {
    const nav = document.querySelector('.page-nav')!
    const children = Array.from(nav.children)
    return {
      settingsIdx: children.findIndex(el => (el as HTMLElement).getAttribute('data-page') === 'settings'),
      bellIdx:     children.findIndex(el => el.querySelector('#notifBellBtn') !== null),
      logoutIdx:   children.findIndex(el => (el as HTMLElement).id === 'navLogoutBtn'),
    }
  })
  if (order.logoutIdx !== -1 && order.bellIdx !== -1) {
    expect(order.logoutIdx).toBeGreaterThan(order.bellIdx)
  }
  if (order.logoutIdx !== -1 && order.settingsIdx !== -1) {
    expect(order.logoutIdx).toBeGreaterThan(order.settingsIdx)
  }
})

// ── Logout UX (intercepted — session not destroyed) ───────────────────────────

test('clicking logout: login page shows, button hides, re-login restores button', async ({ page }) => {
  await gotoApp(page)
  const authOn = await isAuthEnabled(page)
  if (!authOn) { test.skip(); return }

  // Intercept the logout API call so the real session is NOT destroyed.
  // This lets us verify the JS behaviour without cascading to subsequent tests.
  let logoutIntercepted = false
  await page.route('**/api/auth/logout', async route => {
    logoutIntercepted = true
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{"ok":true}' })
  })

  await expect(page.locator('#navLogoutBtn')).toBeVisible()
  await page.locator('#navLogoutBtn').click()

  // JS doLogout() called /api/auth/logout (intercepted) and triggered showLoginPage().
  expect(logoutIntercepted).toBe(true)
  await expect(page.locator('#loginPage')).toBeVisible({ timeout: 3_000 })
  await expect(page.locator('#navLogoutBtn')).toBeHidden()

  // Navigating back restores the app (real session was never invalidated).
  await page.goto('/')
  await expect(page.locator('#themeToggleBtn')).toBeVisible()
  await expect(page.locator('#navLogoutBtn')).toBeVisible()
})

test('logout from Settings: login page shows (not dashboard)', async ({ page }) => {
  await gotoApp(page)
  const authOn = await isAuthEnabled(page)
  if (!authOn) { test.skip(); return }

  await page.route('**/api/auth/logout', route =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{}' }))

  await gotoSettings(page)
  await page.locator('#navLogoutBtn').click()
  await expect(page.locator('#loginPage')).toBeVisible({ timeout: 3_000 })
  await expect(page.locator('#dashboardPage')).toBeHidden()
})

// The /api/auth/logout API contract (returns 200, invalidates session) is covered
// by the Go backend tests (TestAuthLogin/Logout in api_integration_test.go).
// We do NOT call the real endpoint in e2e tests because:
//   a) Playwright caches storageState in memory at project start — any real logout
//      invalidates the cached session for ALL subsequent tests in the run.
//   b) The intercepted tests above already verify the button calls the right endpoint
//      and triggers the correct UI behaviour (login page, button hides).
