import { test, expect } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

test.beforeEach(async ({ page }) => {
  // Clear language cookie before each test
  await page.context().clearCookies();
  await loginAs(page, adminToken());
});

// --- Default language (English) ---

test('default language is English', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const html = page.locator('html');
  await expect(html).toHaveAttribute('lang', 'en');
});

test('sidebar nav shows English labels by default', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  await expect(page.locator('#sidebar nav a[href="/dashboard"] .nav-text')).toHaveText('Dashboard');
  await expect(page.locator('#sidebar nav a[href="/plugins"] .nav-text')).toHaveText('Plugins');
  await expect(page.locator('#sidebar nav a[href="/tokens"] .nav-text')).toHaveText('Tokens');
});

test('sidebar section labels are English by default', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const workspaceLabel = page.locator('.sidebar-label').first();
  await expect(workspaceLabel).toHaveText('Workspace');
});

test('administration section has English labels', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const adminLabel = page.locator('.sidebar-label:has-text("Administration")');
  await expect(adminLabel).toBeVisible();
  await expect(page.locator('#sidebar nav a[href="/admin/settings"] .nav-text')).toHaveText('Settings');
  await expect(page.locator('#sidebar nav a[href="/admin/users"] .nav-text')).toHaveText('Users');
});

// --- Language switcher (lang-picker) ---

test('language picker exists in sidebar footer', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const picker = page.locator('.lang-picker');
  await expect(picker).toBeVisible();
});

test('language picker has EN, DE, RU options', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  await expect(page.locator('.lang-picker-item[data-lang="en"]')).toHaveCount(1);
  await expect(page.locator('.lang-picker-item[data-lang="de"]')).toHaveCount(1);
  await expect(page.locator('.lang-picker-item[data-lang="ru"]')).toHaveCount(1);
});

test('language picker shows current language label', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const label = page.locator('.lang-picker-label');
  await expect(label).toHaveText('English');
});

// --- Switch to German ---

test('switching to German via picker changes nav labels', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);

  // Open picker and click DE
  await page.locator('.lang-picker-toggle').click();
  await page.locator('.lang-picker-item[data-lang="de"]').click();

  // Wait for page refresh
  await page.waitForLoadState('networkidle');
  await page.waitForTimeout(500);

  // Verify German labels
  await expect(page.locator('html')).toHaveAttribute('lang', 'de');
  await expect(page.locator('#sidebar nav a[href="/dashboard"] .nav-text')).toHaveText('Dashboard');
  await expect(page.locator('#sidebar nav a[href="/plugins"] .nav-text')).toHaveText('Plugins');
  await expect(page.locator('#sidebar nav a[href="/tokens"] .nav-text')).toHaveText('Tokens');
});

test('German mode shows Arbeitsbereich and Verwaltung labels', async ({ page }) => {
  await page.context().addCookies([{
    name: 'mc_lang',
    value: 'de',
    url: baseURL,
  }]);
  await page.goto(`${baseURL}/dashboard`);

  const workspaceLabel = page.locator('.sidebar-label').first();
  await expect(workspaceLabel).toHaveText('Arbeitsbereich');

  const adminLabel = page.locator('.sidebar-label:has-text("Verwaltung")');
  await expect(adminLabel).toBeVisible();
});

test('German settings page shows translated labels', async ({ page }) => {
  await page.context().addCookies([{
    name: 'mc_lang',
    value: 'de',
    url: baseURL,
  }]);
  await page.goto(`${baseURL}/admin/settings`);

  const submitBtn = page.locator('form[action="/admin/settings"] button[type="submit"]');
  await expect(submitBtn).toHaveText('Einstellungen speichern');

  const langLabel = page.locator('label[for="language"]');
  await expect(langLabel).toHaveText('Sprache');
});

// --- Switch to Russian ---

test('Russian mode shows correct sidebar labels', async ({ page }) => {
  await page.context().addCookies([{
    name: 'mc_lang',
    value: 'ru',
    url: baseURL,
  }]);
  await page.goto(`${baseURL}/dashboard`);

  await expect(page.locator('html')).toHaveAttribute('lang', 'ru');
  await expect(page.locator('#sidebar nav a[href="/dashboard"] .nav-text')).toHaveText('Дашборд');
  await expect(page.locator('#sidebar nav a[href="/plugins"] .nav-text')).toHaveText('Плагины');
  await expect(page.locator('#sidebar nav a[href="/tokens"] .nav-text')).toHaveText('Токены');
});

test('Russian sidebar section labels', async ({ page }) => {
  await page.context().addCookies([{
    name: 'mc_lang',
    value: 'ru',
    url: baseURL,
  }]);
  await page.goto(`${baseURL}/dashboard`);

  const workspaceLabel = page.locator('.sidebar-label').first();
  await expect(workspaceLabel).toHaveText('Рабочее пространство');

  const adminLabel = page.locator('.sidebar-label:has-text("Администрирование")');
  await expect(adminLabel).toBeVisible();
});

test('Russian admin nav items are translated', async ({ page }) => {
  await page.context().addCookies([{
    name: 'mc_lang',
    value: 'ru',
    url: baseURL,
  }]);
  await page.goto(`${baseURL}/dashboard`);

  await expect(page.locator('#sidebar nav a[href="/admin/profile"] .nav-text')).toHaveText('Профиль');
  await expect(page.locator('#sidebar nav a[href="/admin/sessions"] .nav-text')).toHaveText('Сессии');
  await expect(page.locator('#sidebar nav a[href="/admin/audit"] .nav-text')).toHaveText('Журнал аудита');
  await expect(page.locator('#sidebar nav a[href="/admin/users"] .nav-text')).toHaveText('Пользователи');
  await expect(page.locator('#sidebar nav a[href="/admin/settings"] .nav-text')).toHaveText('Настройки');
});

test('Russian settings page shows translated submit button', async ({ page }) => {
  await page.context().addCookies([{
    name: 'mc_lang',
    value: 'ru',
    url: baseURL,
  }]);
  await page.goto(`${baseURL}/admin/settings`);

  const submitBtn = page.locator('form[action="/admin/settings"] button[type="submit"]');
  await expect(submitBtn).toHaveText('Сохранить настройки');
});

// --- Cookie persistence ---

test('language cookie persists across page navigations', async ({ page }) => {
  await page.context().addCookies([{
    name: 'mc_lang',
    value: 'ru',
    url: baseURL,
  }]);

  await page.goto(`${baseURL}/dashboard`);
  await expect(page.locator('html')).toHaveAttribute('lang', 'ru');

  await page.goto(`${baseURL}/plugins`);
  await expect(page.locator('html')).toHaveAttribute('lang', 'ru');

  await page.goto(`${baseURL}/admin/settings`);
  await expect(page.locator('html')).toHaveAttribute('lang', 'ru');
});

// --- POST /api/language endpoint ---

test('POST /api/language sets mc_lang cookie', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const csrfToken = await page.inputValue('.lang-picker input[name="csrf_token"], #sidebar input[name="csrf_token"]');

  const response = await page.request.post(`${baseURL}/api/language`, {
    form: {
      language: 'de',
      csrf_token: csrfToken,
    },
  });
  expect(response.status()).toBe(204);
  expect(response.headers()['hx-refresh']).toBe('true');
});

test('POST /api/language with unsupported lang falls back to en', async ({ page }) => {
  await page.goto(`${baseURL}/dashboard`);
  const csrfToken = await page.inputValue('#sidebar input[name="csrf_token"]');

  await page.request.post(`${baseURL}/api/language`, {
    form: {
      language: 'xx',
      csrf_token: csrfToken,
    },
  });

  await page.goto(`${baseURL}/dashboard`);
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
});

// --- Settings language field ---

test('settings page has language select field', async ({ page }) => {
  await page.goto(`${baseURL}/admin/settings`);
  const langSelect = page.locator('select[name="language"]#language');
  await expect(langSelect).toBeVisible();
  await expect(langSelect.locator('option')).toHaveCount(3);
});

test('language setting persists after save', async ({ page }) => {
  await page.goto(`${baseURL}/admin/settings`);

  await page.selectOption('select[name="language"]#language', 'de');
  await page.locator('form[action="/admin/settings"] button[type="submit"]').click();

  await page.goto(`${baseURL}/admin/settings`);
  const savedLang = await page.inputValue('select[name="language"]#language');
  expect(savedLang).toBe('de');
});

// --- No JS errors ---

test('no JavaScript errors during language switch', async ({ page }) => {
  const jsErrors: string[] = [];
  page.on('pageerror', (err) => jsErrors.push(err.message));

  await page.goto(`${baseURL}/dashboard`);

  // Switch language via picker
  await page.locator('.lang-picker-toggle').click();
  await page.locator('.lang-picker-item[data-lang="ru"]').click();
  await page.waitForLoadState('networkidle');
  await page.waitForTimeout(500);

  expect(jsErrors).toHaveLength(0);
});

// --- html lang attribute ---

test('html lang attribute matches cookie language', async ({ page }) => {
  const languages = ['en', 'de', 'ru'];
  for (const lang of languages) {
    await page.context().addCookies([{
      name: 'mc_lang',
      value: lang,
      url: baseURL,
    }]);
    await page.goto(`${baseURL}/dashboard`);
    await expect(page.locator('html')).toHaveAttribute('lang', lang);
  }
});
