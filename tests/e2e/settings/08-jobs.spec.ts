import { test, expect } from '@playwright/test'
import { gotoSettings } from '../helpers/nav'

async function openJobsTab(page: Parameters<Parameters<typeof test>[1]>[0]['page']) {
  await gotoSettings(page, 'jobs')
}

async function openAddForm(page: Parameters<Parameters<typeof test>[1]>[0]['page']) {
  await page.locator('#openAddJobBtn').click()
  await expect(page.locator('#jobFormPanel')).toBeVisible()
}

async function saveJob(page: Parameters<Parameters<typeof test>[1]>[0]['page'], name: string) {
  await page.locator('#jobName').fill(name)
  const [res] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/job') && r.request().method() !== 'GET'),
    page.locator('#saveJobBtn').click(),
  ])
  expect(res.status()).toBeLessThan(500)
  await expect(page.locator('#jobsTbody')).toContainText(name)
}

async function deleteJob(page: Parameters<Parameters<typeof test>[1]>[0]['page'], name: string) {
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
    test('create job filtered by container name', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobNameFilter').fill('nginx,redis')
      await saveJob(page, 'e2e-name-job')
    })

    test('create job filtered by label', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
      await page.locator('#jobLabelFilter').fill('app=web')
      await saveJob(page, 'e2e-label-job')
    })

    test('create job filtered by env var', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)
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

    test('clean up filter-type test jobs', async ({ page }) => {
      await openJobsTab(page)
      for (const name of ['e2e-name-job-edited', 'e2e-label-job', 'e2e-env-job']) {
        await deleteJob(page, name)
      }
      await expect(page.locator('#jobsTbody')).toContainText('No jobs')
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
      await page.locator('#jobRetryLimitEnabled').check()
      await expect(page.locator('#jobRetryLimitOptions')).toBeVisible()

      // Choose "Fixed attempts" radio (should be default when enabled)
      await page.locator('#jobRetryModeCount').check()
      await expect(page.locator('#jobRetryCountWrap')).toBeVisible()
      await expect(page.locator('#jobRetryDurationWrap')).toBeHidden()

      await page.locator('#jobRetryCount').fill('3')
      await page.locator('#jobNameFilter').fill('fixed-container')
      await saveJob(page, 'e2e-retry-fixed')
    })

    test('Max duration in minutes — 30 minutes', async ({ page }) => {
      await openJobsTab(page)
      await openAddForm(page)

      await page.locator('#jobRetryLimitEnabled').check()
      await expect(page.locator('#jobRetryLimitOptions')).toBeVisible()

      // Switch to "Max duration" radio
      await page.locator('#jobRetryModeDuration').check()
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

      await page.locator('#jobRetryLimitEnabled').check()
      await page.locator('#jobRetryModeDuration').check()
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
