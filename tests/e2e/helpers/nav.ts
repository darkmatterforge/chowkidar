import { Page, expect } from '@playwright/test'

/**
 * Navigate to the root and wait until the login overlay is gone.
 * #loginPage starts display:none — if auth is disabled or the session is valid
 * it stays hidden. If auth is enabled and the session is invalid, the app shows
 * it; the assertion will fail fast rather than silently blocking every click.
 */
export async function gotoApp(page: Page) {
  await page.goto('/')
  await expect(page.locator('#loginPage')).toBeHidden({ timeout: 5_000 })
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
