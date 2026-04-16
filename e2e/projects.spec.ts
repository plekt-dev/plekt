/**
 * projects.spec.ts — E2E tests for the projects-plugin MCP tools, dashboard widget,
 * and project detail composite page.
 *
 * The projects-plugin must be loaded before these tests run. If the plugin is not
 * loaded (WASM binary not present), all tests skip gracefully.
 *
 * Plugin loading: the test setup attempts to load the plugin via the admin web
 * UI (POST /plugins/load). A compiled plugin.wasm must exist in the projects-plugin
 * directory for loading to succeed.
 */

import { test, expect, Page, APIRequestContext } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

const MCP_PROJECTS = `${baseURL}/plugins/projects-plugin/mcp`;
const MCP_FEDERATED = `${baseURL}/mcp`;
const PLUGIN_DIR = './e2e/test-plugins/projects-plugin';

// ---- Helpers ----

async function adminMCPCall(
  request: APIRequestContext,
  toolName: string,
  args: Record<string, unknown>,
  id = 1,
): Promise<{ result?: unknown; error?: { code: number; message: string } }> {
  const resp = await request.post(MCP_FEDERATED, {
    headers: {
      Authorization: `Bearer ${adminToken()}`,
      'Content-Type': 'application/json',
    },
    data: {
      jsonrpc: '2.0',
      method: 'tools/call',
      params: { name: toolName, arguments: args },
      id,
    },
  });
  return resp.json();
}

async function projectsMCPCall(
  request: APIRequestContext,
  token: string,
  toolName: string,
  args: Record<string, unknown>,
  id = 1,
): Promise<{ result?: unknown; error?: { code: number; message: string }; status: number }> {
  const resp = await request.post(MCP_PROJECTS, {
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
    },
    data: {
      jsonrpc: '2.0',
      method: 'tools/call',
      params: { name: toolName, arguments: args },
      id,
    },
  });
  const body = await resp.json();
  return { ...body, status: resp.status() };
}

async function loadProjectsPluginAndGetToken(
  page: Page,
  request: APIRequestContext,
): Promise<string | null> {
  // Try to get existing token first (plugin may already be loaded).
  const existing = await adminMCPCall(request, 'get_plugin_token', { name: 'projects-plugin' });
  if (existing.result) {
    const res = existing.result as { content?: Array<{ text?: string }> };
    if (res.content?.[0]?.text) {
      try {
        const parsed = JSON.parse(res.content[0].text);
        if (parsed.token) return parsed.token as string;
      } catch {
        // fall through to load
      }
    }
  }

  // Plugin not loaded — attempt to load via web UI.
  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/plugins`);

  const csrfInput = page.locator('#plugin-load-form input[name="csrf_token"]');
  const csrfToken = await csrfInput.inputValue().catch(() => '');
  if (!csrfToken) return null;

  const dirInput = page.locator('#plugin-load-form input[name="dir"]');
  await dirInput.fill(PLUGIN_DIR);

  const [loadResp] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/plugins/load')),
    page.locator('#plugin-load-form button[type="submit"]').click(),
  ]);

  if (loadResp.status() >= 400) return null;

  const tokenResp = await adminMCPCall(request, 'get_plugin_token', { name: 'projects-plugin' });
  if (!tokenResp.result) return null;
  const res = tokenResp.result as { content?: Array<{ text?: string }> };
  if (!res.content?.[0]?.text) return null;
  try {
    const parsed = JSON.parse(res.content[0].text);
    return (parsed.token as string) ?? null;
  } catch {
    return null;
  }
}

// ---- Test setup ----

let projectsToken: string | null = null;

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage();
  projectsToken = await loadProjectsPluginAndGetToken(page, page.request);
  await page.close();
});

function requireToken(): string {
  if (!projectsToken) {
    test.skip();
    return '';
  }
  return projectsToken;
}

// ---- MCP CRUD: create_project ----

test('create_project: valid name returns project with id', async ({ request }) => {
  const token = requireToken();
  const resp = await projectsMCPCall(request, token, 'create_project', {
    name: 'E2E Test Project',
  });
  expect(resp.status).toBe(200);
  expect(resp.error).toBeUndefined();
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.project.id).toBeGreaterThan(0);
  expect(data.project.name).toBe('E2E Test Project');
  expect(data.project.status).toBe('active');
  expect(data.project.color).toBeTruthy();
  expect(data.project.icon).toBeTruthy();
});

test('create_project: custom color and icon are stored', async ({ request }) => {
  const token = requireToken();
  const resp = await projectsMCPCall(request, token, 'create_project', {
    name: 'Colored Project',
    color: '#ec4899',
    icon: 'rocket',
  });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.project.color).toBe('#ec4899');
  expect(data.project.icon).toBe('rocket');
});

test('create_project: missing name returns error', async ({ request }) => {
  const token = requireToken();
  const resp = await projectsMCPCall(request, token, 'create_project', {});
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- MCP CRUD: list_projects ----

test('list_projects: no filter returns active projects', async ({ request }) => {
  const token = requireToken();
  await projectsMCPCall(request, token, 'create_project', { name: 'List Test Project' });

  const resp = await projectsMCPCall(request, token, 'list_projects', {});
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.total).toBeGreaterThan(0);
  expect(Array.isArray(data.projects)).toBe(true);
  for (const p of data.projects) {
    expect(p.status).toBe('active');
  }
});

test('list_projects: status=all returns projects of all statuses', async ({ request }) => {
  const token = requireToken();
  const resp = await projectsMCPCall(request, token, 'list_projects', { status: 'all' });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(typeof data.total).toBe('number');
});

test('list_projects: limit restricts result count', async ({ request }) => {
  const token = requireToken();
  await projectsMCPCall(request, token, 'create_project', { name: 'Limit A' });
  await projectsMCPCall(request, token, 'create_project', { name: 'Limit B' });

  const resp = await projectsMCPCall(request, token, 'list_projects', { limit: 1 });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.projects.length).toBeLessThanOrEqual(1);
});

// ---- MCP CRUD: get_project ----

test('get_project: valid id returns project', async ({ request }) => {
  const token = requireToken();
  const created = await projectsMCPCall(request, token, 'create_project', { name: 'Get Me' });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).project.id;

  const resp = await projectsMCPCall(request, token, 'get_project', { id });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.project.id).toBe(id);
  expect(data.project.name).toBe('Get Me');
});

test('get_project: non-existent id returns error', async ({ request }) => {
  const token = requireToken();
  const resp = await projectsMCPCall(request, token, 'get_project', { id: 999999 });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- MCP CRUD: update_project ----

test('update_project: name change is reflected', async ({ request }) => {
  const token = requireToken();
  const created = await projectsMCPCall(request, token, 'create_project', { name: 'Old Name' });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).project.id;

  const resp = await projectsMCPCall(request, token, 'update_project', {
    id,
    name: 'New Name',
  });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.project.name).toBe('New Name');
});

test('update_project: clear_description empties description', async ({ request }) => {
  const token = requireToken();
  const created = await projectsMCPCall(request, token, 'create_project', {
    name: 'Desc Project',
    description: 'Some description',
  });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).project.id;

  const resp = await projectsMCPCall(request, token, 'update_project', {
    id,
    clear_description: true,
  });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.project.description ?? '').toBe('');
});

test('update_project: no fields returns error', async ({ request }) => {
  const token = requireToken();
  const created = await projectsMCPCall(request, token, 'create_project', { name: 'No-op' });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).project.id;

  const resp = await projectsMCPCall(request, token, 'update_project', { id });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- MCP CRUD: archive_project ----

test('archive_project: sets status to archived', async ({ request }) => {
  const token = requireToken();
  const created = await projectsMCPCall(request, token, 'create_project', {
    name: 'To Archive',
  });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).project.id;

  const resp = await projectsMCPCall(request, token, 'archive_project', { id });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.project.status).toBe('archived');
});

test('archive_project: archived project not returned in default list', async ({ request }) => {
  const token = requireToken();
  const created = await projectsMCPCall(request, token, 'create_project', {
    name: 'Archive Me',
  });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).project.id;

  await projectsMCPCall(request, token, 'archive_project', { id });

  const listResp = await projectsMCPCall(request, token, 'list_projects', {});
  const listResult = listResp.result as { content?: Array<{ text?: string }> };
  const listData = JSON.parse(listResult.content![0].text!);
  const found = listData.projects.find((p: { id: number }) => p.id === id);
  expect(found).toBeUndefined();
});

test('archive_project: non-existent id returns error', async ({ request }) => {
  const token = requireToken();
  const resp = await projectsMCPCall(request, token, 'archive_project', { id: 999999 });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- MCP CRUD: delete_project ----

test('delete_project: existing project returns deleted=true', async ({ request }) => {
  const token = requireToken();
  const created = await projectsMCPCall(request, token, 'create_project', {
    name: 'To Delete',
  });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).project.id;

  const resp = await projectsMCPCall(request, token, 'delete_project', { id });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.deleted).toBe(true);
  expect(data.id).toBe(id);
});

test('delete_project: non-existent project returns deleted=false', async ({ request }) => {
  const token = requireToken();
  const resp = await projectsMCPCall(request, token, 'delete_project', { id: 999997 });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.deleted).toBe(false);
});

test('delete_project: get_project after delete returns error', async ({ request }) => {
  const token = requireToken();
  const created = await projectsMCPCall(request, token, 'create_project', {
    name: 'Delete Then Get',
  });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).project.id;

  await projectsMCPCall(request, token, 'delete_project', { id });

  const getResp = await projectsMCPCall(request, token, 'get_project', { id });
  const hasError =
    getResp.error !== undefined ||
    (getResp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- Bearer token auth ----

test('MCP request with no token returns 401', async ({ request }) => {
  if (!projectsToken) { test.skip(); return; }
  const resp = await request.post(MCP_PROJECTS, {
    headers: { 'Content-Type': 'application/json' },
    data: { jsonrpc: '2.0', method: 'tools/call', params: { name: 'list_projects', arguments: {} }, id: 1 },
  });
  expect(resp.status()).toBe(401);
});

test('MCP request with wrong token returns 401', async ({ request }) => {
  if (!projectsToken) { test.skip(); return; }
  const resp = await request.post(MCP_PROJECTS, {
    headers: { Authorization: 'Bearer wrong-token', 'Content-Type': 'application/json' },
    data: { jsonrpc: '2.0', method: 'tools/call', params: { name: 'list_projects', arguments: {} }, id: 1 },
  });
  expect(resp.status()).toBe(401);
});

// ---- Dashboard widget ----

test('dashboard page shows Projects widget when plugin loaded', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }
  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/dashboard`);
  const bodyText = await page.locator('body').innerText();
  expect(bodyText).toContain('Projects');
});

test('widget refresh for projects_overview returns non-500', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }
  await loginAs(page, adminToken());
  const resp = await page.request.get(
    `${baseURL}/dashboard/widgets/projects-plugin__projects_overview/refresh`,
  );
  expect(resp.status()).not.toBe(500);
});

// ---- UI pages ----

test('projects list page loads at /p/projects-plugin/projects', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }
  await loginAs(page, adminToken());
  const resp = await page.goto(`${baseURL}/p/projects-plugin/projects`);
  expect(resp?.status()).toBe(200);
  const bodyText = await page.locator('body').innerText();
  expect(bodyText.toLowerCase()).toContain('project');
});

test('projects list page is linked in sidebar navigation', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }
  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/dashboard`);
  const navLink = page.locator('a[href*="projects-plugin"]');
  await expect(navLink.first()).toBeVisible();
});

test('project detail page loads at /p/projects-plugin/project/{id}', async ({ page, request }) => {
  if (!projectsToken) { test.skip(); return; }

  // Create a project first.
  const created = await projectsMCPCall(request, projectsToken, 'create_project', {
    name: 'Detail Page Project',
  });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).project.id;

  await loginAs(page, adminToken());
  const resp = await page.goto(`${baseURL}/p/projects-plugin/project/${id}`);
  expect(resp?.status()).toBe(200);
  const bodyText = await page.locator('body').innerText();
  expect(bodyText).toContain('Detail Page Project');
});

test('project detail page shows error for non-existent project', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }
  await loginAs(page, adminToken());
  const resp = await page.goto(`${baseURL}/p/projects-plugin/project/999999`);
  // Either 200 with error message rendered, or 4xx
  const status = resp?.status() ?? 0;
  const bodyText = await page.locator('body').innerText();
  const hasErrorIndicator =
    status >= 400 ||
    bodyText.toLowerCase().includes('not found') ||
    bodyText.toLowerCase().includes('error');
  expect(hasErrorIndicator).toBe(true);
});
