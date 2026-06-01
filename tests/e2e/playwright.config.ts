import { defineConfig, devices } from '@playwright/test'
import path from 'path'

export const AUTH_STATE = path.join(__dirname, '.auth', 'user.json')

export const TEST_USER = process.env.E2E_USERNAME ?? 'testadmin'
export const TEST_PASS = process.env.E2E_PASSWORD ?? 'TestPassword1!'

export default defineConfig({
  testDir: '.',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  retries: process.env.CI ? 1 : 0,
  // Serial in CI — tests share server state and must run in order
  workers: process.env.CI ? 1 : 1,
  globalSetup: './global.setup.ts',
  reporter: [
    ['list'],
    ['html', { outputFolder: 'playwright-report', open: 'never' }],
  ],
  use: {
    baseURL: process.env.BASE_URL ?? 'http://localhost:8080',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'on-first-retry',
  },
  projects: [
    // Group 1: first-run setup panel — no stored auth, must run first
    {
      name: 'first-run',
      testMatch: '01-setup.spec.ts',
    },
    // Group 2: login/session tests — depends on account existing from Group 1
    // saves auth state at end for all subsequent projects
    {
      name: 'auth',
      testMatch: '02-login.spec.ts',
      dependencies: ['first-run'],
    },
    // Groups 3–10: all other tests, use saved auth state
    {
      name: 'app',
      testMatch: /^(?!.*(?:01-setup|02-login|global\.setup)).*\.spec\.ts$/,
      use: {
        ...devices['Desktop Chrome'],
        storageState: AUTH_STATE,
      },
      dependencies: ['auth'],
    },
  ],
})
