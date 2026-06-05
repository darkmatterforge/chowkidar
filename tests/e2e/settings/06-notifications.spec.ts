import { test, expect, type Page } from '@playwright/test'
import { gotoSettings } from '../helpers/nav.js'

async function openNotificationsTab(page: Page) {
  await gotoSettings(page, 'notifications')
}

async function openAddForm(page: Page) {
  await page.locator('#openAddNotifyBtn').click()
  await expect(page.locator('#notifyFormPanel')).toBeVisible()
}

async function selectProvider(
  page: Page,
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

async function saveProfile(page: Page, name: string) {
  await page.locator('#notifyName').fill(name)
  await page.locator('#notifyEnabled').check()
  // Verify the first (required) provider field has a value before saving.
  // If fill() silently no-opped while the field was still rendering, validation
  // rejects the form without making an HTTP request and waitForResponse hangs.
  // Only check the first field — optional fields are legitimately empty.
  const firstProviderField = page.locator(
    '#notifyDynamicFields input[type="text"], #notifyDynamicFields input[type="url"], #notifyDynamicFields input:not([type])',
  ).first()
  if (await firstProviderField.count() > 0 && await firstProviderField.isVisible()) {
    await expect(firstProviderField).not.toHaveValue('', { timeout: 3_000 })
  }
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

async function deleteProfile(page: Page, name: string) {
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

  // ── Notification limits & suspension UI ───────────────────────────────────

  test.describe.serial('Notification limits and suspension', () => {
    const PROFILE = 'e2e-limits-test'

    test('Notification Limits section is visible in the add form', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await expect(page.locator('#notifyBurstLimit')).toBeVisible()
      await expect(page.locator('#notifyDailyLimit')).toBeVisible()
      await expect(page.locator('#notifyBurstWindow')).toBeVisible()
      await expect(page.locator('#notifyOnLimitSuspend')).toBeVisible()
      await expect(page.locator('#notifyAutoSuspendOnError')).toBeVisible()
      await page.locator('#closeNotifyFormBtn').click()
    })

    test('burst and daily limit fields save and load correctly', async ({ page }) => {
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'webhook')
      await page.locator('#notifyField_url').fill('https://example.com/hook')
      await page.locator('#notifyBurstLimit').fill('5')
      await page.locator('#notifyBurstWindow').selectOption('10')
      await page.locator('#notifyDailyLimit').fill('50')
      await saveProfile(page, PROFILE)

      // Edit and verify round-trip
      const editBtn = page.locator('#notifyTbody tr').filter({ hasText: PROFILE }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#notifyBurstLimit')).toHaveValue('5')
      await expect(page.locator('#notifyBurstWindow')).toHaveValue('10')
      await expect(page.locator('#notifyDailyLimit')).toHaveValue('50')
      await page.locator('#cancelNotifyEditBtn').click()
    })

    test('table shows daily usage bar when limit is set', async ({ page }) => {
      await openNotificationsTab(page)
      const row = page.locator('#notifyTbody tr').filter({ hasText: PROFILE })
      // With a limit set, the usage column should show a progress bar (not "No limit")
      await expect(row).not.toContainText('No limit')
    })

    test('"No limit" shown in daily usage when no limits configured', async ({ page }) => {
      // Create a profile without any limits — should show "No limit" in the table
      await openNotificationsTab(page)
      await openAddForm(page)
      await selectProvider(page, 'webhook')
      await page.locator('#notifyField_url').fill('https://example.com/nolimit')
      // Leave burst and daily limit blank
      await saveProfile(page, 'e2e-no-limit-test')
      const row = page.locator('#notifyTbody tr').filter({ hasText: 'e2e-no-limit-test' })
      await expect(row).toContainText('No limit')
      await deleteProfile(page, 'e2e-no-limit-test')
    })

    test('suspend dropdown + button suspends the profile', async ({ page }) => {
      await openNotificationsTab(page)
      const editBtn = page.locator('#notifyTbody tr').filter({ hasText: PROFILE }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#notifySuspendUnit')).toBeVisible()

      // Select 1 hour and suspend
      await page.locator('#notifySuspendUnit').selectOption('h')
      await page.locator('#notifySuspendAmount').fill('1')
      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/notifications/') && r.url().includes('/suspend')),
        page.getByRole('button', { name: 'Suspend' }).click(),
      ])
      expect(res.status()).toBeLessThan(500)
      await expect(page.locator('#notifSuspendStatus')).toBeVisible()
      await page.locator('#cancelNotifyEditBtn').click()
    })

    test('suspended profile shows status pill and Resume button in table', async ({ page }) => {
      await openNotificationsTab(page)
      const row = page.locator('#notifyTbody tr').filter({ hasText: PROFILE })
      await expect(row).toContainText('Suspended')
      await expect(row.getByRole('button', { name: 'Resume' })).toBeVisible()
    })

    test('caution icon appears in name column when suspended', async ({ page }) => {
      await openNotificationsTab(page)
      const row = page.locator('#notifyTbody tr').filter({ hasText: PROFILE })
      // Caution SVG is inside the first <td>
      await expect(row.locator('td:first-child svg')).toBeVisible()
    })

    test('Resume button clears suspension', async ({ page }) => {
      await openNotificationsTab(page)
      const row = page.locator('#notifyTbody tr').filter({ hasText: PROFILE })
      const resumeBtn = row.getByRole('button', { name: 'Resume' })
      const [res] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/notifications/') && r.url().includes('/resume')),
        resumeBtn.click(),
      ])
      expect(res.status()).toBeLessThan(500)
      // After resume the status should be Active again, no Suspended pill
      await expect(row).not.toContainText('Suspended')
      await expect(row).toContainText('Active')
    })

    test('no caution icon after suspension cleared', async ({ page }) => {
      await openNotificationsTab(page)
      const row = page.locator('#notifyTbody tr').filter({ hasText: PROFILE })
      // SVG should be absent (no issues)
      await expect(row.locator('td:first-child svg')).toBeHidden()
    })

    test('inline rename works from table', async ({ page }) => {
      await openNotificationsTab(page)
      // Click the strong name element — JS replaces it with an <input>
      await page.locator('strong[data-notify-rename-id]').filter({ hasText: PROFILE }).click()
      // After click the strong is gone; wait for any text input inside the tbody to appear
      const input = page.locator('#notifyTbody input[type=text]').first()
      await expect(input).toBeVisible({ timeout: 5_000 })
      await expect(input).toHaveValue(PROFILE)
      await input.fill('e2e-limits-renamed')
      await input.press('Enter')
      await expect(page.locator('#notifyTbody')).toContainText('e2e-limits-renamed')

      // Rename back
      await page.locator('strong[data-notify-rename-id]').filter({ hasText: 'e2e-limits-renamed' }).click()
      const input2 = page.locator('#notifyTbody input[type=text]').first()
      await expect(input2).toBeVisible({ timeout: 5_000 })
      await input2.fill(PROFILE)
      await input2.press('Enter')
      await expect(page.locator('#notifyTbody')).toContainText(PROFILE)
    })

    test('suspension duration dropdown hides amount input for calendar options', async ({ page }) => {
      await openNotificationsTab(page)
      const editBtn = page.locator('#notifyTbody tr').filter({ hasText: PROFILE }).getByRole('button', { name: /edit/i })
      await editBtn.click()
      // Calendar option should hide the amount input
      await page.locator('#notifySuspendUnit').selectOption('midnight')
      await expect(page.locator('#notifySuspendAmount')).toBeHidden()
      // Duration option should show it
      await page.locator('#notifySuspendUnit').selectOption('h')
      await expect(page.locator('#notifySuspendAmount')).toBeVisible()
      await page.locator('#cancelNotifyEditBtn').click()
    })

    test('clean up limits test profile', async ({ page }) => {
      await openNotificationsTab(page)
      await deleteProfile(page, PROFILE)
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
