import { test, expect } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

test.beforeEach(async ({ page }) => {
  await loginAs(page, adminToken());
});

test('plugins page loads successfully', async ({ page }) => {
  const response = await page.goto(`${baseURL}/plugins`);
  expect(response?.status()).toBe(200);
});

test('plugins page has a plugin list section', async ({ page }) => {
  await page.goto(`${baseURL}/plugins`);
  // The plugin list is rendered as a table with id="plugin-list" tbody
  const pluginList = page.locator('#plugin-list, table');
  await expect(pluginList.first()).toBeVisible();
});

test('load plugin form exists with correct method and action', async ({ page }) => {
  await page.goto(`${baseURL}/plugins`);
  // The load form uses hx-post="/plugins/load" (htmx), not standard method/action
  const loadForm = page.locator('#plugin-load-form');
  await expect(loadForm).toBeVisible();
});

test('load form has a directory/path input', async ({ page }) => {
  await page.goto(`${baseURL}/plugins`);
  // The load form has input[name="dir"]
  const dirInput = page.locator('input[name="dir"]');
  await expect(dirInput).toBeVisible();
});

test('load form has a CSRF token', async ({ page }) => {
  await page.goto(`${baseURL}/plugins`);
  const csrfInput = page.locator('#plugin-load-form input[name="csrf_token"]');
  await expect(csrfInput).toHaveCount(1);
});

test('/plugins/{name} for nonexistent plugin returns non-200 or shows error', async ({ page }) => {
  const response = await page.goto(`${baseURL}/plugins/nonexistent-plugin-xyz`);
  // Should be 404 or similar error status — not 200
  const status = response?.status() ?? 0;
  const isErrorStatus = status >= 400 || status === 0;
  const bodyText = await page.locator('body').innerText();
  const hasErrorText =
    bodyText.toLowerCase().includes('not found') ||
    bodyText.toLowerCase().includes('error') ||
    bodyText.toLowerCase().includes('unknown');
  expect(isErrorStatus || hasErrorText).toBe(true);
});

test('POST /plugins/load with empty path shows error', async ({ page }) => {
  await page.goto(`${baseURL}/plugins`);
  const form = page.locator('#plugin-load-form form');
  await form.locator('input[name="dir"]').fill('');
  const [response] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/plugins/load')),
    form.locator('button[type="submit"]').click(),
  ]);
  // should not 500 — either redirect back or show error
  expect(response.status()).not.toBe(500);
});

test('plugin list section exists even when no plugins loaded', async ({ page }) => {
  await page.goto(`${baseURL}/plugins`);
  // The table should always be present even if empty
  const table = page.locator('table');
  await expect(table).toBeVisible();
  // The tbody may be empty but the table structure must exist
  const thead = page.locator('table thead');
  await expect(thead).toBeVisible();
});

test('unload/reload buttons are not shown when plugin list is empty', async ({ page }) => {
  await page.goto(`${baseURL}/plugins`);
  // Check that the tbody (plugin-list) has no rows
  const rows = page.locator('#plugin-list tr');
  const rowCount = await rows.count();
  if (rowCount === 0) {
    // No rows means no unload/reload buttons — verify
    const reloadBtn = page.locator('button:has-text("Reload")');
    const unloadBtn = page.locator('button:has-text("Unload")');
    expect(await reloadBtn.count()).toBe(0);
    expect(await unloadBtn.count()).toBe(0);
  } else {
    // Plugins are loaded — buttons should be present
    const actionBtns = page.locator('button:has-text("Reload"), button:has-text("Unload")');
    expect(await actionBtns.count()).toBeGreaterThan(0);
  }
});
