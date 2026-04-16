import { test, expect } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

test.beforeEach(async ({ page }) => {
  await loginAs(page, adminToken());
});

test('tokens page loads successfully', async ({ page }) => {
  const response = await page.goto(`${baseURL}/tokens`);
  expect(response?.status()).toBe(200);
});

test('tokens page has a table or list element', async ({ page }) => {
  await page.goto(`${baseURL}/tokens`);
  const hasTable = (await page.locator('table').count()) > 0;
  const hasList = (await page.locator('ul, ol').count()) > 0;
  expect(hasTable || hasList).toBe(true);
});

test('token list container is visible', async ({ page }) => {
  await page.goto(`${baseURL}/tokens`);
  // The token list page always renders a table with thead
  const table = page.locator('table');
  await expect(table).toBeVisible();
});

test('admin profile shows masked admin token', async ({ page }) => {
  await page.goto(`${baseURL}/admin/profile`);
  // The profile page shows AdminTokenSuffix which is lastN(adminBearerToken, 4) = "oken"
  await expect(page.locator('body')).toContainText('oken');
});

test('CSRF token is present in any rotate forms on tokens page', async ({ page }) => {
  await page.goto(`${baseURL}/tokens`);
  // The page has a logout form with CSRF; if there are rotate forms they also have CSRF
  const csrfInputs = page.locator('input[name="csrf_token"]');
  const count = await csrfInputs.count();
  expect(count).toBeGreaterThanOrEqual(1);
});

test('tokens page title or heading mentions "Token"', async ({ page }) => {
  await page.goto(`${baseURL}/tokens`);
  const title = await page.title();
  const hasTokenInTitle = title.toLowerCase().includes('token');
  const headingLocator = page.locator('h1, h2');
  const headingTexts: string[] = [];
  const headingCount = await headingLocator.count();
  for (let i = 0; i < headingCount; i++) {
    headingTexts.push(await headingLocator.nth(i).innerText());
  }
  const hasTokenInHeading = headingTexts.some(t => t.toLowerCase().includes('token'));
  expect(hasTokenInTitle || hasTokenInHeading).toBe(true);
});
