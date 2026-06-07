/**
 * Restart-persistence tests — kept in a separate file (99-) so they run last.
 * Container restart invalidates all in-memory sessions; restartAndReAuth()
 * re-authenticates via the login form after each restart so subsequent tests
 * in this file still work.
 *
 * Each describe block re-authenticates at the start because the preceding
 * restart may have invalidated the storageState session cookie.
 */
import { test, expect } from '@playwright/test'
import { exec } from 'child_process'
import { promisify } from 'util'
import { gotoApp, gotoSettings } from '../helpers/nav'
import { restartAndReAuth, BASE_URL, CONTAINER } from '../helpers/restart'

const execAsync = promisify(exec)

// ── Monitoring setting survives restart ───────────────────────────────────────

test.describe('Restart persistence — Monitoring', () => {
  test('httpClientTimeoutSeconds survives container restart', async ({ page }) => {
    await gotoSettings(page, 'monitoring')

    const input = page.locator('#httpClientTimeoutSeconds')
    const original = await input.inputValue()
    const newValue = original === '20' ? '25' : '20'

    await input.fill(newValue)
    const [res] = await Promise.all([
      page.waitForResponse(r => r.url().includes('/api/settings') && r.request().method() !== 'GET'),
      page.locator('#saveSettingsBtn').click(),
    ])
    expect(res.status()).toBeLessThan(500)

    await restartAndReAuth(page)

    const s = await (await page.request.get(`${BASE_URL}/api/settings`)).json()
    expect(String(s.httpClientTimeoutSeconds)).toBe(newValue)

    // Restore
    await page.request.put(`${BASE_URL}/api/settings`, {
      data: { ...s, httpClientTimeoutSeconds: Number(original) },
    })
  })
})

// ── General settings survive restart ─────────────────────────────────────────

test.describe('Restart persistence — General settings', () => {
  test('primaryBaseURL, externalHostname, displayTimezone, serverTimezone survive restart', async ({ page }) => {
    const testURL      = 'http://restart-test.local'
    const testHostname = 'restart-host'
    const testTZ       = 'Pacific/Auckland'

    // The preceding restart test invalidates the storageState session cookie.
    // Re-authenticate before making any API calls.
    await gotoApp(page)

    // Read current settings to use as base for the PUT
    const current = await (await page.request.get(`${BASE_URL}/api/settings`)).json()

    await page.request.put(`${BASE_URL}/api/settings`, {
      data: {
        ...current,
        primaryBaseURL:   testURL,
        externalHostname: testHostname,
        displayTimezone:  testTZ,
        serverTimezone:   testTZ,
      },
    })

    await restartAndReAuth(page)

    const s = await (await page.request.get(`${BASE_URL}/api/settings`)).json()
    expect(s.primaryBaseURL).toBe(testURL)
    expect(s.externalHostname).toBe(testHostname)
    expect(s.displayTimezone).toBe(testTZ)
    expect(s.serverTimezone).toBe(testTZ)

    // Restore
    await page.request.put(`${BASE_URL}/api/settings`, {
      data: { ...s, primaryBaseURL: current.primaryBaseURL ?? '', externalHostname: current.externalHostname ?? '' },
    })
  })
})

// ── Docker host fields survive restart ────────────────────────────────────────

test.describe('Restart persistence — Docker host fields', () => {
  test('monitorIntervalSeconds, pingTimeoutSeconds, offlineConfirmSeconds survive restart', async ({ page }) => {
    await gotoApp(page)

    // Create a host with all three monitoring fields set
    const current = await (await page.request.get(`${BASE_URL}/api/docker-hosts`)).json() as { profiles: Array<Record<string, unknown>> }
    const putRes = await page.request.put(`${BASE_URL}/api/docker-hosts`, {
      data: {
        profiles: [
          ...current.profiles.filter((p: Record<string, unknown>) => !p.built_in && p.id !== 'local'),
          {
            id:                     'docker-host-persist-test',
            name:                   'e2e-persist-test',
            type:                   'socket',
            endpoint:               '/var/run/docker.sock',
            enabled:                true,
            monitorIntervalSeconds: 120,
            pingTimeoutSeconds:     8,
            offlineConfirmSeconds:  900,
          },
        ],
      },
    })
    expect(putRes.ok()).toBe(true)

    await restartAndReAuth(page)

    const after = await (await page.request.get(`${BASE_URL}/api/docker-hosts`)).json() as { profiles: Array<{ name: string; monitorIntervalSeconds: number; pingTimeoutSeconds: number; offlineConfirmSeconds: number }> }
    const host = after.profiles.find(p => p.name === 'e2e-persist-test')
    expect(host?.monitorIntervalSeconds).toBe(120)
    expect(host?.pingTimeoutSeconds).toBe(8)
    expect(host?.offlineConfirmSeconds).toBe(900)

    // Restore — remove the test host
    await page.request.put(`${BASE_URL}/api/docker-hosts`, {
      data: { profiles: current.profiles.filter((p: Record<string, unknown>) => !p.built_in && p.id !== 'local') },
    })
  })

  test('monitoring fields show correct values in edit form after restart', async ({ page }) => {
    await gotoApp(page)

    // Create host via UI to exercise the full save path
    await gotoSettings(page, 'dockerHosts')
    await page.locator('#openAddDockerHostBtn').click()
    await page.locator('#dockerHostName').fill('e2e-ui-restart-host')
    await page.locator('#dockerHostEndpoint').fill('/var/run/docker.sock')
    await page.locator('#dockerHostAdvHeader').click()
    await page.locator('#dockerHostMonitorInterval').fill('90')
    await page.locator('#dockerHostPingTimeout').fill('7')
    await page.locator('#dockerHostConfirmScans').fill('600')
    await page.locator('#saveDockerHostBtn').click()
    await expect(page.locator('#dockerHostsTbody')).toContainText('e2e-ui-restart-host')

    await restartAndReAuth(page)

    // Re-open the edit form and verify values are still populated
    await gotoSettings(page, 'dockerHosts')
    const editBtn = page.locator('#dockerHostsTbody tr')
      .filter({ hasText: 'e2e-ui-restart-host' })
      .getByRole('button', { name: /edit/i })
    await editBtn.click()
    await page.locator('#dockerHostAdvHeader').click()
    await expect(page.locator('#dockerHostMonitorInterval')).toHaveValue('90')
    await expect(page.locator('#dockerHostPingTimeout')).toHaveValue('7')
    await expect(page.locator('#dockerHostConfirmScans')).toHaveValue('600')

    // Clean up
    await page.locator('#deleteDockerHostBtn').click()
    await expect(page.locator('#dockerHostsTbody')).not.toContainText('e2e-ui-restart-host')
  })

  test('downTemplate and recoveryTemplate survive restart', async ({ page }) => {
    await gotoApp(page)

    const current = await (await page.request.get(`${BASE_URL}/api/docker-hosts`)).json() as { profiles: Array<Record<string, unknown>> }
    const putRes = await page.request.put(`${BASE_URL}/api/docker-hosts`, {
      data: {
        profiles: [
          ...current.profiles.filter((p: Record<string, unknown>) => !p.built_in && p.id !== 'local'),
          {
            id:               'docker-host-template-test',
            name:             'e2e-template-test',
            type:             'socket',
            endpoint:         '/var/run/docker.sock',
            enabled:          true,
            downTemplate:     'OFFLINE: {{.HostName}}',
            recoveryTemplate: 'ONLINE: {{.HostName}}',
          },
        ],
      },
    })
    expect(putRes.ok()).toBe(true)

    await restartAndReAuth(page)

    const after = await (await page.request.get(`${BASE_URL}/api/docker-hosts`)).json() as { profiles: Array<{ name: string; downTemplate?: string; recoveryTemplate?: string }> }
    const host = after.profiles.find(p => p.name === 'e2e-template-test')
    expect(host?.downTemplate).toBe('OFFLINE: {{.HostName}}')
    expect(host?.recoveryTemplate).toBe('ONLINE: {{.HostName}}')

    // Restore
    await page.request.put(`${BASE_URL}/api/docker-hosts`, {
      data: { profiles: current.profiles.filter((p: Record<string, unknown>) => !p.built_in && p.id !== 'local') },
    })
  })

  test('notification profile IDs survive restart', async ({ page }) => {
    await gotoApp(page)

    const current = await (await page.request.get(`${BASE_URL}/api/docker-hosts`)).json() as { profiles: Array<Record<string, unknown>> }
    const putRes = await page.request.put(`${BASE_URL}/api/docker-hosts`, {
      data: {
        profiles: [
          ...current.profiles.filter((p: Record<string, unknown>) => !p.built_in && p.id !== 'local'),
          {
            id:            'docker-host-notif-test',
            name:          'e2e-notif-persist-test',
            type:          'socket',
            endpoint:      '/var/run/docker.sock',
            enabled:       true,
            notifications: ['test-notif-id-1', 'test-notif-id-2'],
          },
        ],
      },
    })
    expect(putRes.ok()).toBe(true)

    await restartAndReAuth(page)

    const after = await (await page.request.get(`${BASE_URL}/api/docker-hosts`)).json() as { profiles: Array<{ name: string; notifications?: string[] }> }
    const host = after.profiles.find(p => p.name === 'e2e-notif-persist-test')
    expect(host?.notifications).toEqual(['test-notif-id-1', 'test-notif-id-2'])

    // Restore
    await page.request.put(`${BASE_URL}/api/docker-hosts`, {
      data: { profiles: current.profiles.filter((p: Record<string, unknown>) => !p.built_in && p.id !== 'local') },
    })
  })
})

// ── Job dockerHostIDs survive restart ────────────────────────────────────────

test.describe('Restart persistence — Job dockerHostIDs', () => {
  test('dockerHostIDs and healthCheckScript survive restart', async ({ page }) => {
    await gotoApp(page)

    const createRes = await page.request.post(`${BASE_URL}/api/jobs`, {
      data: {
        name:                'e2e-dockerids-persist-job',
        action:              'restart',
        enabled:             true,
        containerNameFilter: 'persist-test-container',
        dockerHostIDs:       ['local'],
        healthCheckScript:   '#!/bin/sh\nexit 1',
      },
    })
    expect(createRes.ok()).toBe(true)
    const created = await createRes.json() as { id: string }

    await restartAndReAuth(page)

    const res  = await page.request.get(`${BASE_URL}/api/jobs`)
    const data = await res.json() as { jobs: Array<{ name: string; dockerHostIDs?: string[]; healthCheckScript?: string }> }
    const job  = data.jobs.find(j => j.name === 'e2e-dockerids-persist-job')
    expect(job?.dockerHostIDs).toEqual(['local'])
    expect(job?.healthCheckScript).toContain('exit 1')

    // Clean up
    await page.request.delete(`${BASE_URL}/api/jobs/${created.id}`)
  })
})

// ── Log level survives restart ────────────────────────────────────────────────

test.describe('Restart persistence — Log level', () => {
  test('log level without env override persists after restart', async ({ page }) => {
    await gotoApp(page)

    const current = await (await page.request.get(`${BASE_URL}/api/settings`)).json()
    const original = current.logLevel || 'info'

    await page.request.put(`${BASE_URL}/api/settings`, { data: { ...current, logLevel: 'warn' } })

    await restartAndReAuth(page)

    const after = await (await page.request.get(`${BASE_URL}/api/settings`)).json()
    expect(after.logLevel).toBe('warn')

    // Restore
    await page.request.put(`${BASE_URL}/api/settings`, { data: { ...after, logLevel: original } })
  })

  test('debug messages written to log file when level is debug', async ({ page }) => {
    await gotoApp(page)

    const current = await (await page.request.get(`${BASE_URL}/api/settings`)).json()

    await page.request.put(`${BASE_URL}/api/settings`, {
      data: { ...current, logLevel: 'debug', logToFile: true },
    })

    await restartAndReAuth(page)

    // GET /api/diagnostics always emits a [DEBUG] log line
    await page.request.get(`${BASE_URL}/api/diagnostics`)

    const today = new Date().toISOString().slice(0, 10)
    const { stdout } = await execAsync(`docker exec ${CONTAINER} cat /config/logs/app-${today}.log`)

    expect(stdout).toContain('[DEBUG]')

    // Restore
    const restored = await (await page.request.get(`${BASE_URL}/api/settings`)).json()
    await page.request.put(`${BASE_URL}/api/settings`, {
      data: { ...restored, logLevel: current.logLevel || 'info', logToFile: current.logToFile ?? false },
    })
  })

  test('info and debug messages absent from log file when level is warn', async ({ page }) => {
    await gotoApp(page)

    const current = await (await page.request.get(`${BASE_URL}/api/settings`)).json()
    const today   = new Date().toISOString().slice(0, 10)
    const logPath = `/config/logs/app-${today}.log`

    // logToFile defaults to true, so the file already holds [INFO] entries from
    // the entire run (boot lines, monitor scans, earlier restarts/tests — including
    // the preceding "debug" test). Only content appended AFTER this point reflects
    // the warn-level config we're about to apply, so capture the current size first
    // and diff against it below rather than scanning the whole day's file.
    let baselineSize = 0
    try {
      const { stdout } = await execAsync(`docker exec ${CONTAINER} sh -c "wc -c < ${logPath} 2>/dev/null || echo 0"`)
      baselineSize = parseInt(stdout.trim(), 10) || 0
    } catch {
      baselineSize = 0
    }

    await page.request.put(`${BASE_URL}/api/settings`, {
      data: { ...current, logLevel: 'warn', logToFile: true },
    })

    await restartAndReAuth(page)

    // These requests would produce [DEBUG] and [INFO] lines at lower levels
    await page.request.get(`${BASE_URL}/api/diagnostics`)
    await page.request.get(`${BASE_URL}/api/settings`)

    // Read only the bytes appended since the baseline — i.e. what was logged
    // under the new warn-level config (including this restart's own boot line).
    let newContent = ''
    try {
      const { stdout } = await execAsync(`docker exec ${CONTAINER} tail -c +${baselineSize + 1} ${logPath}`)
      newContent = stdout
    } catch {
      // File missing or truncated (e.g. rotated to a new day) — no new messages, which is correct for warn level
    }

    expect(newContent).not.toContain('[DEBUG]')
    expect(newContent).not.toContain('[INFO]')

    // Restore
    const restored = await (await page.request.get(`${BASE_URL}/api/settings`)).json()
    await page.request.put(`${BASE_URL}/api/settings`, {
      data: { ...restored, logLevel: current.logLevel || 'info', logToFile: current.logToFile ?? false },
    })
  })
})
