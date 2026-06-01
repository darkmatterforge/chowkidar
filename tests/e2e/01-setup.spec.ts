import { test, expect } from '@playwright/test'
import { TEST_USER, TEST_PASS } from './playwright.config'

const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

async function isSetupRequired(): Promise<boolean> {
  const res = await fetch(`${BASE_URL}/api/auth/status`)
  const data = await res.json()
  return data.setupRequired === true
}

test.describe.serial('Setup — first-run account creation', () => {
  test.beforeEach(async ({ page }) => {
    // These tests only apply when the app has never been configured.
    // On subsequent runs (local dev) the tests are skipped gracefully.
    const setupRequired = await isSetupRequired()
    test.skip(!setupRequired, 'Auth already configured — setup tests skipped')
    await page.goto('/')
  })

  test('shows setup panel (not login panel) on fresh install', async ({ page }) => {
    await expect(page.locator('#setupPanel')).toBeVisible()
    await expect(page.locator('#loginPanel')).toBeHidden()
  })

  test('shows error when username is empty', async ({ page }) => {
    await page.locator('#setupPassword').fill(TEST_PASS)
    await page.locator('#setupConfirm').fill(TEST_PASS)
    await page.getByRole('button', { name: 'Create Account' }).click()
    await expect(page.locator('#setupError')).toBeVisible()
    await expect(page.locator('#setupError')).toContainText('Username required')
  })

  test('shows error when password is empty', async ({ page }) => {
    await page.locator('#setupUsername').fill(TEST_USER)
    await page.getByRole('button', { name: 'Create Account' }).click()
    await expect(page.locator('#setupError')).toBeVisible()
    await expect(page.locator('#setupError')).toContainText('Password required')
  })

  test('shows error when passwords do not match', async ({ page }) => {
    await page.locator('#setupUsername').fill(TEST_USER)
    await page.locator('#setupPassword').fill(TEST_PASS)
    await page.locator('#setupConfirm').fill('WrongConfirm99!')
    await page.getByRole('button', { name: 'Create Account' }).click()
    await expect(page.locator('#setupError')).toBeVisible()
    await expect(page.locator('#setupError')).toContainText('Passwords do not match')
  })

  test('creates account successfully and transitions to login panel', async ({ page }) => {
    await page.locator('#setupUsername').fill(TEST_USER)
    await page.locator('#setupPassword').fill(TEST_PASS)
    await page.locator('#setupConfirm').fill(TEST_PASS)
    await page.getByRole('button', { name: 'Create Account' }).click()

    // After setup the app auto-populates the login form username and shows login panel
    await expect(page.locator('#loginPanel')).toBeVisible()
    await expect(page.locator('#setupPanel')).toBeHidden()
    await expect(page.locator('#loginUsername')).toHaveValue(TEST_USER)
  })
})
