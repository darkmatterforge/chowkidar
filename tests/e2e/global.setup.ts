import { FullConfig } from '@playwright/test'
import fs from 'fs'
import path from 'path'
import { AUTH_STATE } from './playwright.config.js'
import { spawnSync } from 'child_process'

const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

async function waitForHealth(maxWaitMs = 60_000): Promise<void> {
  const deadline = Date.now() + maxWaitMs
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${BASE_URL}/api/health`)
      if (res.ok) return
    } catch {
      // not ready yet
    }
    await new Promise(r => setTimeout(r, 1_000))
  }
  throw new Error(`App at ${BASE_URL} did not become healthy within ${maxWaitMs}ms`)
}

/**
 * Seed 30 history entries so activity-feed, pagination, and timestamp-format
 * tests never skip due to an empty history file.
 *
 * 30 entries: enough to trigger pagination (default page size 25) and to
 * provide realistic data for bar charts and timestamp tests.
 *
 * Varied statuses mirror real-world monitoring events:
 *   success  — container restarted and recovered
 *   healthy  — periodic health-check pulse (every 3rd)
 *   failed   — restart attempt failed (every 5th)
 *   skipped  — action skipped due to cooldown (every 7th)
 *
 * These entries are cleared by the "Clear All History" test in
 * settings/11-general-improvements.spec.ts which runs last in the suite —
 * that test also verifies the DELETE /api/history endpoint works correctly.
 */
async function seedHistory(): Promise<void> {
  const statuses = ['success', 'success', 'healthy', 'success', 'failed', 'success', 'skipped']
  // Fetch server bootTime and seed entries starting 1s after boot so the
  // ordering invariant (monitoring_started <= failed_recovery timestamps)
  // always holds.
  const healthRes = await fetch(`${BASE_URL}/api/health`)
  const health = await healthRes.json()
  const bootMs = new Date(health.bootTime).getTime()
  const baseMs = bootMs + 1000 // 1 second after boot
  const entries = Array.from({ length: 30 }, (_, i) => {
    const n = i + 1
    const ts = new Date(baseMs + n * 60 * 1000).toISOString() // 1-minute spacing
    return {
      timestamp:     ts,
      containerId:   `seed${String(n).padStart(3, '0')}`,
      containerName: 'e2e-unhealthy', // real monitored container → appears in feed
      reason:        'unhealthy',
      action:        'restart',
      attempt:       1,
      status:        statuses[n % statuses.length],
      durationMs:    1234,
    }
  })

    const res = await fetch(`${BASE_URL}/api/history`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(entries),
    })
    if (!res.ok) {
      const body = await res.text().catch(() => '')
      const status = res.status
      console.warn(`HTTP seed failed: ${status} ${body}`)
      // Fall back to writing the NDJSON directly into the running container's
      // history file when the API is not usable (examples: auth enabled,
      // CORS preflight, or unexpected HTTP response). This requires Docker
      // available on the host running tests and an image named
      // `chowkidar:e2e-local` (used by the local e2e script).
      try {
        const ndjson = entries.map(e => JSON.stringify(e)).join('\n') + '\n'
        // Find a running container based on the test image
        const ps = spawnSync('docker', ['ps', '--filter', 'ancestor=chowkidar:e2e-local', '--format', '{{.ID}}'])
        if (ps.status !== 0) {
          throw new Error(`docker ps failed: ${ps.stderr?.toString()}`)
        }
        const cid = ps.stdout.toString().trim().split(/\s+/)[0]
        if (!cid) throw new Error('no chowkidar:e2e-local container found')
        const execRes = spawnSync('docker', ['exec', '-i', cid, 'sh', '-c', "cat >> /config/data/action-history.json"], { input: ndjson })
        if (execRes.status !== 0) {
          throw new Error(`docker exec failed: ${execRes.stderr?.toString()}`)
        }
        console.log(`Seeded ${entries.length} history entries via docker into container ${cid}`)
        return
      } catch (dockErr) {
        const msg = (dockErr as Error).message || String(dockErr)
        throw new Error(`Failed to seed history: ${status} ${body}; docker fallback error: ${msg}`)
      }
    }
    const { seeded } = await res.json()
    console.log(`Seeded ${seeded} history entries.`)
}

export default async function globalSetup(_config: FullConfig) {
  console.log(`\nWaiting for app at ${BASE_URL}...`)
  await waitForHealth()
  console.log('App is healthy.')

  // Seed history entries before any tests run.
  await seedHistory()

  // Ensure .auth directory exists and has an empty placeholder so Playwright
  // doesn't error when the 'app' project tries to read the state file before
  // 02-login.spec.ts has written the real session.
  const authDir = path.dirname(AUTH_STATE)
  fs.mkdirSync(authDir, { recursive: true })
  if (!fs.existsSync(AUTH_STATE)) {
    fs.writeFileSync(AUTH_STATE, JSON.stringify({ cookies: [], origins: [] }))
  }
}
