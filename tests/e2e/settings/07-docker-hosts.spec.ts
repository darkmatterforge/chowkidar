import { test, expect } from '@playwright/test'
import { gotoSettings } from '../helpers/nav'

async function openDockerHostsTab(page: Parameters<Parameters<typeof test>[1]>[0]['page']) {
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
})
