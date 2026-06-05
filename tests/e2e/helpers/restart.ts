import { Page } from '@playwright/test'
import { exec } from 'child_process'
import { promisify } from 'util'
import { gotoApp } from './nav'

const execAsync = promisify(exec)

export const BASE_URL  = process.env.BASE_URL           ?? 'http://localhost:8080'
export const CONTAINER = process.env.E2E_CONTAINER_NAME ?? 'chowkidar-e2e'

/**
 * Restart the app container and re-authenticate.
 * Container restart drops all in-memory sessions — this helper waits for the
 * app to come back healthy then delegates to gotoApp, which waits for the
 * auth/status response and auto-logs in if the session is gone.
 */
export async function restartAndReAuth(page: Page): Promise<void> {
  await execAsync(`docker restart ${CONTAINER}`)

  // Wait for health endpoint (no auth required)
  const deadline = Date.now() + 45_000
  while (Date.now() < deadline) {
    try {
      const h = await page.request.get(`${BASE_URL}/api/health`, { timeout: 2_000 })
      if (h.ok()) break
    } catch { /* still starting */ }
    await page.waitForTimeout(1_000)
  }

  await gotoApp(page)
}
