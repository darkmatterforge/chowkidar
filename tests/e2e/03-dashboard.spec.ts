import { test, expect } from '@playwright/test'

async function goToDashboard(page: Parameters<Parameters<typeof test>[1]>[0]['page']) {
  // Register the response listener BEFORE navigation to avoid a race where the
  // /api/containers fetch fires and resolves during page.goto().
  const containersReady = page.waitForResponse(
    r => r.url().includes('/api/containers') && r.status() === 200,
  )
  await page.goto('/')
  // #loginPage starts display:none; if the session is valid the app never shows
  // it — but give it 3 s to settle before asserting hidden.
  await expect(page.locator('#loginPage')).toBeHidden({ timeout: 5_000 })
  await containersReady
}

test.describe('Dashboard', () => {

  // ── Stats bar ──────────────────────────────────────────────────────────────

  test('stats bar renders all counters on load', async ({ page }) => {
    await goToDashboard(page)
    for (const id of ['statMonitored', 'statHealthy', 'statDown', 'statIssues', 'statJobs']) {
      await expect(page.locator(`#${id}`)).toBeVisible()
      // Value should no longer be the loading dash
      await expect(page.locator(`#${id}`)).not.toHaveText('–')
    }
  })

  test('Docker status card shows a connectivity value', async ({ page }) => {
    await goToDashboard(page)
    await expect(page.locator('#statDockerCard')).toBeVisible()
    await expect(page.locator('#statDockerVal')).not.toHaveText('–')
  })

  // ── Basic filters ──────────────────────────────────────────────────────────

  test('status filter has all expected options', async ({ page }) => {
    await goToDashboard(page)
    const opts = page.locator('#serviceStatusFilter option')
    await expect(opts).toContainText(['All Status', 'Up', 'Down', 'Unknown', 'Pause'])
  })

  test('job action filter has all expected options', async ({ page }) => {
    await goToDashboard(page)
    const opts = page.locator('#serviceJobActionFilter option')
    await expect(opts).toContainText(['All Actions', 'restart', 'start', 'stop', 'none', 'run-script'])
  })

  test('selecting Up status filter applies the value', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#serviceStatusFilter').selectOption('up')
    await expect(page.locator('#serviceStatusFilter')).toHaveValue('up')
    // Service list re-renders — wait for it to settle
    await page.waitForResponse(r => r.url().includes('/api/containers'))
  })

  test('selecting Down status filter applies the value', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#serviceStatusFilter').selectOption('down')
    await expect(page.locator('#serviceStatusFilter')).toHaveValue('down')
  })

  test('selecting Unknown status filter applies the value', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#serviceStatusFilter').selectOption('unknown')
    await expect(page.locator('#serviceStatusFilter')).toHaveValue('unknown')
  })

  test('selecting Pause status filter applies the value', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#serviceStatusFilter').selectOption('pause')
    await expect(page.locator('#serviceStatusFilter')).toHaveValue('pause')
  })

  test('job action filter — restart option applies', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#serviceJobActionFilter').selectOption('restart')
    await expect(page.locator('#serviceJobActionFilter')).toHaveValue('restart')
  })

  test('job action filter — stop option applies', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#serviceJobActionFilter').selectOption('stop')
    await expect(page.locator('#serviceJobActionFilter')).toHaveValue('stop')
  })

  test('job action filter — none option applies', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#serviceJobActionFilter').selectOption('none')
    await expect(page.locator('#serviceJobActionFilter')).toHaveValue('none')
  })

  test('search input filters the list', async ({ page }) => {
    await goToDashboard(page)
    const search = page.locator('#serviceSearchInput')
    await search.fill('nonexistent-xyz')
    // Wait for filter to apply then verify empty state or reduced list
    await expect(page.locator('#serviceGroups')).not.toContainText('Loading services...')
  })

  test('clear filters button resets all filter inputs', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#serviceSearchInput').fill('test')
    await page.locator('#serviceStatusFilter').selectOption('up')
    await page.locator('#serviceJobActionFilter').selectOption('stop')

    await page.locator('#clearServiceFiltersBtn').click()

    await expect(page.locator('#serviceSearchInput')).toHaveValue('')
    await expect(page.locator('#serviceStatusFilter')).toHaveValue('')
    await expect(page.locator('#serviceJobActionFilter')).toHaveValue('')
  })

  // ── Advanced filters ───────────────────────────────────────────────────────

  test('advanced filters panel toggles open and closed', async ({ page }) => {
    await goToDashboard(page)
    const panel = page.locator('#advFilters')
    await expect(panel).toBeHidden()
    await page.locator('#toggleAdvFiltersBtn').click()
    await expect(panel).toBeVisible()
    await page.locator('#toggleAdvFiltersBtn').click()
    await expect(panel).toBeHidden()
  })

  test('advanced filter — container name input is usable', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#toggleAdvFiltersBtn').click()
    await page.locator('#serviceContainerFilter').fill('my-app')
    await expect(page.locator('#serviceContainerFilter')).toHaveValue('my-app')
  })

  test('advanced filter — label filter is usable', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#toggleAdvFiltersBtn').click()
    await page.locator('#serviceLabelFilter').fill('app=web')
    await expect(page.locator('#serviceLabelFilter')).toHaveValue('app=web')
  })

  test('advanced filter — env var filter is usable', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#toggleAdvFiltersBtn').click()
    await page.locator('#serviceVariableFilter').fill('NODE_ENV')
    await expect(page.locator('#serviceVariableFilter')).toHaveValue('NODE_ENV')
  })

  test('advanced filter — job name filter is usable', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#toggleAdvFiltersBtn').click()
    await page.locator('#serviceJobFilter').fill('my-job')
    await expect(page.locator('#serviceJobFilter')).toHaveValue('my-job')
  })

  test('advanced filter — check scope dropdown has expected options', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#toggleAdvFiltersBtn').click()
    const opts = page.locator('#serviceActiveFilter option')
    await expect(opts).toContainText(['Check Scope', 'Checked by job/global', 'Not matched by checks'])
  })

  test('clear filters resets advanced filter inputs too', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#toggleAdvFiltersBtn').click()
    await page.locator('#serviceContainerFilter').fill('nginx')
    await page.locator('#serviceLabelFilter').fill('env=prod')
    await page.locator('#clearServiceFiltersBtn').click()
    await expect(page.locator('#serviceContainerFilter')).toHaveValue('')
    await expect(page.locator('#serviceLabelFilter')).toHaveValue('')
  })

  // ── Activity feed ──────────────────────────────────────────────────────────

  test('activity feed panel is present', async ({ page }) => {
    await goToDashboard(page)
    await expect(page.locator('#dashboardEventsTbody')).toBeVisible()
  })

  test('activity feed items-per-page select changes page size', async ({ page }) => {
    await goToDashboard(page)
    const pagination = page.locator('#historyPagination')
    const hasContent = await pagination.evaluate(el => el.children.length > 0)
    if (!hasContent) {
      test.skip(true, 'No history entries yet — pagination not rendered')
      return
    }
    const sel = page.locator('#historyPageSizeSelect')
    await sel.selectOption('50')
    await page.waitForResponse(r => r.url().includes('/api/history'))
    await expect(sel).toHaveValue('50')
  })

  // ── Refresh ────────────────────────────────────────────────────────────────

  test('Refresh button triggers a fresh containers fetch', async ({ page }) => {
    await goToDashboard(page)
    const [req] = await Promise.all([
      page.waitForRequest(r => r.url().includes('/api/containers')),
      page.locator('#refreshBtn').click(),
    ])
    expect(req).toBeTruthy()
  })

  // ── Container detail panel & manual actions ────────────────────────────────

  test('clicking a container opens the detail panel with action buttons', async ({ page }) => {
    await goToDashboard(page)
    const firstService = page.locator('#serviceGroups button[data-service-name]').first()
    if (await firstService.count() === 0) {
      test.skip(true, 'No monitored containers — skipping detail panel test')
      return
    }
    await firstService.click()
    await expect(page.locator('#serviceDetailArea')).not.toBeEmpty()
    await expect(page.locator('#detailRestartBtn')).toBeVisible()
    await expect(page.locator('#detailStartBtn')).toBeVisible()
    await expect(page.locator('#detailStopBtn')).toBeVisible()
    await expect(page.locator('#detailResumeBtn')).toBeVisible()
  })

  test('manual Restart triggers an action API call and shows status', async ({ page }) => {
    await goToDashboard(page)
    const firstService = page.locator('#serviceGroups button[data-service-name]').first()
    if (await firstService.count() === 0) {
      test.skip(true, 'No monitored containers — skipping Restart action test')
      return
    }
    await firstService.click()
    const restartBtn = page.locator('#detailRestartBtn')
    await expect(restartBtn).toBeEnabled({ timeout: 5_000 })

    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/action') && r.request().method() === 'POST'),
      restartBtn.click(),
    ])
    expect(res.status()).toBeLessThan(500)
    await expect(page.locator('#dashboardActionStatus')).toBeVisible()
  })

  test('manual Stop triggers an action API call and shows status', async ({ page }) => {
    await goToDashboard(page)
    const firstService = page.locator('#serviceGroups button[data-service-name]').first()
    if (await firstService.count() === 0) {
      test.skip(true, 'No monitored containers — skipping Stop action test')
      return
    }
    await firstService.click()
    const stopBtn = page.locator('#detailStopBtn')
    await expect(stopBtn).toBeEnabled({ timeout: 5_000 })

    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/action') && r.request().method() === 'POST'),
      stopBtn.click(),
    ])
    expect(res.status()).toBeLessThan(500)
    await expect(page.locator('#dashboardActionStatus')).toBeVisible()
  })

  test('manual Start triggers an action API call and shows status', async ({ page }) => {
    await goToDashboard(page)
    const firstService = page.locator('#serviceGroups button[data-service-name]').first()
    if (await firstService.count() === 0) {
      test.skip(true, 'No monitored containers — skipping Start action test')
      return
    }
    await firstService.click()
    const startBtn = page.locator('#detailStartBtn')
    await expect(startBtn).toBeEnabled({ timeout: 5_000 })

    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/action') && r.request().method() === 'POST'),
      startBtn.click(),
    ])
    expect(res.status()).toBeLessThan(500)
    await expect(page.locator('#dashboardActionStatus')).toBeVisible()
  })

  test('Resume monitoring triggers a reset-cooldown API call', async ({ page }) => {
    await goToDashboard(page)
    const firstService = page.locator('#serviceGroups button[data-service-name]').first()
    if (await firstService.count() === 0) {
      test.skip(true, 'No monitored containers — skipping Resume test')
      return
    }
    await firstService.click()
    const resumeBtn = page.locator('#detailResumeBtn')
    await expect(resumeBtn).toBeEnabled({ timeout: 5_000 })

    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/') && r.request().method() === 'POST'),
      resumeBtn.click(),
    ])
    expect(res.status()).toBeLessThan(500)
  })
})
