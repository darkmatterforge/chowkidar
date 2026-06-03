/**
 * E2E tests for bell notifications, save feedback banners, and UI behaviour.
 */

import { test, expect, Page } from '@playwright/test'
import { gotoApp, gotoSettings } from './helpers/nav.js'

const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

// ── Bell timing ───────────────────────────────────────────────────────────────

test('bell refreshes immediately on open and also fetches notifications', async ({ page }) => {
  await gotoApp(page)

  const [, alertsResp, notifResp] = await Promise.all([
    page.locator('#notifBellBtn').click(),
    page.waitForResponse(r => r.url().includes('/api/system-alerts'), { timeout: 2000 }),
    page.waitForResponse(r => r.url().includes('/api/notifications'),  { timeout: 2000 }),
  ])
  expect(alertsResp.status()).toBe(200)
  expect(notifResp.status()).toBe(200)

  // Close bell.
  await page.mouse.click(10, 10)
})

// ── Bell alert ordering ───────────────────────────────────────────────────────

test('bell: critical alerts before monitoring_started; monitoring_started uses bootTime', async ({ page }) => {
  await gotoApp(page)

  // Fetch health + alerts with a short retry loop to avoid flakes where
  // system-alerts hasn't incorporated freshly seeded history yet.
  async function fetchHealthAndAlerts() {
    const h = await page.request.get(`${BASE_URL}/api/health`)
    const a = await page.request.get(`${BASE_URL}/api/system-alerts`)
    const health = await h.json()
    const payload = await a.json()
    return { health, alerts: payload.alerts }
  }

  let health: any = null, alerts: any[] = []
  for (let attempt = 0; attempt < 4; attempt++) {
    const res = await fetchHealthAndAlerts()
    health = res.health
    alerts = res.alerts || []
    const started = (alerts as any[]).find((a: any) => a.type === 'monitoring_started')
    const criticals = (alerts as any[]).filter(
      (a: any) => a.type === 'failed_recovery' || a.type === 'paused_monitoring',
    )
    if (!started) { break }
    // If no criticals or started is not newer than any critical, we're good.
    const bad = criticals.some((c: any) => new Date(started.timestamp).getTime() > new Date(c.timestamp).getTime())
    if (!bad) break
    // wait briefly and retry
    await page.waitForTimeout(500)
  }

  const started = (alerts as any[]).find((a: any) => a.type === 'monitoring_started')
  const criticals = (alerts as any[]).filter(
    (a: any) => a.type === 'failed_recovery' || a.type === 'paused_monitoring',
  )

  if (!started) { test.skip(); return }

  // monitoring_started must use bootTime, not lastScan.
  const diffFromBoot     = Math.abs(new Date(started.timestamp).getTime() - new Date(health.bootTime).getTime())
  const diffFromLastScan = Math.abs(new Date(started.timestamp).getTime() - new Date(health.lastScan).getTime())
  if (diffFromLastScan < diffFromBoot && diffFromBoot > 2000) {
    throw new Error(`monitoring_started is closer to lastScan than bootTime — regression`)
  }

  // If criticals present, monitoring_started timestamp must not be newer.
  for (const c of criticals) {
    if (new Date(started.timestamp).getTime() > new Date(c.timestamp).getTime()) {
      throw new Error(`monitoring_started (${started.timestamp}) newer than ${c.type} (${c.timestamp})`)
    }
  }
})

// ── Job save feedback ─────────────────────────────────────────────────────────

test('job save: banner outside form, success state, DOM position', async ({ page }) => {
  await gotoSettings(page, 'jobs')

  // Verify DOM: #jobSaveStatus must NOT be inside #jobFormPanel.
  await expect(page.locator('#jobFormPanel #jobSaveStatus')).toHaveCount(0)

  // Save a job and check the banner appears outside the (now-closed) form.
  await page.locator('#openAddJobBtn').click()
  await page.locator('#jobName').fill('e2e-save-feedback-job')
  await page.locator('#jobNameFilter').fill('e2e-feedback-container')

  const [res] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/job') && r.request().method() !== 'GET'),
    page.locator('#saveJobBtn').click(),
  ])
  expect(res.status()).toBeLessThan(500)
  const createdId = (await res.json()).id

  await expect(page.locator('#jobFormPanel')).toBeHidden()
  await expect(page.locator('#jobSaveStatus')).toBeVisible({ timeout: 2000 })
  await expect(page.locator('#jobSaveStatus')).toHaveClass(/success/)
  await expect(page.locator('#jobSaveStatus .test-result-title')).toContainText(/job added/i)

  // Cleanup.
  if (createdId) await page.request.delete(`${BASE_URL}/api/jobs/${createdId}`)
})

// ── Notification agent save feedback ─────────────────────────────────────────

test('notification agent save: banner outside form, DOM position verified', async ({ page }) => {
  await gotoSettings(page, 'notifications')

  // DOM: #notifyGlobalStatus must NOT be inside #notifyFormPanel.
  await expect(page.locator('#notifyFormPanel #notifyGlobalStatus')).toHaveCount(0)

  await page.locator('#openAddNotifyBtn').click()
  await expect(page.locator('#notifyFormPanel')).toBeVisible()
  await page.locator('#notifyName').fill('E2E Feedback Test Agent')

  const webhookInput = page.locator('#notifyFormPanel input[placeholder*="webhooks"]').first()
  if (await webhookInput.count() > 0) {
    await webhookInput.fill('https://discord.com/api/webhooks/123/abc')
  }

  const [res] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/notifications') && r.request().method() === 'PUT'),
    page.locator('#saveNotifyBtn').click(),
  ])
  expect(res.status()).toBeLessThan(500)
  await expect(page.locator('#notifyFormPanel')).toBeHidden()
  await expect(page.locator('#notifyGlobalStatus')).toBeVisible({ timeout: 2000 })

  // Cleanup.
  const data = await (await page.request.get(`${BASE_URL}/api/notifications`)).json()
  const filtered = (data.profiles as any[]).filter((p: any) => p.name !== 'E2E Feedback Test Agent')
  await page.request.put(`${BASE_URL}/api/notifications`, { data: { profiles: filtered } })
})

// ── Theme toggle does NOT navigate away ───────────────────────────────────────

test('theme toggle stays on current page; bell click stays on current page', async ({ page }) => {
  await gotoSettings(page)

  // Toggle from Settings — must stay on Settings.
  await page.locator('#themeToggleBtn').click()
  await expect(page.locator('#settingsPage')).toBeVisible()
  await expect(page.locator('.nav-btn[data-page="settings"]')).toHaveClass(/active/)

  // Bell click — must stay on Settings.
  await page.locator('#notifBellBtn').click()
  await expect(page.locator('#settingsPage')).toBeVisible()
  await page.mouse.click(10, 10)

  // Toggle from Dashboard — must stay on Dashboard.
  await page.locator('.nav-btn[data-page="dashboard"]').click()
  await page.locator('#themeToggleBtn').click()
  await expect(page.locator('#dashboardPage')).toBeVisible()
  await expect(page.locator('.nav-btn[data-page="dashboard"]')).toHaveClass(/active/)

  // Restore theme.
  for (let i = 0; i < 4; i++) {
    if (!await page.evaluate(() => document.documentElement.getAttribute('data-theme'))) break
    await page.locator('#themeToggleBtn').click()
  }
})

// ── Job filter inputs dark mode ───────────────────────────────────────────────

test('job filter inputs use theme-aware background (not hardcoded white)', async ({ page }) => {
  await gotoSettings(page, 'jobs')
  const themeBtn = page.locator('#themeToggleBtn')

  // Cycle to dark.
  for (let i = 0; i < 4; i++) {
    if (await page.evaluate(() => document.documentElement.getAttribute('data-theme')) === 'dark') break
    await themeBtn.click()
  }
  const isDark = (await page.evaluate(() => document.documentElement.getAttribute('data-theme'))) === 'dark'
  if (!isDark) { test.skip(); return }

  // All inputs visible and not pure-white background.
  await expect(page.locator('#jobSearch')).toBeVisible()
  await expect(page.locator('#jobFilterEnabled')).toBeVisible()
  const bg = await page.locator('#jobFilterEnabled').evaluate(el => window.getComputedStyle(el).backgroundColor)
  expect(bg).not.toBe('rgb(255, 255, 255)')

  // Restore.
  for (let i = 0; i < 4; i++) {
    if (!await page.evaluate(() => document.documentElement.getAttribute('data-theme'))) break
    await themeBtn.click()
  }
})

// ── Timestamp formatting ──────────────────────────────────────────────────────

test('activity feed history timestamps use YYYY-MM-DD HH:MM:SS format (not browser locale)', async ({ page }) => {
  // Register the history listener BEFORE navigation so we never miss the response.
  const historyLoaded = page.waitForResponse(
    r => r.url().includes('/api/history') && !r.url().includes('bars='),
    { timeout: 10_000 },
  )
  await gotoApp(page)
  await historyLoaded

  // The history feed renders as a <table>.  Column 4 (DateTime) contains the
  // timestamp formatted by formatTimestamp().  Use Playwright auto-wait so the
  // table renders before we check (seeded entries guarantee rows exist).
  const dateCol = page.locator('#dashboardEventsTbody table tbody tr td:nth-child(4)')
  await expect(dateCol.first()).toBeVisible({ timeout: 5_000 })

  for (let i = 0; i < Math.min(await dateCol.count(), 3); i++) {
    const text = await dateCol.nth(i).innerText()
    // Must NOT use browser locale format like "6/3/2026, 1:19:00 AM"
    expect(text).not.toMatch(/\d{1,2}\/\d{1,2}\/\d{4},/)
    // Must contain clean date like "2026-06-03"
    if (text.trim()) expect(text).toMatch(/\d{4}-\d{2}-\d{2}/)
  }
})

test('job save: validation error shown in status span when filter missing', async ({ page }) => {
  await gotoSettings(page, 'jobs')
  await page.locator('#openAddJobBtn').click()
  await page.locator('#jobName').fill('e2e-validation-test')
  // Submit without filling the required filter field.
  await page.locator('#saveJobBtn').click()
  // Client-side validation error must appear inside the span, not as a banner outside the form.
  await expect(page.locator('#jobStatus')).toContainText(/fix highlighted/i)
  // The form must stay open (error path).
  await expect(page.locator('#jobFormPanel')).toBeVisible()
  await page.locator('#closeJobFormBtn').click()
})

test('display timezone: persisted in settings and available via /api/settings', async ({ page }) => {
  await gotoApp(page)
  const res = await page.request.get(`${BASE_URL}/api/settings`)
  const data = await res.json()
  // Field must exist (empty string = auto, or an IANA zone).
  expect('displayTimezone' in data).toBe(true)
})

test('system alert timestamps in bell use YYYY-MM-DD format (not browser locale)', async ({ page }) => {
  await gotoApp(page)
  const alertsData = await (await page.request.get(`${BASE_URL}/api/system-alerts`)).json()
  if (!(alertsData.alerts as any[]).length) { test.skip(); return }

  await page.evaluate(() => localStorage.removeItem('cwk-dismissed-alerts'))
  await page.reload()
  await expect(page.locator('#themeToggleBtn')).toBeVisible()

  // Use Promise.all to register the response listener BEFORE the click so we
  // never miss a fast response.
  const [, ] = await Promise.all([
    page.locator('#notifBellBtn').click(),
    page.waitForResponse(r => r.url().includes('/api/system-alerts'), { timeout: 3000 }),
  ])

  const listText = await page.locator('#notifBellList').innerText()
  // Must NOT use browser locale format like "6/3/2026, 1:19:00 AM".
  if (/\d{1,2}\/\d{1,2}\/\d{4},/.test(listText)) {
    throw new Error(`Bell uses browser locale format. Expected YYYY-MM-DD. Excerpt: ${listText.slice(0, 200)}`)
  }
  await page.mouse.click(10, 10)
})
