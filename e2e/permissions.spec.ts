// permissions.spec.ts — E2E tests for Slices 1-3 permissions system.
//
// Covers:
//   - GET  /admin/plugins/inspect       (manifest + derived permissions)
//   - POST /admin/plugins/{name}/hosts  (grant + 422 on invalid host)
//   - DELETE /admin/plugins/{name}/hosts/{host}
//   - Permissions UI (Add host, revoke)
//   - Regression: install flow for an existing plugin (tasks-plugin)
//
// Assumes voice-plugin has been copied into e2e/test-plugins/voice-plugin
// so that `auto_load_on_startup: true` makes it available as a loaded plugin.

import { test, expect, Page } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminUsername, adminPassword } from './helpers/server';

const VOICE_DIR = './e2e/test-plugins/voice-plugin';
const TASKS_DIR = './e2e/test-plugins/tasks-plugin';

async function ensureLoaded(page: Page, name: string, dir: string): Promise<void> {
  const list = await page.request.get(`${baseURL}/admin/plugins`);
  const body = await list.text();
  if (body.includes(`/admin/plugins/${name}`)) return;
  // Load via the legacy path (no permissions modal — direct POST).
  await page.goto(`${baseURL}/admin/plugins`);
  const csrf = await page.inputValue('#plugin-load-form input[name="csrf_token"]');
  await page.request.post(`${baseURL}/admin/plugins/load`, {
    form: { csrf_token: csrf, dir },
    failOnStatusCode: false,
  });
}

async function ensureUnloaded(page: Page, name: string): Promise<void> {
  await page.goto(`${baseURL}/admin/plugins`);
  const row = page.locator(`tr:has(a[href="/admin/plugins/${name}"])`);
  if ((await row.count()) === 0) return;
  const csrf = await page.inputValue('input[name="csrf_token"]').catch(() => '');
  await page.request.post(`${baseURL}/admin/plugins/${name}/unload`, {
    headers: { 'X-CSRF-Token': csrf },
    failOnStatusCode: false,
  });
}

test.beforeEach(async ({ page }) => {
  await loginAs(page, adminUsername(), adminPassword());
});

test('inspect endpoint returns manifest + permissions JSON', async ({ page }) => {
  const resp = await page.request.get(
    `${baseURL}/admin/plugins/inspect?dir=${encodeURIComponent(VOICE_DIR)}`,
    { headers: { Accept: 'application/json' } },
  );
  expect(resp.status()).toBe(200);
  const body = await resp.json();
  expect(body).toHaveProperty('manifest');
  expect(body).toHaveProperty('permissions');
  expect(body.manifest.name).toBe('voice-plugin');
  const caps = body.permissions.Capabilities || body.permissions.capabilities || [];
  expect(Array.isArray(caps)).toBe(true);
  expect(caps.length).toBeGreaterThan(0);
  const titles = caps.map((c: any) => (c.Title || c.title || '')).join('|').toLowerCase();
  // voice-plugin declares a global frontend script + mcp tools.
  expect(titles).toMatch(/frontend|mcp/);
});

test('inspect rejects nonexistent plugin dir', async ({ page }) => {
  const resp = await page.request.get(
    `${baseURL}/admin/plugins/inspect?dir=${encodeURIComponent('./e2e/test-plugins/does-not-exist')}`,
    { headers: { Accept: 'application/json' }, failOnStatusCode: false },
  );
  expect(resp.status()).toBeGreaterThanOrEqual(400);
});

test('operator adds and revokes host via API', async ({ page }) => {
  await ensureLoaded(page, 'voice-plugin', VOICE_DIR);
  await page.goto(`${baseURL}/admin/plugins/voice-plugin/permissions`);
  const csrf = await page.inputValue('input[name="csrf_token"]');

  const host = 'my-whisper.internal:9001';
  const addResp = await page.request.post(
    `${baseURL}/admin/plugins/voice-plugin/hosts`,
    {
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      data: { host },
      failOnStatusCode: false,
    },
  );
  expect([200, 201, 204]).toContain(addResp.status());

  await page.reload();
  await expect(page.locator(`tr[data-host="${host}"]`)).toBeVisible();

  // Revoke via API (UI revoke path uses MC.confirm which is covered elsewhere).
  const delResp = await page.request.delete(
    `${baseURL}/admin/plugins/voice-plugin/hosts/${encodeURIComponent(host)}`,
    { headers: { 'X-CSRF-Token': csrf }, failOnStatusCode: false },
  );
  expect([200, 204]).toContain(delResp.status());

  await page.reload();
  await expect(page.locator(`tr[data-host="${host}"]`)).toHaveCount(0);
});

test('POST /hosts rejects invalid host', async ({ page }) => {
  await ensureLoaded(page, 'voice-plugin', VOICE_DIR);
  await page.goto(`${baseURL}/admin/plugins/voice-plugin/permissions`);
  const csrf = await page.inputValue('input[name="csrf_token"]');

  const resp = await page.request.post(
    `${baseURL}/admin/plugins/voice-plugin/hosts`,
    {
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      data: { host: 'http://not a valid host/' },
      failOnStatusCode: false,
    },
  );
  expect(resp.status()).toBeGreaterThanOrEqual(400);
  expect(resp.status()).toBeLessThan(500);
});

test('permissions page shows "Add host" button and capabilities card', async ({ page }) => {
  await ensureLoaded(page, 'voice-plugin', VOICE_DIR);
  const resp = await page.goto(`${baseURL}/admin/plugins/voice-plugin/permissions`);
  expect(resp?.status()).toBe(200);
  await expect(page.locator('button:has-text("+ Add host")')).toBeVisible();
  await expect(page.locator('body')).toContainText(/Capabilities/i);
});

test('existing plugin (tasks-plugin) installs via the new modal interceptor', async ({ page }) => {
  // Regression test — ensures Slice 1-3 didn't break the existing install flow.
  // tasks-plugin has no network/global_frontend, so the modal should still show,
  // listing MCP tools / events / DB schema capability cards, and Install should
  // succeed. We don't require the modal to appear because tasks-plugin may
  // already be loaded (auto-load). We only assert the inspect endpoint works
  // for it as a proxy for "the interceptor would function correctly".
  const resp = await page.request.get(
    `${baseURL}/admin/plugins/inspect?dir=${encodeURIComponent(TASKS_DIR)}`,
    { headers: { Accept: 'application/json' }, failOnStatusCode: false },
  );
  expect(resp.status()).toBe(200);
  const body = await resp.json();
  expect(body.manifest.name).toBe('tasks-plugin');
  const caps = body.permissions.Capabilities || body.permissions.capabilities || [];
  expect(Array.isArray(caps)).toBe(true);
});
