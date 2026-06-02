import { test, expect } from '@playwright/test'
import { gotoApp } from './helpers/nav'

// These tests require the e2e-unhealthy container started by the test
// setup script (run-e2e-local.sh / ci.yml). The container:
//  1. starts healthy (health file exists)
//  2. becomes unhealthy after ~15s (health file removed)
//  3. chowkidar restarts it → becomes healthy again
//
// Total cycle: ~40-50s. Tests use a 120s timeout.

const CONTAINER = 'e2e-unhealthy'
const POLL_INTERVAL_MS = 5_000

async function waitForStatus(
  page: Parameters<Parameters<typeof test>[1]>[0]['page'],
  statusClass: string,
  timeoutMs: number,
) {
  const deadline = Date.now() + timeoutMs
  const btn = page.locator('#serviceGroups button').filter({ hasText: CONTAINER })

  while (Date.now() < deadline) {
    const cls = await btn.getAttribute('class').catch(() => '')
    if (cls?.includes(statusClass)) return

    // Trigger a fresh containers fetch then wait one interval
    await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/containers')),
      page.locator('#refreshBtn').click(),
    ])
    await page.waitForTimeout(POLL_INTERVAL_MS)
  }
  // Final assertion — gives a clear error message on timeout
  await expect(btn).toHaveClass(new RegExp(statusClass))
}

test.describe('Health recovery', () => {
  test.setTimeout(150_000)

  test('e2e-unhealthy container appears as monitored and starts healthy', async ({ page }) => {
    await gotoApp(page)

    // Container should appear in the monitored list (job covers it)
    const btn = page.locator('#serviceGroups button').filter({ hasText: CONTAINER })
    await expect(btn).toBeVisible({ timeout: 30_000 })
    await expect(btn).toHaveClass(/status-up/)
  })

  test('chowkidar detects the container going unhealthy', async ({ page }) => {
    await gotoApp(page)

    // The container removes its health file after ~15s then Docker marks it unhealthy
    // after one failed check interval (3s). Chowkidar polls every 8s so detection
    // should happen within ~30s from the health file removal.
    await waitForStatus(page, 'status-down', 70_000)

    const btn = page.locator('#serviceGroups button').filter({ hasText: CONTAINER })
    await expect(btn).toHaveClass(/status-down/)
  })

  test('chowkidar restarts the container and it recovers', async ({ page }) => {
    await gotoApp(page)

    // May already be down or in the process of restarting — wait for healthy
    await waitForStatus(page, 'status-up', 80_000)

    const btn = page.locator('#serviceGroups button').filter({ hasText: CONTAINER })
    await expect(btn).toHaveClass(/status-up/)
  })

  test('activity feed shows a restart event for the recovered container', async ({ page }) => {
    await gotoApp(page)

    // Give the history a moment to be written (the restart may have just happened)
    await page.waitForResponse(r => r.url().includes('/api/history'))

    // If no history entry is visible yet, do one more refresh cycle
    const feed = page.locator('#dashboardEventsTbody')
    const hasEntry = await feed.locator(`text=${CONTAINER}`).count() > 0
    if (!hasEntry) {
      await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/containers')),
        page.locator('#refreshBtn').click(),
      ])
      await page.waitForTimeout(3_000)
    }

    await expect(feed).toContainText(CONTAINER)
  })
})
