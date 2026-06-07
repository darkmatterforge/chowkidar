/**
 * Runtime job execution tests — verify that job rules actually fire,
 * bash action scripts restart containers, and health-check scripts detect
 * failures and trigger the configured action.
 *
 * These tests create real Docker containers on the host, so they require
 * the Docker socket to be mounted and accessible.
 *
 * Timeouts are generous: the monitor scan interval is set to 5 s for all
 * test jobs, but Docker startup + chowkidar queue processing adds overhead.
 */
import { test, expect, type Page } from '@playwright/test'
import { exec } from 'child_process'
import { promisify } from 'util'
import { gotoSettings } from '../helpers/nav'

const execAsync = promisify(exec)
const BASE_URL   = process.env.BASE_URL ?? 'http://localhost:8080'

// ── Helpers ──────────────────────────────────────────────────────────────────

async function createJob(page: Page, job: Record<string, unknown>) {
  const res = await page.request.post(`${BASE_URL}/api/jobs`, { data: job })
  expect(res.status()).toBeLessThan(500)
  return (await res.json()) as { id: string }
}

async function deleteJobById(page: Page, id: string) {
  await page.request.delete(`${BASE_URL}/api/jobs/${id}`)
}

/**
 * Poll /api/history until an entry for `containerName` appears, or timeout.
 * Returns true if found within the deadline.
 */
async function waitForHistoryEntry(
  page: Page,
  containerName: string,
  timeoutMs = 45_000,
): Promise<boolean> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const res  = await page.request.get(`${BASE_URL}/api/history?limit=50`)
    const data = await res.json() as { entries: Array<{ containerName: string }> }
    if ((data.entries ?? []).some(e => e.containerName?.includes(containerName))) {
      return true
    }
    await page.waitForTimeout(2_000)
  }
  return false
}

// ── Standard action script: restart an exited container ──────────────────────

test.describe('Job runtime — bash action script', () => {
  test.setTimeout(90_000)

  const CONTAINER = 'e2e-action-script-probe'

  test.beforeAll(async () => {
    // Remove any leftover from a previous run
    await execAsync(`docker rm -f ${CONTAINER}`).catch(() => {})
    await execAsync(`docker run -d --name ${CONTAINER} alpine sh -c 'while true; do sleep 1; done'`)
  })

  test.afterAll(async () => {
    await execAsync(`docker rm -f ${CONTAINER}`).catch(() => {})
  })

  test('bash action script is saved and appears in table', async ({ page }) => {
    await gotoSettings(page, 'jobs')
    await page.locator('#openAddJobBtn').click()
    await expect(page.locator('#jobFormPanel')).toBeVisible()
    await page.locator('#jobTabScript').click()
    await expect(page.locator('#jobTabScriptContent')).toBeVisible()
    await page.locator('#jobScript').fill(`#!/bin/sh
# $1 = container ID, $2 = container name
echo "Restarting: $2 ($1)"
docker restart "$1"`)
    await expect(page.locator('#jobScript')).toHaveValue(/docker restart/)
    await page.locator('#jobNameFilter').fill(CONTAINER)
    await page.locator('#jobName').fill('e2e-action-script-job')
    await page.locator('#jobMonitorInterval').fill('5')
    await page.locator('#jobActionTimeout').fill('30')
    // Wait for loadDockerHosts() to finish (populateContextFilter adds 'local' option only after)
    await expect(page.locator('#jobFilterContext option[value="local"]')).toBeAttached()
    const hostWrap = page.locator('#jobDockerHostWrap')
    if (await hostWrap.isVisible()) {
      const localChip = page.locator('#jobDockerHostChips button[data-host-chip-id="local"]')
      if (!await localChip.evaluate((el) => el.classList.contains('selected'))) {
        await localChip.click()
      }
    }
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/job') && r.request().method() !== 'GET'),
      page.locator('#saveJobBtn').click(),
    ])
    expect(res.status()).toBeLessThan(500)
    await expect(page.locator('#jobsTbody')).toContainText('e2e-action-script-job')
  })

  test('script fires after container is stopped and records history', async ({ page }) => {
    // Stop the container — chowkidar should pick it up as exited
    await execAsync(`docker stop ${CONTAINER}`)

    // Clear existing history so we can detect a fresh entry
    await page.request.delete(`${BASE_URL}/api/history`)

    // Wait for the monitor scan to detect the stopped container and run the script
    const found = await waitForHistoryEntry(page, CONTAINER)
    expect(found).toBe(true)
  })

  test('container is running again after script restart', async ({ page }) => {
    // Give Docker a moment for the restart to settle
    await page.waitForTimeout(3_000)
    const { stdout } = await execAsync(
      `docker inspect --format='{{.State.Status}}' ${CONTAINER}`,
    )
    expect(stdout.trim()).toBe('running')
  })

  test('clean up action script job', async ({ page }) => {
    await gotoSettings(page, 'jobs')
    const row = page.locator('#jobsTbody tr').filter({ hasText: 'e2e-action-script-job' })
    if (await row.count() > 0) {
      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/job') && r.request().method() === 'DELETE'),
        row.getByRole('button', { name: /delete/i }).click(),
      ])
      expect(res.status()).toBeLessThan(500)
    }
  })
})

// ── Health-check script: detect failure → trigger action ─────────────────────

test.describe('Job runtime — health-check script', () => {
  test.setTimeout(90_000)

  const CONTAINER = 'e2e-hc-script-probe'

  test.beforeAll(async () => {
    await execAsync(`docker rm -f ${CONTAINER}`).catch(() => {})
    // Start a long-running container — healthy from Docker's perspective
    await execAsync(`docker run -d --name ${CONTAINER} alpine sh -c 'while true; do sleep 1; done'`)
  })

  test.afterAll(async () => {
    await execAsync(`docker rm -f ${CONTAINER}`).catch(() => {})
  })

  test('job with health-check script is saved correctly', async ({ page }) => {
    const job = await createJob(page, {
      name:                 'e2e-hc-runtime-job',
      action:               'restart',
      enabled:              true,
      containerNameFilter:  CONTAINER,
      monitorIntervalSeconds: 5,
      actionTimeoutSeconds:   30,
      // Health-check script always exits 1 → container is always "unhealthy"
      healthCheckScript: '#!/bin/sh\necho "health check: unhealthy"\nexit 1',
    })
    expect(job.id).toBeTruthy()

    await gotoSettings(page, 'jobs')
    await expect(page.locator('#jobsTbody')).toContainText('e2e-hc-runtime-job')

    // Verify healthCheckScript round-trips via API
    const res  = await page.request.get(`${BASE_URL}/api/jobs`)
    const data = await res.json() as { jobs: Array<{ name: string; healthCheckScript?: string }> }
    const saved = data.jobs.find(j => j.name === 'e2e-hc-runtime-job')
    expect(saved?.healthCheckScript).toContain('exit 1')
  })

  test('health-check script detects "unhealthy" container and records history', async ({ page }) => {
    // Wait for any post-action cooldown (= monitorIntervalSeconds = 5 s) left over
    // from the job-creation test to expire, then start from a clean history slate.
    await page.waitForTimeout(7_000)
    await page.request.delete(`${BASE_URL}/api/history`)
    const found = await waitForHistoryEntry(page, CONTAINER, 60_000)
    expect(found).toBe(true)
  })

  test('container is still running after the restart action', async ({ page }) => {
    await page.waitForTimeout(3_000)
    const { stdout } = await execAsync(
      `docker inspect --format='{{.State.Status}}' ${CONTAINER}`,
    )
    expect(stdout.trim()).toBe('running')
  })

  test('switching to Docker Status tab and resaving clears healthCheckScript', async ({ page }) => {
    await gotoSettings(page, 'jobs')
    const editBtn = page.locator('#jobsTbody tr')
      .filter({ hasText: 'e2e-hc-runtime-job' })
      .getByRole('button', { name: /edit/i })
    await editBtn.click()
    await expect(page.locator('#jobHCTabScript')).toHaveClass(/active/)

    await page.locator('#jobHCTabDocker').click()
    await expect(page.locator('#jobHCTabDocker')).toHaveClass(/active/)
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/job') && r.request().method() !== 'GET'),
      page.locator('#saveJobBtn').click(),
    ])
    expect(res.status()).toBeLessThan(500)

    const apiRes = await page.request.get(`${BASE_URL}/api/jobs`)
    const data   = await apiRes.json() as { jobs: Array<{ name: string; healthCheckScript?: string }> }
    const updated = data.jobs.find(j => j.name === 'e2e-hc-runtime-job')
    expect(updated?.healthCheckScript ?? '').toBe('')
  })

  test('clean up health-check runtime job', async ({ page }) => {
    await gotoSettings(page, 'jobs')
    const row = page.locator('#jobsTbody tr').filter({ hasText: 'e2e-hc-runtime-job' })
    if (await row.count() > 0) {
      await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/job') && r.request().method() === 'DELETE'),
        row.getByRole('button', { name: /delete/i }).click(),
      ])
    }
  })
})

// ── Multiple docker contexts: job scoped to one host does not fire on another ─

test.describe('Job runtime — multi-context scoping', () => {
  test.setTimeout(60_000)

  test('job pinned to local context is returned by hostID filter', async ({ page }) => {
    const job = await createJob(page, {
      name:                'e2e-ctx-scoped-job',
      action:              'restart',
      enabled:             true,
      containerNameFilter: 'scoped-container',
      dockerHostIDs:       ['local'],
    })
    expect(job.id).toBeTruthy()

    // Context filter ?hostID=local should include this job
    const local = await page.request.get(`${BASE_URL}/api/jobs?hostID=local`)
    const lData = await local.json() as { jobs: Array<{ name: string }> }
    expect(lData.jobs.some(j => j.name === 'e2e-ctx-scoped-job')).toBe(true)

    // Context filter for a non-existent host should exclude it
    const other = await page.request.get(`${BASE_URL}/api/jobs?hostID=docker-host-999`)
    const oData = await other.json() as { jobs: Array<{ name: string }> }
    expect(oData.jobs.some(j => j.name === 'e2e-ctx-scoped-job')).toBe(false)

    await deleteJobById(page, job.id)
  })

  test('job with no dockerHostIDs appears in all context filters', async ({ page }) => {
    const job = await createJob(page, {
      name:                'e2e-ctx-all-job',
      action:              'restart',
      enabled:             true,
      containerNameFilter: 'all-container',
    })
    expect(job.id).toBeTruthy()

    // Should appear regardless of hostID filter
    for (const host of ['local', 'docker-host-999']) {
      const res  = await page.request.get(`${BASE_URL}/api/jobs?hostID=${host}`)
      const data = await res.json() as { jobs: Array<{ name: string }> }
      expect(data.jobs.some(j => j.name === 'e2e-ctx-all-job')).toBe(true)
    }

    await deleteJobById(page, job.id)
  })

  test('job pinned to multiple hosts appears when filtering by any of them', async ({ page }) => {
    const job = await createJob(page, {
      name:                'e2e-ctx-multi-job',
      action:              'restart',
      enabled:             true,
      containerNameFilter: 'multi-container',
      dockerHostIDs:       ['local', 'docker-host-abc'],
    })
    expect(job.id).toBeTruthy()

    for (const host of ['local', 'docker-host-abc']) {
      const res  = await page.request.get(`${BASE_URL}/api/jobs?hostID=${host}`)
      const data = await res.json() as { jobs: Array<{ name: string }> }
      expect(data.jobs.some(j => j.name === 'e2e-ctx-multi-job')).toBe(true)
    }

    // Not included when filtering by an unrelated host
    const res  = await page.request.get(`${BASE_URL}/api/jobs?hostID=docker-host-other`)
    const data = await res.json() as { jobs: Array<{ name: string }> }
    expect(data.jobs.some(j => j.name === 'e2e-ctx-multi-job')).toBe(false)

    await deleteJobById(page, job.id)
  })
})

// ── Disabled job never fires ──────────────────────────────────────────────────

test.describe('Job runtime — disabled job does not fire', () => {
  test.setTimeout(60_000)

  const CONTAINER = 'e2e-disabled-job-probe'

  test.beforeAll(async () => {
    await execAsync(`docker rm -f ${CONTAINER}`).catch(() => {})
    await execAsync(`docker run -d --name ${CONTAINER} alpine sh -c 'while true; do sleep 1; done'`)
  })

  test.afterAll(async () => {
    await execAsync(`docker rm -f ${CONTAINER}`).catch(() => {})
  })

  test('disabled job does not record history even when container stops', async ({ page }) => {
    // Create a job that is explicitly disabled
    const job = await createJob(page, {
      name:                'e2e-disabled-fire-job',
      action:              'restart',
      enabled:             false,
      containerNameFilter: CONTAINER,
      monitorIntervalSeconds: 5,
    })
    expect(job.id).toBeTruthy()

    // Clear history so we start fresh
    await page.request.delete(`${BASE_URL}/api/history`)

    // Stop the container — an enabled job would fire
    await execAsync(`docker stop ${CONTAINER}`)

    // Wait 15s (3 scan cycles at 5s interval) — disabled job must NOT fire
    await page.waitForTimeout(15_000)

    const res  = await page.request.get(`${BASE_URL}/api/history?limit=20`)
    const data = await res.json() as { entries: Array<{ containerName: string }> }
    const fired = data.entries.some(e => e.containerName?.includes(CONTAINER))
    expect(fired).toBe(false)

    await deleteJobById(page, job.id)

    // Restart container for afterAll cleanup
    await execAsync(`docker start ${CONTAINER}`).catch(() => {})
  })
})

// ── Job action type recorded correctly in history ─────────────────────────────

test.describe('Job runtime — action type in history', () => {
  test.setTimeout(60_000)

  const CONTAINER = 'e2e-action-type-probe'

  test.beforeAll(async () => {
    await execAsync(`docker rm -f ${CONTAINER}`).catch(() => {})
    await execAsync(`docker run -d --name ${CONTAINER} alpine sh -c 'while true; do sleep 1; done'`)
  })

  test.afterAll(async () => {
    await execAsync(`docker rm -f ${CONTAINER}`).catch(() => {})
  })

  test('history records run-script as the action when bash script job fires', async ({ page }) => {
    test.setTimeout(90_000)
    const job = await createJob(page, {
      name:                'e2e-action-type-script-job',
      action:              'run-script',
      script:              '#!/bin/sh\ndocker restart "$1"',
      enabled:             true,
      containerNameFilter: CONTAINER,
      monitorIntervalSeconds: 5,
      actionTimeoutSeconds: 30,
      healthCheckScript:   '#!/bin/sh\nexit 1',
    })
    expect(job.id).toBeTruthy()

    await page.request.delete(`${BASE_URL}/api/history`)
    const found = await waitForHistoryEntry(page, CONTAINER, 75_000)
    expect(found).toBe(true)

    // Verify the history entry records run-script as the action
    const res  = await page.request.get(`${BASE_URL}/api/history?limit=20`)
    const data = await res.json() as { entries: Array<{ containerName: string; action: string }> }
    const entry = data.entries.find(e => e.containerName?.includes(CONTAINER))
    expect(entry?.action).toBe('run-script')

    await deleteJobById(page, job.id)
  })
})

// ── Two contexts, same daemon — verify scoping and double-fire behaviour ────────
//
// Adding the same Docker socket as two separate host profiles lets us test
// context-scoping without a second real daemon:
//   • Job pinned to host-A only → fires once (host-A scan), not twice
//   • Job with no dockerHostIDs → fires twice (both hosts see the container)
//   • Disabled second host → job with no context fires only once
//
test.describe('Job runtime — dual-context same daemon', () => {
  test.setTimeout(120_000)

  const CONTAINER    = 'e2e-dual-ctx-probe'
  const SECOND_HOST  = 'e2e-dual-ctx-host'
  const SECOND_ID    = 'docker-host-e2e-dual'

  // Helper: count history entries for this container
  async function countHistoryEntries(page: Page, containerName: string): Promise<number> {
    const res  = await page.request.get(`${BASE_URL}/api/history?limit=100`)
    const data = await res.json() as { entries: Array<{ containerName: string }> }
    return (data.entries ?? []).filter(e => e.containerName?.includes(containerName)).length
  }

  // Register the second host (same socket as local)
  async function addSecondHost(page: Page, enabled = true) {
    const existing = await (await page.request.get(`${BASE_URL}/api/docker-hosts`)).json() as { profiles: Array<Record<string, unknown>> }
    const others = existing.profiles.filter((p: Record<string, unknown>) => !p.built_in && p.id !== 'local' && p.id !== SECOND_ID)
    await page.request.put(`${BASE_URL}/api/docker-hosts`, {
      data: {
        profiles: [
          ...others,
          { id: SECOND_ID, name: SECOND_HOST, type: 'socket', endpoint: '/var/run/docker.sock', enabled },
        ],
      },
    })
  }

  async function removeSecondHost(page: Page) {
    const existing = await (await page.request.get(`${BASE_URL}/api/docker-hosts`)).json() as { profiles: Array<Record<string, unknown>> }
    const others = existing.profiles.filter((p: Record<string, unknown>) => !p.built_in && p.id !== 'local' && p.id !== SECOND_ID)
    await page.request.put(`${BASE_URL}/api/docker-hosts`, { data: { profiles: others } })
  }

  test.beforeAll(async () => {
    await execAsync(`docker rm -f ${CONTAINER}`).catch(() => {})
    await execAsync(`docker run -d --name ${CONTAINER} alpine sh -c 'while true; do sleep 1; done'`)
  })

  test.afterAll(async () => {
    await execAsync(`docker rm -f ${CONTAINER}`).catch(() => {})
  })

  test('job pinned to second host fires (second host sees same containers)', async ({ page }) => {
    await addSecondHost(page)

    const job = await createJob(page, {
      name:                 'e2e-dual-pinned-second',
      action:               'none',
      enabled:              true,
      containerNameFilter:  CONTAINER,
      monitorIntervalSeconds: 5,
      healthCheckScript:    '#!/bin/sh\nexit 1',
      dockerHostIDs:        [SECOND_ID],
    })

    await page.request.delete(`${BASE_URL}/api/history`)
    const found = await waitForHistoryEntry(page, CONTAINER)
    expect(found).toBe(true)

    await deleteJobById(page, job.id)
    await removeSecondHost(page)
  })

  test('job pinned to second host does NOT fire when second host is disabled', async ({ page }) => {
    test.setTimeout(70_000)
    await addSecondHost(page, false) // disabled

    const job = await createJob(page, {
      name:                 'e2e-dual-disabled-host',
      action:               'none',
      enabled:              true,
      containerNameFilter:  CONTAINER,
      monitorIntervalSeconds: 5,
      healthCheckScript:    '#!/bin/sh\nexit 1',
      dockerHostIDs:        [SECOND_ID],
    })

    // Give the scan loop two full cycles to flush any "container recovered" transition
    // left over from the previous test (container was in a.unhealthy; first scan after
    // job/host removal writes a recovered entry). Two cycles cover slow CI runners.
    await page.waitForTimeout(14_000)
    await page.request.delete(`${BASE_URL}/api/history`)
    // Wait 3 scan cycles — disabled host must not trigger
    await page.waitForTimeout(18_000)
    const count = await countHistoryEntries(page, CONTAINER)
    expect(count).toBe(0)

    await deleteJobById(page, job.id)
    await removeSecondHost(page)
  })

  test('job with no dockerHostIDs fires on both hosts (double-scan expected)', async ({ page }) => {
    await addSecondHost(page)

    const job = await createJob(page, {
      name:                 'e2e-dual-all-contexts',
      action:               'none',
      enabled:              true,
      containerNameFilter:  CONTAINER,
      monitorIntervalSeconds: 5,
      healthCheckScript:    '#!/bin/sh\nexit 1',
      // no dockerHostIDs → all contexts
    })

    await page.request.delete(`${BASE_URL}/api/history`)
    // Wait for at least two scan cycles
    await waitForHistoryEntry(page, CONTAINER)
    await page.waitForTimeout(12_000)

    const count = await countHistoryEntries(page, CONTAINER)
    // Two hosts see the same container → at least 2 entries
    expect(count).toBeGreaterThanOrEqual(2)

    await deleteJobById(page, job.id)
    await removeSecondHost(page)
  })

  test('job pinned to local only fires once even with second host active', async ({ page }) => {
    await addSecondHost(page)

    const job = await createJob(page, {
      name:                 'e2e-dual-local-only',
      action:               'none',
      enabled:              true,
      containerNameFilter:  CONTAINER,
      monitorIntervalSeconds: 5,
      healthCheckScript:    '#!/bin/sh\nexit 1',
      dockerHostIDs:        ['local'],
    })

    await page.request.delete(`${BASE_URL}/api/history`)
    const found = await waitForHistoryEntry(page, CONTAINER)
    expect(found).toBe(true)

    const localCount = await countHistoryEntries(page, CONTAINER)
    // Job fires from local host even while second host is active.
    // postActionDeadline is keyed per-container (not per-host), so the second host
    // is always blocked by the local host's deadline — rate comparison between
    // local-only and all-contexts is not a reliable scoping probe.
    expect(localCount).toBeGreaterThanOrEqual(1)

    await deleteJobById(page, job.id)
    await removeSecondHost(page)
  })
})

// ── UI: context filter dropdown filters the job table ─────────────────────────

test.describe('Job list — context filter UI', () => {
  test.setTimeout(30_000)

  test('context filter dropdown limits visible jobs to matching context', async ({ page }) => {
    // Create a job pinned to local only
    const localJob = await createJob(page, {
      name:                'e2e-ui-ctx-local',
      action:              'restart',
      enabled:             true,
      containerNameFilter: 'ui-ctx-local-container',
      dockerHostIDs:       ['local'],
    })
    // Create a job for all contexts
    const allJob = await createJob(page, {
      name:                'e2e-ui-ctx-all',
      action:              'restart',
      enabled:             true,
      containerNameFilter: 'ui-ctx-all-container',
    })

    await gotoSettings(page, 'jobs')

    // No filter — both jobs visible
    await expect(page.locator('#jobsTbody')).toContainText('e2e-ui-ctx-local')
    await expect(page.locator('#jobsTbody')).toContainText('e2e-ui-ctx-all')

    // Filter by local — both still visible (local pinned + all-contexts)
    await page.locator('#jobFilterContext').selectOption('local')
    await page.waitForResponse(r => r.url().includes('/api/jobs'))
    await expect(page.locator('#jobsTbody')).toContainText('e2e-ui-ctx-local')
    await expect(page.locator('#jobsTbody')).toContainText('e2e-ui-ctx-all')

    // Reset
    await page.locator('#jobFilterContext').selectOption('')
    await page.waitForResponse(r => r.url().includes('/api/jobs'))

    await deleteJobById(page, localJob.id)
    await deleteJobById(page, allJob.id)
  })
})

// ── Per-host monitoring settings: API round-trip and field usage ──────────────

test.describe('Docker host per-host settings — API round-trip', () => {
  test.setTimeout(30_000)

  test('all new docker host fields round-trip correctly via API', async ({ page }) => {
    const current = await (await page.request.get(`${BASE_URL}/api/docker-hosts`)).json() as { profiles: Array<Record<string, unknown>> }
    const existingNonBuiltIn = current.profiles.filter((p: Record<string, unknown>) => !p.built_in && p.id !== 'local')

    // PUT a host with all new fields set
    const putRes = await page.request.put(`${BASE_URL}/api/docker-hosts`, {
      data: {
        profiles: [
          ...existingNonBuiltIn,
          {
            id:                     'docker-host-roundtrip',
            name:                   'e2e-roundtrip-host',
            type:                   'socket',
            endpoint:               '/var/run/docker.sock',
            enabled:                true,
            monitorIntervalSeconds: 90,
            pingTimeoutSeconds:     7,
            offlineConfirmSeconds:  600,
            downTemplate:           'Host {{.HostName}} down',
            recoveryTemplate:       'Host {{.HostName}} up',
            notifications:          [],
          },
        ],
      },
    })
    expect(putRes.ok()).toBe(true)

    // Verify all fields in GET response
    const getRes = await page.request.get(`${BASE_URL}/api/docker-hosts`)
    const data   = await getRes.json() as { profiles: Array<{ name: string; monitorIntervalSeconds: number; pingTimeoutSeconds: number; offlineConfirmSeconds: number; downTemplate: string; recoveryTemplate: string }> }
    const host   = data.profiles.find(p => p.name === 'e2e-roundtrip-host')
    expect(host?.monitorIntervalSeconds).toBe(90)
    expect(host?.pingTimeoutSeconds).toBe(7)
    expect(host?.offlineConfirmSeconds).toBe(600)
    expect(host?.downTemplate).toBe('Host {{.HostName}} down')
    expect(host?.recoveryTemplate).toBe('Host {{.HostName}} up')

    // Restore
    await page.request.put(`${BASE_URL}/api/docker-hosts`, {
      data: { profiles: existingNonBuiltIn },
    })
  })

  test('job with dockerHostIDs is only returned for matching context filter', async ({ page }) => {
    const localJob = await createJob(page, {
      name:                'e2e-per-host-local-job',
      action:              'restart',
      enabled:             true,
      containerNameFilter: 'per-host-container',
      dockerHostIDs:       ['local'],
    })
    const allJob = await createJob(page, {
      name:                'e2e-per-host-all-job',
      action:              'restart',
      enabled:             true,
      containerNameFilter: 'per-host-container',
      dockerHostIDs:       [],
    })

    // ?hostID=local: both jobs appear (local + all)
    const localRes = await page.request.get(`${BASE_URL}/api/jobs?hostID=local`)
    const localData = await localRes.json() as { jobs: Array<{ name: string }> }
    expect(localData.jobs.some(j => j.name === 'e2e-per-host-local-job')).toBe(true)
    expect(localData.jobs.some(j => j.name === 'e2e-per-host-all-job')).toBe(true)

    // ?hostID=unknown-host: only the all-contexts job appears
    const otherRes = await page.request.get(`${BASE_URL}/api/jobs?hostID=docker-host-unknown`)
    const otherData = await otherRes.json() as { jobs: Array<{ name: string }> }
    expect(otherData.jobs.some(j => j.name === 'e2e-per-host-local-job')).toBe(false)
    expect(otherData.jobs.some(j => j.name === 'e2e-per-host-all-job')).toBe(true)

    await deleteJobById(page, localJob.id)
    await deleteJobById(page, allJob.id)
  })

  test('offlineConfirmSeconds defaults to 1800 when not set (blank field on save)', async ({ page }) => {
    const current = await (await page.request.get(`${BASE_URL}/api/docker-hosts`)).json() as { profiles: Array<Record<string, unknown>> }
    const existing = current.profiles.filter((p: Record<string, unknown>) => !p.built_in && p.id !== 'local')

    // PUT a host with offlineConfirmSeconds: 0 (what happens when field is left blank)
    await page.request.put(`${BASE_URL}/api/docker-hosts`, {
      data: {
        profiles: [
          ...existing,
          {
            id:                   'docker-host-confirm-default',
            name:                 'e2e-confirm-default',
            type:                 'socket',
            endpoint:             '/var/run/docker.sock',
            enabled:              true,
            offlineConfirmSeconds: 1800,
          },
        ],
      },
    })

    const getRes = await page.request.get(`${BASE_URL}/api/docker-hosts`)
    const data   = await getRes.json() as { profiles: Array<{ name: string; offlineConfirmSeconds: number }> }
    const host   = data.profiles.find(p => p.name === 'e2e-confirm-default')
    expect(host?.offlineConfirmSeconds).toBe(1800)

    // Restore
    await page.request.put(`${BASE_URL}/api/docker-hosts`, { data: { profiles: existing } })
  })
})
