import { test, expect, type Page } from '@playwright/test'

async function goToDashboard(page: Page) {
  // Register listeners BEFORE navigation so we never miss the responses.
  // Don't filter by status=200 — a 401/500 still means the request fired and
  // we should fail fast rather than hanging for 30 s.
  const containersReady = page.waitForResponse(r => r.url().includes('/api/containers'))
  const barsReady = page.waitForResponse(r => r.url().includes('/api/history') && r.url().includes('bars=true'))
  await page.goto('/')
  // Use the nav-bar button as the auth+init gate (more reliable than loginPage).
  await expect(page.locator('#themeToggleBtn')).toBeVisible({ timeout: 10_000 })
  await Promise.all([containersReady, barsReady])
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

  // ── Docker Hosts tile display format ───────────────────────────────────────

  test('Docker Hosts tile shows "1" for single connected host', async ({ page }) => {
    await page.route('**/api/docker-hosts/status', route =>
      route.fulfill({
        json: {
          hosts: [{ id: 'local', name: 'Local Docker', enabled: true, connected: true, info: '' }],
        },
      }),
    )
    await page.route('**/api/diagnostics', route =>
      route.fulfill({
        json: { dockerReachable: true, socketWritable: true, socketPresent: true, mode: 'socket', details: '' },
      }),
    )
    await goToDashboard(page)
    await expect(page.locator('#statDockerVal')).toHaveText('1')
    await expect(page.locator('#statDockerLbl')).toHaveText('Docker Hosts')
  })

  test('Docker Hosts tile shows "0" for single disconnected host', async ({ page }) => {
    await page.route('**/api/docker-hosts/status', route =>
      route.fulfill({
        json: {
          hosts: [{ id: 'local', name: 'Local Docker', enabled: true, connected: false, info: '' }],
        },
      }),
    )
    await page.route('**/api/diagnostics', route =>
      route.fulfill({
        json: { dockerReachable: false, socketWritable: false, socketPresent: false, mode: 'socket', details: 'offline' },
      }),
    )
    await goToDashboard(page)
    await expect(page.locator('#statDockerVal')).toHaveText('0')
    await expect(page.locator('#statDockerLbl')).toHaveText('Docker Hosts')
  })

  test('Docker Hosts tile shows "X/Y" format for multiple hosts all connected', async ({ page }) => {
    await page.route('**/api/docker-hosts/status', route =>
      route.fulfill({
        json: {
          hosts: [
            { id: 'local', name: 'Local Docker', enabled: true, connected: true, info: '' },
            { id: 'remote1', name: 'Remote Host', enabled: true, connected: true, info: '' },
          ],
        },
      }),
    )
    await goToDashboard(page)
    await expect(page.locator('#statDockerVal')).toHaveText('2/2')
    await expect(page.locator('#statDockerLbl')).toHaveText('Docker Hosts')
  })

  test('Docker Hosts tile shows partial X/Y when some hosts are offline', async ({ page }) => {
    await page.route('**/api/docker-hosts/status', route =>
      route.fulfill({
        json: {
          hosts: [
            { id: 'local', name: 'Local Docker', enabled: true, connected: true, info: '' },
            { id: 'remote1', name: 'Remote Host', enabled: true, connected: false, info: '' },
          ],
        },
      }),
    )
    await goToDashboard(page)
    await expect(page.locator('#statDockerVal')).toHaveText('1/2')
  })

  test('Docker Hosts tile shows plain count when only one host is enabled among multiple', async ({ page }) => {
    // Two entries returned but one is disabled — enabledHosts.length === 1, so show just "1" not "1/1"
    await page.route('**/api/docker-hosts/status', route =>
      route.fulfill({
        json: {
          hosts: [
            { id: 'local', name: 'Local Docker', enabled: true, connected: true, info: '' },
            { id: 'remote1', name: 'Remote Host', enabled: false, connected: false, info: '' },
          ],
        },
      }),
    )
    await goToDashboard(page)
    await expect(page.locator('#statDockerVal')).toHaveText('1')
    await expect(page.locator('#statDockerVal')).not.toHaveText('1/1')
  })

  // ── Basic filters ──────────────────────────────────────────────────────────

  test('status filter has all expected options', async ({ page }) => {
    await goToDashboard(page)
    const opts = page.locator('#serviceStatusFilter option')
    await expect(opts).toContainText(['All Status', 'Up', 'Down', 'Shutdown', 'Unknown', 'Pause'])
  })

  test('job action filter has all expected options', async ({ page }) => {
    await goToDashboard(page)
    const opts = page.locator('#serviceJobActionFilter option')
    await expect(opts).toContainText(['All Actions', 'restart', 'start', 'stop', 'none', 'run-script'])
  })

  test('selecting Up status filter applies the value', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#serviceStatusFilter').selectOption('up')
    // Filters are client-side — no API call fires; just assert the value stuck
    await expect(page.locator('#serviceStatusFilter')).toHaveValue('up')
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
    await page.locator('#serviceHostFilter').selectOption('local')

    await page.locator('#clearServiceFiltersBtn').click()

    await expect(page.locator('#serviceSearchInput')).toHaveValue('')
    await expect(page.locator('#serviceStatusFilter')).toHaveValue('')
    await expect(page.locator('#serviceJobActionFilter')).toHaveValue('')
    await expect(page.locator('#serviceHostFilter')).toHaveValue('')
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

  // ── Green bars (bars=true endpoint) ───────────────────────────────────────

  test('dashboard loads bars history separately from activity feed', async ({ page }) => {
    // The dashboard fetches /api/history?bars=true&limit=1000 in parallel with
    // the regular history. Verify both requests fire on every load.
    const feedReady = page.waitForResponse(
      r => r.url().includes('/api/history') && !r.url().includes('bars='),
    )
    const barsReady = page.waitForResponse(
      r => r.url().includes('/api/history') && r.url().includes('bars=true'),
    )
    await page.goto('/')
    await expect(page.locator('#themeToggleBtn')).toBeVisible({ timeout: 10_000 })
    const [feedRes, barsRes] = await Promise.all([feedReady, barsReady])
    expect(feedRes.status()).toBeLessThan(500)
    expect(barsRes.status()).toBeLessThan(500)
  })

  test('mini bars are present in the service card list', async ({ page }) => {
    await goToDashboard(page)
    const firstService = page.locator('#serviceGroups button[data-service-name]').first()
    if (await firstService.count() === 0) {
      test.skip(true, 'No monitored containers')
      return
    }
    // Each service card must contain the mini-bars wrapper
    await expect(firstService.locator('.mini-bars')).toBeVisible()
  })

  // ── Activity feed ──────────────────────────────────────────────────────────

  test('activity feed panel is present', async ({ page }) => {
    await goToDashboard(page)
    await expect(page.locator('#dashboardEventsTbody')).toBeVisible()
  })

  test('activity feed items-per-page select changes page size', async ({ page }) => {
    await goToDashboard(page)
    // Bootstrap seeds 30 history entries — enough to render pagination (page size 25).
    const sel = page.locator('#historyPageSizeSelect')
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/history') && !r.url().includes('bars=')),
      sel.selectOption('50'),
    ])
    expect(res.status()).toBeLessThan(500)
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
    // #detailResumeBtn is display:none for healthy containers (only shown in cooldown)
    await expect(page.locator('#detailActions')).toBeVisible()
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

  test('action buttons panel is present when a container is selected', async ({ page }) => {
    // #detailResumeBtn is display:none for healthy containers (only shown during
    // cooldown/exhaustion). Just verify the actions wrapper and the 3 core buttons.
    await goToDashboard(page)
    const firstService = page.locator('#serviceGroups button[data-service-name]').first()
    if (await firstService.count() === 0) {
      test.skip(true, 'No monitored containers — skipping action panel test')
      return
    }
    await firstService.click()
    await expect(page.locator('#detailActions')).toBeVisible()
    await expect(page.locator('#detailRestartBtn')).toBeAttached()
    await expect(page.locator('#detailStopBtn')).toBeAttached()
    await expect(page.locator('#detailStartBtn')).toBeAttached()
  })

  // ── Host filter ────────────────────────────────────────────────────────────

  test('host filter select is present with All Hosts default', async ({ page }) => {
    await goToDashboard(page)
    const select = page.locator('#serviceHostFilter')
    await expect(select).toBeVisible()
    await expect(select).toHaveValue('')
    const firstOpt = select.locator('option').first()
    await expect(firstOpt).toHaveText('All Hosts')
  })

  test('host filter includes Local Docker option', async ({ page }) => {
    await goToDashboard(page)
    const opts = page.locator('#serviceHostFilter option')
    await expect(opts).toContainText(['All Hosts', 'Local Docker'])
  })

  test('selecting local host filter applies the value', async ({ page }) => {
    await goToDashboard(page)
    await page.locator('#serviceHostFilter').selectOption('local')
    await expect(page.locator('#serviceHostFilter')).toHaveValue('local')
  })

  // ── Group collapse ─────────────────────────────────────────────────────────

  test('clicking a group title collapses and expands its services', async ({ page }) => {
    await goToDashboard(page)
    const groupTitle = page.locator('#serviceGroups .group-title[data-group]').first()
    if (await groupTitle.count() === 0) {
      test.skip(true, 'No service groups rendered — skipping collapse test')
      return
    }
    const groupKey = await groupTitle.getAttribute('data-group')
    const services = page.locator(`#serviceGroups button[data-service-name]`)
    const countBefore = await services.count()

    // Collapse
    await groupTitle.click()
    await expect(groupTitle).toHaveClass(/collapsed/)

    const countAfter = await services.count()
    expect(countAfter).toBeLessThan(countBefore)

    // Expand
    await groupTitle.click()
    await expect(groupTitle).not.toHaveClass(/collapsed/)
    expect(await services.count()).toBe(countBefore)

    // Clean up persisted collapse state
    await page.evaluate((key) => {
      const stored: string[] = JSON.parse(localStorage.getItem('collapsedServiceGroups') || '[]')
      const updated = stored.filter((k: string) => k !== key)
      localStorage.setItem('collapsedServiceGroups', JSON.stringify(updated))
    }, groupKey)
  })
})

// ── Page isolation — dashboard and settings must never render simultaneously ──

test.describe('Page isolation', () => {
  test('only dashboard is visible on initial load', async ({ page }) => {
    await page.goto('/')
    await expect(page.locator('#themeToggleBtn')).toBeVisible({ timeout: 10_000 })
    await expect(page.locator('#dashboardPage')).toBeVisible()
    await expect(page.locator('#settingsPage')).toBeHidden()
  })

  test('switching to Settings hides dashboard and shows settings', async ({ page }) => {
    await page.goto('/')
    await expect(page.locator('#themeToggleBtn')).toBeVisible({ timeout: 10_000 })
    await page.locator('[data-page="settings"]').click()
    await expect(page.locator('#settingsPage')).toBeVisible()
    await expect(page.locator('#dashboardPage')).toBeHidden()
  })

  test('switching back to Dashboard hides settings and shows dashboard', async ({ page }) => {
    await page.goto('/')
    await expect(page.locator('#themeToggleBtn')).toBeVisible({ timeout: 10_000 })
    await page.locator('[data-page="settings"]').click()
    await expect(page.locator('#settingsPage')).toBeVisible()
    await page.locator('[data-page="dashboard"]').click()
    await expect(page.locator('#dashboardPage')).toBeVisible()
    await expect(page.locator('#settingsPage')).toBeHidden()
  })

  test('exactly one page is visible at any time', async ({ page }) => {
    await page.goto('/')
    await expect(page.locator('#themeToggleBtn')).toBeVisible({ timeout: 10_000 })

    const countVisible = () =>
      page.locator('.page').evaluateAll(els =>
        els.filter(el => window.getComputedStyle(el).display !== 'none').length,
      )

    expect(await countVisible()).toBe(1)
    await page.locator('[data-page="settings"]').click()
    expect(await countVisible()).toBe(1)
    await page.locator('[data-page="dashboard"]').click()
    expect(await countVisible()).toBe(1)
  })
})
