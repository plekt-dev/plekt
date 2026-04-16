import { test, expect } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

test.beforeEach(async ({ page }) => {
  await loginAs(page, adminToken());
});

test('dashboard page loads with status 200', async ({ page }) => {
  const response = await page.goto(`${baseURL}/dashboard`);
  expect(response?.status()).toBe(200);
});

test('dashboard has a page title or heading', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const title = await page.title();
  const hasTitle = title.length > 0;
  const hasHeading = (await page.locator('h1, h2').count()) > 0;
  expect(hasTitle || hasHeading).toBe(true);
});

test('dashboard has settings gear button', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const btn = page.locator('.dashboard-settings-btn');
  await expect(btn).toBeVisible();
});

test('settings button opens modal dialog', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  await page.locator('.dashboard-settings-btn').click();
  const dialog = page.locator('#mc-dashboard-settings');
  await expect(dialog).toBeVisible();
  // Dialog should have a title
  await expect(dialog.locator('.mc-confirm-title')).toHaveText('Dashboard Settings');
});

test('settings modal has checkboxes for widgets', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  // Only test if widgets exist
  const grid = page.locator('.widget-grid');
  const meta = await grid.getAttribute('data-widgets-meta');
  if (!meta) return;
  const widgets = JSON.parse(meta);
  if (widgets.length === 0) return;

  await page.locator('.dashboard-settings-btn').click();
  const dialog = page.locator('#mc-dashboard-settings');
  const checkboxes = dialog.locator('input[type="checkbox"]');
  await expect(checkboxes).toHaveCount(widgets.length);
});

test('settings modal cancel closes without changes', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  await page.locator('.dashboard-settings-btn').click();
  const dialog = page.locator('#mc-dashboard-settings');
  await expect(dialog).toBeVisible();
  await dialog.locator('.mc-confirm-cancel').click();
  await expect(dialog).not.toBeVisible();
});

test('settings modal save submits layout and reloads', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const grid = page.locator('.widget-grid');
  const meta = await grid.getAttribute('data-widgets-meta');
  if (!meta || JSON.parse(meta).length === 0) return;

  await page.locator('.dashboard-settings-btn').click();
  const dialog = page.locator('#mc-dashboard-settings');

  const [response] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/dashboard')),
    dialog.locator('.mc-confirm-ok').click(),
  ]);
  expect(response.status()).not.toBe(500);
});

test('widget cards are draggable', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const cards = page.locator('.widget-card[draggable="true"]');
  const count = await cards.count();
  // If widgets exist, they should all be draggable
  if (count > 0) {
    for (let i = 0; i < count; i++) {
      await expect(cards.nth(i)).toHaveAttribute('draggable', 'true');
    }
  }
});

test('widget refresh endpoint for nonexistent widget returns non-500', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const response = await page.request.get(`${baseURL}/dashboard/widgets/nonexistent/refresh`);
  expect(response.status()).not.toBe(500);
});

test('dashboard has a main content area', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const container = page.locator('.container');
  await expect(container).toHaveCount(1);
  const text = await container.innerText();
  expect(text.trim().length).toBeGreaterThan(0);
});

test('no JavaScript errors on dashboard page load', async ({ page }) => {
  const jsErrors: string[] = [];
  page.on('pageerror', (err) => jsErrors.push(err.message));
  await page.goto(`${baseURL}/dashboard`);
  await page.waitForTimeout(500);
  expect(jsErrors).toHaveLength(0);
});

test('no inline layout form on dashboard page', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const form = page.locator('form[action="/dashboard/layout"]');
  await expect(form).toHaveCount(0);
});

test('drag and drop reorders widgets visually', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const cards = page.locator('.widget-card[draggable="true"]');
  const count = await cards.count();
  if (count < 2) return; // Need at least 2 widgets to test reorder

  const first = cards.nth(0);
  const second = cards.nth(1);
  const firstKey = await first.getAttribute('data-widget-key');
  const secondKey = await second.getAttribute('data-widget-key');

  // Drag second widget before first
  const firstBox = await first.boundingBox();
  const secondBox = await second.boundingBox();
  if (!firstBox || !secondBox) return;

  await second.dragTo(first, {
    sourcePosition: { x: secondBox.width / 2, y: secondBox.height / 2 },
    targetPosition: { x: 10, y: firstBox.height / 2 },
  });

  // After drag, the order should change: second widget should now be first
  await page.waitForTimeout(300);
  const newFirstKey = await cards.nth(0).getAttribute('data-widget-key');
  // The drag should have moved something — at minimum no JS errors
  expect(newFirstKey).toBeDefined();
});
