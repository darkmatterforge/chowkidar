import { Page, expect } from '@playwright/test'

/**
 * Navigate to the root and wait until the login overlay is gone.
 * #loginPage starts display:none — if auth is disabled or the session is valid
 * it stays hidden. If auth is enabled and the session is invalid, the app shows
 * it; the assertion will fail fast rather than silently blocking every click.
 */
export async function gotoApp(page: Page) {
  await page.goto('/')
  // Wait for the nav-bar theme button — it only appears after the app has
  // fully initialised AND auth is confirmed. Checking #loginPage hidden is
  // unreliable because the element starts display:none and the auth JS may not
  // have run yet when the assertion fires.
  await expect(page.locator('#themeToggleBtn')).toBeVisible({ timeout: 10_000 })
}

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
