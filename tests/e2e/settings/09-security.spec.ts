import { test, expect, type Page } from '@playwright/test'
import { TEST_USER, TEST_PASS } from '../playwright.config'
import { gotoSettings } from '../helpers/nav'

async function openSecurityTab(page: Page) {
  await gotoSettings(page, 'security')
}

test.describe('Settings — Security tab', () => {
  test('security tab shows current user and change-password section', async ({ page }) => {
    await openSecurityTab(page)
    await expect(page.locator('#secCurrentUser')).toBeVisible()
    await expect(page.locator('#secUsername')).toContainText(TEST_USER)
    await expect(page.locator('#secChangeSection')).toBeVisible()
  })

  test('change password with wrong current password shows error', async ({ page }) => {
    await openSecurityTab(page)
    await expect(page.locator('#secChangeSection')).toBeVisible()

    await page.locator('#secCurrentPwd').fill('WrongCurrentPass!')
    await page.locator('#secNewPwd').fill('NewPassword2!')
    await page.locator('#secConfirmPwd').fill('NewPassword2!')
    await page.getByRole('button', { name: 'Update Password' }).click()

    await expect(page.locator('#secChangeError')).toBeVisible()
    await expect(page.locator('#secChangeError')).not.toBeEmpty()
  })

  test('change password with empty fields shows error', async ({ page }) => {
    await openSecurityTab(page)
    await page.getByRole('button', { name: 'Update Password' }).click()
    await expect(page.locator('#secChangeError')).toBeVisible()
  })

  test('disable auth section is visible and shows Disable Auth button', async ({ page }) => {
    await openSecurityTab(page)
    await expect(page.locator('#secDisableSection')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Disable Auth' })).toBeVisible()
  })
})
