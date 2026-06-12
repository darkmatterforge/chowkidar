import { test, expect, type Page } from '@playwright/test'
import { gotoApp, gotoSettings } from '../helpers/nav'

const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

// ── Helpers ───────────────────────────────────────────────────────────────────

async function openMaintenanceTab(page: Page) {
  await gotoSettings(page, 'maintenance')
}

async function openAddForm(page: Page) {
  await page.locator('#openAddMaintenanceBtn').click()
  await expect(page.locator('#maintenanceFormPanel')).toBeVisible()
  await expect(page.locator('#maintTitle')).toBeVisible()
}

/** Creates a minimal Manual window via the UI and returns the created window ID from the API. */
async function createManualWindow(page: Page, title: string, onStart = 'allow-finish') {
  await openAddForm(page)
  await page.locator('#maintTitle').fill(title)
  await page.locator('#maintOnStart').selectOption(onStart)
  // Manual strategy needs the Docker Hosts tab (local host) as a target
  await page.locator('#maintTargetTypeHosts').click()
  await expect(page.locator('#maintTargetTypeHosts')).toHaveClass(/active/)
  const localChip = page.locator('#maintHostChips button[data-maint-host-chip-id="local"]')
  await localChip.click()
  const [res] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/maintenance') && r.request().method() === 'POST'),
    page.locator('#saveMaintenanceBtn').click(),
  ])
  expect(res.status()).toBeLessThan(300)
  const body = await res.json() as { id: string }
  await expect(page.locator('#maintenanceTbody')).toContainText(title)
  return body.id
}

async function deleteWindowByTitle(page: Page, title: string) {
  const row = page.locator('#maintenanceTbody tr').filter({ hasText: title })
  if (await row.count() === 0) return
  const [res] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/maintenance') && r.request().method() === 'DELETE'),
    row.getByRole('button', { name: /delete/i }).click(),
  ])
  expect(res.status()).toBeLessThan(300)
  await expect(page.locator('#maintenanceTbody')).not.toContainText(title)
}

async function cleanupWindowByApi(page: Page, id: string) {
  if (!id) return
  await page.request.delete(`${BASE_URL}/api/maintenance/${id}`).catch(() => null)
}

// ── Maintenance tab ───────────────────────────────────────────────────────────

test.describe('Settings — Maintenance tab', () => {
  test('Maintenance tab is reachable from Settings', async ({ page }) => {
    await openMaintenanceTab(page)
    await expect(page.locator('#openAddMaintenanceBtn')).toBeVisible()
    await expect(page.locator('#maintenanceTbody')).toBeVisible()
  })

  test('Add Window button opens the form with correct default values', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)

    await expect(page.locator('#maintTitle')).toHaveValue('')
    await expect(page.locator('#maintActive')).toBeChecked()
    await expect(page.locator('#maintOnStart')).toHaveValue('allow-finish')
    await expect(page.locator('#maintStrategy')).toHaveValue('manual')
    await expect(page.locator('#maintTargetTypeJobs')).toHaveClass(/active/)
    await expect(page.locator('#saveMaintenanceBtn')).toHaveText('Add Window')
  })

  test('Cancel button closes the form', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#cancelMaintenanceBtn').click()
    await expect(page.locator('#maintenanceFormPanel')).not.toHaveClass(/open/)
  })

  test('Close (×) button closes the form', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#closeMaintenanceFormBtn').click()
    await expect(page.locator('#maintenanceFormPanel')).not.toHaveClass(/open/)
  })

  test('Title is required — shows error when saving blank title', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#saveMaintenanceBtn').click()
    await expect(page.locator('#maintTitleError')).toHaveText(/required/i)
  })
})

// ── OnStart field ─────────────────────────────────────────────────────────────

test.describe('Maintenance — OnStart field', () => {
  test('OnStart select has exactly two options (cancel-queued removed)', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    const sel = page.locator('#maintOnStart')
    await expect(sel.locator('option')).toHaveCount(2)
    await expect(sel.locator('option[value="allow-finish"]')).toHaveCount(1)
    await expect(sel.locator('option[value="force-cancel"]')).toHaveCount(1)
    await expect(sel.locator('option[value="cancel-queued"]')).toHaveCount(0)
  })

  test('OnStart defaults to allow-finish on a new form', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await expect(page.locator('#maintOnStart')).toHaveValue('allow-finish')
  })

  test('OnStart resets to allow-finish when form is cancelled and re-opened', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintOnStart').selectOption('force-cancel')
    await page.locator('#cancelMaintenanceBtn').click()
    await openAddForm(page)
    await expect(page.locator('#maintOnStart')).toHaveValue('allow-finish')
  })

  test('API round-trip: allow-finish is saved and returned', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-allow-finish',
        active: false,
        onStart: 'allow-finish',
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(true)
    const created = await res.json() as { id: string; onStart: string }
    expect(created.onStart).toBe('allow-finish')

    const listRes = await page.request.get(`${BASE_URL}/api/maintenance`)
    const list = await listRes.json() as { windows: Array<{ id: string; onStart: string }> }
    const found = list.windows.find(w => w.id === created.id)
    expect(found?.onStart).toBe('allow-finish')

    await cleanupWindowByApi(page, created.id)
  })

  test('API round-trip: force-cancel is saved and returned', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-force-cancel',
        active: false,
        onStart: 'force-cancel',
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(true)
    const created = await res.json() as { id: string; onStart: string }
    expect(created.onStart).toBe('force-cancel')
    await cleanupWindowByApi(page, created.id)
  })

  test('API normalises legacy cancel-queued to allow-finish', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-legacy-cancel-queued',
        active: false,
        onStart: 'cancel-queued',
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(true)
    const created = await res.json() as { id: string; onStart: string }
    expect(created.onStart).toBe('allow-finish')
    await cleanupWindowByApi(page, created.id)
  })

  test('API rejects unknown onStart value', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-bad-onstart',
        active: false,
        onStart: 'nuke-everything',
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(false)
    expect(res.status()).toBe(400)
  })

  test('Edit form populates OnStart from saved window and survives round-trip', async ({ page }) => {
    await gotoApp(page)
    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-onstart-edit-test',
        active: false,
        onStart: 'allow-finish',
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    const created = await createRes.json() as { id: string }

    await openMaintenanceTab(page)
    const editBtn = page.locator('#maintenanceTbody tr')
      .filter({ hasText: 'e2e-onstart-edit-test' })
      .getByRole('button', { name: /edit/i })
    await editBtn.click()
    await expect(page.locator('#maintenanceFormPanel')).toBeVisible()
    await expect(page.locator('#maintOnStart')).toHaveValue('allow-finish')
    await expect(page.locator('#saveMaintenanceBtn')).toHaveText('Save Changes')

    // Change to force-cancel and save
    await page.locator('#maintOnStart').selectOption('force-cancel')
    const [saveRes] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/maintenance') && r.request().method() === 'PUT'),
      page.locator('#saveMaintenanceBtn').click(),
    ])
    expect(saveRes.status()).toBeLessThan(300)

    // Re-open and verify force-cancel persisted
    const editBtn2 = page.locator('#maintenanceTbody tr')
      .filter({ hasText: 'e2e-onstart-edit-test' })
      .getByRole('button', { name: /edit/i })
    await editBtn2.click()
    await expect(page.locator('#maintOnStart')).toHaveValue('force-cancel')

    await cleanupWindowByApi(page, created.id)
  })
})

// ── Mutually-exclusive target tabs ────────────────────────────────────────────

test.describe('Maintenance — target type tabs', () => {
  test('Jobs tab is active by default on a new form', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await expect(page.locator('#maintTargetTypeJobs')).toHaveClass(/active/)
    await expect(page.locator('#maintTargetTypeHosts')).not.toHaveClass(/active/)
  })

  test('Switching to Docker Hosts tab activates it and deactivates Jobs tab', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTargetTypeHosts').click()
    await expect(page.locator('#maintTargetTypeHosts')).toHaveClass(/active/)
    await expect(page.locator('#maintTargetTypeJobs')).not.toHaveClass(/active/)
  })

  test('Switching tabs shows the correct content panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    // Jobs panel should be visible by default
    await expect(page.locator('#maintTargetJobsWrap')).toHaveClass(/active/)
    await expect(page.locator('#maintTargetHostsWrap')).not.toHaveClass(/active/)
    // Switch to hosts
    await page.locator('#maintTargetTypeHosts').click()
    await expect(page.locator('#maintTargetHostsWrap')).toHaveClass(/active/)
    await expect(page.locator('#maintTargetJobsWrap')).not.toHaveClass(/active/)
  })

  test('API rejects a window targeting both jobs and Docker hosts', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-both-targets',
        active: false,
        strategy: 'manual',
        timezone: 'UTC',
        jobIDs: ['some-job-id'],
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(false)
    expect(res.status()).toBe(400)
  })

  test('API rejects a window targeting neither jobs nor Docker hosts', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-no-targets',
        active: false,
        strategy: 'manual',
        timezone: 'UTC',
        jobIDs: [],
        dockerHostIDs: [],
      },
    })
    expect(res.ok()).toBe(false)
    expect(res.status()).toBe(400)
  })
})

// ── CRUD round-trips ──────────────────────────────────────────────────────────

test.describe('Maintenance — CRUD', () => {
  test('Create Manual window → appears in list → delete', async ({ page }) => {
    await openMaintenanceTab(page)
    const id = await createManualWindow(page, 'e2e-crud-manual')
    await deleteWindowByTitle(page, 'e2e-crud-manual')
    await cleanupWindowByApi(page, id)
  })

  test('Edit window title and verify it updates in list', async ({ page }) => {
    await gotoApp(page)
    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-edit-before',
        active: false,
        onStart: 'allow-finish',
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    const created = await createRes.json() as { id: string }

    await openMaintenanceTab(page)
    const editBtn = page.locator('#maintenanceTbody tr')
      .filter({ hasText: 'e2e-edit-before' })
      .getByRole('button', { name: /edit/i })
    await editBtn.click()
    await expect(page.locator('#maintenanceFormPanel')).toBeVisible()
    await expect(page.locator('#maintTitle')).toHaveValue('e2e-edit-before')

    await page.locator('#maintTitle').fill('e2e-edit-after')
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/maintenance') && r.request().method() === 'PUT'),
      page.locator('#saveMaintenanceBtn').click(),
    ])
    expect(res.status()).toBeLessThan(300)
    await expect(page.locator('#maintenanceTbody')).toContainText('e2e-edit-after')
    await expect(page.locator('#maintenanceTbody')).not.toContainText('e2e-edit-before')

    await cleanupWindowByApi(page, created.id)
  })

  test('Delete button removes window from list', async ({ page }) => {
    await gotoApp(page)
    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-delete-me',
        active: false,
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    const created = await createRes.json() as { id: string }

    await openMaintenanceTab(page)
    await expect(page.locator('#maintenanceTbody')).toContainText('e2e-delete-me')
    await deleteWindowByTitle(page, 'e2e-delete-me')
    await cleanupWindowByApi(page, created.id)
  })

  test('Delete button hidden in Add mode, visible in Edit mode', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await expect(page.locator('#deleteMaintenanceBtn')).toBeHidden()

    await gotoApp(page)
    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-delete-btn-visibility',
        active: false,
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    const created = await createRes.json() as { id: string }

    await openMaintenanceTab(page)
    const editBtn = page.locator('#maintenanceTbody tr')
      .filter({ hasText: 'e2e-delete-btn-visibility' })
      .getByRole('button', { name: /edit/i })
    await editBtn.click()
    await expect(page.locator('#deleteMaintenanceBtn')).toBeVisible()

    await cleanupWindowByApi(page, created.id)
  })
})

// ── Strategy panels ───────────────────────────────────────────────────────────

test.describe('Maintenance — Strategy switching', () => {
  test('Manual strategy shows its panel, hides others', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('manual')
    await expect(page.locator('#maintManualWrap')).toBeVisible()
    await expect(page.locator('#maintSingleWrap')).not.toBeVisible()
    await expect(page.locator('#maintCronWrap')).not.toBeVisible()
    // Timezone is shown for all strategies except Single Window
    await expect(page.locator('#maintTimezoneWrap')).toBeVisible()
  })

  test('Single Window strategy shows its panel and hides timezone', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('single')
    await expect(page.locator('#maintSingleWrap')).toBeVisible()
    await expect(page.locator('#maintManualWrap')).not.toBeVisible()
    // Timezone is hidden only for Single Window strategy
    await expect(page.locator('#maintTimezoneWrap')).not.toBeVisible()
  })

  test('Cron strategy shows its panel and the shared timezone field', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('cron')
    await expect(page.locator('#maintCronWrap')).toBeVisible()
    await expect(page.locator('#maintTimezoneWrap')).toBeVisible()
    await expect(page.locator('#maintManualWrap')).not.toBeVisible()
  })

  test('Recurring Interval strategy shows its panel and timezone', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('recur-interval')
    await expect(page.locator('#maintIntervalWrap')).toBeVisible()
    await expect(page.locator('#maintTimezoneWrap')).toBeVisible()
  })

  test('Recurring Day of Week strategy shows its panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('recur-day-of-week')
    await expect(page.locator('#maintWeeklyWrap')).toBeVisible()
    await expect(page.locator('#maintTimezoneWrap')).toBeVisible()
  })

  test('Recurring Day of Month strategy shows its panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('recur-day-of-month')
    await expect(page.locator('#maintMonthlyWrap')).toBeVisible()
    await expect(page.locator('#maintTimezoneWrap')).toBeVisible()
  })

  test('Effective range controls are hidden by default, visible when enabled', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('cron')
    await expect(page.locator('#maintEffectiveRangeOptions')).not.toBeVisible()
    await page.locator('#maintEffectiveRangeEnabled').check()
    await expect(page.locator('#maintEffectiveRangeOptions')).toBeVisible()
    await expect(page.locator('#maintEffectiveFrom')).toBeVisible()
    await expect(page.locator('#maintEffectiveTo')).toBeVisible()
  })

  test('Strategy label appears correctly in the windows list', async ({ page }) => {
    await gotoApp(page)
    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-strategy-label-test',
        active: false,
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    const created = await createRes.json() as { id: string }

    await openMaintenanceTab(page)
    const row = page.locator('#maintenanceTbody tr').filter({ hasText: 'e2e-strategy-label-test' })
    await expect(row).toContainText('Manual')

    await cleanupWindowByApi(page, created.id)
  })
})

// ── Per-strategy API CRUD ─────────────────────────────────────────────────────

test.describe('Maintenance — Per-strategy API CRUD', () => {
  test('Manual strategy: create, list, delete', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-strategy-manual',
        active: false,
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(true)
    const w = await res.json() as { id: string; strategy: string }
    expect(w.strategy).toBe('manual')

    const list = await page.request.get(`${BASE_URL}/api/maintenance`)
    const body = await list.json() as { windows: Array<{ id: string }> }
    expect(body.windows.some(x => x.id === w.id)).toBe(true)

    await cleanupWindowByApi(page, w.id)
  })

  test('Single Window strategy: create with start/end, verify fields returned', async ({ page }) => {
    await gotoApp(page)
    const start = new Date(Date.now() + 60 * 60 * 1000).toISOString()
    const end   = new Date(Date.now() + 2 * 60 * 60 * 1000).toISOString()
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-strategy-single',
        active: false,
        strategy: 'single',
        single: { start, end },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(true)
    const w = await res.json() as { id: string; strategy: string; single: { start: string; end: string } }
    expect(w.strategy).toBe('single')
    expect(w.single).toBeDefined()
    await cleanupWindowByApi(page, w.id)
  })

  test('Cron strategy: create with expression and duration, verify fields returned', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-strategy-cron',
        active: false,
        strategy: 'cron',
        timezone: 'UTC',
        cron: { expression: '0 2 * * *', durationMinutes: 60 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(true)
    const w = await res.json() as { id: string; strategy: string; cron: { expression: string; durationMinutes: number } }
    expect(w.strategy).toBe('cron')
    expect(w.cron?.expression).toBe('0 2 * * *')
    expect(w.cron?.durationMinutes).toBe(60)
    await cleanupWindowByApi(page, w.id)
  })

  test('Recurring Interval strategy: create with everyDays/timeOfDay/duration, verify fields', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-strategy-interval',
        active: false,
        strategy: 'recur-interval',
        timezone: 'UTC',
        recurInterval: { everyDays: 7, timeOfDay: '02:00', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(true)
    const w = await res.json() as { id: string; strategy: string; recurInterval: { everyDays: number; timeOfDay: string; durationMinutes: number } }
    expect(w.strategy).toBe('recur-interval')
    expect(w.recurInterval?.everyDays).toBe(7)
    expect(w.recurInterval?.timeOfDay).toBe('02:00')
    expect(w.recurInterval?.durationMinutes).toBe(30)
    await cleanupWindowByApi(page, w.id)
  })

  test('Recurring Day of Week strategy: create with weekdays/timeOfDay/duration, verify fields', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-strategy-weekly',
        active: false,
        strategy: 'recur-day-of-week',
        timezone: 'UTC',
        recurWeekly: { weekdays: [0, 6], timeOfDay: '03:00', durationMinutes: 45 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(true)
    const w = await res.json() as { id: string; strategy: string; recurWeekly: { weekdays: number[]; timeOfDay: string; durationMinutes: number } }
    expect(w.strategy).toBe('recur-day-of-week')
    expect(w.recurWeekly?.weekdays).toEqual(expect.arrayContaining([0, 6]))
    expect(w.recurWeekly?.timeOfDay).toBe('03:00')
    expect(w.recurWeekly?.durationMinutes).toBe(45)
    await cleanupWindowByApi(page, w.id)
  })

  test('Recurring Day of Month strategy: create with monthDays/timeOfDay/duration, verify fields', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-strategy-monthly',
        active: false,
        strategy: 'recur-day-of-month',
        timezone: 'UTC',
        recurMonthly: { daysOfMonth: [1, 15], timeOfDay: '01:00', durationMinutes: 120 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.ok()).toBe(true)
    const w = await res.json() as { id: string; strategy: string; recurMonthly: { daysOfMonth: number[]; timeOfDay: string; durationMinutes: number } }
    expect(w.strategy).toBe('recur-day-of-month')
    expect(w.recurMonthly?.daysOfMonth).toEqual(expect.arrayContaining([1, 15]))
    expect(w.recurMonthly?.timeOfDay).toBe('01:00')
    expect(w.recurMonthly?.durationMinutes).toBe(120)
    await cleanupWindowByApi(page, w.id)
  })

  test('Each strategy label appears correctly in list view', async ({ page }) => {
    await gotoApp(page)
    const strategies = [
      { strategy: 'manual', label: 'Manual', extra: { timezone: 'UTC', dockerHostIDs: ['local'] } },
      { strategy: 'single', label: 'Single Window', extra: { single: { start: new Date(Date.now() + 3600000).toISOString(), end: new Date(Date.now() + 7200000).toISOString() }, dockerHostIDs: ['local'] } },
      { strategy: 'cron', label: 'Cron', extra: { cron: { expression: '0 3 * * *', durationMinutes: 30 }, timezone: 'UTC', dockerHostIDs: ['local'] } },
      { strategy: 'recur-interval', label: 'Interval', extra: { recurInterval: { everyDays: 7, timeOfDay: '02:00', durationMinutes: 30 }, timezone: 'UTC', dockerHostIDs: ['local'] } },
      { strategy: 'recur-day-of-week', label: 'Day of Week', extra: { recurWeekly: { weekdays: [0], timeOfDay: '02:00', durationMinutes: 30 }, timezone: 'UTC', dockerHostIDs: ['local'] } },
      { strategy: 'recur-day-of-month', label: 'Day of Month', extra: { recurMonthly: { daysOfMonth: [1], timeOfDay: '02:00', durationMinutes: 30 }, timezone: 'UTC', dockerHostIDs: ['local'] } },
    ]

    for (const s of strategies) {
      const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
        data: { title: `e2e-label-${s.strategy}`, active: false, strategy: s.strategy, ...s.extra },
      })
      expect(res.ok()).toBe(true)
      const created = await res.json() as { id: string }

      const [,] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/maintenance') && !r.url().includes('/active') && r.request().method() === 'GET'),
        openMaintenanceTab(page),
      ])
      const row = page.locator('#maintenanceTbody tr').filter({ hasText: `e2e-label-${s.strategy}` })
      await expect(row).toContainText(s.label)

      await cleanupWindowByApi(page, created.id)
    }
  })

  test('Single strategy: UI form saves with correct strategy label in list', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-ui-single')
    await page.locator('#maintStrategy').selectOption('single')
    const start = new Date(Date.now() + 3600000)
    const end   = new Date(Date.now() + 7200000)
    const toLocal = (d: Date) => d.toISOString().slice(0, 16)
    await page.locator('#maintSingleStart').fill(toLocal(start))
    await page.locator('#maintSingleEnd').fill(toLocal(end))
    await page.locator('#maintTargetTypeHosts').click()
    await page.locator('#maintHostChips button[data-maint-host-chip-id="local"]').click()
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/maintenance') && r.request().method() === 'POST'),
      page.locator('#saveMaintenanceBtn').click(),
    ])
    const created = await res.json() as { id: string }
    const row = page.locator('#maintenanceTbody tr').filter({ hasText: 'e2e-ui-single' })
    await expect(row).toContainText('Single Window')
    await cleanupWindowByApi(page, created.id)
  })

  test('Cron strategy: UI form saves with correct strategy label in list', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-ui-cron')
    await page.locator('#maintStrategy').selectOption('cron')
    await page.locator('#maintCronExpr').fill('0 2 * * *')
    await page.locator('#maintCronDuration').fill('60')
    await page.locator('#maintTargetTypeHosts').click()
    await page.locator('#maintHostChips button[data-maint-host-chip-id="local"]').click()
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/maintenance') && r.request().method() === 'POST'),
      page.locator('#saveMaintenanceBtn').click(),
    ])
    const created = await res.json() as { id: string }
    const row = page.locator('#maintenanceTbody tr').filter({ hasText: 'e2e-ui-cron' })
    await expect(row).toContainText('Cron')
    await cleanupWindowByApi(page, created.id)
  })

  test('Interval strategy: UI form saves with correct strategy label in list', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-ui-interval')
    await page.locator('#maintStrategy').selectOption('recur-interval')
    await page.locator('#maintIntervalDays').fill('7')
    await page.locator('#maintIntervalTime').fill('02:00')
    await page.locator('#maintIntervalDuration').fill('30')
    await page.locator('#maintTargetTypeHosts').click()
    await page.locator('#maintHostChips button[data-maint-host-chip-id="local"]').click()
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/maintenance') && r.request().method() === 'POST'),
      page.locator('#saveMaintenanceBtn').click(),
    ])
    const created = await res.json() as { id: string }
    const row = page.locator('#maintenanceTbody tr').filter({ hasText: 'e2e-ui-interval' })
    await expect(row).toContainText('Interval')
    await cleanupWindowByApi(page, created.id)
  })

  test('Day of Week strategy: UI form saves with correct strategy label in list', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-ui-weekly')
    await page.locator('#maintStrategy').selectOption('recur-day-of-week')
    await page.locator('#maintWeekdayChips button').first().click()
    await page.locator('#maintWeeklyTime').fill('03:00')
    await page.locator('#maintWeeklyDuration').fill('45')
    await page.locator('#maintTargetTypeHosts').click()
    await page.locator('#maintHostChips button[data-maint-host-chip-id="local"]').click()
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/maintenance') && r.request().method() === 'POST'),
      page.locator('#saveMaintenanceBtn').click(),
    ])
    const created = await res.json() as { id: string }
    const row = page.locator('#maintenanceTbody tr').filter({ hasText: 'e2e-ui-weekly' })
    await expect(row).toContainText('Day of Week')
    await cleanupWindowByApi(page, created.id)
  })

  test('Day of Month strategy: UI form saves with correct strategy label in list', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-ui-monthly')
    await page.locator('#maintStrategy').selectOption('recur-day-of-month')
    await page.locator('#maintMonthDayChips button').first().click()
    await page.locator('#maintMonthlyTime').fill('01:00')
    await page.locator('#maintMonthlyDuration').fill('120')
    await page.locator('#maintTargetTypeHosts').click()
    await page.locator('#maintHostChips button[data-maint-host-chip-id="local"]').click()
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/maintenance') && r.request().method() === 'POST'),
      page.locator('#saveMaintenanceBtn').click(),
    ])
    const created = await res.json() as { id: string }
    const row = page.locator('#maintenanceTbody tr').filter({ hasText: 'e2e-ui-monthly' })
    await expect(row).toContainText('Day of Month')
    await cleanupWindowByApi(page, created.id)
  })
})

// ── API negative validation ───────────────────────────────────────────────────

test.describe('Maintenance — API negative validation', () => {
  test('Missing title → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { strategy: 'manual', timezone: 'UTC', dockerHostIDs: ['local'] },
    })
    expect(res.status()).toBe(400)
  })

  test('Unknown strategy → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-bad-strategy', strategy: 'not-a-strategy', timezone: 'UTC', dockerHostIDs: ['local'] },
    })
    expect(res.status()).toBe(400)
  })

  test('Empty strategy string → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-empty-strategy', strategy: '', timezone: 'UTC', dockerHostIDs: ['local'] },
    })
    expect(res.status()).toBe(400)
  })

  test('Unknown timezone → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-bad-tz', strategy: 'manual', timezone: 'Not/AValidTimezone', dockerHostIDs: ['local'] },
    })
    expect(res.status()).toBe(400)
  })

  // ── Manual ─────────────────────────────────────────────────────────────────

  test('Manual: missing timezone → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-manual-no-tz', strategy: 'manual', dockerHostIDs: ['local'] },
    })
    expect(res.status()).toBe(400)
  })

  // ── Single Window ──────────────────────────────────────────────────────────

  test('Single: missing single object → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-single-no-obj', strategy: 'single', dockerHostIDs: ['local'] },
    })
    expect(res.status()).toBe(400)
  })

  test('Single: end equals start → 400', async ({ page }) => {
    await gotoApp(page)
    const t = new Date(Date.now() + 3600000).toISOString()
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-single-end-eq-start',
        strategy: 'single',
        single: { start: t, end: t },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Single: end before start → 400', async ({ page }) => {
    await gotoApp(page)
    const start = new Date(Date.now() + 7200000).toISOString()
    const end   = new Date(Date.now() + 3600000).toISOString()
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-single-end-before-start',
        strategy: 'single',
        single: { start, end },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  // ── Cron ───────────────────────────────────────────────────────────────────

  test('Cron: missing cron object → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-cron-no-obj', strategy: 'cron', timezone: 'UTC', dockerHostIDs: ['local'] },
    })
    expect(res.status()).toBe(400)
  })

  test('Cron: invalid expression → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-cron-bad-expr',
        strategy: 'cron',
        timezone: 'UTC',
        cron: { expression: 'not-a-cron', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Cron: zero duration → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-cron-zero-dur',
        strategy: 'cron',
        timezone: 'UTC',
        cron: { expression: '0 2 * * *', durationMinutes: 0 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Cron: negative duration → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-cron-neg-dur',
        strategy: 'cron',
        timezone: 'UTC',
        cron: { expression: '0 2 * * *', durationMinutes: -10 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Cron: missing timezone → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-cron-no-tz',
        strategy: 'cron',
        cron: { expression: '0 2 * * *', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  // ── Recurring Interval ─────────────────────────────────────────────────────

  test('Interval: missing recurInterval object → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-interval-no-obj', strategy: 'recur-interval', timezone: 'UTC', dockerHostIDs: ['local'] },
    })
    expect(res.status()).toBe(400)
  })

  test('Interval: everyDays = 0 → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-interval-zero-days',
        strategy: 'recur-interval',
        timezone: 'UTC',
        recurInterval: { everyDays: 0, timeOfDay: '02:00', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Interval: invalid timeOfDay format → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-interval-bad-time',
        strategy: 'recur-interval',
        timezone: 'UTC',
        recurInterval: { everyDays: 7, timeOfDay: '25:00', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Interval: zero duration → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-interval-zero-dur',
        strategy: 'recur-interval',
        timezone: 'UTC',
        recurInterval: { everyDays: 7, timeOfDay: '02:00', durationMinutes: 0 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  // ── Day of Week ────────────────────────────────────────────────────────────

  test('Day of Week: empty weekdays array → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-weekly-empty',
        strategy: 'recur-day-of-week',
        timezone: 'UTC',
        recurWeekly: { weekdays: [], timeOfDay: '02:00', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Day of Week: weekday 7 is out of range → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-weekly-day7',
        strategy: 'recur-day-of-week',
        timezone: 'UTC',
        recurWeekly: { weekdays: [7], timeOfDay: '02:00', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Day of Week: negative weekday → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-weekly-neg-day',
        strategy: 'recur-day-of-week',
        timezone: 'UTC',
        recurWeekly: { weekdays: [-1], timeOfDay: '02:00', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Day of Week: missing recurWeekly object → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-weekly-no-obj', strategy: 'recur-day-of-week', timezone: 'UTC', dockerHostIDs: ['local'] },
    })
    expect(res.status()).toBe(400)
  })

  test('Day of Week: invalid timeOfDay → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-weekly-bad-time',
        strategy: 'recur-day-of-week',
        timezone: 'UTC',
        recurWeekly: { weekdays: [1], timeOfDay: 'noon', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Day of Week: zero duration → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-weekly-zero-dur',
        strategy: 'recur-day-of-week',
        timezone: 'UTC',
        recurWeekly: { weekdays: [1], timeOfDay: '02:00', durationMinutes: 0 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  // ── Day of Month ───────────────────────────────────────────────────────────

  test('Day of Month: empty daysOfMonth array → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-monthly-empty',
        strategy: 'recur-day-of-month',
        timezone: 'UTC',
        recurMonthly: { daysOfMonth: [], timeOfDay: '02:00', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Day of Month: day 0 out of range → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-monthly-day0',
        strategy: 'recur-day-of-month',
        timezone: 'UTC',
        recurMonthly: { daysOfMonth: [0], timeOfDay: '02:00', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Day of Month: day 32 out of range → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-monthly-day32',
        strategy: 'recur-day-of-month',
        timezone: 'UTC',
        recurMonthly: { daysOfMonth: [32], timeOfDay: '02:00', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Day of Month: missing recurMonthly object → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-monthly-no-obj', strategy: 'recur-day-of-month', timezone: 'UTC', dockerHostIDs: ['local'] },
    })
    expect(res.status()).toBe(400)
  })

  test('Day of Month: invalid timeOfDay → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-monthly-bad-time',
        strategy: 'recur-day-of-month',
        timezone: 'UTC',
        recurMonthly: { daysOfMonth: [1], timeOfDay: '24:00', durationMinutes: 30 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Day of Month: zero duration → 400', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-monthly-zero-dur',
        strategy: 'recur-day-of-month',
        timezone: 'UTC',
        recurMonthly: { daysOfMonth: [1], timeOfDay: '02:00', durationMinutes: 0 },
        dockerHostIDs: ['local'],
      },
    })
    expect(res.status()).toBe(400)
  })

  // ── Effective date range ───────────────────────────────────────────────────

  test('Effective range: effectiveTo before effectiveFrom → 400', async ({ page }) => {
    await gotoApp(page)
    const from = new Date(Date.now() + 7200000).toISOString()
    const to   = new Date(Date.now() + 3600000).toISOString()
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-eff-range-reversed',
        strategy: 'cron',
        timezone: 'UTC',
        cron: { expression: '0 2 * * *', durationMinutes: 30 },
        dockerHostIDs: ['local'],
        effectiveFrom: from,
        effectiveTo: to,
      },
    })
    expect(res.status()).toBe(400)
  })

  test('Effective range: effectiveTo equal to effectiveFrom → 400', async ({ page }) => {
    await gotoApp(page)
    const t = new Date(Date.now() + 3600000).toISOString()
    const res = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-eff-range-equal',
        strategy: 'cron',
        timezone: 'UTC',
        cron: { expression: '0 2 * * *', durationMinutes: 30 },
        dockerHostIDs: ['local'],
        effectiveFrom: t,
        effectiveTo: t,
      },
    })
    expect(res.status()).toBe(400)
  })

  // ── Non-existent resources ─────────────────────────────────────────────────

  test('DELETE non-existent window → 404', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.delete(`${BASE_URL}/api/maintenance/maint-nonexistent-00000000000`)
    expect(res.status()).toBe(404)
  })
})

// ── UI form client-side validation ────────────────────────────────────────────

test.describe('Maintenance — UI form validation', () => {
  test('Jobs tab: save with no job selected shows error', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-no-job')
    // Jobs tab active by default — click save without picking any job
    await page.locator('#saveMaintenanceBtn').click()
    await expect(page.locator('#maintJobChipsError')).not.toBeEmpty()
  })

  test('Hosts tab: save with no host selected shows error', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-no-host')
    await page.locator('#maintTargetTypeHosts').click()
    // Switch to Hosts tab but pick no host
    await page.locator('#saveMaintenanceBtn').click()
    await expect(page.locator('#maintHostChipsError')).not.toBeEmpty()
  })

  test('Cron: save with empty expression shows error', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-cron-empty-expr')
    await page.locator('#maintStrategy').selectOption('cron')
    await page.locator('#maintTargetTypeHosts').click()
    await page.locator('#maintHostChips button[data-maint-host-chip-id="local"]').click()
    // Leave cron expression empty
    await page.locator('#saveMaintenanceBtn').click()
    await expect(page.locator('#maintCronExprError')).not.toBeEmpty()
  })

  test('Day of Week: save with no weekday selected shows error', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-weekly-no-day')
    await page.locator('#maintStrategy').selectOption('recur-day-of-week')
    await page.locator('#maintWeeklyTime').fill('02:00')
    await page.locator('#maintWeeklyDuration').fill('30')
    await page.locator('#maintTargetTypeHosts').click()
    await page.locator('#maintHostChips button[data-maint-host-chip-id="local"]').click()
    // Leave no weekday selected
    await page.locator('#saveMaintenanceBtn').click()
    await expect(page.locator('#maintWeekdayChipsError')).not.toBeEmpty()
  })

  test('Day of Month: save with no day selected shows error', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-monthly-no-day')
    await page.locator('#maintStrategy').selectOption('recur-day-of-month')
    await page.locator('#maintMonthlyTime').fill('02:00')
    await page.locator('#maintMonthlyDuration').fill('30')
    await page.locator('#maintTargetTypeHosts').click()
    await page.locator('#maintHostChips button[data-maint-host-chip-id="local"]').click()
    // Leave no month day selected
    await page.locator('#saveMaintenanceBtn').click()
    await expect(page.locator('#maintMonthDayChipsError')).not.toBeEmpty()
  })

  test('Single: save with end before start shows error', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintTitle').fill('e2e-single-bad-range')
    await page.locator('#maintStrategy').selectOption('single')
    const toLocal = (d: Date) => d.toISOString().slice(0, 16)
    const earlier = toLocal(new Date(Date.now() + 3600000))
    const later   = toLocal(new Date(Date.now() + 7200000))
    await page.locator('#maintSingleStart').fill(later)    // start is later
    await page.locator('#maintSingleEnd').fill(earlier)    // end is earlier
    await page.locator('#maintTargetTypeHosts').click()
    await page.locator('#maintHostChips button[data-maint-host-chip-id="local"]').click()
    await page.locator('#saveMaintenanceBtn').click()
    await expect(page.locator('#maintSingleEndError')).not.toBeEmpty()
  })
})

// ── Info-tips ─────────────────────────────────────────────────────────────────

test.describe('Maintenance — Info-tips', () => {
  async function expectTipVisible(page: Page, icon: ReturnType<Page['locator']>) {
    await icon.scrollIntoViewIfNeeded()
    await page.waitForTimeout(150) // let any scroll settle
    await icon.dispatchEvent('mouseover')
    const bubble = page.locator('#infoTipBubble')
    await expect(bubble).toHaveClass(/visible/)
    await expect(bubble).not.toHaveText('')
  }

  test('"When this window starts" info-tip appears on hover', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    const icon = page.locator('#maintOnStart').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
  })

  test('"Active" info-tip appears on hover', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    const icon = page.locator('#maintActive').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
  })

  test('"Schedule Strategy" info-tip appears on hover', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    const icon = page.locator('#maintStrategy').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
  })

  test('"End" info-tip appears on hover in Single Window panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('single')
    const icon = page.locator('#maintSingleEnd').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
    const bubble = page.locator('#infoTipBubble')
    await expect(bubble).toContainText('auto-prune')
  })

  test('"Duration" info-tip appears on hover in Cron panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('cron')
    const icon = page.locator('#maintCronDuration').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
    await expect(page.locator('#infoTipBubble')).toContainText('pause lasts')
  })

  test('"Duration" info-tip appears in Interval panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('recur-interval')
    const icon = page.locator('#maintIntervalDuration').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
  })

  test('"Duration" info-tip appears in Weekly panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('recur-day-of-week')
    const icon = page.locator('#maintWeeklyDuration').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
  })

  test('"Duration" info-tip appears in Monthly panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('recur-day-of-month')
    const icon = page.locator('#maintMonthlyDuration').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
  })

  test('"Time of day" info-tip appears in Interval panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('recur-interval')
    const icon = page.locator('#maintIntervalTime').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
    await expect(page.locator('#infoTipBubble')).toContainText('clock time')
  })

  test('"Time of day" info-tip appears in Weekly panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('recur-day-of-week')
    const icon = page.locator('#maintWeeklyTime').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
  })

  test('"Time of day" info-tip appears in Monthly panel', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('recur-day-of-month')
    const icon = page.locator('#maintMonthlyTime').locator('..').locator('.info-tip')
    await expectTipVisible(page, icon)
  })

  test('"Timezone" info-tip appears when timezone field is visible', async ({ page }) => {
    await openMaintenanceTab(page)
    await openAddForm(page)
    await page.locator('#maintStrategy').selectOption('cron')
    await expect(page.locator('#maintTimezoneWrap')).toBeVisible()
    const icon = page.locator('#maintTimezoneWrap .info-tip')
    await expectTipVisible(page, icon)
    await expect(page.locator('#infoTipBubble')).toContainText('timezone')
  })
})

// ── Affects column ────────────────────────────────────────────────────────────

test.describe('Maintenance — Affects column', () => {
  test('"Full App" shown when local host is the sole target', async ({ page }) => {
    await gotoApp(page)
    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-full-app-test',
        active: false,
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    const created = await createRes.json() as { id: string }

    await openMaintenanceTab(page)
    const row = page.locator('#maintenanceTbody tr').filter({ hasText: 'e2e-full-app-test' })
    await expect(row).toContainText('Full App')

    await cleanupWindowByApi(page, created.id)
  })

  test('Last/Next occurrence row renders under window title', async ({ page }) => {
    await gotoApp(page)
    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-last-next-test',
        active: false,
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    const created = await createRes.json() as { id: string }

    await openMaintenanceTab(page)
    const row = page.locator('#maintenanceTbody tr').filter({ hasText: 'e2e-last-next-test' })
    // The hint div shows "Last: … · Next: …"
    await expect(row.locator('.hint')).toContainText('Last:')
    await expect(row.locator('.hint')).toContainText('Next:')

    await cleanupWindowByApi(page, created.id)
  })
})

// ── Bell lifecycle alerts ─────────────────────────────────────────────────────

test.describe('Maintenance — Bell lifecycle alert types', () => {
  test('/api/system-alerts can return maintenance_started alerts', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.get(`${BASE_URL}/api/system-alerts`)
    const body = await res.json() as { alerts: Array<{ type: string; id: string }> }
    // Alert types are registered; even if none are active we can verify the API schema
    expect(Array.isArray(body.alerts)).toBe(true)
    // If any maintenance_started alert exists its ID must follow the stable format
    const started = body.alerts.filter(a => a.type === 'maintenance_started')
    for (const a of started) {
      expect(a.id).toMatch(/^maint-started-/)
    }
  })

  test('/api/system-alerts can return maintenance_ended alerts', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.get(`${BASE_URL}/api/system-alerts`)
    const body = await res.json() as { alerts: Array<{ type: string; id: string }> }
    const ended = body.alerts.filter(a => a.type === 'maintenance_ended')
    for (const a of ended) {
      expect(a.id).toMatch(/^maint-ended-/)
    }
  })
})

// ── Bell lifecycle UI ─────────────────────────────────────────────────────────

test.describe('Maintenance — Bell lifecycle UI', () => {
  async function openBell(page: Page): Promise<void> {
    await page.locator('#notifBellBtn').click()
    await expect(page.locator('#notifBellDropdown')).not.toHaveCSS('display', 'none')
  }

  async function closeBell(page: Page): Promise<void> {
    const style = await page.locator('#notifBellDropdown').getAttribute('style') ?? ''
    if (!style.includes('none')) await page.locator('#notifBellBtn').click()
  }

  // Polls /api/system-alerts every 2s until an alert of `type` whose ID
  // contains `windowId` appears, or the timeout elapses.
  async function pollForAlert(
    page: Page, type: string, windowId: string, timeoutMs = 30_000,
  ): Promise<{ id: string; message: string } | null> {
    const deadline = Date.now() + timeoutMs
    while (Date.now() < deadline) {
      const res = await page.request.get(`${BASE_URL}/api/system-alerts`)
      const body = await res.json() as { alerts: Array<{ id: string; type: string; message: string }> }
      const found = body.alerts.find(a => a.type === type && a.id.includes(windowId))
      if (found) return found
      await page.waitForTimeout(2000)
    }
    return null
  }

  test('maintenance_upcoming alert renders in bell with 🛠️ icon', async ({ page }) => {
    await gotoApp(page)
    // Single window starting 30 min from now — sits inside the 2h reminder bucket.
    // upcoming alerts are computed live on each GET, so no scan cycle is needed.
    const start = new Date(Date.now() + 30 * 60 * 1000)
    const end = new Date(start.getTime() + 5 * 60 * 1000)
    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-bell-upcoming',
        active: true,
        strategy: 'single',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
        single: { start: start.toISOString(), end: end.toISOString() },
      },
    })
    const created = await createRes.json() as { id: string }

    try {
      const alertsRes = await page.request.get(`${BASE_URL}/api/system-alerts`)
      const body = await alertsRes.json() as { alerts: Array<{ type: string; id: string }> }
      const upcoming = body.alerts.find(a => a.type === 'maintenance_upcoming' && a.id.includes(created.id))
      expect(upcoming).toBeTruthy()

      await page.reload()
      await expect(page.locator('#themeToggleBtn')).toBeVisible()
      await openBell(page)
      await page.waitForTimeout(600)

      const listText = await page.locator('#notifBellList').innerText()
      expect(listText).toContain('🛠️')
      expect(listText).toContain('Upcoming maintenance') // label in renderNotifBellList
    } finally {
      try { await closeBell(page) } catch { /* page may be closed */ }
      await cleanupWindowByApi(page, created.id)
    }
  })

  // Creates a short-interval enabled job to keep the monitor scan cycle fast.
  // spec-08 deletes e2e-monitor, so by spec-13 there may be no enabled jobs and
  // timeUntilNextJobDue falls back to 60s.  The scan driver keeps cycles at 5s.
  // The maintenance windows target a synthetic fake job ID (not the driver's real
  // ID) so the driver is never added to pausedJobIDs and keeps driving scans.
  // JobIDs values are not validated against real jobs, so a synthetic ID is fine.
  async function createScanDriver(page: Page): Promise<string> {
    const res = await page.request.post(`${BASE_URL}/api/jobs`, {
      data: { name: 'e2e-bell-scan-driver', action: 'restart', containerNameFilter: 'e2e-nonexistent-bell', monitorIntervalSeconds: 5, enabled: true },
    })
    return (await res.json() as { id: string }).id
  }

  test('maintenance_started alert renders in bell with ⏸️ icon', async ({ page }) => {
    test.setTimeout(60_000)
    await gotoApp(page)
    const driverJobId = await createScanDriver(page)

    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-bell-started', active: true, strategy: 'manual', timezone: 'UTC', jobIDs: ['e2e-synthetic-target'] },
    })
    const created = await createRes.json() as { id: string }

    try {
      const alert = await pollForAlert(page, 'maintenance_started', created.id, 40_000)
      expect(alert).not.toBeNull()
      if (!alert) return

      await page.reload()
      await expect(page.locator('#themeToggleBtn')).toBeVisible()
      await openBell(page)
      await page.waitForTimeout(600)

      const listText = await page.locator('#notifBellList').innerText()
      expect(listText).toContain('⏸️')
      expect(listText).toContain('Maintenance started')

      await page.request.post(`${BASE_URL}/api/system-alerts/dismiss`, { data: { ids: [alert.id] } })
    } finally {
      try { await closeBell(page) } catch { /* page may be closed */ }
      await cleanupWindowByApi(page, created.id)
      await page.request.delete(`${BASE_URL}/api/jobs/${driverJobId}`).catch(() => null)
    }
  })

  test('maintenance_ended alert renders in bell with ▶️ icon after window is deactivated', async ({ page }) => {
    // Two polls of 30s each; driver job keeps scan cycle at 5s so 30s covers 6+ cycles.
    test.setTimeout(90_000)
    await gotoApp(page)
    const driverJobId = await createScanDriver(page)

    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: { title: 'e2e-bell-ended', active: true, strategy: 'manual', timezone: 'UTC', jobIDs: ['e2e-synthetic-target'] },
    })
    const created = await createRes.json() as { id: string }

    let startedId = ''
    try {
      const startedAlert = await pollForAlert(page, 'maintenance_started', created.id, 30_000)
      expect(startedAlert).not.toBeNull()
      if (!startedAlert) return
      startedId = startedAlert.id

      // Delete window — next scan (≤5s away) detects it's gone and emits "ended".
      await cleanupWindowByApi(page, created.id)

      const endedAlert = await pollForAlert(page, 'maintenance_ended', created.id, 30_000)
      expect(endedAlert).not.toBeNull()
      if (!endedAlert) return

      await page.reload()
      await expect(page.locator('#themeToggleBtn')).toBeVisible()
      await openBell(page)
      await page.waitForTimeout(600)

      const listText = await page.locator('#notifBellList').innerText()
      expect(listText).toContain('▶️')
      expect(listText).toContain('Maintenance ended')

      const toDismiss = [endedAlert.id, ...(startedId ? [startedId] : [])]
      await page.request.post(`${BASE_URL}/api/system-alerts/dismiss`, { data: { ids: toDismiss } })
    } finally {
      try { await closeBell(page) } catch { /* page may be closed */ }
      await cleanupWindowByApi(page, created.id) // no-op if already deleted above
      await page.request.delete(`${BASE_URL}/api/jobs/${driverJobId}`).catch(() => null)
    }
  })
})

// ── Active-window dashboard banner ───────────────────────────────────────────

test.describe('Maintenance — Active window banner', () => {
  test('/api/maintenance/active returns windows array', async ({ page }) => {
    await gotoApp(page)
    const res = await page.request.get(`${BASE_URL}/api/maintenance/active`)
    expect(res.ok()).toBe(true)
    const body = await res.json() as { windows: unknown[] }
    expect(Array.isArray(body.windows)).toBe(true)
  })

  test('Active Manual window appears in /api/maintenance/active', async ({ page }) => {
    await gotoApp(page)
    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-active-banner-test',
        active: true,
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    const created = await createRes.json() as { id: string }

    const activeRes = await page.request.get(`${BASE_URL}/api/maintenance/active`)
    const active = await activeRes.json() as { windows: Array<{ id: string }> }
    expect(active.windows.some(w => w.id === created.id)).toBe(true)

    await cleanupWindowByApi(page, created.id)
  })

  test('Inactive window does not appear in /api/maintenance/active', async ({ page }) => {
    await gotoApp(page)
    const createRes = await page.request.post(`${BASE_URL}/api/maintenance`, {
      data: {
        title: 'e2e-inactive-not-active',
        active: false,
        strategy: 'manual',
        timezone: 'UTC',
        dockerHostIDs: ['local'],
      },
    })
    const created = await createRes.json() as { id: string }

    const activeRes = await page.request.get(`${BASE_URL}/api/maintenance/active`)
    const active = await activeRes.json() as { windows: Array<{ id: string }> }
    expect(active.windows.some(w => w.id === created.id)).toBe(false)

    await cleanupWindowByApi(page, created.id)
  })
})
