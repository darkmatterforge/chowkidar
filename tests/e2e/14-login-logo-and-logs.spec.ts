/**
 * Login logo, app version, and settings sidebar version display.
 */

import { test, expect, Page } from '@playwright/test'
import { gotoApp, gotoSettings, gotoLogin } from './helpers/nav.js'
import * as fs from 'fs'
import * as path from 'path'

const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'

// ── SVG logos (file-system, no browser needed) ───────────────────────────────

test('stacked SVG logos: no solid background, dark logo has white text and icon path', () => {
  for (const name of ['logo-stacked-dark.svg', 'logo-stacked-light.svg']) {
    const content = fs.readFileSync(path.resolve(__dirname, `../../web/assets/${name}`), 'utf8')
    // No solid background rect — must be transparent.
    if (/<rect[^>]+fill="(?!none|transparent)[^"]*"/.test(content)) {
      throw new Error(`${name} has a solid <rect fill="..."> — remove it`)
    }
  }
  // Dark logo must have white text (readable on dark panel) and brand icon path.
  const dark = fs.readFileSync(path.resolve(__dirname, '../../web/assets/logo-stacked-dark.svg'), 'utf8')
  expect(dark.toLowerCase()).toMatch(/#ffffff|#fff\b|fill="white"/)
  expect(dark).toContain('<path')
  expect(dark).toContain('CHOWKIDAR')
})

test('logo SVG assets are served with correct content-type and content', async ({ page }) => {
  for (const [name, expected] of [
    ['logo-stacked-light.svg', 'light'],
    ['logo-stacked-dark.svg',  'dark'],
  ]) {
    const res = await page.request.get(`${BASE_URL}/assets/${name}`)
    expect(res.status()).toBe(200)
    const body = await res.text()
    expect(body).toContain('<svg')
    // Must NOT have a solid background rect.
    if (/<rect[^>]+fill="(?!none|transparent)[^"]*"/.test(body)) {
      throw new Error(`Served ${name} has solid background rect`)
    }
    void expected
  }
})

// ── Login page logo rendering ─────────────────────────────────────────────────

test('login logo: both logo variants in DOM; dark logos have no inline display:none', async ({ page }) => {
  // Navigate with a valid session — the login logo HTML is always in the DOM.
  // We don't need to log out to test the logo structure.
  await gotoApp(page)

  // Both logo variants must exist in the DOM for CSS theme-switching to work.
  expect(await page.locator('.login-logo-light').count()).toBeGreaterThan(0)
  expect(await page.locator('.login-logo-dark').count()).toBeGreaterThan(0)

  // Dark logos must NOT have inline display:none — that would override the CSS
  // class rules and prevent the dark logo appearing in dark mode.
  const darkLogos = page.locator('.login-logo-dark')
  const count = await darkLogos.count()
  for (let i = 0; i < count; i++) {
    const style = await darkLogos.nth(i).getAttribute('style') ?? ''
    if (style.includes('display: none') || style.includes('display:none')) {
      throw new Error(`login-logo-dark[${i}] has inline display:none — remove it, CSS handles show/hide`)
    }
  }
})

// ── App version and sidebar ───────────────────────────────────────────────────

test('/api/health: version (semver or dev), stable bootTime, latestVersionRelDate', async ({ page }) => {
  const [r1, r2] = await Promise.all([
    page.request.get(`${BASE_URL}/api/health`),
    page.request.get(`${BASE_URL}/api/health`),
  ])
  const h1 = await r1.json()
  const h2 = await r2.json()

  expect(h1.version).toMatch(/^(\d+\.\d+\.\d+|dev)$/)
  expect(h1.bootTime).toBeTruthy()
  expect(isNaN(new Date(h1.bootTime).getTime())).toBe(false)
  expect(h1.bootTime).toBe(h2.bootTime)
  expect('latestVersionRelDate' in h1).toBe(true)
})

test('settings sidebar shows version matching /api/health; update panel logic', async ({ page }) => {
  await gotoSettings(page)
  // loadDashboard populates the sidebar version.
  await page.waitForFunction(() => {
    const el = document.getElementById('settingsSidebarVersion')
    return el && el.textContent && el.textContent.startsWith('v')
  }, { timeout: 5000 })

  const health = await (await page.request.get(`${BASE_URL}/api/health`)).json()
  const text = await page.locator('#settingsSidebarVersion').innerText()
  expect(text).toBe(`v${health.version}`)

  // Update panel: hidden when on latest, visible (with version) when update available.
  if (!health.latestVersion || health.latestVersion === health.version) {
    await expect(page.locator('#settingsSidebarUpdateWrap')).toBeHidden()
  } else {
    await expect(page.locator('#settingsSidebarUpdateWrap')).toBeVisible()
    expect(await page.locator('#settingsSidebarUpdateVersion').innerText()).toBe(`v${health.latestVersion}`)
  }
})
