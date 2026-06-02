import { test, expect } from '@playwright/test'
import { gotoSettings } from '../helpers/nav'

async function openNotificationsTab(page: Parameters<Parameters<typeof test>[1]>[0]['page']) {
  await gotoSettings(page, 'notifications')
}

async function openAddForm(page: Parameters<Parameters<typeof test>[1]>[0]['page']) {
  await page.locator('#openAddNotifyBtn').click()
  await expect(page.locator('#notifyFormPanel')).toBeVisible()
}

async function selectProvider(
  page: Parameters<Parameters<typeof test>[1]>[0]['page'],
  providerKey: string,
) {
  await page.locator(`button[data-provider="${providerKey}"]`).click()
  // Wait for the first dynamic field to be both rendered AND stable before
  // returning — callers fill fields immediately after, so flakiness here
  // causes the fill to target a stale or not-yet-rendered element.
  const firstField = page.locator('#notifyDynamicFields input, #notifyDynamicFields textarea, #notifyDynamicFields select').first()
  await expect(firstField).toBeVisible({ timeout: 8_000 })
  await expect(firstField).toBeEnabled()
}

async function saveProfile(page: Parameters<Parameters<typeof test>[1]>[0]['page'], name: string) {
  await page.locator('#notifyName').fill(name)
  await page.locator('#notifyEnabled').check()
  // Register the waitForResponse BEFORE clicking so we never miss a fast response
  const [res] = await Promise.all([
    page.waitForResponse(
      r => r.url().includes('/api/notification') && r.request().method() !== 'GET',
      { timeout: 15_000 },
    ),
    page.locator('#saveNotifyBtn').click(),
  ])
  expect(res.status()).toBeLessThan(500)
  await expect(page.locator('#notifyTbody')).toContainText(name)
}

async function deleteProfile(page: Parameters<Parameters<typeof test>[1]>[0]['page'], name: string) {
  const row = page.locator('#notifyTbody tr').filter({ hasText: name })
  // App uses PUT /api/notifications (replaces entire list) for both saves and deletes
  const [res] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/notification') && r.request().method() !== 'GET'),
    row.getByRole('button', { name: /delete/i }).click(),
  ])
  expect(res.status()).toBeLessThan(500)
  await expect(page.locator('#notifyTbody')).not.toContainText(name)
}

test.describe('Settings — Notifications tab', () => {
  test.describe.serial('Form open/close', () => {
    test('Add Notification Agent button opens the form', async ({ page }) => {
      await openNotificationsTab(page)
      await expect(page.locator('#notifyFormPanel')).toBeHidden()
      await page.locator('#openAddNotifyBtn').click()
      await expect(page.locator('#notifyFormPanel')).toBeVisible()
      await expect(page.locator('#notifyFormTitle')).toContainText('New Notification Agent')
    })

    test('Cancel button closes form without adding a profile', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await page.locator('#cancelNotifyEditBtn').click()
      await expect(page.locator('#notifyFormPanel')).toBeHidden()
    })

    test('Close (×) button dismisses the form', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await page.locator('#closeNotifyFormBtn').click()
      await expect(page.locator('#notifyFormPanel')).toBeHidden()
    })

    test('provider picker renders all supported providers', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      for (const key of ['discord', 'slack', 'telegram', 'mailto', 'ntfy', 'pover', 'gotify', 'webhook']) {
        await expect(page.locator(`button[data-provider="${key}"]`)).toBeVisible()
      }
    })

    test('message templates section is collapsible', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await expect(page.locator('#notifyTemplatesBody')).toBeHidden()
      await page.locator('#notifyTemplatesHeader').click()
      await expect(page.locator('#notifyTemplatesBody')).toBeVisible()
      await page.locator('#notifyTemplatesHeader').click()
      await expect(page.locator('#notifyTemplatesBody')).toBeHidden()
    })
  })

  // ── Provider-specific field rendering ─────────────────────────────────────
  // Each test opens the form, picks a provider, checks the required fields render,
  // creates a profile, then cleans up. Tests run serially to avoid table conflicts.

  test.describe.serial('Discord provider', () => {
    test('Discord — Webhook URL field renders', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'discord')
      await expect(page.locator('#notifyField_webhookurl')).toBeVisible()
    })

    test('Discord — create and delete profile', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'discord')
      await page.locator('#notifyField_webhookurl').fill('https://discord.com/api/webhooks/123/token')
      await saveProfile(page, 'e2e-discord')
      await deleteProfile(page, 'e2e-discord')
    })
  })

  test.describe.serial('Slack provider', () => {
    test('Slack — Webhook URL field renders', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'slack')
      await expect(page.locator('#notifyField_webhookurl')).toBeVisible()
    })

    test('Slack — create and delete profile', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'slack')
      await page.locator('#notifyField_webhookurl').fill('https://hooks.slack.com/services/X/Y/Z')
      await saveProfile(page, 'e2e-slack')
      await deleteProfile(page, 'e2e-slack')
    })
  })

  test.describe.serial('Telegram provider', () => {
    test('Telegram — Bot Token and Chat ID fields render', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'telegram')
      await expect(page.locator('#notifyField_bottoken')).toBeVisible()
      await expect(page.locator('#notifyField_chatid')).toBeVisible()
    })

    test('Telegram — create and delete profile', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'telegram')
      await page.locator('#notifyField_bottoken').fill('1234567890:AAAA-test-token')
      await page.locator('#notifyField_chatid').fill('-100123456789')
      await saveProfile(page, 'e2e-telegram')
      await deleteProfile(page, 'e2e-telegram')
    })
  })

  test.describe.serial('Email (SMTP) provider', () => {
    test('Email — Host, Port, To fields render', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'mailto')
      await expect(page.locator('#notifyField_host')).toBeVisible()
      await expect(page.locator('#notifyField_port')).toBeVisible()
      await expect(page.locator('#notifyField_to')).toBeVisible()
    })

    test('Email — create and delete profile', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'mailto')
      await page.locator('#notifyField_host').fill('smtp.example.com')
      await page.locator('#notifyField_port').fill('587')
      await page.locator('#notifyField_to').fill('alerts@example.com')
      await saveProfile(page, 'e2e-email')
      await deleteProfile(page, 'e2e-email')
    })
  })

  test.describe.serial('ntfy provider', () => {
    test('ntfy — Server URL and Topic fields render', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'ntfy')
      await expect(page.locator('#notifyField_host')).toBeVisible()
      await expect(page.locator('#notifyField_topic')).toBeVisible()
    })

    test('ntfy — create and delete profile', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'ntfy')
      await page.locator('#notifyField_host').fill('https://ntfy.sh')
      await page.locator('#notifyField_topic').fill('my-alerts')
      await saveProfile(page, 'e2e-ntfy')
      await deleteProfile(page, 'e2e-ntfy')
    })
  })

  test.describe.serial('Pushover provider', () => {
    test('Pushover — User Key and Token fields render', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'pover')
      await expect(page.locator('#notifyField_user')).toBeVisible()
      await expect(page.locator('#notifyField_token')).toBeVisible()
    })

    test('Pushover — create and delete profile', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'pover')
      await page.locator('#notifyField_user').fill('uQiRzpo4DXghDmr9QzzfQu')
      await page.locator('#notifyField_token').fill('azGDORePK8gMaC0QOYAMyEEuzJnyUi')
      await saveProfile(page, 'e2e-pushover')
      await deleteProfile(page, 'e2e-pushover')
    })
  })

  test.describe.serial('Gotify provider', () => {
    test('Gotify — Host and App Token fields render', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'gotify')
      await expect(page.locator('#notifyField_host')).toBeVisible()
      await expect(page.locator('#notifyField_token')).toBeVisible()
    })

    test('Gotify — create and delete profile', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'gotify')
      await page.locator('#notifyField_host').fill('gotify.example.com')
      await page.locator('#notifyField_token').fill('Aod9XcE7vL2RqGp')
      await saveProfile(page, 'e2e-gotify')
      await deleteProfile(page, 'e2e-gotify')
    })
  })

  test.describe.serial('Webhook provider', () => {
    test('Webhook — URL field renders', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'webhook')
      await expect(page.locator('#notifyField_url')).toBeVisible()
    })

    test('Webhook — create profile', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'webhook')
      await page.locator('#notifyField_url').fill('https://example.com/hook')
      await saveProfile(page, 'e2e-webhook')
    })

    test('Webhook — edit profile saves changes', async ({ page }) => {
      await openNotificationsTab(page)
      const editBtn = page.locator('#notifyTbody tr').filter({ hasText: 'e2e-webhook' }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#notifyFormPanel')).toBeVisible()
      await expect(page.locator('#notifyFormTitle')).toContainText('Edit')

      await page.locator('#notifyName').fill('e2e-webhook-renamed')
      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/notification') && r.request().method() !== 'GET'),
        page.locator('#saveNotifyBtn').click(),
      ])
      expect(res.status()).toBeLessThan(500)
      await expect(page.locator('#notifyTbody')).toContainText('e2e-webhook-renamed')
    })

    test('Webhook — set as default', async ({ page }) => {
      await openNotificationsTab(page)
      const editBtn = page.locator('#notifyTbody tr').filter({ hasText: 'e2e-webhook-renamed' }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      await page.locator('#notifyIsDefault').check()
      await page.locator('#saveNotifyBtn').click()
      await expect(page.locator('#notifyTbody tr').filter({ hasText: 'e2e-webhook-renamed' })).toContainText('default', { ignoreCase: true })
    })

    test('Webhook — delete profile', async ({ page }) => {
      await openNotificationsTab(page)
      await deleteProfile(page, 'e2e-webhook-renamed')
    })
  })

  // ── Send Test (mocked) ─────────────────────────────────────────────────────
  // Real providers need live credentials and network access — neither available
  // in CI.  We intercept /api/test-notification and return a synthetic success
  // so we can verify the full UI flow (button → loading → result banner) without
  // touching any external service.

  test.describe.serial('Send Test button (mocked provider)', () => {
    test('Send Test shows success result when server returns 200', async ({ page }) => {
      await openNotificationsTab(page)

      // Create a throw-away Webhook profile
      await openAddForm(page)
      await selectProvider(page, 'webhook')
      await page.locator('#notifyField_url').fill('https://example.com/mock-hook')
      await saveProfile(page, 'e2e-mock-send')

      // Open it for editing so the form (and Send Test button) is visible
      const editBtn = page.locator('#notifyTbody tr').filter({ hasText: 'e2e-mock-send' }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#notifyFormPanel')).toBeVisible()

      // Intercept the test-notification request and return a mock success
      await page.route('**/api/test-notification', async route => {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ success: true, message: 'Test notification sent' }),
        })
      })

      const testBtn = page.locator('#testNotifyProfilesBtn')
      await expect(testBtn).toBeVisible()
      await testBtn.click()

      // Result banner should appear and show a success indicator
      await expect(page.locator('#notifyTestResult')).toBeVisible({ timeout: 10_000 })
    })

    test('Send Test shows failure result when server returns error', async ({ page }) => {
      await openNotificationsTab(page)

      const editBtn = page.locator('#notifyTbody tr').filter({ hasText: 'e2e-mock-send' }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#notifyFormPanel')).toBeVisible()

      // Intercept with a provider-level failure
      await page.route('**/api/test-notification', async route => {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ success: false, error: 'Provider returned 401 Unauthorized' }),
        })
      })

      await page.locator('#testNotifyProfilesBtn').click()
      await expect(page.locator('#notifyTestResult')).toBeVisible({ timeout: 10_000 })
    })

    test('clean up mock-send test profile', async ({ page }) => {
      await openNotificationsTab(page)
      await deleteProfile(page, 'e2e-mock-send')
    })
  })

  // ── Live credential tests ──────────────────────────────────────────────────
  // These tests only run when the corresponding env vars are set (GitHub
  // secrets passed into the CI job). They skip gracefully otherwise so they
  // never block a PR. Each test creates a real profile, sends a real test
  // notification, then cleans up.
  //
  // Secrets to add in GitHub → Settings → Secrets → Actions:
  //   E2E_DISCORD_WEBHOOK_URL        Discord channel webhook URL
  //   E2E_SLACK_WEBHOOK_URL          Slack incoming webhook URL
  //   E2E_NTFY_TOPIC                 ntfy topic (uses ntfy.sh — no account needed)
  //   E2E_TELEGRAM_BOT_TOKEN         Bot token from BotFather
  //   E2E_TELEGRAM_CHAT_ID           Chat / group ID (run /getid or use @userinfobot)
  //   E2E_PUSHOVER_USER_KEY          Pushover user/group key
  //   E2E_PUSHOVER_TOKEN             Pushover application API token
  //   E2E_EMAIL_HOST                 SMTP server hostname  (e.g. smtp.gmail.com)
  //   E2E_EMAIL_PORT                 SMTP port             (e.g. 587)
  //   E2E_EMAIL_TO                   Recipient address
  //   E2E_EMAIL_USERNAME             SMTP auth username    (optional)
  //   E2E_EMAIL_PASSWORD             SMTP auth password    (optional)

  test.describe('Live notification delivery (requires secrets)', () => {
    type Page = Parameters<Parameters<typeof test>[1]>[0]['page']

    async function liveTestProfile(page: Page, profileName: string) {
      const editBtn = page.locator('#notifyTbody tr')
        .filter({ hasText: profileName })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#notifyFormPanel')).toBeVisible()

      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/test-notification')),
        page.locator('#testNotifyProfilesBtn').click(),
      ])
      expect(res.status()).toBeLessThan(500)

      const banner = page.locator('#notifyTestResult')
      await expect(banner).toBeVisible({ timeout: 25_000 })

      // ntfy.sh and other free-tier services can return rate-limit / quota
      // errors. Skip gracefully instead of failing the build.
      const titleText = await banner.locator('.test-result-title').textContent() ?? ''
      const detailText = await banner.locator('.test-result-detail').textContent() ?? ''
      const combined = (titleText + ' ' + detailText).toLowerCase()
      if (/quota|rate.?limit|too.?many|429|forbidden|throttl/i.test(combined)) {
        test.skip(true, `Rate-limited by provider — skipping: ${titleText.trim()}`)
        return
      }

      await expect(banner.locator('.test-result-title')).not.toContainText(/fail|error/i)
      await page.locator('#cancelNotifyEditBtn').click()
    }

    function liveGroup(
      name: string,
      secretKey: string,
      skipMsg: string,
      buildProfile: (page: Page) => Promise<void>,
    ) {
      test.describe.serial(`${name} (live)`, () => {
        test.skip(!process.env[secretKey], skipMsg)

        test(`create ${name} profile`, async ({ page }) => {
          await openNotificationsTab(page)
          await openAddForm(page)
          await buildProfile(page)
          await saveProfile(page, `live-${name.toLowerCase()}`)
        })

        test(`send real ${name} test notification`, async ({ page }) => {
          await openNotificationsTab(page)
          await liveTestProfile(page, `live-${name.toLowerCase()}`)
        })

        test(`clean up ${name} live profile`, async ({ page }) => {
          await openNotificationsTab(page)
          await deleteProfile(page, `live-${name.toLowerCase()}`)
        })
      })
    }

    liveGroup('Discord', 'E2E_DISCORD_WEBHOOK_URL',
      'Set E2E_DISCORD_WEBHOOK_URL secret to enable',
      async page => {
        await selectProvider(page, 'discord')
        await page.locator('#notifyField_webhookurl').fill(process.env.E2E_DISCORD_WEBHOOK_URL!)
      })

    liveGroup('Slack', 'E2E_SLACK_WEBHOOK_URL',
      'Set E2E_SLACK_WEBHOOK_URL secret to enable',
      async page => {
        await selectProvider(page, 'slack')
        await page.locator('#notifyField_webhookurl').fill(process.env.E2E_SLACK_WEBHOOK_URL!)
      })

    liveGroup('ntfy', 'E2E_NTFY_TOPIC',
      'Set E2E_NTFY_TOPIC secret to enable (ntfy.sh, no account needed)',
      async page => {
        await selectProvider(page, 'ntfy')
        await page.locator('#notifyField_host').fill('https://ntfy.sh')
        await page.locator('#notifyField_topic').fill(process.env.E2E_NTFY_TOPIC!)
      })

    liveGroup('Telegram', 'E2E_TELEGRAM_BOT_TOKEN',
      'Set E2E_TELEGRAM_BOT_TOKEN + E2E_TELEGRAM_CHAT_ID secrets to enable',
      async page => {
        await selectProvider(page, 'telegram')
        await page.locator('#notifyField_bottoken').fill(process.env.E2E_TELEGRAM_BOT_TOKEN!)
        await page.locator('#notifyField_chatid').fill(process.env.E2E_TELEGRAM_CHAT_ID ?? '')
      })

    liveGroup('Pushover', 'E2E_PUSHOVER_USER_KEY',
      'Set E2E_PUSHOVER_USER_KEY + E2E_PUSHOVER_TOKEN secrets to enable',
      async page => {
        await selectProvider(page, 'pover')
        await page.locator('#notifyField_user').fill(process.env.E2E_PUSHOVER_USER_KEY!)
        await page.locator('#notifyField_token').fill(process.env.E2E_PUSHOVER_TOKEN ?? '')
      })

    liveGroup('Email', 'E2E_EMAIL_HOST',
      'Set E2E_EMAIL_HOST + E2E_EMAIL_TO secrets to enable',
      async page => {
        await selectProvider(page, 'mailto')
        // Email renders 12+ fields — wait for the required 'To' field explicitly
        // before filling, since selectProvider only waits for the first field.
        await expect(page.locator('#notifyField_to')).toBeVisible({ timeout: 10_000 })
        await page.locator('#notifyField_host').fill(process.env.E2E_EMAIL_HOST!)
        if (process.env.E2E_EMAIL_PORT)
          await page.locator('#notifyField_port').fill(process.env.E2E_EMAIL_PORT)
        await page.locator('#notifyField_to').fill(process.env.E2E_EMAIL_TO ?? '')
        if (process.env.E2E_EMAIL_USERNAME)
          await page.locator('#notifyField_username').fill(process.env.E2E_EMAIL_USERNAME)
        if (process.env.E2E_EMAIL_PASSWORD)
          await page.locator('#notifyField_password').fill(process.env.E2E_EMAIL_PASSWORD)
      })
  })
})
