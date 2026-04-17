/**
 * mcp-permissions.spec.ts — verifies that the /mcp endpoint enforces
 * per-agent tool permissions. Specifically guards the regression where a
 * narrowed agent kept seeing the default wildcard permissions because the
 * SetPermissions service call did not invalidate the token cache.
 *
 * Scenario:
 *   1. Two fresh agents are created. Both inherit `*`/`*` (wildcard) by
 *      default at creation time.
 *   2. Agent B is narrowed to NO permissions.
 *   3. Both tokens hit /mcp tools/list and tools/call.
 *      - Agent A still sees plugin tools and can call them.
 *      - Agent B sees an empty tools list and gets a permission error on
 *        tools/call.
 *
 * If the cache is left stale, B continues to behave like a wildcard agent
 * and the assertions on the empty list / denied call fail.
 */

import { test, expect, APIRequestContext, Page } from '@playwright/test';
import { loginAs, createAgentToken } from './helpers/auth';
import { baseURL, adminUsername, adminPassword } from './helpers/server';

const MCP_URL = `${baseURL}/mcp`;

async function mcpCall(
  request: APIRequestContext,
  token: string,
  method: string,
  params?: Record<string, unknown>,
) {
  const body: Record<string, unknown> = { jsonrpc: '2.0', method, id: 1 };
  if (params !== undefined) body.params = params;
  return request.post(MCP_URL, {
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
    },
    data: body,
    failOnStatusCode: false,
  });
}

// Reads the agents JSON list and returns the numeric ID for `name`.
async function findAgentID(page: Page, name: string): Promise<number> {
  const resp = await page.request.get(`${baseURL}/admin/agents`, {
    headers: { Accept: 'application/json' },
  });
  expect(resp.status()).toBe(200);
  const body = (await resp.json()) as { agents: Array<{ id: number; name: string }> };
  const match = body.agents.find(a => a.name === name);
  if (!match) throw new Error(`agent ${name} not found in /admin/agents JSON`);
  return match.id;
}

// Replaces an agent's permissions wholesale by POSTing to the
// /admin/agents/{id}/permissions endpoint. perms is { plugin: tools[] };
// pass {} to clear all permissions.
async function setAgentPermissions(
  page: Page,
  agentID: number,
  perms: Record<string, string[]>,
): Promise<void> {
  await page.goto(`${baseURL}/admin/agents/${agentID}`);
  const csrf = await page.inputValue(
    `form[action="/admin/agents/${agentID}/permissions"] input[name="csrf_token"]`,
  );

  // Build the form body. Each `perm_<plugin>` field is a multi-value form
  // field; for "no perms" we just submit csrf_token alone.
  const params = new URLSearchParams();
  params.append('csrf_token', csrf);
  for (const [plugin, tools] of Object.entries(perms)) {
    for (const tool of tools) params.append(`perm_${plugin}`, tool);
  }

  const resp = await page.request.post(
    `${baseURL}/admin/agents/${agentID}/permissions`,
    {
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      data: params.toString(),
      maxRedirects: 0,
      failOnStatusCode: false,
    },
  );
  expect([302, 303]).toContain(resp.status());
}

test('per-agent permissions are enforced on /mcp (narrowed agent cannot see or call plugin tools)', async ({
  page,
  request,
}) => {
  // Both helpers expect us to be logged in as admin.
  await loginAs(page, adminUsername(), adminPassword());

  // Two agents with deterministic names to avoid cross-test collisions.
  const stamp = Date.now();
  const wideName = `e2e-perm-wide-${stamp}`;
  const narrowName = `e2e-perm-narrow-${stamp}`;

  const wideToken = await createAgentToken(page, wideName);
  const narrowToken = await createAgentToken(page, narrowName);

  // Strip the narrow agent down to zero permissions.
  const narrowID = await findAgentID(page, narrowName);
  await setAgentPermissions(page, narrowID, {});

  // tools/list — wide agent sees tools, narrow agent sees an empty list.
  const wideListResp = await mcpCall(request, wideToken, 'tools/list');
  expect(wideListResp.status()).toBe(200);
  const wideList = await wideListResp.json();
  const wideTools = (wideList.result.tools as Array<{ name: string }>).map(t => t.name);
  expect(wideTools.length).toBeGreaterThan(0);
  // list_plugins is always exposed by the federated dispatcher to anyone
  // with the builtin/* perm; wildcard agents must see it.
  expect(wideTools).toContain('list_plugins');

  const narrowListResp = await mcpCall(request, narrowToken, 'tools/list');
  expect(narrowListResp.status()).toBe(200);
  const narrowList = await narrowListResp.json();
  const narrowTools = (narrowList.result.tools as Array<{ name: string }>).map(t => t.name);
  expect(narrowTools.length).toBe(0);

  // tools/call — wide agent's call succeeds (returns either result or
  // app-level error envelope, but no JSON-RPC permission error); narrow
  // agent's call is rejected with a JSON-RPC error.
  const wideCallResp = await mcpCall(request, wideToken, 'tools/call', {
    name: 'list_plugins',
    arguments: {},
  });
  const wideCall = await wideCallResp.json();
  expect(wideCall.result).toBeDefined();
  expect(wideCall.error).toBeUndefined();

  const narrowCallResp = await mcpCall(request, narrowToken, 'tools/call', {
    name: 'list_plugins',
    arguments: {},
  });
  const narrowCall = await narrowCallResp.json();
  expect(narrowCall.error).toBeDefined();
  expect(narrowCall.error.code).toBeLessThan(0);
});

test('granting a single tool exposes only that tool to the agent', async ({
  page,
  request,
}) => {
  await loginAs(page, adminUsername(), adminPassword());

  const stamp = Date.now();
  const name = `e2e-perm-onetool-${stamp}`;
  const token = await createAgentToken(page, name);
  const id = await findAgentID(page, name);

  // Grant the one builtin we know exists on every install.
  await setAgentPermissions(page, id, { __builtin: ['list_plugins'] });

  const listResp = await mcpCall(request, token, 'tools/list');
  const list = await listResp.json();
  const names = (list.result.tools as Array<{ name: string }>).map(t => t.name);
  expect(names).toContain('list_plugins');
  // No other builtin should be visible.
  expect(names).not.toContain('install_plugin');
  expect(names).not.toContain('unload_plugin');

  // Allowed tool succeeds.
  const okResp = await mcpCall(request, token, 'tools/call', {
    name: 'list_plugins',
    arguments: {},
  });
  const ok = await okResp.json();
  expect(ok.result).toBeDefined();

  // Denied tool returns a JSON-RPC error.
  const denyResp = await mcpCall(request, token, 'tools/call', {
    name: 'install_plugin',
    arguments: { dir: './e2e/test-plugins/notes-plugin' },
  });
  const deny = await denyResp.json();
  expect(deny.error).toBeDefined();
  expect(deny.error.code).toBeLessThan(0);
});
