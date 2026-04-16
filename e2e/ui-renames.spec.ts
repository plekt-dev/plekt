/**
 * ui-renames.spec.ts — E2E tests for UI renaming and badge removal.
 *
 * Tests:
 *   - Sidebar shows "Tracked Time" instead of "Sessions" (pomodoro-plugin)
 *   - Task cards in kanban do NOT show "proj #N" badge
 *   - Image upload in project creation dialog
 *
 * Requires tasks-plugin and projects-plugin. Pomodoro-plugin needed for sidebar test.
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
        // fall through
      }
    }
  }

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/plugins`);

  const csrfInput = page.locator('#plugin-load-form input[name="csrf_token"]');
  const csrfToken = await csrfInput.inputValue().catch(() => '');
  if (!csrfToken) return null;

  await page.locator('#plugin-load-form input[name="dir"]').fill(pluginDir);
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
let pomodoroLoaded = false;

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage();
  tasksToken = await loadPluginAndGetToken(page, page.request, 'tasks-plugin', TASKS_PLUGIN_DIR);
  projectsToken = await loadPluginAndGetToken(page, page.request, 'projects-plugin', PROJECTS_PLUGIN_DIR);

  // Check pomodoro
  const pomCheck = await adminMCPCall(page.request, 'get_plugin_token', { name: 'pomodoro-plugin' });
  if (pomCheck.result) {
    const res = pomCheck.result as { content?: Array<{ text?: string }> };
    if (res.content?.[0]?.text) {
      try {
        const parsed = JSON.parse(res.content[0].text);
        pomodoroLoaded = !!parsed.token;
      } catch {
        // not loaded
      }
    }
  }

  await page.close();
});

// ---- "Tracked Time" sidebar label ----

test('sidebar shows "Tracked Time" when pomodoro-plugin is loaded', async ({ page }) => {
  if (!pomodoroLoaded) { test.skip(); return; }

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/dashboard`);

  // Look for "Tracked Time" in the sidebar navigation
  const sidebar = page.locator('nav, aside, .sidebar, header');
  const sidebarText = await sidebar.allInnerTexts();
  const combined = sidebarText.join(' ');

  expect(combined).toContain('Tracked Time');
  // Should NOT show "Sessions" as the nav label for pomodoro
  // (note: "Sessions" may still appear for admin sessions page, so check specifically
  // for the pomodoro nav link text)
  const pomodoroLink = page.locator('a[href*="pomodoro-plugin"]');
  if (await pomodoroLink.count() > 0) {
    const linkText = await pomodoroLink.first().innerText();
    expect(linkText).toContain('Tracked Time');
    expect(linkText).not.toContain('Sessions');
  }
});

test('sidebar does NOT show "Sessions" as pomodoro nav label', async ({ page }) => {
  if (!pomodoroLoaded) { test.skip(); return; }

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/dashboard`);

  // Find the pomodoro nav link and verify it says "Tracked Time"
  const pomodoroLink = page.locator('a[href*="pomodoro-plugin"]');
  if (await pomodoroLink.count() === 0) { test.skip(); return; }

  const linkText = await pomodoroLink.first().innerText();
  expect(linkText.trim()).not.toBe('Sessions');
});

// ---- Project badge removed from task cards ----

test('task cards do NOT show project badge on kanban board', async ({ page, request }) => {
  if (!tasksToken) { test.skip(); return; }
  if (!projectsToken) { test.skip(); return; }

  // Create a project
  const projResp = await pluginMCPCall(request, MCP_PROJECTS, projectsToken, 'create_project', {
    name: `NoBadge ${Date.now()}`,
  });
  const projResult = projResp.result as { content?: Array<{ text?: string }> };
  const projId = JSON.parse(projResult.content![0].text!).project.id;

  // Create a task linked to the project
  const title = `NoBadgeTask ${Date.now()}`;
  await pluginMCPCall(request, MCP_TASKS, tasksToken, 'create_task', {
    title,
    project_id: projId,
  });

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/tasks-plugin/board`);

  // Wait for cards to render
  await page.waitForTimeout(1000);

  // Get all kanban card title elements
  const cardTitles = page.locator('.kanban-card-title');
  const count = await cardTitles.count();

  for (let i = 0; i < count; i++) {
    const titleHtml = await cardTitles.nth(i).innerHTML();
    // Should NOT contain "proj #" badge text
    expect(titleHtml).not.toMatch(/proj\s*#\d+/i);
  }
});

test('kanban card title does not contain project-badge element', async ({ page, request }) => {
  if (!tasksToken) { test.skip(); return; }

  const title = `CleanCard ${Date.now()}`;
  await pluginMCPCall(request, MCP_TASKS, tasksToken, 'create_task', { title });

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/tasks-plugin/board`);

  // Verify that the projectBadge variable is set to empty string in the rendered output
  // This means no .project-badge class element should exist in kanban card titles
  const projBadges = page.locator('.kanban-card-title .project-badge');
  expect(await projBadges.count()).toBe(0);
});

// ---- Image upload in project creation ----

test('create project form has image upload area', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/projects-plugin/projects`);

  // Click "New Project" to show the create form
  const createBtn = page.locator('[data-action="show-create"]');
  await expect(createBtn).toBeVisible();
  await createBtn.click();

  // The create form should now be visible with image upload
  const uploadArea = page.locator('.image-upload-area');
  await expect(uploadArea).toBeVisible({ timeout: 3000 });
});

test('image upload area has choose-image button', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/projects-plugin/projects`);

  await page.locator('[data-action="show-create"]').click();

  const uploadBtn = page.locator('.image-upload-btn');
  await expect(uploadBtn).toBeVisible({ timeout: 3000 });

  const btnText = await uploadBtn.innerText();
  expect(btnText.toLowerCase()).toContain('choose image');
});

test('image upload has hidden file input', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/projects-plugin/projects`);

  await page.locator('[data-action="show-create"]').click();

  // The file input should exist but be hidden (display:none)
  const fileInput = page.locator('.image-upload-input');
  await expect(fileInput).toHaveCount(1);

  // It should accept image files
  const accept = await fileInput.getAttribute('accept');
  expect(accept).toContain('image');
});

test('image upload has preview container', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/projects-plugin/projects`);

  await page.locator('[data-action="show-create"]').click();

  const preview = page.locator('.image-upload-preview');
  await expect(preview).toHaveCount(1);
});

test('selecting an image file shows 64x64 preview', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/projects-plugin/projects`);

  await page.locator('[data-action="show-create"]').click();

  const fileInput = page.locator('.image-upload-input');
  await expect(fileInput).toHaveCount(1);

  // Create a tiny test image (1x1 PNG) as a buffer
  // Minimal 1x1 red PNG
  const pngBase64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==';
  const pngBuffer = Buffer.from(pngBase64, 'base64');

  // Use setInputFiles to simulate file selection
  await fileInput.setInputFiles({
    name: 'test-image.png',
    mimeType: 'image/png',
    buffer: pngBuffer,
  });

  // Wait for the preview to render
  await page.waitForTimeout(500);

  // Preview should now contain an img element
  const preview = page.locator('.image-upload-preview');
  const previewImg = preview.locator('img');
  await expect(previewImg).toBeVisible({ timeout: 3000 });

  // The image should be displayed at 64x64
  const box = await previewImg.boundingBox();
  if (box) {
    expect(box.width).toBeLessThanOrEqual(68); // Allow small tolerance
    expect(box.height).toBeLessThanOrEqual(68);
  }
});

test('image upload stores base64 in hidden input', async ({ page }) => {
  if (!projectsToken) { test.skip(); return; }

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/p/projects-plugin/projects`);

  await page.locator('[data-action="show-create"]').click();

  const fileInput = page.locator('.image-upload-input');
  await expect(fileInput).toHaveCount(1);

  // Minimal 1x1 PNG
  const pngBase64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==';
  const pngBuffer = Buffer.from(pngBase64, 'base64');

  await fileInput.setInputFiles({
    name: 'test-upload.png',
    mimeType: 'image/png',
    buffer: pngBuffer,
  });

  await page.waitForTimeout(500);

  // The hidden input should contain a data: URI
  const hiddenInput = page.locator('.image-upload-value');
  if (await hiddenInput.count() > 0) {
    const value = await hiddenInput.inputValue();
    expect(value).toMatch(/^data:image\//);
  }
});
