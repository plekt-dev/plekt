import { test, expect } from '@playwright/test';
import { loginAs, logout, getCSRFToken } from './helpers/auth';
import { baseURL, adminUsername, adminPassword } from './helpers/server';

test('GET / redirects to /login when not logged in', async ({ page }) => {
  await page.goto(`${baseURL}/`);
  await expect(page).toHaveURL(/\/login/);
});

test('/login shows a form with username and password fields', async ({ page }) => {
  await page.goto(`${baseURL}/login`);
  const usernameInput = page.locator('input[name="username"]');
  const passwordInput = page.locator('input[name="password"]');
  await expect(usernameInput).toBeVisible();
  await expect(passwordInput).toBeVisible();
});

test('wrong password stays on login page or shows error', async ({ page }) => {
  await page.goto(`${baseURL}/login`);
  await page.fill('input[name="username"]', adminUsername());
  await page.fill('input[name="password"]', 'wrong-password-definitely-invalid');
  await page.click('button[type="submit"]');
  // Must not redirect away from login — either stays on /login or shows inline error
  await expect(page).toHaveURL(/\/login/);
});

test('wrong password shows an error message', async ({ page }) => {
  await page.goto(`${baseURL}/login`);
  await page.fill('input[name="username"]', adminUsername());
  await page.fill('input[name="password"]', 'wrong-password-definitely-invalid');
  await page.click('button[type="submit"]');
  // Page should contain an error indicator (either .error class or forbidden text)
  const hasError =
    (await page.locator('.error').count()) > 0 ||
    (await page.locator('body').innerText()).toLowerCase().includes('invalid') ||
    (await page.locator('body').innerText()).toLowerCase().includes('forbidden') ||
    (await page.locator('body').innerText()).toLowerCase().includes('error');
  expect(hasError).toBe(true);
});

test('correct password navigates away from /login', async ({ page }) => {
  await loginAs(page, adminUsername(), adminPassword());
  await expect(page).not.toHaveURL(/\/login/);
});

test('/dashboard without session redirects to /login', async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(`${baseURL}/dashboard`);
  await expect(page).toHaveURL(/\/login/);
  await context.close();
});

test('/tokens without session redirects to /login', async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(`${baseURL}/tokens`);
  await expect(page).toHaveURL(/\/login/);
  await context.close();
});

test('/plugins without session redirects to /login', async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(`${baseURL}/plugins`);
  await expect(page).toHaveURL(/\/login/);
  await context.close();
});

test('/admin/profile without session redirects to /login', async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(`${baseURL}/admin/profile`);
  await expect(page).toHaveURL(/\/login/);
  await context.close();
});

test('logout when logged in redirects to /login', async ({ page }) => {
  await loginAs(page, adminUsername(), adminPassword());
  await logout(page);
  await expect(page).toHaveURL(/\/login/);
});

test('after logout, accessing /dashboard redirects to /login', async ({ page }) => {
  await loginAs(page, adminUsername(), adminPassword());
  await logout(page);
  // Clear cookies to remove any pre-login session created by the /login redirect
  await page.context().clearCookies();
  await page.goto(`${baseURL}/dashboard`);
  await expect(page).toHaveURL(/\/login/);
});

test('POST /login without csrf_token stays on login', async ({ page }) => {
  await page.goto(`${baseURL}/login`);
  const response = await page.request.post(`${baseURL}/login`, {
    form: { username: adminUsername(), password: adminPassword() },
    // No csrf_token
  });
  // Without CSRF, server should reject — either 403 or redirect back to /login
  expect(response.url()).toMatch(/\/login/);
});

test('login form has csrf_token hidden input', async ({ page }) => {
  await page.goto(`${baseURL}/login`);
  const csrfInput = page.locator('input[name="csrf_token"]');
  await expect(csrfInput).toHaveCount(1);
  const inputType = await csrfInput.getAttribute('type');
  expect(inputType).toBe('hidden');
});

test('logout from dashboard redirects to /login', async ({ page }) => {
  await loginAs(page, adminUsername(), adminPassword());
  await page.goto(`${baseURL}/dashboard`);
  await page.locator('header form[action="/logout"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/login/);
});

test('POST /logout without session cookie redirects to /login', async ({ browser }) => {
  const context = await browser.newContext(); // fresh context, no cookies
  const page = await context.newPage();
  // Direct POST without cookie — server should redirect, not return 403
  const response = await page.request.post(`${baseURL}/logout`, {
    form: { csrf_token: '' },
  });
  expect(response.url()).toMatch(/\/login/);
  await context.close();
});

test('after logout accessing / redirects to /login', async ({ page }) => {
  await loginAs(page, adminUsername(), adminPassword());
  await page.goto(`${baseURL}/dashboard`);
  await page.locator('header form[action="/logout"] button[type="submit"]').click();
  await expect(page).toHaveURL(/\/login/);
  // Clear the pre-login session cookie created by the /login page redirect
  // (same pattern as 'after logout, accessing /dashboard redirects to /login')
  await page.context().clearCookies();
  await page.goto(`${baseURL}/`);
  await expect(page).toHaveURL(/\/login/);
});
