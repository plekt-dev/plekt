import { test, expect } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

const authedPages = [
  { path: '/dashboard', title: 'Dashboard' },
  { path: '/tokens', title: 'Tokens' },
  { path: '/plugins', title: 'Plugins' },
  { path: '/admin/profile', title: 'Profile' },
  { path: '/admin/sessions', title: 'Sessions' },
  { path: '/admin/audit', title: 'Audit Log' },
];

test.beforeEach(async ({ page }) => {
  await loginAs(page, adminToken());
});

for (const { path, title } of authedPages) {
  test(`${path} has consistent sidebar nav`, async ({ page }) => {
    await page.goto(`${baseURL}${path}`);
    // sidebar exists
    await expect(page.locator('#sidebar')).toHaveCount(1);
    // title contains Plekt
    await expect(page).toHaveTitle(/Plekt/);
    // nav links present in sidebar
    await expect(page.locator('#sidebar nav a[href="/dashboard"]')).toBeVisible();
    await expect(page.locator('#sidebar nav a[href="/tokens"]')).toBeVisible();
    // logout form in sidebar footer
    await expect(page.locator('#sidebar form[action="/logout"]')).toHaveCount(1);
  });
}
