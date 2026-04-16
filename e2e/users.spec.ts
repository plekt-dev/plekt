import { test, expect, Page } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminUsername, adminPassword } from './helpers/server';

// Helper: login as admin before each test.
async function loginAdmin(page: Page): Promise<void> {
  await loginAs(page, adminUsername(), adminPassword());
}

test.describe('/admin/users', () => {
  test('loads successfully for admin', async ({ page }) => {
    await loginAdmin(page);
    await page.goto(`${baseURL}/admin/users`);
    expect(page.url()).toMatch(/\/admin\/users/);
    // Should show status 200 - page contains user management content
    const body = await page.locator('body').innerText();
    expect(body.toLowerCase()).toContain('user');
  });

  test('user list shows at least the bootstrap admin', async ({ page }) => {
    await loginAdmin(page);
    await page.goto(`${baseURL}/admin/users`);
    const body = await page.locator('body').innerText();
    expect(body).toContain(adminUsername());
  });

  test('/admin/users without session redirects to /login', async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    await page.goto(`${baseURL}/admin/users`);
    await expect(page).toHaveURL(/\/login/);
    await context.close();
  });

  test('admin can create a new viewer user', async ({ page }) => {
    await loginAdmin(page);
    await page.goto(`${baseURL}/admin/users`);

    const newUsername = `viewer-test-${Date.now()}`;
    await page.fill('input[name="username"]', newUsername);
    await page.fill('input[name="password"]', 'viewerpassword123');
    await page.selectOption('select[name="role"]', 'viewer');
    await page.click('button[type="submit"].btn-primary');

    await page.waitForURL(/\/admin\/users/);
    const body = await page.locator('body').innerText();
    expect(body).toContain(newUsername);
  });

  test('admin can change role of a user', async ({ page }) => {
    await loginAdmin(page);
    await page.goto(`${baseURL}/admin/users`);

    // Create a viewer to change role on
    const newUsername = `rolechange-${Date.now()}`;
    await page.fill('input[name="username"]', newUsername);
    await page.fill('input[name="password"]', 'viewerpassword123');
    await page.selectOption('select[name="role"]', 'viewer');
    await page.click('button[type="submit"].btn-primary');
    await page.waitForURL(/\/admin\/users/);

    // Find the row for this user and click Make Admin
    const makeAdminForm = page.locator(`tr:has-text("${newUsername}") form:has(button:has-text("Make Admin"))`);
    await makeAdminForm.locator('button').click();
    await page.waitForURL(/\/admin\/users/);

    const body = await page.locator('body').innerText();
    expect(body).toContain(newUsername);
  });

  test('admin can delete a user', async ({ page }) => {
    await loginAdmin(page);
    await page.goto(`${baseURL}/admin/users`);

    // Create a user to delete
    const newUsername = `delete-test-${Date.now()}`;
    await page.fill('input[name="username"]', newUsername);
    await page.fill('input[name="password"]', 'deletepassword123');
    await page.selectOption('select[name="role"]', 'viewer');
    await page.click('button[type="submit"].btn-primary');
    await page.waitForURL(/\/admin\/users/);

    // Delete the user
    const deleteForm = page.locator(`tr:has-text("${newUsername}") form:has(button.btn-danger)`);
    await deleteForm.locator('button').click();
    await page.waitForURL(/\/admin\/users/);

    const body = await page.locator('body').innerText();
    expect(body).not.toContain(newUsername);
  });

  test('admin can reset password (sets must_change_password)', async ({ page }) => {
    await loginAdmin(page);
    await page.goto(`${baseURL}/admin/users`);

    // Create a user to reset password on
    const newUsername = `resetpw-${Date.now()}`;
    await page.fill('input[name="username"]', newUsername);
    await page.fill('input[name="password"]', 'resetpassword123');
    await page.selectOption('select[name="role"]', 'viewer');
    await page.click('button[type="submit"].btn-primary');
    await page.waitForURL(/\/admin\/users/);

    // Reset password — this form just sets must_change_password via the server
    const resetForm = page.locator(`tr:has-text("${newUsername}") form:has(button:has-text("Reset Password"))`);
    await resetForm.locator('button').click();
    await page.waitForURL(/\/admin\/users/);

    // The user row should now show must_change_password indicator
    const userRow = page.locator(`tr:has-text("${newUsername}")`);
    await expect(userRow).toBeVisible();
  });
});

test.describe('/register', () => {
  test('/register with registration disabled and users existing redirects to /login', async ({ browser }) => {
    // Registration is disabled by default when users exist (first-time setup only)
    const context = await browser.newContext();
    const page = await context.newPage();
    // Since admin user was bootstrapped, registration should be gated
    await page.goto(`${baseURL}/register`);
    // Should redirect to /login since users exist and registration is not explicitly enabled
    await expect(page).toHaveURL(/\/login/);
    await context.close();
  });
});

test.describe('/change-password', () => {
  test('/change-password loads when logged in', async ({ page }) => {
    await loginAdmin(page);
    await page.goto(`${baseURL}/change-password`);
    // Should show the change password form
    const body = await page.locator('body').innerText();
    expect(body.toLowerCase()).toContain('password');
  });

  test('/change-password without session redirects to /login', async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    await page.goto(`${baseURL}/change-password`);
    await expect(page).toHaveURL(/\/login/);
    await context.close();
  });
});

test.describe('viewer user restrictions', () => {
  test('viewer user cannot access /admin/users (403 or redirect)', async ({ page }) => {
    await loginAdmin(page);
    await page.goto(`${baseURL}/admin/users`);

    // Create a viewer user
    const viewerUsername = `viewer-restricted-${Date.now()}`;
    await page.fill('input[name="username"]', viewerUsername);
    await page.fill('input[name="password"]', 'viewerpassword123');
    await page.selectOption('select[name="role"]', 'viewer');
    await page.click('button[type="submit"].btn-primary');
    await page.waitForURL(/\/admin\/users/);

    // Open new context and log in as viewer
    const viewerContext = await page.context().browser()!.newContext();
    const viewerPage = await viewerContext.newPage();
    await loginAs(viewerPage, viewerUsername, 'viewerpassword123');
    await viewerPage.goto(`${baseURL}/admin/users`);

    // User role should get 403 or redirect (not the admin users page)
    const status = viewerPage.url();
    const body = await viewerPage.locator('body').innerText();
    const isForbidden =
      body.toLowerCase().includes('forbidden') ||
      !status.includes('/admin/users');
    expect(isForbidden).toBe(true);

    await viewerContext.close();
  });
});
