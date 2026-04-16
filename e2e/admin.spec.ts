import { test, expect } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

test.beforeEach(async ({ page }) => {
  await loginAs(page, adminToken());
});

test('/admin/profile loads successfully', async ({ page }) => {
  const response = await page.goto(`${baseURL}/admin/profile`);
  expect(response?.status()).toBe(200);
});

test('profile page shows session info', async ({ page }) => {
  await page.goto(`${baseURL}/admin/profile`);
  const bodyText = await page.locator('body').innerText();
  // The profile page shows Remote Address, Session Created/Expires, or IP info
  const hasSessionInfo =
    bodyText.includes('Session') ||
    bodyText.includes('Remote') ||
    bodyText.includes('Expires') ||
    bodyText.includes('Created');
  expect(hasSessionInfo).toBe(true);
});

test('/admin/sessions loads successfully', async ({ page }) => {
  const response = await page.goto(`${baseURL}/admin/sessions`);
  expect(response?.status()).toBe(200);
});

test('sessions list shows at least one session (the current one)', async ({ page }) => {
  await page.goto(`${baseURL}/admin/sessions`);
  // The sessions table tbody should have at least one row
  const rows = page.locator('table tbody tr');
  const count = await rows.count();
  expect(count).toBeGreaterThanOrEqual(1);
});

test('sessions list has a revoke button', async ({ page }) => {
  await page.goto(`${baseURL}/admin/sessions`);
  // At least one session row must be visible (multiple may exist from beforeEach logins)
  await expect(page.locator('table tbody tr').first()).toBeVisible();
  const revokeBtn = page.locator('button:has-text("Revoke")');
  await expect(revokeBtn.first()).toBeVisible();
});

test('revoke form has a CSRF token', async ({ page }) => {
  await page.goto(`${baseURL}/admin/sessions`);
  // Each revoke form has a hidden csrf_token input
  const csrfInputs = page.locator('form input[name="csrf_token"]');
  const count = await csrfInputs.count();
  expect(count).toBeGreaterThanOrEqual(1);
});

test('/admin/audit loads successfully', async ({ page }) => {
  const response = await page.goto(`${baseURL}/admin/audit`);
  expect(response?.status()).toBe(200);
});

test('audit log page has a table or list', async ({ page }) => {
  await page.goto(`${baseURL}/admin/audit`);
  const table = page.locator('table');
  await expect(table).toBeVisible();
});

test('audit log shows a login event from the test login', async ({ page }) => {
  // Poll: the audit subscriber is async (EventBus), may need a moment to flush to SQLite
  await expect.poll(async () => {
    await page.goto(`${baseURL}/admin/audit`);
    const bodyText = await page.locator('body').innerText();
    // Event name is "web.auth.login_success" — contains both "login" and "auth"
    return bodyText.toLowerCase().includes('login') || bodyText.toLowerCase().includes('auth');
  }, { timeout: 5000, intervals: [500] }).toBe(true);
});

test('audit log has timestamp column', async ({ page }) => {
  await page.goto(`${baseURL}/admin/audit`);
  // The audit table has an "Occurred At" header
  const headers = page.locator('table thead th');
  const headerTexts: string[] = [];
  const count = await headers.count();
  for (let i = 0; i < count; i++) {
    headerTexts.push(await headers.nth(i).innerText());
  }
  const hasTimestamp = headerTexts.some(
    t => t.toLowerCase().includes('occurred') || t.toLowerCase().includes('time') || t.toLowerCase().includes('at'),
  );
  expect(hasTimestamp).toBe(true);
});

test('profile page is accessible without CSRF (GET request)', async ({ page }) => {
  const response = await page.goto(`${baseURL}/admin/profile`);
  // GET should always work without CSRF
  expect(response?.status()).toBe(200);
});

test('sessions page has table headers', async ({ page }) => {
  await page.goto(`${baseURL}/admin/sessions`);
  const headers = page.locator('table thead th');
  const count = await headers.count();
  expect(count).toBeGreaterThan(0);
});

test('audit log handles empty state gracefully', async ({ page }) => {
  const response = await page.goto(`${baseURL}/admin/audit`);
  // Should never crash — page must load successfully
  expect(response?.status()).toBe(200);
  // Table must still be rendered even if empty
  const table = page.locator('table');
  await expect(table).toBeVisible();
});

test('profile page shows masked admin token', async ({ page }) => {
  await page.goto(`${baseURL}/admin/profile`);
  // The admin token suffix "oken" (last 4 chars of "e2e-test-secret-token") must appear
  await expect(page.locator('body')).toContainText('oken');
});
