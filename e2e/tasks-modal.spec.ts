import { test, expect, Page, APIRequestContext } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

const MCP_TASKS = `${baseURL}/plugins/tasks-plugin/mcp`;
const MCP_FEDERATED = `${baseURL}/mcp`;
const PLUGIN_DIR = './e2e/test-plugins/tasks-plugin';

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

async function tasksMCPCall(
  request: APIRequestContext,
  token: string,
  toolName: string,
  args: Record<string, unknown>,
  id = 1,
): Promise<{ result?: unknown; error?: { code: number; message: string }; status: number }> {
  const resp = await request.post(MCP_TASKS, {
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

async function loadTasksPluginAndGetToken(
  page: Page,
  request: APIRequestContext,
): Promise<string | null> {
  const existing = await adminMCPCall(request, 'get_plugin_token', { name: 'tasks-plugin' });
  if (existing.result) {
    const res = existing.result as { content?: Array<{ text?: string }> };
    if (res.content?.[0]?.text) {
      try {
        const parsed = JSON.parse(res.content[0].text);
        if (parsed.token) return parsed.token as string;
      } catch {
        // fall through
      }
    }
  }

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

  const tokenResp = await adminMCPCall(request, 'get_plugin_token', { name: 'tasks-plugin' });
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

let tasksToken: string | null = null;

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage();
  const request = page.request;
  tasksToken = await loadTasksPluginAndGetToken(page, request);
  await page.close();
});

function requireToken(): string {
  if (!tasksToken) {
    test.skip();
    return '';
  }
  return tasksToken;
}

async function createTaskAndOpenDetail(page: Page, request: APIRequestContext): Promise<{ dialog: ReturnType<Page['locator']> }> {
  const token = requireToken();
  const title = `E2E modal drag ${Date.now()}`;

  const createResp = await tasksMCPCall(request, token, 'create_task', { title });
  expect(createResp.status).toBe(200);
  expect(createResp.error).toBeUndefined();

  const result = createResp.result as { content?: Array<{ text?: string }> };
  expect(result.content?.[0]?.text).toBeTruthy();
  const created = JSON.parse(result.content![0].text!);
  const taskId = Number(created.task?.id);
  expect(taskId).toBeGreaterThan(0);

  await loginAs(page, adminToken());
  const resp = await page.goto(`${baseURL}/p/tasks-plugin/board`);
  expect(resp?.ok()).toBeTruthy();

  const card = page.locator(`.kanban-card[data-task-id="${taskId}"]`).first();
  await expect(card).toBeVisible();
  await card.click();

  const dialog = page.locator('dialog#mc-task-detail');
  await expect(dialog).toBeVisible();
  return { dialog };
}

test('task detail modal: mousedown inside + mouseup outside does not close', async ({ page, request }) => {
  const { dialog } = await createTaskAndOpenDetail(page, request);

  const box = await dialog.boundingBox();
  expect(box).not.toBeNull();
  if (!box) return;

  const startX = box.x + box.width / 2;
  const startY = box.y + Math.min(box.height / 2, 60);
  const endX = Math.max(1, box.x - 20);
  const endY = Math.max(1, box.y - 20);

  await page.mouse.move(startX, startY);
  await page.mouse.down();
  await page.mouse.move(endX, endY);
  await page.mouse.up();

  await expect(dialog).toBeVisible();
});

test('task detail modal: mousedown outside + mouseup outside closes', async ({ page, request }) => {
  const { dialog } = await createTaskAndOpenDetail(page, request);

  const box = await dialog.boundingBox();
  expect(box).not.toBeNull();
  if (!box) return;

  const outsideX = Math.max(1, box.x - 20);
  const outsideY = Math.max(1, box.y - 20);

  await page.mouse.move(outsideX, outsideY);
  await page.mouse.down();
  await page.mouse.up();

  await expect(dialog).not.toBeVisible();
});

test('board settings modal: mousedown inside + mouseup outside does not close', async ({ page }) => {
  requireToken();
  await loginAs(page, adminToken());
  const resp = await page.goto(`${baseURL}/p/tasks-plugin/board`);
  expect(resp?.ok()).toBeTruthy();

  await page.locator('[data-action="open-board-settings"]').click();
  const dialog = page.locator('dialog#mc-board-settings');
  await expect(dialog).toBeVisible();

  const box = await dialog.boundingBox();
  expect(box).not.toBeNull();
  if (!box) return;

  const startX = box.x + box.width / 2;
  const startY = box.y + Math.min(box.height / 2, 60);
  const endX = Math.max(1, box.x - 20);
  const endY = Math.max(1, box.y - 20);

  await page.mouse.move(startX, startY);
  await page.mouse.down();
  await page.mouse.move(endX, endY);
  await page.mouse.up();

  await expect(dialog).toBeVisible();
});
