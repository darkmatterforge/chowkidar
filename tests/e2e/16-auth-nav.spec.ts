/**
 * Auth navigation — lean spec.
 * Tests /api/health and /api/auth/status endpoint contracts in both auth states.
 * Navigation helper (gotoApp/gotoLogin) correctness is exercised by every other
 * spec, so we don't test the helpers themselves here.
 */

import { test, expect } from '@playwright/test'
import { gotoApp, isAuthEnabled } from './helpers/nav.js'

const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

test('/api/health: no auth required, returns ok + version + bootTime', async ({ page }) => {
  // This must work without any session cookie (Docker HEALTHCHECK path).
  const res = await page.request.get(`${BASE_URL}/api/health`)
  expect(res.status()).toBe(200)
  const body = await res.json()
  expect(body.ok).toBe(true)
  expect(typeof body.version).toBe('string')
  expect(body.version.length).toBeGreaterThan(0)
  expect(body.bootTime).toBeTruthy()
  // bootTime must be a valid ISO date string.
  expect(isNaN(new Date(body.bootTime).getTime())).toBe(false)
})

test('/api/health: stable bootTime across calls', async ({ page }) => {
  const [r1, r2] = await Promise.all([
    page.request.get(`${BASE_URL}/api/health`),
    page.request.get(`${BASE_URL}/api/health`),
  ])
  const b1 = await r1.json()
  const b2 = await r2.json()
  expect(b1.bootTime).toBe(b2.bootTime)
})

test('/api/auth/status: shape, loggedIn reflects session, ?tz= accepted', async ({ page }) => {
  await gotoApp(page)

  // With session: loggedIn matches auth state.
  const withSession = await page.request.get(`${BASE_URL}/api/auth/status`)
  expect(withSession.status()).toBe(200)
  const body = await withSession.json()
  expect(typeof body.enabled).toBe('boolean')
  expect(typeof body.loggedIn).toBe('boolean')

  const authOn = await isAuthEnabled(page)
  if (authOn) {
    expect(body.loggedIn).toBe(true)
  }

  // Without session cookie: loggedIn must be false when auth is on.
  if (authOn) {
    await page.context().clearCookies()
    const noSession = await page.request.get(`${BASE_URL}/api/auth/status`)
    const nb = await noSession.json()
    expect(nb.loggedIn).toBe(false)
  }

  // ?tz= query param must not break the endpoint.
  const withTZ = await page.request.get(`${BASE_URL}/api/auth/status?tz=Pacific%2FAuckland`)
  expect(withTZ.status()).toBe(200)
})
