/**
 * pomodoro.spec.ts — E2E tests for the pomodoro-plugin MCP tools and dashboard widget.
 *
 * The pomodoro-plugin must be loaded before these tests run. If the plugin is not
 * loaded (WASM binary not present), all tests skip gracefully.
 *
 * Plugin loading: the test setup attempts to load the plugin via the admin web
 * UI (POST /plugins/load). A compiled plugin.wasm must exist in the pomodoro-plugin
 * directory for loading to succeed.
 *
 * Token retrieval: after loading, the bearer token is fetched via the federated
 * MCP endpoint POST /mcp using the admin token with get_plugin_token tool.
 */

import { test, expect, Page, APIRequestContext } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

const MCP_POMODORO = `${baseURL}/plugins/pomodoro-plugin/mcp`;
const MCP_FEDERATED = `${baseURL}/mcp`;
const PLUGIN_DIR = './plugins/pomodoro-plugin';

// ---- Helpers ----

/** Calls a tool on the federated /mcp endpoint using the admin bearer token. */
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
  const body = await resp.json();
  return body;
}

/** Calls a tool on the pomodoro-plugin MCP endpoint. Returns status code alongside body. */
async function pomodoroMCPCall(
  request: APIRequestContext,
  token: string,
  toolName: string,
  args: Record<string, unknown>,
  id = 1,
): Promise<{ result?: unknown; error?: { code: number; message: string }; status: number }> {
  const resp = await request.post(MCP_POMODORO, {
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

/**
 * Loads the pomodoro-plugin via the web admin UI and returns the bearer token.
 * Returns null if the plugin could not be loaded (e.g. WASM binary missing).
 */
async function loadPomodoroPluginAndGetToken(
  page: Page,
  request: APIRequestContext,
): Promise<string | null> {
  // Try to get the token first (plugin may already be loaded).
  const existing = await adminMCPCall(request, 'get_plugin_token', { name: 'pomodoro-plugin' });
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

  // Plugin not loaded — attempt to load it via the web UI.
  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/plugins`);

  // Get a CSRF token for the load form.
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

  // Now retrieve the token via MCP.
  const tokenResp = await adminMCPCall(request, 'get_plugin_token', { name: 'pomodoro-plugin' });
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

let pomodoroToken: string | null = null;

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage();
  const request = page.request;
  pomodoroToken = await loadPomodoroPluginAndGetToken(page, request);
  await page.close();
});

test.beforeEach(async ({ request }) => {
  // Stop any active session to ensure a clean state for each test.
  if (!pomodoroToken) return;
  // Ignore errors — there may be no active session.
  await request.post(MCP_POMODORO, {
    headers: {
      Authorization: `Bearer ${pomodoroToken}`,
      'Content-Type': 'application/json',
    },
    data: {
      jsonrpc: '2.0',
      method: 'tools/call',
      params: { name: 'stop_session', arguments: { interrupted: true } },
      id: 99,
    },
  }).catch(() => {/* ignore */});
});

function requireToken(): string {
  if (!pomodoroToken) {
    test.skip();
    return ''; // unreachable, skip throws
  }
  return pomodoroToken;
}

// ---- MCP: start_session ----

test('start_session: valid work session returns session with id', async ({ request }) => {
  const token = requireToken();
  const resp = await pomodoroMCPCall(request, token, 'start_session', { session_type: 'work' });
  expect(resp.status).toBe(200);
  expect(resp.error).toBeUndefined();
  const result = resp.result as { content?: Array<{ text?: string }> };
  expect(result.content?.[0]?.text).toBeTruthy();
  const data = JSON.parse(result.content![0].text!);
  expect(data.session.id).toBeGreaterThan(0);
  expect(data.session.session_type).toBe('work');
  expect(data.session.ended_at).toBeFalsy();
});

test('start_session: short_break type accepted', async ({ request }) => {
  const token = requireToken();
  const resp = await pomodoroMCPCall(request, token, 'start_session', { session_type: 'short_break' });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.session.session_type).toBe('short_break');
});

test('start_session: long_break type accepted', async ({ request }) => {
  const token = requireToken();
  const resp = await pomodoroMCPCall(request, token, 'start_session', { session_type: 'long_break' });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.session.session_type).toBe('long_break');
});

test('start_session: invalid session_type returns error', async ({ request }) => {
  const token = requireToken();
  const resp = await pomodoroMCPCall(request, token, 'start_session', { session_type: 'nap' });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

test('start_session: already active session returns error', async ({ request }) => {
  const token = requireToken();
  // Start a session.
  await pomodoroMCPCall(request, token, 'start_session', { session_type: 'work' });
  // Try to start another.
  const resp = await pomodoroMCPCall(request, token, 'start_session', { session_type: 'work' });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- MCP: stop_session ----

test('stop_session: stops active session as completed', async ({ request }) => {
  const token = requireToken();
  await pomodoroMCPCall(request, token, 'start_session', { session_type: 'work' });

  const resp = await pomodoroMCPCall(request, token, 'stop_session', { interrupted: false });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.session.interrupted).toBe(false);
  expect(data.session.ended_at).toBeTruthy();
});

test('stop_session: stops active session as interrupted', async ({ request }) => {
  const token = requireToken();
  await pomodoroMCPCall(request, token, 'start_session', { session_type: 'work' });

  const resp = await pomodoroMCPCall(request, token, 'stop_session', { interrupted: true });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.session.interrupted).toBe(true);
  expect(data.session.ended_at).toBeTruthy();
});

test('stop_session: no active session returns error', async ({ request }) => {
  const token = requireToken();
  // No session started — beforeEach already stopped any active session.
  const resp = await pomodoroMCPCall(request, token, 'stop_session', {});
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- MCP: get_current ----

test('get_current: returns active session when one is running', async ({ request }) => {
  const token = requireToken();
  await pomodoroMCPCall(request, token, 'start_session', { session_type: 'work' });

  const resp = await pomodoroMCPCall(request, token, 'get_current', {});
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.active).toBe(true);
  expect(data.session.id).toBeGreaterThan(0);
});

test('get_current: returns active=false when idle', async ({ request }) => {
  const token = requireToken();
  // beforeEach stopped any session, so we should be idle.
  const resp = await pomodoroMCPCall(request, token, 'get_current', {});
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.active).toBe(false);
});

// ---- MCP: list_sessions ----

test('list_sessions: no filter returns sessions', async ({ request }) => {
  const token = requireToken();
  // Create and complete a session.
  await pomodoroMCPCall(request, token, 'start_session', { session_type: 'work' });
  await pomodoroMCPCall(request, token, 'stop_session', { interrupted: false });

  const resp = await pomodoroMCPCall(request, token, 'list_sessions', {});
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(Array.isArray(data.sessions)).toBe(true);
  expect(data.total).toBeGreaterThan(0);
});

test('list_sessions: session_type filter returns only matching', async ({ request }) => {
  const token = requireToken();
  await pomodoroMCPCall(request, token, 'start_session', { session_type: 'short_break' });
  await pomodoroMCPCall(request, token, 'stop_session', { interrupted: false });

  const resp = await pomodoroMCPCall(request, token, 'list_sessions', { session_type: 'short_break' });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  for (const s of data.sessions) {
    expect(s.session_type).toBe('short_break');
  }
});

test('list_sessions: limit restricts result count', async ({ request }) => {
  const token = requireToken();
  // Ensure multiple sessions exist.
  await pomodoroMCPCall(request, token, 'start_session', { session_type: 'work' });
  await pomodoroMCPCall(request, token, 'stop_session', { interrupted: false });
  await pomodoroMCPCall(request, token, 'start_session', { session_type: 'work' });
  await pomodoroMCPCall(request, token, 'stop_session', { interrupted: false });

  const resp = await pomodoroMCPCall(request, token, 'list_sessions', { limit: 1 });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.sessions.length).toBeLessThanOrEqual(1);
});

test('list_sessions: results ordered by started_at DESC', async ({ request }) => {
  const token = requireToken();
  const resp = await pomodoroMCPCall(request, token, 'list_sessions', { limit: 10 });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  if (data.sessions.length < 2) return;
  for (let i = 0; i < data.sessions.length - 1; i++) {
    const a = data.sessions[i].started_at as string;
    const b = data.sessions[i + 1].started_at as string;
    expect(a >= b).toBe(true);
  }
});

// ---- MCP: get_stats ----

test('get_stats: returns stats for today by default', async ({ request }) => {
  const token = requireToken();
  const resp = await pomodoroMCPCall(request, token, 'get_stats', {});
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.stats).toBeDefined();
  expect(data.stats.date).toBeTruthy();
  expect(typeof data.stats.total_sessions).toBe('number');
});

test('get_stats: active_session flag is true when session running', async ({ request }) => {
  const token = requireToken();
  await pomodoroMCPCall(request, token, 'start_session', { session_type: 'work' });

  const resp = await pomodoroMCPCall(request, token, 'get_stats', {});
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.stats.active_session).toBe(true);
});

test('get_stats: explicit date filters correctly', async ({ request }) => {
  const token = requireToken();
  const resp = await pomodoroMCPCall(request, token, 'get_stats', { date: '2000-01-01' });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.stats.date).toBe('2000-01-01');
  expect(data.stats.total_sessions).toBe(0);
});

// ---- Bearer token authentication ----

test('MCP request with no token returns 401', async ({ request }) => {
  requireToken();
  const resp = await request.post(MCP_POMODORO, {
    headers: { 'Content-Type': 'application/json' },
    data: {
      jsonrpc: '2.0',
      method: 'tools/call',
      params: { name: 'get_current', arguments: {} },
      id: 1,
    },
  });
  expect(resp.status()).toBe(401);
});

test('MCP request with wrong token returns 401', async ({ request }) => {
  requireToken();
  const resp = await request.post(MCP_POMODORO, {
    headers: {
      Authorization: 'Bearer wrong-token-value',
      'Content-Type': 'application/json',
    },
    data: {
      jsonrpc: '2.0',
      method: 'tools/call',
      params: { name: 'get_current', arguments: {} },
      id: 1,
    },
  });
  expect(resp.status()).toBe(401);
});

// ---- Dashboard widget ----

test('dashboard page loads and shows Pomodoro Status widget when plugin loaded', async ({ page }) => {
  if (!pomodoroToken) {
    test.skip();
    return;
  }
  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/dashboard`);
  // The widget title is "Pomodoro Status" as declared in manifest.json.
  const bodyText = await page.locator('body').innerText();
  expect(bodyText).toContain('Pomodoro Status');
});

test('widget refresh endpoint for pomodoro_status returns non-500', async ({ page }) => {
  if (!pomodoroToken) {
    test.skip();
    return;
  }
  await loginAs(page, adminToken());
  const resp = await page.request.get(
    `${baseURL}/dashboard/widgets/pomodoro-plugin__pomodoro_status/refresh`,
  );
  expect(resp.status()).not.toBe(500);
});
