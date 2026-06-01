import { chromium, FullConfig } from '@playwright/test'
import fs from 'fs'
import path from 'path'
import { AUTH_STATE } from './playwright.config'

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

export default async function globalSetup(_config: FullConfig) {
  console.log(`\nWaiting for app at ${BASE_URL}...`)
  await waitForHealth()
  console.log('App is healthy.')

  // Ensure .auth directory exists and has an empty placeholder so Playwright
  // doesn't error when the 'app' project tries to read the state file before
  // 02-login.spec.ts has written the real session.
  const authDir = path.dirname(AUTH_STATE)
  fs.mkdirSync(authDir, { recursive: true })
  if (!fs.existsSync(AUTH_STATE)) {
    fs.writeFileSync(AUTH_STATE, JSON.stringify({ cookies: [], origins: [] }))
  }
}
