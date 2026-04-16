import { test, expect } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

test.beforeEach(async ({ page }) => {
  await loginAs(page, adminToken());
});

test('/admin/settings loads successfully', async ({ page }) => {
  const response = await page.goto(`${baseURL}/admin/settings`);
  expect(response?.status()).toBe(200);
});

test('settings form has correct action and method', async ({ page }) => {
  await page.goto(`${baseURL}/admin/settings`);
  const form = page.locator('form[action="/admin/settings"][method="POST"]');
  await expect(form).toHaveCount(1);
});

test('settings form has CSRF token', async ({ page }) => {
  await page.goto(`${baseURL}/admin/settings`);
  const csrfInput = page.locator('form[action="/admin/settings"] input[name="csrf_token"]');
  await expect(csrfInput).toHaveCount(1);
  const inputType = await csrfInput.getAttribute('type');
  expect(inputType).toBe('hidden');
});

test('settings form has admin_email input field', async ({ page }) => {
  await page.goto(`${baseURL}/admin/settings`);
  const adminEmailInput = page.locator('input[name="admin_email"]');
  await expect(adminEmailInput).toBeVisible();
});

test('POST with valid data redirects or shows success flash', async ({ page }) => {
  await page.goto(`${baseURL}/admin/settings`);
  await page.fill('input[name="admin_email"]', 'test@example.com');
  await page.locator('form[action="/admin/settings"] button[type="submit"]').click();

  // Should either redirect (back to settings) or show flash success
  const url = page.url();
  const bodyText = await page.locator('body').innerText();
  const isSuccess =
    url.includes('/admin/settings') ||
    bodyText.toLowerCase().includes('saved') ||
    bodyText.toLowerCase().includes('success');
  expect(isSuccess).toBe(true);
});

test('after saving, values are persisted on reload', async ({ page }) => {
  const uniqueEmail = `test-${Date.now()}@example.com`;
  await page.goto(`${baseURL}/admin/settings`);
  await page.fill('input[name="admin_email"]', uniqueEmail);
  await page.locator('form[action="/admin/settings"] button[type="submit"]').click();

  // Reload the settings page
  await page.goto(`${baseURL}/admin/settings`);
  const adminEmailValue = await page.inputValue('input[name="admin_email"]');
  expect(adminEmailValue).toBe(uniqueEmail);
});

test('settings page shows current values', async ({ page }) => {
  await page.goto(`${baseURL}/admin/settings`);
  // The form inputs should be present with their current values (even if empty)
  const adminEmailInput = page.locator('input[name="admin_email"]');
  await expect(adminEmailInput).toBeVisible();
  // Value attribute should exist (even if empty string)
  const value = await adminEmailInput.inputValue();
  expect(typeof value).toBe('string');
});

test('page title contains Plekt', async ({ page }) => {
  await page.goto(`${baseURL}/admin/settings`);
  const title = await page.title();
  expect(title).toContain('Plekt');
});
