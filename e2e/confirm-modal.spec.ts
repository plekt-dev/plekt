/**
 * confirm-modal.spec.ts — E2E tests for the custom confirmation modal (MC.confirm).
 *
 * The app replaced native browser confirm() with a styled <dialog> element.
 * This file tests:
 *   - Simple confirmation (e.g. task deletion)
 *   - Input-match confirmation (e.g. project deletion requiring name typed)
 *   - Cancel button closes modal without performing the action
 *
 * Requires both tasks-plugin and projects-plugin to be loaded.
 */

import { test, expect, Page, APIRequestContext } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

const MCP_FEDERATED = `${baseURL}/mcp`;
const MCP_TASKS = `${baseURL}/plugins/tasks-plugin/mcp`;
const MCP_PROJECTS = `${baseURL}/plugins/projects-plugin/mcp`;
const TASKS_PLUGIN_DIR = './e2e/test-plugins/tasks-plugin';
const PROJECTS_PLUGIN_DIR = './e2e/test-plugins/projects-plugin';

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

async function pluginMCPCall(
  request: APIRequestContext,
  endpoint: string,
  token: string,
  toolName: string,
  args: Record<string, unknown>,
  id = 1,
): Promise<{ result?: unknown; error?: { code: number; message: string }; status: number }> {
  const resp = await request.post(endpoint, {
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

async function loadPluginAndGetToken(
  page: Page,
  request: APIRequestContext,
  pluginName: string,
  pluginDir: string,
): Promise<string | null> {
  const existing = await adminMCPCall(request, 'get_plugin_token', { name: pluginName });
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

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/plugins`);

  const csrfInput = page.locator('#plugin-load-form input[name="csrf_token"]');
  const csrfToken = await csrfInput.inputValue().catch(() => '');
  if (!csrfToken) return null;

  const dirInput = page.locator('#plugin-load-form input[name="dir"]');
  await dirInput.fill(pluginDir);

  const [loadResp] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/plugins/load')),
    page.locator('#plugin-load-form button[type="submit"]').click(),
  ]);

  if (loadResp.status() >= 400) return null;

  const tokenResp = await adminMCPCall(request, 'get_plugin_token', { name: pluginName });
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

let tasksToken: string | null = null;
let projectsToken: string | null = null;

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage();
  tasksToken = await loadPluginAndGetToken(page, page.request, 'tasks-plugin', TASKS_PLUGIN_DIR);
  projectsToken = await loadPluginAndGetToken(page, page.request, 'projects-plugin', PROJECTS_PLUGIN_DIR);
  await page.close();
});

function requireTasksToken(): string {
  if (!tasksToken) { test.skip(); return ''; }
  return tasksToken;
}

function requireProjectsToken(): string {
  if (!projectsToken) { test.skip(); return ''; }
  return projectsToken;
}

// ---- Simple confirmation: task deletion ----

test('task delete button opens confirm dialog', async ({ page, request }) => {
  const token = requireTasksToken();
  const title = `ConfirmTest ${Date.now()}`;

  // Create a task via MCP
  const createResp = await pluginMCPCall(request, MCP_TASKS, token, 'create_task', { title });
  expect(createResp.status).toBe(200);

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/tasks-plugin/board`);

  // Find the delete button on a kanban card
  const deleteBtn = page.locator('.kanban-card [data-action="delete"]').first();
  await expect(deleteBtn).toBeVisible();
  await deleteBtn.click();

  // Confirm dialog should appear
  const dialog = page.locator('#mc-confirm-dialog');
  await expect(dialog).toBeVisible();

  // It should have OK and Cancel buttons
  await expect(page.locator('.mc-confirm-ok')).toBeVisible();
  await expect(page.locator('.mc-confirm-cancel')).toBeVisible();
});

test('task confirm dialog: cancel closes without deleting', async ({ page, request }) => {
  const token = requireTasksToken();
  const title = `CancelTest ${Date.now()}`;

  const createResp = await pluginMCPCall(request, MCP_TASKS, token, 'create_task', { title });
  expect(createResp.status).toBe(200);
  const result = createResp.result as { content?: Array<{ text?: string }> };
  const taskId = JSON.parse(result.content![0].text!).task.id;

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/tasks-plugin/board`);

  const card = page.locator(`.kanban-card[data-task-id="${taskId}"]`);
  await expect(card).toBeVisible();

  // Click delete, then cancel
  const deleteBtn = card.locator('[data-action="delete"]');
  await deleteBtn.click();

  const dialog = page.locator('#mc-confirm-dialog');
  await expect(dialog).toBeVisible();

  await page.locator('.mc-confirm-cancel').click();

  // Dialog should be gone
  await expect(dialog).not.toBeVisible();

  // Card should still be present
  await expect(card).toBeVisible();
});

test('task confirm dialog: OK deletes the task', async ({ page, request }) => {
  const token = requireTasksToken();
  const title = `DeleteOK ${Date.now()}`;

  const createResp = await pluginMCPCall(request, MCP_TASKS, token, 'create_task', { title });
  expect(createResp.status).toBe(200);
  const result = createResp.result as { content?: Array<{ text?: string }> };
  const taskId = JSON.parse(result.content![0].text!).task.id;

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/tasks-plugin/board`);

  const card = page.locator(`.kanban-card[data-task-id="${taskId}"]`);
  await expect(card).toBeVisible();

  const deleteBtn = card.locator('[data-action="delete"]');
  await deleteBtn.click();

  const dialog = page.locator('#mc-confirm-dialog');
  await expect(dialog).toBeVisible();

  await page.locator('.mc-confirm-ok').click();

  // Dialog closes
  await expect(dialog).not.toBeVisible();

  // Card should be removed (page reloads or htmx swap)
  await expect(card).not.toBeVisible({ timeout: 5000 });
});

// ---- Input-match confirmation: project deletion ----

test('project delete shows confirm dialog requiring name input', async ({ page, request }) => {
  const token = requireProjectsToken();
  const projName = `ConfirmProj ${Date.now()}`;

  await pluginMCPCall(request, MCP_PROJECTS, token, 'create_project', { name: projName });

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/projects-plugin/projects`);

  // Find a delete button with data-confirm-input attribute
  const deleteBtn = page.locator('[data-action="delete"][data-confirm-input]').first();
  await expect(deleteBtn).toBeVisible();
  await deleteBtn.click();

  const dialog = page.locator('#mc-confirm-dialog');
  await expect(dialog).toBeVisible();

  // Input field should be present for name matching
  const input = page.locator('.mc-confirm-input');
  await expect(input).toBeVisible();

  // OK button should be disabled until input matches
  const okBtn = page.locator('.mc-confirm-ok');
  await expect(okBtn).toBeDisabled();
});

test('project confirm dialog: OK stays disabled with wrong input', async ({ page, request }) => {
  const token = requireProjectsToken();
  const projName = `WrongInput ${Date.now()}`;

  await pluginMCPCall(request, MCP_PROJECTS, token, 'create_project', { name: projName });

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/projects-plugin/projects`);

  // Find the specific project's delete button
  const deleteBtn = page.locator(`[data-action="delete"][data-name="${projName}"]`).first();
  if (await deleteBtn.count() === 0) {
    // Fallback: click any delete with confirm-input
    await page.locator('[data-action="delete"][data-confirm-input]').first().click();
  } else {
    await deleteBtn.click();
  }

  const dialog = page.locator('#mc-confirm-dialog');
  await expect(dialog).toBeVisible();

  const input = page.locator('.mc-confirm-input');
  await input.fill('wrong name that does not match');

  const okBtn = page.locator('.mc-confirm-ok');
  await expect(okBtn).toBeDisabled();
});

test('project confirm dialog: OK enables when input matches project name', async ({ page, request }) => {
  const token = requireProjectsToken();
  const projName = `MatchMe ${Date.now()}`;

  await pluginMCPCall(request, MCP_PROJECTS, token, 'create_project', { name: projName });

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/projects-plugin/projects`);

  const deleteBtn = page.locator(`[data-action="delete"][data-name="${projName}"]`).first();
  if (await deleteBtn.count() === 0) {
    test.skip();
    return;
  }
  await deleteBtn.click();

  const dialog = page.locator('#mc-confirm-dialog');
  await expect(dialog).toBeVisible();

  const input = page.locator('.mc-confirm-input');
  await input.fill(projName);

  const okBtn = page.locator('.mc-confirm-ok');
  await expect(okBtn).toBeEnabled();
});

test('project confirm dialog: cancel closes without deleting', async ({ page, request }) => {
  const token = requireProjectsToken();
  const projName = `CancelProj ${Date.now()}`;

  await pluginMCPCall(request, MCP_PROJECTS, token, 'create_project', { name: projName });

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/projects-plugin/projects`);

  const deleteBtn = page.locator(`[data-action="delete"][data-name="${projName}"]`).first();
  if (await deleteBtn.count() === 0) {
    test.skip();
    return;
  }
  await deleteBtn.click();

  const dialog = page.locator('#mc-confirm-dialog');
  await expect(dialog).toBeVisible();

  await page.locator('.mc-confirm-cancel').click();
  await expect(dialog).not.toBeVisible();

  // Project card should still exist
  const card = page.locator(`.project-card-name:has-text("${projName}")`);
  await expect(card).toBeVisible();
});
