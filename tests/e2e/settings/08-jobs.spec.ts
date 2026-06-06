import { test, expect, type Page } from '@playwright/test'
import { gotoSettings } from '../helpers/nav'

async function openJobsTab(page: Page) {
  await gotoSettings(page, 'jobs')
}

async function openAddForm(page: Page) {
  await page.locator('#openAddJobBtn').click()
  await expect(page.locator('#jobFormPanel')).toBeVisible()
}

async function saveJob(page: Page, name: string) {
  await page.locator('#jobName').fill(name)
  const [res] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/job') && r.request().method() !== 'GET'),
    page.locator('#saveJobBtn').click(),
  ])
  expect(res.status()).toBeLessThan(500)
  await expect(page.locator('#jobsTbody')).toContainText(name)
}

async function deleteJob(page: Page, name: string) {
  const row = page.locator('#jobsTbody tr').filter({ hasText: name })
  if (await row.count() === 0) return
  const [res] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/job') && r.request().method() === 'DELETE'),
    row.getByRole('button', { name: /delete/i }).click(),
  ])
  expect(res.status()).toBeLessThan(500)
  await expect(page.locator('#jobsTbody')).not.toContainText(name)
}

test.describe('Settings — Jobs tab', () => {

  // ── Queue settings ─────────────────────────────────────────────────────────

  test('Queue Settings card is collapsible', async ({ page }) => {
    await openJobsTab(page)
    const body = page.locator('#jobQueueBody')
    await expect(body).toBeHidden()
    await page.locator('#jobQueueHeader').click()
    await expect(body).toBeVisible()
    await page.locator('#jobQueueHeader').click()
    await expect(body).toBeHidden()
  })

  test('notification cooldown change saves and persists', async ({ page }) => {
    await openJobsTab(page)
    await page.locator('#jobQueueHeader').click()

    await page.locator('#notificationCooldownSeconds').fill('120')
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/settings') && r.request().method() !== 'GET'),
      page.locator('#saveJobQueueBtn').click(),
    ])
    expect(res.status()).toBeLessThan(500)
    await expect(page.locator('#jobQueueStatus')).toBeVisible()

    await gotoSettings(page, 'jobs')
    await page.locator('#jobQueueHeader').click()
    await expect(page.locator('#notificationCooldownSeconds')).toHaveValue('120')

    // Restore
    await page.locator('#notificationCooldownSeconds').fill('0')
    await page.locator('#saveJobQueueBtn').click()
  })

  // ── Form open/close ────────────────────────────────────────────────────────

  test('Add Job button opens the form', async ({ page }) => {
    await openJobsTab(page)
    await expect(page.locator('#jobFormPanel')).toBeHidden()
    await page.locator('#openAddJobBtn').click()
    await expect(page.locator('#jobFormPanel')).toBeVisible()
    await expect(page.locator('#jobFormTitle')).toContainText('New Job')
  })

  test('Cancel button closes form without adding a job', async ({ page }) => {
    await openJobsTab(page)
    await openAddForm(page)
    await page.locator('#cancelJobEditBtn').click()
    await expect(page.locator('#jobFormPanel')).toBeHidden()
  })

  test('Close (×) button dismisses the form', async ({ page }) => {
    await openJobsTab(page)
    await openAddForm(page)
    await page.locator('#closeJobFormBtn').click()
    await expect(page.locator('#jobFormPanel')).toBeHidden()
  })

  // ── Filter types ───────────────────────────────────────────────────────────

  test.describe.serial('Filter types', () => {
    test('container name filter radio shows name input', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      // "Container Name" radio should be selected by default
      await expect(page.locator('#jobFilterTypeName')).toHaveClass(/active/)
      await expect(page.locator('#jobFilterNameWrap')).toBeVisible()
      await expect(page.locator('#jobFilterLabelWrap')).toBeHidden()
      await expect(page.locator('#jobFilterEnvWrap')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('label filter radio shows key+value inputs', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobFilterTypeLabel').click()
      await expect(page.locator('#jobFilterLabelWrap')).toBeVisible()
      await expect(page.locator('#jobFilterNameWrap')).toBeHidden()
      await expect(page.locator('#jobFilterEnvWrap')).toBeHidden()
      await expect(page.locator('#jobLabelKey')).toBeVisible()
      await expect(page.locator('#jobLabelValue')).toBeVisible()
      await page.locator('#closeJobFormBtn').click()
    })

    test('env var filter radio shows key input', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobFilterTypeEnv').click()
      await expect(page.locator('#jobFilterEnvWrap')).toBeVisible()
      await expect(page.locator('#jobFilterNameWrap')).toBeHidden()
      await expect(page.locator('#jobFilterLabelWrap')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('create job filtered by container name', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      // Container Name is default — just fill and save
      await page.locator('#jobNameFilter').fill('nginx,redis')
      await saveJob(page, 'e2e-name-job')
    })

    test('create job filtered by label (key + value)', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobFilterTypeLabel').click()
      await expect(page.locator('#jobFilterLabelWrap')).toBeVisible()
      await page.locator('#jobLabelKey').fill('app')
      await page.locator('#jobLabelValue').fill('web')
      await saveJob(page, 'e2e-label-job')
      // The saved filter should be stored as 'app=web'
      const editBtn = page.locator('#jobsTbody tr').filter({ hasText: 'e2e-label-job' }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#jobFilterTypeLabel')).toHaveClass(/active/)
      await expect(page.locator('#jobLabelKey')).toHaveValue('app')
      await expect(page.locator('#jobLabelValue')).toHaveValue('web')
      await page.locator('#cancelJobEditBtn').click()
    })

    test('create job filtered by label (key only, no value)', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobFilterTypeLabel').click()
      await page.locator('#jobLabelKey').fill('chowkidar.monitor')
      // Leave value blank
      await saveJob(page, 'e2e-label-key-only-job')
    })

    test('create job filtered by env var', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobFilterTypeEnv').click()
      await page.locator('#jobEnvFilter').fill('SERVICE_NAME')
      await saveJob(page, 'e2e-env-job')
    })

    test('edit a job and save changes', async ({ page }) => {
      await openJobsTab(page)
      const editBtn = page.locator('#jobsTbody tr').filter({ hasText: 'e2e-name-job' }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#jobFormPanel')).toBeVisible()
      await page.locator('#jobName').fill('e2e-name-job-edited')
      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/job') && r.request().method() !== 'GET'),
        page.locator('#saveJobBtn').click(),
      ])
      expect(res.status()).toBeLessThan(500)
      await expect(page.locator('#jobsTbody')).toContainText('e2e-name-job-edited')
    })

    // ── Migration: jobs created before the radio-filter UI ────────────────
    // Existing jobs may have more than one filter field set. When the user
    // opens them for editing the form should select the primary filter type
    // and show the migration notice.

    test('migration notice shown when editing a job with multiple filters', async ({ page }) => {
      await openJobsTab(page)
      const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

      // Create a legacy job with BOTH name and label filters via the API
      // (bypasses the UI which now only allows one)
      const res = await page.request.post(`${BASE_URL}/api/jobs`, {
        data: {
          name: 'e2e-multi-filter-legacy',
          action: 'restart',
          enabled: true,
          containerNameFilter: 'legacy-app',
          containerLabelFilter: 'env=prod',
        },
      })
      expect(res.status()).toBeLessThan(500)

      await openJobsTab(page)
      const editBtn = page.locator('#jobsTbody tr').filter({ hasText: 'e2e-multi-filter-legacy' }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#jobFormPanel')).toBeVisible()

      // Migration notice must appear
      await expect(page.locator('#jobFilterMigrationNotice')).toBeVisible()

      // Form should have auto-selected the name filter (higher priority)
      await expect(page.locator('#jobFilterTypeName')).toHaveClass(/active/)
      await expect(page.locator('#jobNameFilter')).toHaveValue('legacy-app')

      await page.locator('#cancelJobEditBtn').click()
      await deleteJob(page, 'e2e-multi-filter-legacy')
    })

    test('migration notice hidden for single-filter jobs', async ({ page }) => {
      await openJobsTab(page)
      const editBtn = page.locator('#jobsTbody tr').filter({ hasText: 'e2e-label-job' }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#jobFormPanel')).toBeVisible()
      await expect(page.locator('#jobFilterMigrationNotice')).toBeHidden()
      await page.locator('#cancelJobEditBtn').click()
    })

    test('migration notice hidden after form is reset', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobFilterMigrationNotice')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('clean up filter-type test jobs', async ({ page }) => {
      await openJobsTab(page)
      for (const name of ['e2e-name-job', 'e2e-name-job-edited', 'e2e-label-job', 'e2e-label-key-only-job', 'e2e-env-job', 'e2e-multi-filter-legacy']) {
        await deleteJob(page, name)
      }
      await deleteJob(page, 'e2e-monitor')
    })
  })

  // ── Retry strategies ───────────────────────────────────────────────────────

  test.describe.serial('Retry strategies', () => {

    test('Unlimited retries — Limit retries unchecked (default)', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)

      // By default the "Limit retries" checkbox is unchecked = unlimited
      await expect(page.locator('#jobRetryLimitEnabled')).not.toBeChecked()
      // The retry options panel should be hidden
      await expect(page.locator('#jobRetryLimitOptions')).toBeHidden()

      await page.locator('#jobNameFilter').fill('unlimited-container')
      await saveJob(page, 'e2e-retry-unlimited')
    })

    test('Fixed attempt count — 3 attempts', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)

      // Enable retry limit
      await page.locator(`#jobRetryLimitEnabled`).check()
      await expect(page.locator('#jobRetryLimitOptions')).toBeVisible()

      // Choose "Fixed attempts" radio (should be default when enabled)
      await page.locator('#jobRetryModeCount').click()
      await expect(page.locator('#jobRetryCountWrap')).toBeVisible()
      await expect(page.locator('#jobRetryDurationWrap')).toBeHidden()

      await page.locator('#jobRetryCount').fill('3')
      await page.locator('#jobNameFilter').fill('fixed-container')
      await saveJob(page, 'e2e-retry-fixed')
    })

    test('Max duration in minutes — 30 minutes', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)

      await page.locator(`#jobRetryLimitEnabled`).check()
      await expect(page.locator('#jobRetryLimitOptions')).toBeVisible()

      // Switch to "Max duration" radio
      await page.locator('#jobRetryModeDuration').click()
      await expect(page.locator('#jobRetryDurationWrap')).toBeVisible()
      await expect(page.locator('#jobRetryCountWrap')).toBeHidden()

      await page.locator('#jobMaxMonDurationValue').fill('30')
      await page.locator('#jobMaxMonDurationUnit').selectOption('minutes')
      await expect(page.locator('#jobMaxMonDurationUnit')).toHaveValue('minutes')

      await page.locator('#jobNameFilter').fill('duration-min-container')
      await saveJob(page, 'e2e-retry-minutes')
    })

    test('Max duration in hours — 2 hours', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)

      await page.locator(`#jobRetryLimitEnabled`).check()
      await page.locator('#jobRetryModeDuration').click()
      await expect(page.locator('#jobRetryDurationWrap')).toBeVisible()

      await page.locator('#jobMaxMonDurationValue').fill('2')
      await page.locator('#jobMaxMonDurationUnit').selectOption('hours')
      await expect(page.locator('#jobMaxMonDurationUnit')).toHaveValue('hours')

      await page.locator('#jobNameFilter').fill('duration-hr-container')
      await saveJob(page, 'e2e-retry-hours')
    })

    test('all retry-strategy jobs appear in the table', async ({ page }) => {
      await openJobsTab(page)
      for (const name of ['e2e-retry-unlimited', 'e2e-retry-fixed', 'e2e-retry-minutes', 'e2e-retry-hours']) {
        await expect(page.locator('#jobsTbody')).toContainText(name)
      }
    })

    test('clean up retry-strategy test jobs', async ({ page }) => {
      await openJobsTab(page)
      for (const name of ['e2e-retry-unlimited', 'e2e-retry-fixed', 'e2e-retry-minutes', 'e2e-retry-hours']) {
        await deleteJob(page, name)
      }
    })
  })

  // ── Retry mode toggle (tab-btn style) ─────────────────────────────────────

  test.describe.serial('Retry mode toggle', () => {
    test('Fixed attempts is active by default when limit enabled', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobRetryLimitEnabled').check()
      await expect(page.locator('#jobRetryModeCount')).toHaveClass(/active/)
      await expect(page.locator('#jobRetryModeDuration')).not.toHaveClass(/active/)
      await expect(page.locator('#jobRetryCountWrap')).toBeVisible()
      await expect(page.locator('#jobRetryDurationWrap')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('Switching to Max duration activates the duration panel', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobRetryLimitEnabled').check()
      await page.locator('#jobRetryModeDuration').click()
      await expect(page.locator('#jobRetryModeDuration')).toHaveClass(/active/)
      await expect(page.locator('#jobRetryModeCount')).not.toHaveClass(/active/)
      await expect(page.locator('#jobRetryDurationWrap')).toBeVisible()
      await expect(page.locator('#jobRetryCountWrap')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('Switching back to Fixed attempts restores count panel', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobRetryLimitEnabled').check()
      await page.locator('#jobRetryModeDuration').click()
      await page.locator('#jobRetryModeCount').click()
      await expect(page.locator('#jobRetryModeCount')).toHaveClass(/active/)
      await expect(page.locator('#jobRetryCountWrap')).toBeVisible()
      await expect(page.locator('#jobRetryDurationWrap')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })
  })

  // ── Bash Script tab ────────────────────────────────────────────────────────

  test.describe.serial('Bash Script action', () => {
    test('Bash Script tab is present and shows a code editor', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#jobTabScriptContent')).toBeVisible()
      await expect(page.locator('#jobScript')).toBeVisible()
      await page.locator('#closeJobFormBtn').click()
    })

    test('create job with inline bash script for recovery', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      // Wait for tab content to be visible before filling — the class toggle
      // must complete and the textarea must be interactive, otherwise fill()
      // is a no-op, script validation fails, and no HTTP request is made.
      await expect(page.locator('#jobTabScriptContent')).toBeVisible()
      await expect(page.locator('#jobScript')).toBeVisible()
      await page.locator('#jobScript').fill(`#!/bin/sh
# $1 = container ID, $2 = container name
echo "Recovering container: $2 (id=$1)"
docker restart "$1" && echo "Restarted successfully"`)
      // Verify fill actually worked before proceeding — if empty, saveJob will
      // fail validation silently (no HTTP call) and waitForResponse will timeout.
      await expect(page.locator('#jobScript')).toHaveValue(/Recovering container/)
      await page.locator('#jobNameFilter').fill('bash-test-container')
      await saveJob(page, 'e2e-bash-script-job')
    })

    test('bash script job appears in the table with run-script action', async ({ page }) => {
      await openJobsTab(page)
      const row = page.locator('#jobsTbody tr').filter({ hasText: 'e2e-bash-script-job' })
      await expect(row).toContainText('run-script')
    })

    test('editing bash script job restores the script content', async ({ page }) => {
      await openJobsTab(page)
      const editBtn = page.locator('#jobsTbody tr')
        .filter({ hasText: 'e2e-bash-script-job' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#jobTabScriptContent')).toBeVisible()
      const script = await page.locator('#jobScript').inputValue()
      expect(script).toContain('Recovering container')
      await page.locator('#cancelJobEditBtn').click()
    })

    test('clean up bash script job', async ({ page }) => {
      await openJobsTab(page)
      await deleteJob(page, 'e2e-bash-script-job')
    })

    // ── Action Timeout applies to both docker actions and bash scripts ────────

    test('action timeout field is visible for standard and script actions', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobActionTimeout')).toBeVisible()
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#jobTabScriptContent')).toBeVisible()
      await expect(page.locator('#jobActionTimeout')).toBeVisible()
      await page.locator('#closeJobFormBtn').click()
    })

    test('action timeout value is saved and restored when editing a bash script job', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#jobTabScriptContent')).toBeVisible()
      await expect(page.locator('#jobScript')).toBeVisible()
      await page.locator('#jobScript').fill('#!/bin/sh\necho hi')
      await expect(page.locator('#jobScript')).toHaveValue(/echo hi/)
      await page.locator('#jobActionTimeout').fill('180')
      await page.locator('#jobNameFilter').fill('timeout-test-container')
      await saveJob(page, 'e2e-action-timeout-job')

      const editBtn = page.locator('#jobsTbody tr')
        .filter({ hasText: 'e2e-action-timeout-job' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#jobActionTimeout')).toHaveValue('180')
      await page.locator('#cancelJobEditBtn').click()
      await deleteJob(page, 'e2e-action-timeout-job')
    })

    // ── Template upgrade notice ───────────────────────────────────────────────

    test('upgrade notice is hidden when no script is loaded', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#scriptUpgradeNotice')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('upgrade notice is hidden for up-to-date template scripts', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      // Apply the current "restart" template (already at latest version)
      await page.locator('#scriptTemplateSelect').selectOption('restart')
      await expect(page.locator('#scriptUpgradeNotice')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('upgrade notice hidden after form reset', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#scriptUpgradeNotice')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
      await expect(page.locator('#scriptUpgradeNotice')).toBeHidden()
    })

    test('upgrade notice appears when editing a job with an outdated script version', async ({ page }) => {
      // Create a job with an old version marker (version 1, current is 2)
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#jobTabScriptContent')).toBeVisible()
      await page.locator('#jobScript').fill(`#!/bin/sh
# chowkidar-template: restart@1
docker restart "$1"`)
      await expect(page.locator('#jobScript')).toHaveValue(/restart/)
      await page.locator('#jobNameFilter').fill('upgrade-test-container')
      await saveJob(page, 'e2e-upgrade-notice-job')

      // Edit the job — upgrade notice should appear
      const editBtn = page.locator('#jobsTbody tr')
        .filter({ hasText: 'e2e-upgrade-notice-job' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#scriptUpgradeNotice')).toBeVisible()

      await page.locator('#cancelJobEditBtn').click()
    })

    test('applying the upgrade replaces script content and hides the notice', async ({ page }) => {
      await openJobsTab(page)
      const editBtn = page.locator('#jobsTbody tr')
        .filter({ hasText: 'e2e-upgrade-notice-job' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#scriptUpgradeNotice')).toBeVisible()

      // Click Apply update
      await page.locator('#scriptUpgradeNotice button', { hasText: 'Apply update' }).click()
      await expect(page.locator('#scriptUpgradeNotice')).toBeHidden()

      // Script should now contain the latest version marker (version 2)
      const script = await page.locator('#jobScript').inputValue()
      expect(script).toContain('chowkidar-template: restart@2')

      await page.locator('#cancelJobEditBtn').click()
    })

    test('skipping upgrade hides notice and suppresses it on re-open', async ({ page }) => {
      // First give the job the old script back so the notice shows again
      await openJobsTab(page)
      const editBtn = page.locator('#jobsTbody tr')
        .filter({ hasText: 'e2e-upgrade-notice-job' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await page.locator('#jobScript').fill(`#!/bin/sh
# chowkidar-template: restart@1
docker restart "$1"`)
      await page.locator('#saveJobBtn').click()
      await expect(page.locator('#jobFormPanel')).toBeHidden()

      // Edit again — notice should show; click Skip
      await editBtn.click()
      await expect(page.locator('#scriptUpgradeNotice')).toBeVisible()
      await page.locator('#scriptUpgradeNotice button', { hasText: 'Skip' }).click()
      await expect(page.locator('#scriptUpgradeNotice')).toBeHidden()
      await page.locator('#cancelJobEditBtn').click()

      // Re-open the same job — notice should stay hidden (suppressed in localStorage)
      await editBtn.click()
      await expect(page.locator('#scriptUpgradeNotice')).toBeHidden()
      await page.locator('#cancelJobEditBtn').click()

      // Cleanup: clear the localStorage dismissal so other tests are unaffected
      await page.evaluate(() => localStorage.removeItem('chowkidar-dismissed-upgrades'))
    })

    test('clean up upgrade notice test job', async ({ page }) => {
      await openJobsTab(page)
      await deleteJob(page, 'e2e-upgrade-notice-job')
    })

    // ── Dry run ───────────────────────────────────────────────────────────────

    test('dry run button is visible on Bash Script tab', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#dryRunScriptBtn')).toBeVisible()
      await page.locator('#closeJobFormBtn').click()
    })

    test('dry run with a safe script shows output', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#jobTabScriptContent')).toBeVisible()
      await page.locator('#jobScript').fill('#!/bin/sh\necho "dry-run-ok"')
      await expect(page.locator('#jobScript')).toHaveValue(/dry-run-ok/)
      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/scripts/dry-run')),
        page.locator('#dryRunScriptBtn').click(),
      ])
      expect(res.status()).toBeLessThan(500)
      await expect(page.locator('#dryRunResult')).toBeVisible()
      await expect(page.locator('#dryRunOutput')).toContainText('dry-run-ok')
      await page.locator('#closeJobFormBtn').click()
    })

    test('dry run output does not repeat in the result box', async ({ page }) => {
      // Regression: output used to appear both in error text and in the output pre block
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#jobTabScriptContent')).toBeVisible()
      await page.locator('#jobScript').fill('#!/bin/sh\necho "unique-marker-xyz"')
      await expect(page.locator('#jobScript')).toHaveValue(/unique-marker-xyz/)
      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/scripts/dry-run')),
        page.locator('#dryRunScriptBtn').click(),
      ])
      expect(res.status()).toBeLessThan(500)
      const outputText = await page.locator('#dryRunOutput').textContent()
      // The marker should appear exactly once, not duplicated
      const occurrences = (outputText ?? '').split('unique-marker-xyz').length - 1
      expect(occurrences).toBe(1)
      await page.locator('#closeJobFormBtn').click()
    })

    test('dry run injects DRY_RUN=1 and does not actually restart containers', async ({ page }) => {
      // A script that would exit non-zero if DRY_RUN is not set should still work in dry-run mode
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#jobTabScriptContent')).toBeVisible()
      await page.locator('#jobScript').fill('#!/bin/sh\n[ "$DRY_RUN" = "1" ] && echo "dry-run-mode" || exit 99')
      await expect(page.locator('#jobScript')).toHaveValue(/dry-run-mode/)
      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/scripts/dry-run')),
        page.locator('#dryRunScriptBtn').click(),
      ])
      expect(res.status()).toBeLessThan(500)
      await expect(page.locator('#dryRunOutput')).toContainText('dry-run-mode')
      await page.locator('#closeJobFormBtn').click()
    })
  })

  // ── Job table filters ──────────────────────────────────────────────────────

  test('job search input is present and accepts input', async ({ page }) => {
    await openJobsTab(page)
    await expect(page.locator('#jobSearch')).toBeVisible()
    await page.locator('#jobSearch').fill('nginx')
    await expect(page.locator('#jobSearch')).toHaveValue('nginx')
  })

  test('job enabled filter has expected options', async ({ page }) => {
    await openJobsTab(page)
    const opts = page.locator('#jobFilterEnabled option')
    await expect(opts).toContainText(['all states', 'enabled', 'disabled'])
  })

  test('job action filter has expected options', async ({ page }) => {
    await openJobsTab(page)
    const opts = page.locator('#jobFilterAction option')
    await expect(opts).toContainText(['all actions', 'restart', 'start', 'stop', 'none', 'run-script'])
  })

  test('job context filter is present with "all contexts" option', async ({ page }) => {
    await openJobsTab(page)
    const sel = page.locator('#jobFilterContext')
    await expect(sel).toBeVisible()
    await expect(sel.locator('option').first()).toHaveText('all contexts')
  })

  // ── Enabled and Start Exited checkboxes ───────────────────────────────────

  test.describe.serial('Enabled / Start Exited checkboxes', () => {
    test('Enabled checkbox is checked by default on new job form', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobEnabled')).toBeChecked()
      await page.locator('#closeJobFormBtn').click()
    })

    test('Start Exited checkbox is unchecked by default', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobStartExited')).not.toBeChecked()
      await page.locator('#closeJobFormBtn').click()
    })

    test('creating a disabled job saves enabled=false and persists via API', async ({ page }) => {
      const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobEnabled').uncheck()
      await expect(page.locator('#jobEnabled')).not.toBeChecked()
      await page.locator('#jobNameFilter').fill('checkbox-test-container')
      await saveJob(page, 'e2e-disabled-job')

      const res  = await page.request.get(`${BASE_URL}/api/jobs`)
      const data = await res.json() as { jobs: Array<{ name: string; enabled: boolean }> }
      const job  = data.jobs.find(j => j.name === 'e2e-disabled-job')
      expect(job?.enabled).toBe(false)
    })

    test('editing a disabled job restores Enabled checkbox as unchecked', async ({ page }) => {
      await openJobsTab(page)
      const editBtn = page.locator('#jobsTbody tr')
        .filter({ hasText: 'e2e-disabled-job' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#jobEnabled')).not.toBeChecked()
      await page.locator('#cancelJobEditBtn').click()
    })

    test('creating a job with startExited saves startExited=true', async ({ page }) => {
      const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobStartExited').check()
      await expect(page.locator('#jobStartExited')).toBeChecked()
      await page.locator('#jobNameFilter').fill('exited-test-container')
      await saveJob(page, 'e2e-start-exited-job')

      const res  = await page.request.get(`${BASE_URL}/api/jobs`)
      const data = await res.json() as { jobs: Array<{ name: string; startExited: boolean }> }
      const job  = data.jobs.find(j => j.name === 'e2e-start-exited-job')
      expect(job?.startExited).toBe(true)
    })

    test('clean up checkbox test jobs', async ({ page }) => {
      await openJobsTab(page)
      await deleteJob(page, 'e2e-disabled-job')
      await deleteJob(page, 'e2e-start-exited-job')
    })
  })

  // ── Bash script action tab ─────────────────────────────────────────────────

  test('Bash Script tab is present and shows a code editor', async ({ page }) => {
    await openJobsTab(page)
    await openAddForm(page)
    await page.locator('#jobTabScript').click()
    await expect(page.locator('#jobTabScriptContent')).toBeVisible()
    await expect(page.locator('#jobScript')).toBeVisible()
    // Switch back to Standard Action
    await page.locator('#jobTabStandard').click()
    await expect(page.locator('#jobTabStandardContent')).toBeVisible()
  })

  // ── Health Check tab ───────────────────────────────────────────────────────

  test.describe.serial('Health Check tab', () => {
    test('Health Check section is present with Docker Status and Bash Script tabs', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobHCTabDocker')).toBeVisible()
      await expect(page.locator('#jobHCTabScript')).toBeVisible()
      await page.locator('#closeJobFormBtn').click()
    })

    test('Docker Status tab is active by default', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobHCTabDocker')).toHaveClass(/active/)
      await expect(page.locator('#jobHCTabScript')).not.toHaveClass(/active/)
      await expect(page.locator('#jobHCDockerContent')).toBeVisible()
      await expect(page.locator('#jobHCScriptContent')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('switching to Bash Script tab shows the health check editor', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobHCTabScript').click()
      await expect(page.locator('#jobHCTabScript')).toHaveClass(/active/)
      await expect(page.locator('#jobHCScriptContent')).toBeVisible()
      await expect(page.locator('#jobHCDockerContent')).toBeHidden()
      await expect(page.locator('#jobHealthCheckScript')).toBeVisible()
      await expect(page.locator('#hcDryRunBtn')).toBeVisible()
      await page.locator('#closeJobFormBtn').click()
    })

    test('health check template picker inserts a script', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobHCTabScript').click()
      await expect(page.locator('#jobHCScriptContent')).toBeVisible()
      await page.locator('#hcTemplateSelect').selectOption('hc-http-exec')
      const script = await page.locator('#jobHealthCheckScript').inputValue()
      expect(script).toContain('#!/bin/bash')
      expect(script).toContain('curl')
      await page.locator('#closeJobFormBtn').click()
    })

    test('health check dry run button runs the script and shows output', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobHCTabScript').click()
      await expect(page.locator('#jobHCScriptContent')).toBeVisible()
      await page.locator('#jobHealthCheckScript').fill('#!/bin/sh\necho "hc-dry-run-ok"')
      await expect(page.locator('#jobHealthCheckScript')).toHaveValue(/hc-dry-run-ok/)
      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/scripts/dry-run')),
        page.locator('#hcDryRunBtn').click(),
      ])
      expect(res.status()).toBeLessThan(500)
      await expect(page.locator('#hcDryRunResult')).toBeVisible()
      await expect(page.locator('#hcDryRunOutput')).toContainText('hc-dry-run-ok')
      await page.locator('#closeJobFormBtn').click()
    })

    test('saving a job with health check script persists healthCheckScript', async ({ page }) => {
      const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobHCTabScript').click()
      await expect(page.locator('#jobHCScriptContent')).toBeVisible()
      await page.locator('#jobHealthCheckScript').fill('#!/bin/sh\ndocker exec "$1" curl -sf http://localhost/health')
      await expect(page.locator('#jobHealthCheckScript')).toHaveValue(/curl/)
      await page.locator('#jobNameFilter').fill('hc-script-container')
      await saveJob(page, 'e2e-hc-script-job')

      // Verify via API that healthCheckScript was saved
      const res = await page.request.get(`${BASE_URL}/api/jobs`)
      const data = await res.json()
      const job = (data.jobs as Array<{ name: string; healthCheckScript?: string }>)
        .find(j => j.name === 'e2e-hc-script-job')
      expect(job?.healthCheckScript).toContain('curl')
    })

    test('editing a job with health check script restores tab and content', async ({ page }) => {
      await openJobsTab(page)
      const editBtn = page.locator('#jobsTbody tr')
        .filter({ hasText: 'e2e-hc-script-job' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#jobFormPanel')).toBeVisible()
      // Health Check tab should switch to Script
      await expect(page.locator('#jobHCTabScript')).toHaveClass(/active/)
      await expect(page.locator('#jobHCScriptContent')).toBeVisible()
      const script = await page.locator('#jobHealthCheckScript').inputValue()
      expect(script).toContain('curl')
      await page.locator('#cancelJobEditBtn').click()
    })

    test('saving with Docker Status tab clears healthCheckScript', async ({ page }) => {
      const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'
      await openJobsTab(page)
      const editBtn = page.locator('#jobsTbody tr')
        .filter({ hasText: 'e2e-hc-script-job' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      // Switch back to Docker Status (clears script on save)
      await page.locator('#jobHCTabDocker').click()
      await expect(page.locator('#jobHCTabDocker')).toHaveClass(/active/)
      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/job') && r.request().method() !== 'GET'),
        page.locator('#saveJobBtn').click(),
      ])
      expect(res.status()).toBeLessThan(500)

      const apiRes = await page.request.get(`${BASE_URL}/api/jobs`)
      const data = await apiRes.json()
      const job = (data.jobs as Array<{ name: string; healthCheckScript?: string }>)
        .find(j => j.name === 'e2e-hc-script-job')
      expect(job?.healthCheckScript ?? '').toBe('')
    })

    test('clean up health check script job', async ({ page }) => {
      await openJobsTab(page)
      await deleteJob(page, 'e2e-hc-script-job')
    })
  })

  // ── Docker contexts (multi-host) ───────────────────────────────────────────

  test.describe.serial('Docker contexts', () => {
    const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

    // ── Chip count scales with number of configured hosts ─────────────────────

    test('with no extra hosts, Docker contexts section is hidden', async ({ page }) => {
      // Verify baseline: only built-in Local Docker → section hidden
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobDockerHostWrap')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('adding one extra host shows 2 chips (Local + 1 extra)', async ({ page }) => {
      await gotoSettings(page, 'dockerHosts')
      await page.locator('#openAddDockerHostBtn').click()
      await page.locator('#dockerHostName').fill('e2e-extra-context')
      await page.locator('#dockerHostEndpoint').fill('/var/run/docker.sock')
      await page.locator('#saveDockerHostBtn').click()
      await expect(page.locator('#dockerHostsTbody')).toContainText('e2e-extra-context')

      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobDockerHostWrap')).toBeVisible()
      const count = await page.locator('#jobDockerHostChips button[data-host-chip-id]').count()
      expect(count).toBe(2) // Local Docker + e2e-extra-context
      await page.locator('#closeJobFormBtn').click()
    })

    test('adding a second extra host shows 3 chips', async ({ page }) => {
      await gotoSettings(page, 'dockerHosts')
      await page.locator('#openAddDockerHostBtn').click()
      await page.locator('#dockerHostName').fill('e2e-extra-context-2')
      await page.locator('#dockerHostEndpoint').fill('/var/run/docker.sock')
      await page.locator('#saveDockerHostBtn').click()
      await expect(page.locator('#dockerHostsTbody')).toContainText('e2e-extra-context-2')

      await openJobsTab(page)
      await openAddForm(page)
      const count = await page.locator('#jobDockerHostChips button[data-host-chip-id]').count()
      expect(count).toBe(3) // Local Docker + e2e-extra-context + e2e-extra-context-2
      await page.locator('#closeJobFormBtn').click()
    })

    test('each chip shows the host name as its label', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobDockerHostChips')).toContainText('e2e-extra-context')
      await expect(page.locator('#jobDockerHostChips')).toContainText('e2e-extra-context-2')
      await page.locator('#closeJobFormBtn').click()
    })

    test('removing a host reduces chip count back to 2', async ({ page }) => {
      await gotoSettings(page, 'dockerHosts')
      const editBtn = page.locator('#dockerHostsTbody tr')
        .filter({ hasText: 'e2e-extra-context-2' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await page.locator('#deleteDockerHostBtn').click()
      await expect(page.locator('#dockerHostsTbody')).not.toContainText('e2e-extra-context-2')

      await openJobsTab(page)
      await openAddForm(page)
      const count = await page.locator('#jobDockerHostChips button[data-host-chip-id]').count()
      expect(count).toBe(2)
      await page.locator('#closeJobFormBtn').click()
    })

    test('Docker contexts chips section appears when multiple contexts exist', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobDockerHostWrap')).toBeVisible()
      await expect(page.locator('#jobDockerHostChips')).toBeVisible()
      await page.locator('#closeJobFormBtn').click()
    })

    test('can select a context chip — chip gets selected class', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      const chip = page.locator('#jobDockerHostChips button[data-host-chip-id="local"]')
      await expect(chip).toBeVisible()
      await chip.click()
      await expect(chip).toHaveClass(/selected/)
      // Deselect
      await chip.click()
      await expect(chip).not.toHaveClass(/selected/)
      await page.locator('#closeJobFormBtn').click()
    })

    test('saving a job with selected contexts persists dockerHostIDs', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      // Select Local Docker chip
      const chip = page.locator('#jobDockerHostChips button[data-host-chip-id="local"]')
      await chip.click()
      await expect(chip).toHaveClass(/selected/)
      await page.locator('#jobNameFilter').fill('context-test-container')
      await saveJob(page, 'e2e-context-pinned-job')

      const res = await page.request.get(`${BASE_URL}/api/jobs`)
      const data = await res.json()
      const job = (data.jobs as Array<{ name: string; dockerHostIDs?: string[] }>)
        .find(j => j.name === 'e2e-context-pinned-job')
      expect(job?.dockerHostIDs).toEqual(['local'])
    })

    test('editing a job restores its selected context chips', async ({ page }) => {
      await openJobsTab(page)
      const editBtn = page.locator('#jobsTbody tr')
        .filter({ hasText: 'e2e-context-pinned-job' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#jobFormPanel')).toBeVisible()
      const chip = page.locator('#jobDockerHostChips button[data-host-chip-id="local"]')
      await expect(chip).toHaveClass(/selected/)
      await page.locator('#cancelJobEditBtn').click()
    })

    test('context filter returns only jobs matching the selected host', async ({ page }) => {
      // Create an unfiltered job via API (no dockerHostIDs = matches all contexts).
      // UI validation now requires selecting at least one host when multiple hosts exist,
      // so use the API directly for a job intentionally scoped to all contexts.
      await page.request.post(`${BASE_URL}/api/jobs`, {
        data: {
          name: 'e2e-all-contexts-job',
          action: 'restart',
          enabled: true,
          containerNameFilter: 'all-contexts-container',
        },
      })

      // Filter by local context — both jobs should appear (pinned to local + all-contexts)
      await page.locator('#jobFilterContext').selectOption('local')
      await expect(page.locator('#jobsTbody')).toContainText('e2e-context-pinned-job')
      await expect(page.locator('#jobsTbody')).toContainText('e2e-all-contexts-job')

      // Filter by the extra context — only the all-contexts job should appear
      // (e2e-context-pinned-job is pinned to local only)
      const extraId = await page.locator('#jobFilterContext option').filter({ hasText: 'e2e-extra-context' }).getAttribute('value')
      if (extraId) {
        await page.locator('#jobFilterContext').selectOption(extraId)
        await expect(page.locator('#jobsTbody')).not.toContainText('e2e-context-pinned-job')
        await expect(page.locator('#jobsTbody')).toContainText('e2e-all-contexts-job')
      }

      // Reset filter
      await page.locator('#jobFilterContext').selectOption('')
    })

    test('job created via API with old dockerHostID field is migrated to dockerHostIDs', async ({ page }) => {
      // The API no longer accepts dockerHostID (json:"-") but we can verify
      // that a job created with dockerHostIDs round-trips correctly.
      const res = await page.request.post(`${BASE_URL}/api/jobs`, {
        data: {
          name: 'e2e-migration-test-job',
          action: 'restart',
          enabled: true,
          containerNameFilter: 'migration-container',
          dockerHostIDs: ['local'],
        },
      })
      expect(res.status()).toBeLessThan(500)
      const job = await res.json()
      expect(job.dockerHostIDs).toEqual(['local'])
      // Verify it appears in context filter
      await openJobsTab(page)
      await page.locator('#jobFilterContext').selectOption('local')
      await expect(page.locator('#jobsTbody')).toContainText('e2e-migration-test-job')
      await page.locator('#jobFilterContext').selectOption('')
      await deleteJob(page, 'e2e-migration-test-job')
    })

    test('clean up docker context test jobs and extra host', async ({ page }) => {
      await openJobsTab(page)
      await deleteJob(page, 'e2e-context-pinned-job')
      await deleteJob(page, 'e2e-all-contexts-job')
      // Remove the extra docker context
      await gotoSettings(page, 'dockerHosts')
      const editBtn = page.locator('#dockerHostsTbody tr')
        .filter({ hasText: 'e2e-extra-context' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await page.locator('#deleteDockerHostBtn').click()
      await expect(page.locator('#dockerHostsTbody')).not.toContainText('e2e-extra-context')
    })
  })
})
