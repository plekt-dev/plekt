/**
 * mcp.spec.ts — E2E tests for the federated /mcp endpoint.
 *
 * Covers: initialization, tools/list, tools/call, error handling,
 * authentication, and presence of newly-added plugin tools.
 */

import { test, expect, APIRequestContext } from '@playwright/test';
import { adminToken, baseURL } from './helpers/server';

const MCP_FEDERATED = `${baseURL}/mcp`;

// ---- Helper ----

async function mcpCall(
  request: APIRequestContext,
  method: string,
  params?: Record<string, unknown>,
  id: number | string = 1,
) {
  const body: Record<string, unknown> = {
    jsonrpc: '2.0',
    method,
    id,
  };
  if (params !== undefined) {
    body.params = params;
  }
  return request.post(MCP_FEDERATED, {
    headers: {
      Authorization: `Bearer ${adminToken()}`,
      'Content-Type': 'application/json',
    },
    data: body,
  });
}

// ---- Setup: install plugins via MCP ----

const PLUGIN_DIRS = [
  './e2e/test-plugins/projects-plugin',
  './e2e/test-plugins/tasks-plugin',
  './e2e/test-plugins/notes-plugin',
  './e2e/test-plugins/pomodoro-plugin',
];

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage();
  for (const dir of PLUGIN_DIRS) {
    await mcpCall(page.request, 'tools/call', {
      name: 'install_plugin',
      arguments: { dir },
    });
  }
  await page.close();
});

// ---- Tests ----

test('federated initialize returns protocol version and session id', async ({ request }) => {
  const resp = await mcpCall(request, 'initialize', {
    protocolVersion: '2025-03-26',
    clientInfo: { name: 'e2e-test', version: '1.0' },
  });
  expect(resp.status()).toBe(200);
  const json = await resp.json();
  expect(json.result).toBeDefined();
  expect(json.result.protocolVersion).toBeTruthy();
  const sessionId = resp.headers()['mcp-session-id'];
  expect(sessionId).toBeTruthy();
});

test('federated tools/list includes built-in system tools', async ({ request }) => {
  const resp = await mcpCall(request, 'tools/list');
  expect(resp.status()).toBe(200);
  const json = await resp.json();
  const tools = json.result.tools as Array<{ name: string }>;
  const names = tools.map(t => t.name);
  expect(names).toContain('list_plugins');
  expect(names).toContain('install_plugin');
  expect(names).toContain('get_plugin');
});

test('federated tools/call list_plugins', async ({ request }) => {
  const resp = await mcpCall(request, 'tools/call', {
    name: 'list_plugins',
    arguments: {},
  });
  expect(resp.status()).toBe(200);
  const json = await resp.json();
  expect(json.result).toBeDefined();
  expect(json.result.content).toBeDefined();
  expect(Array.isArray(json.result.content)).toBe(true);
});

test('unknown tool returns error', async ({ request }) => {
  const resp = await mcpCall(request, 'tools/call', {
    name: 'nonexistent_tool_xyz',
    arguments: {},
  });
  const json = await resp.json();
  expect(json.error).toBeDefined();
  expect(json.error.code).toBeLessThan(0);
});

test('no auth header returns 401', async ({ request }) => {
  const resp = await request.post(MCP_FEDERATED, {
    headers: { 'Content-Type': 'application/json' },
    data: {
      jsonrpc: '2.0',
      method: 'tools/list',
      id: 1,
    },
  });
  expect(resp.status()).toBe(401);
});

test('invalid bearer token returns 401', async ({ request }) => {
  const resp = await request.post(MCP_FEDERATED, {
    headers: {
      Authorization: 'Bearer wrong-token',
      'Content-Type': 'application/json',
    },
    data: {
      jsonrpc: '2.0',
      method: 'tools/list',
      id: 1,
    },
  });
  expect(resp.status()).toBe(401);
});

test('unknown JSON-RPC method returns method not found', async ({ request }) => {
  const resp = await mcpCall(request, 'nonexistent/method');
  const json = await resp.json();
  expect(json.error).toBeDefined();
  expect(json.error.code).toBe(-32601);
});

test('malformed JSON returns parse error', async ({ request }) => {
  const resp = await request.post(MCP_FEDERATED, {
    headers: {
      Authorization: `Bearer ${adminToken()}`,
      'Content-Type': 'application/json',
    },
    data: 'not json at all',
  });
  expect(resp.status()).toBe(400);
});

test('routes plugin tools through federated endpoint', async ({ request }) => {
  const listResp = await mcpCall(request, 'tools/list');
  const listJson = await listResp.json();
  const tools = listJson.result.tools as Array<{ name: string }>;
  const pluginTool = tools.find(t => t.name.includes('__'));
  if (!pluginTool) {
    test.skip();
    return;
  }
  const callResp = await mcpCall(request, 'tools/call', {
    name: pluginTool.name,
    arguments: {},
  });
  const callJson = await callResp.json();
  // Should return a result (even if the plugin returns an app-level error, the
  // JSON-RPC envelope should have either result or error).
  expect(callJson.result !== undefined || callJson.error !== undefined).toBe(true);
});

test('pomodoro pause_session and resume_session appear in tools/list', async ({ request }) => {
  const resp = await mcpCall(request, 'tools/list');
  const json = await resp.json();
  const tools = json.result.tools as Array<{ name: string }>;
  const names = tools.map(t => t.name);
  expect(names).toContain('pomodoro-plugin__pause_session');
  expect(names).toContain('pomodoro-plugin__resume_session');
});

test('projects toggle_favourite appears in tools/list', async ({ request }) => {
  const resp = await mcpCall(request, 'tools/list');
  const json = await resp.json();
  const tools = json.result.tools as Array<{ name: string }>;
  const names = tools.map(t => t.name);
  expect(names).toContain('projects-plugin__toggle_favourite');
});
