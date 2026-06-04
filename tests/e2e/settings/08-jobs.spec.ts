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

    // ── Script Timeout field ──────────────────────────────────────────────────

    test('script timeout field is hidden for standard action', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await expect(page.locator('#jobScriptTimeoutWrap')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('script timeout field appears when Bash Script tab is selected', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#jobScriptTimeoutWrap')).toBeVisible()
      // Switching back hides it again
      await page.locator('#jobTabStandard').click()
      await expect(page.locator('#jobScriptTimeoutWrap')).toBeHidden()
      await page.locator('#closeJobFormBtn').click()
    })

    test('script timeout value is saved and restored when editing a job', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobTabScript').click()
      await expect(page.locator('#jobTabScriptContent')).toBeVisible()
      await expect(page.locator('#jobScript')).toBeVisible()
      await page.locator('#jobScript').fill('#!/bin/sh\necho hi')
      await expect(page.locator('#jobScript')).toHaveValue(/echo hi/)
      await page.locator('#jobScriptTimeout').fill('180')
      await page.locator('#jobNameFilter').fill('timeout-test-container')
      await saveJob(page, 'e2e-script-timeout-job')

      const editBtn = page.locator('#jobsTbody tr')
        .filter({ hasText: 'e2e-script-timeout-job' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#jobScriptTimeoutWrap')).toBeVisible()
      await expect(page.locator('#jobScriptTimeout')).toHaveValue('180')
      await page.locator('#cancelJobEditBtn').click()
      await deleteJob(page, 'e2e-script-timeout-job')
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
})
