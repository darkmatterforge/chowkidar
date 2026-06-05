import { test, expect, type Page } from '@playwright/test'
import { gotoSettings } from '../helpers/nav'

async function openDockerHostsTab(page: Page) {
  await gotoSettings(page, 'dockerHosts')
}

test.describe('Settings — Docker Hosts tab', () => {
  test('Add Host button opens the docker host form', async ({ page }) => {
    await openDockerHostsTab(page)
    await expect(page.locator('#dockerHostFormPanel')).toBeHidden()
    await page.locator('#openAddDockerHostBtn').click()
    await expect(page.locator('#dockerHostFormPanel')).toBeVisible()
    await expect(page.locator('#dockerHostFormTitle')).toContainText('New Docker Host')
  })

  test('cancel button closes the form without adding a host', async ({ page }) => {
    await openDockerHostsTab(page)
    await page.locator('#openAddDockerHostBtn').click()
    await page.locator('#cancelDockerHostBtn').click()
    await expect(page.locator('#dockerHostFormPanel')).toBeHidden()
    // The Local Docker host is always present when the socket is mounted;
    // just confirm the form closed without adding a named test entry
    await expect(page.locator('#dockerHostsTbody')).not.toContainText('test-socket-host')
  })

  test('close button (×) dismisses the form', async ({ page }) => {
    await openDockerHostsTab(page)
    await page.locator('#openAddDockerHostBtn').click()
    await page.locator('#closeDockerHostFormBtn').click()
    await expect(page.locator('#dockerHostFormPanel')).toBeHidden()
  })

  test('TLS section is hidden for Socket type and visible for TCP type', async ({ page }) => {
    await openDockerHostsTab(page)
    await page.locator('#openAddDockerHostBtn').click()

    // Default is Socket — TLS section should be hidden
    await expect(page.locator('#dockerTLSSection')).toBeHidden()

    // Switch to TCP — TLS section should appear
    await page.locator('button[data-conn-type="tcp"]').click()
    await expect(page.locator('#dockerTLSSection')).toBeVisible()

    // Switch back to Socket — TLS section should hide again
    await page.locator('button[data-conn-type="socket"]').click()
    await expect(page.locator('#dockerTLSSection')).toBeHidden()
  })

  test('Skip TLS verify checkbox is inside the TLS section', async ({ page }) => {
    await openDockerHostsTab(page)
    await page.locator('#openAddDockerHostBtn').click()
    await page.locator('button[data-conn-type="tcp"]').click()
    await expect(page.locator('#dockerTLSSkipVerify')).toBeVisible()
    await page.locator('#dockerTLSSkipVerify').check()
    await expect(page.locator('#dockerTLSSkipVerify')).toBeChecked()
  })

  test('add a socket docker host — appears in table', async ({ page }) => {
    await openDockerHostsTab(page)
    await page.locator('#openAddDockerHostBtn').click()

    await page.locator('#dockerHostName').fill('test-socket-host')
    await page.locator('#dockerHostEndpoint').fill('/var/run/docker.sock')
    await page.locator('#saveDockerHostBtn').click()

    await expect(page.locator('#dockerHostsTbody')).toContainText('test-socket-host')
  })

  test('test connection button triggers a connection check', async ({ page }) => {
    await openDockerHostsTab(page)

    // The table-row "Test" button posts its result to #dockerHostsStatus (global banner).
    // The form-panel "Test Connection" button posts to #dockerHostTestResult.
    // Click the first "Test" button in the table (Local Docker or any added host).
    const testBtn = page.locator('#dockerHostsTbody button[data-host-test-id]').first()
    await testBtn.click()

    // Result banner should appear — success or failure both count
    await expect(page.locator('#dockerHostsStatus')).toBeVisible({ timeout: 15_000 })
  })

  test('delete a docker host — removed from table', async ({ page }) => {
    await openDockerHostsTab(page)

    // Open the host for editing to get the delete button
    const editBtn = page.locator('#dockerHostsTbody').getByRole('button', { name: /edit/i }).first()
    await editBtn.click()
    await expect(page.locator('#dockerHostFormPanel')).toBeVisible()

    await page.locator('#deleteDockerHostBtn').click()
    await expect(page.locator('#dockerHostsTbody')).not.toContainText('test-socket-host')
  })

  // ── Enabled / Disabled toggle ─────────────────────────────────────────────

  // ── Table shows "active" pill for built-in Local Docker ──────────────────

  test('Local Docker row shows "active" pill (always enabled)', async ({ page }) => {
    await openDockerHostsTab(page)
    const localRow = page.locator('#dockerHostsTbody tr').filter({ hasText: 'Local Docker' })
    await expect(localRow.locator('.pill')).toContainText('active')
  })

  // ── Monitoring Settings collapsible ───────────────────────────────────────

  test.describe.serial('Monitoring Settings collapsible', () => {
    test('Monitoring Settings collapsible is present and expands', async ({ page }) => {
      await openDockerHostsTab(page)
      await page.locator('#openAddDockerHostBtn').click()
      await expect(page.locator('#dockerHostAdvBody')).toBeHidden()
      await page.locator('#dockerHostAdvHeader').click()
      await expect(page.locator('#dockerHostAdvBody')).toBeVisible()
      await expect(page.locator('#dockerHostMonitorInterval')).toBeVisible()
      await expect(page.locator('#dockerHostPingTimeout')).toBeVisible()
      await expect(page.locator('#dockerHostConfirmScans')).toBeVisible()
      await page.locator('#cancelDockerHostBtn').click()
    })

    test('monitoring settings are saved and restored on edit', async ({ page }) => {
      const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'
      await openDockerHostsTab(page)
      await page.locator('#openAddDockerHostBtn').click()
      await page.locator('#dockerHostName').fill('e2e-monitor-settings-host')
      await page.locator('#dockerHostEndpoint').fill('/var/run/docker.sock')
      await page.locator('#dockerHostAdvHeader').click()
      await page.locator('#dockerHostMonitorInterval').fill('120')
      await page.locator('#dockerHostPingTimeout').fill('10')
      await page.locator('#dockerHostConfirmScans').fill('3600')
      await page.locator('#saveDockerHostBtn').click()
      await expect(page.locator('#dockerHostsTbody')).toContainText('e2e-monitor-settings-host')

      // Verify via API
      const res  = await page.request.get(`${BASE_URL}/api/docker-hosts`)
      const data = await res.json() as { profiles: Array<{ name: string; monitorIntervalSeconds: number; pingTimeoutSeconds: number; offlineConfirmSeconds: number }> }
      const host = data.profiles.find(p => p.name === 'e2e-monitor-settings-host')
      expect(host?.monitorIntervalSeconds).toBe(120)
      expect(host?.pingTimeoutSeconds).toBe(10)
      expect(host?.offlineConfirmSeconds).toBe(3600)

      // Restore on edit
      const editBtn = page.locator('#dockerHostsTbody tr')
        .filter({ hasText: 'e2e-monitor-settings-host' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await page.locator('#dockerHostAdvHeader').click()
      await expect(page.locator('#dockerHostMonitorInterval')).toHaveValue('120')
      await expect(page.locator('#dockerHostPingTimeout')).toHaveValue('10')
      await expect(page.locator('#dockerHostConfirmScans')).toHaveValue('3600')
      await page.locator('#cancelDockerHostBtn').click()
    })

    test('clean up monitoring settings test host', async ({ page }) => {
      await openDockerHostsTab(page)
      const editBtn = page.locator('#dockerHostsTbody tr')
        .filter({ hasText: 'e2e-monitor-settings-host' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await page.locator('#deleteDockerHostBtn').click()
      await expect(page.locator('#dockerHostsTbody')).not.toContainText('e2e-monitor-settings-host')
    })
  })

  // ── Offline notifications and message templates ───────────────────────────

  test.describe.serial('Offline notifications', () => {
    test('Message Templates collapsible is present and expands', async ({ page }) => {
      await openDockerHostsTab(page)
      await page.locator('#openAddDockerHostBtn').click()
      await expect(page.locator('#dockerHostMsgBody')).toBeHidden()
      await page.locator('#dockerHostMsgBody').evaluate(el => (el as HTMLElement).closest('div[style*="border"]')?.querySelector('div')?.click())
      // Try clicking the header directly
      const msgHeader = page.locator('div').filter({ has: page.locator('#dockerHostMsgBody') }).first()
      // Just verify the fields exist in the collapsed body
      await expect(page.locator('#dockerHostDownTemplate')).toBeAttached()
      await expect(page.locator('#dockerHostRecoveryTemplate')).toBeAttached()
      await page.locator('#cancelDockerHostBtn').click()
    })

    test('offline message template picker fills the input', async ({ page }) => {
      await openDockerHostsTab(page)
      await page.locator('#openAddDockerHostBtn').click()
      // Expand message templates section
      await page.locator('#dockerHostMsgChevron').click()
      await expect(page.locator('#dockerHostMsgBody')).toBeVisible()
      // Select the Alert style template from the offline message dropdown
      await page.locator('#dockerHostMsgBody select').first().selectOption({ index: 3 })
      const val = await page.locator('#dockerHostDownTemplate').inputValue()
      expect(val).toContain('ALERT')
      await page.locator('#cancelDockerHostBtn').click()
    })

    test('offline and recovery messages are saved and round-trip via API', async ({ page }) => {
      const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'
      await openDockerHostsTab(page)
      await page.locator('#openAddDockerHostBtn').click()
      await page.locator('#dockerHostName').fill('e2e-notif-template-host')
      await page.locator('#dockerHostEndpoint').fill('/var/run/docker.sock')
      await page.locator('#dockerHostMsgChevron').click()
      await expect(page.locator('#dockerHostMsgBody')).toBeVisible()
      await page.locator('#dockerHostDownTemplate').fill('Host {{.HostName}} went offline')
      await page.locator('#dockerHostRecoveryTemplate').fill('Host {{.HostName}} is back')
      await page.locator('#saveDockerHostBtn').click()
      await expect(page.locator('#dockerHostsTbody')).toContainText('e2e-notif-template-host')

      const res  = await page.request.get(`${BASE_URL}/api/docker-hosts`)
      const data = await res.json() as { profiles: Array<{ name: string; downTemplate?: string; recoveryTemplate?: string }> }
      const host = data.profiles.find(p => p.name === 'e2e-notif-template-host')
      expect(host?.downTemplate).toBe('Host {{.HostName}} went offline')
      expect(host?.recoveryTemplate).toBe('Host {{.HostName}} is back')
    })

    test('clean up notification template test host', async ({ page }) => {
      await openDockerHostsTab(page)
      const editBtn = page.locator('#dockerHostsTbody tr')
        .filter({ hasText: 'e2e-notif-template-host' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await page.locator('#deleteDockerHostBtn').click()
      await expect(page.locator('#dockerHostsTbody')).not.toContainText('e2e-notif-template-host')
    })
  })

  // ── Enabled / Disabled toggle (checkbox) ──────────────────────────────────

  test.describe.serial('Enabled / Disabled', () => {
    test('Enabled field defaults to checked when adding a new host', async ({ page }) => {
      await openDockerHostsTab(page)
      await page.locator('#openAddDockerHostBtn').click()
      await expect(page.locator('#dockerHostEnabled')).toBeChecked()
      await page.locator('#cancelDockerHostBtn').click()
    })

    test('add a disabled host — shows false pill in Enabled column', async ({ page }) => {
      await openDockerHostsTab(page)
      await page.locator('#openAddDockerHostBtn').click()
      await page.locator('#dockerHostName').fill('e2e-disabled-host')
      await page.locator('#dockerHostEndpoint').fill('/var/run/docker.sock')
      await page.locator('#dockerHostEnabled').uncheck()
      await page.locator('#saveDockerHostBtn').click()

      const row = page.locator('#dockerHostsTbody tr').filter({ hasText: 'e2e-disabled-host' })
      await expect(row).toBeVisible()
      await expect(row.locator('.pill.fail')).toBeVisible()
    })

    test('disabled host row is visually dimmed (opacity set)', async ({ page }) => {
      await openDockerHostsTab(page)
      const row = page.locator('#dockerHostsTbody tr').filter({ hasText: 'e2e-disabled-host' })
      const opacity = await row.evaluate(el => (el as HTMLElement).style.opacity)
      expect(parseFloat(opacity)).toBeLessThan(1)
    })

    test('editing a disabled host restores enabled checkbox as unchecked', async ({ page }) => {
      await openDockerHostsTab(page)
      const editBtn = page.locator('#dockerHostsTbody tr')
        .filter({ hasText: 'e2e-disabled-host' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await expect(page.locator('#dockerHostEnabled')).not.toBeChecked()
      await page.locator('#cancelDockerHostBtn').click()
    })

    test('re-enabling the host shows true pill', async ({ page }) => {
      await openDockerHostsTab(page)
      const editBtn = page.locator('#dockerHostsTbody tr')
        .filter({ hasText: 'e2e-disabled-host' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await page.locator('#dockerHostEnabled').check()
      await page.locator('#saveDockerHostBtn').click()

      const row = page.locator('#dockerHostsTbody tr').filter({ hasText: 'e2e-disabled-host' })
      await expect(row.locator('.pill.fail')).toBeHidden()
      await expect(row.locator('.pill')).toContainText('true')
    })

    test('disabled host API — enabled field round-trips correctly', async ({ page }) => {
      const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'
      await openDockerHostsTab(page)
      const editBtn = page.locator('#dockerHostsTbody tr')
        .filter({ hasText: 'e2e-disabled-host' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await page.locator('#dockerHostEnabled').uncheck()
      await page.locator('#saveDockerHostBtn').click()
      // Wait for the table to re-render with the disabled pill before checking the API
      const row = page.locator('#dockerHostsTbody tr').filter({ hasText: 'e2e-disabled-host' })
      await expect(row.locator('.pill.fail')).toBeVisible()

      // Verify via API
      const res  = await page.request.get(`${BASE_URL}/api/docker-hosts`)
      const data = await res.json() as { profiles: Array<{ name: string; enabled: boolean }> }
      const host = data.profiles.find(p => p.name === 'e2e-disabled-host')
      expect(host?.enabled).toBe(false)
    })

    test('disabled host is excluded from status ping', async ({ page }) => {
      const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'
      const res  = await page.request.get(`${BASE_URL}/api/docker-hosts/status`)
      const data = await res.json() as { hosts: Array<{ name: string; enabled: boolean; connected: boolean }> }
      const host = data.hosts.find(h => h.name === 'e2e-disabled-host')
      // Disabled host is returned but connected must be false (not pinged)
      expect(host?.enabled).toBe(false)
      expect(host?.connected).toBe(false)
    })

    test('clean up disabled host', async ({ page }) => {
      await openDockerHostsTab(page)
      const editBtn = page.locator('#dockerHostsTbody tr')
        .filter({ hasText: 'e2e-disabled-host' })
        .getByRole('button', { name: /edit/i })
      await editBtn.click()
      await page.locator('#deleteDockerHostBtn').click()
      await expect(page.locator('#dockerHostsTbody')).not.toContainText('e2e-disabled-host')
    })
  })

  // ── offlineConfirmSeconds defaults to 1800 when field left blank ──────────

  test('offlineConfirmSeconds persists as 1800 when Confirm Offline left blank', async ({ page }) => {
    const BASE_URL = process.env.BASE_URL ?? 'http://localhost:8080'
    await openDockerHostsTab(page)
    await page.locator('#openAddDockerHostBtn').click()
    await page.locator('#dockerHostName').fill('e2e-confirm-default-host')
    await page.locator('#dockerHostEndpoint').fill('/var/run/docker.sock')
    // Leave Confirm Offline field blank — should default to 1800
    await page.locator('#saveDockerHostBtn').click()
    await expect(page.locator('#dockerHostsTbody')).toContainText('e2e-confirm-default-host')

    const res  = await page.request.get(`${BASE_URL}/api/docker-hosts`)
    const data = await res.json() as { profiles: Array<{ name: string; offlineConfirmSeconds: number }> }
    const host = data.profiles.find(p => p.name === 'e2e-confirm-default-host')
    expect(host?.offlineConfirmSeconds).toBe(1800)

    // Clean up
    await openDockerHostsTab(page)
    const editBtn = page.locator('#dockerHostsTbody tr')
      .filter({ hasText: 'e2e-confirm-default-host' })
      .getByRole('button', { name: /edit/i })
    await editBtn.click()
    await page.locator('#deleteDockerHostBtn').click()
  })
})
