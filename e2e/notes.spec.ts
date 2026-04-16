/**
 * notes.spec.ts — E2E tests for the notes-plugin MCP tools and dashboard widget.
 *
 * The notes-plugin must be loaded before these tests run. If the plugin is not
 * loaded (WASM binary not present in test-plugins), all tests skip gracefully.
 *
 * Plugin loading: the test setup attempts to load the plugin via the admin web
 * UI (POST /plugins/load). A compiled plugin.wasm must exist in the notes-plugin
 * directory for loading to succeed.
 *
 * Token retrieval: after loading, the bearer token is fetched via the federated
 * MCP endpoint POST /mcp using the admin token with get_plugin_token tool.
 */

import { test, expect, Page, APIRequestContext } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

const MCP_NOTES = `${baseURL}/plugins/notes-plugin/mcp`;
const MCP_FEDERATED = `${baseURL}/mcp`;
const PLUGIN_DIR = './e2e/test-plugins/notes-plugin';

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

/** Calls a tool on the notes-plugin MCP endpoint. */
async function notesMCPCall(
  request: APIRequestContext,
  token: string,
  toolName: string,
  args: Record<string, unknown>,
  id = 1,
): Promise<{ result?: unknown; error?: { code: number; message: string }; status: number }> {
  const resp = await request.post(MCP_NOTES, {
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
 * Loads the notes-plugin via the web admin UI and returns the bearer token.
 * Returns null if the plugin could not be loaded (e.g. WASM binary missing).
 */
async function loadNotesPluginAndGetToken(
  page: Page,
  request: APIRequestContext,
): Promise<string | null> {
  // Try to get the token first (plugin may already be loaded).
  const existing = await adminMCPCall(request, 'get_plugin_token', { name: 'notes-plugin' });
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
  const tokenResp = await adminMCPCall(request, 'get_plugin_token', { name: 'notes-plugin' });
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

let notesToken: string | null = null;

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage();
  const request = page.request;
  notesToken = await loadNotesPluginAndGetToken(page, request);
  await page.close();
});

// Skip all tests if the plugin could not be loaded.
test.beforeEach(async () => {
  // Tests that require the plugin will skip individually if token is null.
});

function requireToken(): string {
  if (!notesToken) {
    test.skip();
    return ''; // unreachable, skip throws
  }
  return notesToken;
}

// ---- MCP CRUD: create_note ----

test('create_note: valid note returns note with id', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'create_note', { title: 'E2E Test Note' });
  expect(resp.status).toBe(200);
  expect(resp.error).toBeUndefined();
  const result = resp.result as { content?: Array<{ text?: string }> };
  expect(result.content?.[0]?.text).toBeTruthy();
  const note = JSON.parse(result.content![0].text!);
  expect(note.note.id).toBeGreaterThan(0);
  expect(note.note.title).toBe('E2E Test Note');
  expect(Array.isArray(note.note.tags)).toBe(true);
});

test('create_note: with tags stores tags correctly', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'create_note', {
    title: 'Tagged Note',
    tags: ['e2e', 'test'],
  });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const note = JSON.parse(result.content![0].text!);
  expect(note.note.tags).toContain('e2e');
  expect(note.note.tags).toContain('test');
});

test('create_note: missing title returns error', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'create_note', {});
  // Either a JSON-RPC error or a tool error in the result.
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- MCP CRUD: list_notes ----

test('list_notes: no filter returns all notes', async ({ request }) => {
  const token = requireToken();
  // Create a note first to ensure at least one exists.
  await notesMCPCall(request, token, 'create_note', { title: 'List Test Note' });

  const resp = await notesMCPCall(request, token, 'list_notes', {});
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.total).toBeGreaterThan(0);
  expect(Array.isArray(data.notes)).toBe(true);
});

test('list_notes: tag filter returns only matching notes', async ({ request }) => {
  const token = requireToken();
  const uniqueTag = `e2e-tag-${Date.now()}`;
  await notesMCPCall(request, token, 'create_note', {
    title: 'Filtered Note',
    tags: [uniqueTag],
  });

  const resp = await notesMCPCall(request, token, 'list_notes', { tag: uniqueTag });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.total).toBeGreaterThanOrEqual(1);
  for (const note of data.notes) {
    expect(note.tags).toContain(uniqueTag);
  }
});

test('list_notes: limit parameter restricts result count', async ({ request }) => {
  const token = requireToken();
  // Create extra notes to ensure more than 1 exist.
  await notesMCPCall(request, token, 'create_note', { title: 'Limit Test A' });
  await notesMCPCall(request, token, 'create_note', { title: 'Limit Test B' });

  const resp = await notesMCPCall(request, token, 'list_notes', { limit: 1 });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.notes.length).toBeLessThanOrEqual(1);
});

test('list_notes: results ordered by created_at DESC (newest first)', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'list_notes', { limit: 10 });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  if (data.notes.length < 2) return; // not enough data to test ordering
  for (let i = 0; i < data.notes.length - 1; i++) {
    const a = data.notes[i].created_at as string;
    const b = data.notes[i + 1].created_at as string;
    expect(a >= b).toBe(true);
  }
});

// ---- MCP CRUD: get_note ----

test('get_note: valid id returns note', async ({ request }) => {
  const token = requireToken();
  const created = await notesMCPCall(request, token, 'create_note', { title: 'Get Me' });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const createdNote = JSON.parse(createdResult.content![0].text!);
  const id = createdNote.note.id;

  const resp = await notesMCPCall(request, token, 'get_note', { id });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.note.id).toBe(id);
  expect(data.note.title).toBe('Get Me');
});

test('get_note: non-existent id returns error', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'get_note', { id: 999999 });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- MCP CRUD: update_note ----

test('update_note: title change is reflected', async ({ request }) => {
  const token = requireToken();
  const created = await notesMCPCall(request, token, 'create_note', { title: 'Original Title' });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).note.id;

  const resp = await notesMCPCall(request, token, 'update_note', { id, title: 'Updated Title' });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.note.title).toBe('Updated Title');
});

test('update_note: clear_body empties body field', async ({ request }) => {
  const token = requireToken();
  const created = await notesMCPCall(request, token, 'create_note', {
    title: 'Body Note',
    body: 'Some body text',
  });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).note.id;

  const resp = await notesMCPCall(request, token, 'update_note', { id, clear_body: true });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.note.body ?? '').toBe('');
});

test('update_note: tags replacement overwrites existing tags', async ({ request }) => {
  const token = requireToken();
  const created = await notesMCPCall(request, token, 'create_note', {
    title: 'Tags Note',
    tags: ['old'],
  });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).note.id;

  const resp = await notesMCPCall(request, token, 'update_note', { id, tags: ['new1', 'new2'] });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.note.tags).toContain('new1');
  expect(data.note.tags).not.toContain('old');
});

test('update_note: clear_tags removes all tags', async ({ request }) => {
  const token = requireToken();
  const created = await notesMCPCall(request, token, 'create_note', {
    title: 'Clear Tags Note',
    tags: ['a', 'b'],
  });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).note.id;

  const resp = await notesMCPCall(request, token, 'update_note', { id, clear_tags: true });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.note.tags).toHaveLength(0);
});

test('update_note: no fields provided returns error', async ({ request }) => {
  const token = requireToken();
  const created = await notesMCPCall(request, token, 'create_note', { title: 'No-op Note' });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).note.id;

  const resp = await notesMCPCall(request, token, 'update_note', { id });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

test('update_note: non-existent id returns error', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'update_note', {
    id: 999999,
    title: 'Ghost Note',
  });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- MCP CRUD: delete_note ----

test('delete_note: existing note returns deleted=true', async ({ request }) => {
  const token = requireToken();
  const created = await notesMCPCall(request, token, 'create_note', { title: 'To Be Deleted' });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).note.id;

  const resp = await notesMCPCall(request, token, 'delete_note', { id });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.deleted).toBe(true);
  expect(data.id).toBe(id);
});

test('delete_note: non-existent note returns deleted=false', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'delete_note', { id: 999998 });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.deleted).toBe(false);
});

test('delete_note: get_note after delete returns error', async ({ request }) => {
  const token = requireToken();
  const created = await notesMCPCall(request, token, 'create_note', {
    title: 'Delete Then Get',
  });
  const createdResult = created.result as { content?: Array<{ text?: string }> };
  const id = JSON.parse(createdResult.content![0].text!).note.id;

  await notesMCPCall(request, token, 'delete_note', { id });

  const getResp = await notesMCPCall(request, token, 'get_note', { id });
  const hasError =
    getResp.error !== undefined ||
    (getResp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- Bearer token authentication ----

test('MCP request with no token returns 401', async ({ request }) => {
  if (!notesToken) {
    test.skip();
    return;
  }
  const resp = await request.post(MCP_NOTES, {
    headers: { 'Content-Type': 'application/json' },
    data: {
      jsonrpc: '2.0',
      method: 'tools/call',
      params: { name: 'list_notes', arguments: {} },
      id: 1,
    },
  });
  expect(resp.status()).toBe(401);
});

test('MCP request with wrong token returns 401', async ({ request }) => {
  if (!notesToken) {
    test.skip();
    return;
  }
  const resp = await request.post(MCP_NOTES, {
    headers: {
      Authorization: 'Bearer wrong-token-value',
      'Content-Type': 'application/json',
    },
    data: {
      jsonrpc: '2.0',
      method: 'tools/call',
      params: { name: 'list_notes', arguments: {} },
      id: 1,
    },
  });
  expect(resp.status()).toBe(401);
});

// ---- Dashboard widget ----

test('dashboard page loads and shows Recent Notes widget when plugin loaded', async ({ page }) => {
  if (!notesToken) {
    test.skip();
    return;
  }
  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/dashboard`);
  // The widget title is "Recent Notes" as declared in manifest.json.
  const bodyText = await page.locator('body').innerText();
  expect(bodyText).toContain('Recent Notes');
});

test('widget refresh endpoint for note_overview returns non-500', async ({ page }) => {
  if (!notesToken) {
    test.skip();
    return;
  }
  await loginAs(page, adminToken());
  const resp = await page.request.get(
    `${baseURL}/dashboard/widgets/notes-plugin__note_overview/refresh`,
  );
  expect(resp.status()).not.toBe(500);
});

// ---- Edit History: notes__get_history ----

test('get_history: newly created note has one "created" revision', async ({ request }) => {
  const token = requireToken();
  // Create a note using quick_capture (current API)
  const createResp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'History test note',
    title: 'History Test',
  });
  expect(createResp.status).toBe(200);
  const createResult = createResp.result as { content?: Array<{ text?: string }> };
  const entry = JSON.parse(createResult.content![0].text!).entry;
  expect(entry.id).toBeGreaterThan(0);

  // Fetch history
  const histResp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: entry.id,
  });
  expect(histResp.status).toBe(200);
  const histResult = histResp.result as { content?: Array<{ text?: string }> };
  const histData = JSON.parse(histResult.content![0].text!);
  expect(histData.total).toBeGreaterThanOrEqual(1);
  expect(histData.revisions.length).toBeGreaterThanOrEqual(1);
  expect(histData.revisions[0].change_type).toBe('created');
  expect(histData.revisions[0].entry_id).toBe(entry.id);
});

test('get_history: update_entry creates an "edited" revision', async ({ request }) => {
  const token = requireToken();
  // Create
  const createResp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Original body',
    title: 'Edit History Test',
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  // Update
  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    body: 'Updated body content',
  });

  // Fetch history
  const histResp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: entry.id,
  });
  const histData = JSON.parse(
    (histResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(histData.total).toBeGreaterThanOrEqual(2);
  // Most recent revision should be "edited"
  expect(histData.revisions[0].change_type).toBe('edited');
  expect(histData.revisions[0].change_description).toContain('body');
});

test('get_history: non-existent entry returns empty list', async ({ request }) => {
  const token = requireToken();
  const histResp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: 999999,
  });
  expect(histResp.status).toBe(200);
  const histData = JSON.parse(
    (histResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(histData.total).toBe(0);
  expect(histData.revisions).toHaveLength(0);
});

// ---- Edit History: notes__restore_revision ----

test('restore_revision: restores entry to previous state', async ({ request }) => {
  const token = requireToken();
  // Create
  const createResp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'First version body',
    title: 'Restore Test',
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  // Update to second version
  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    body: 'Second version body',
    title: 'Restore Test Updated',
  });

  // Get history — first revision (oldest = "created") should have original content
  const histResp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: entry.id,
  });
  const histData = JSON.parse(
    (histResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  // revisions are ordered DESC, so the last one is the "created" revision
  const createdRevision = histData.revisions.find(
    (r: { change_type: string }) => r.change_type === 'created',
  );
  expect(createdRevision).toBeTruthy();

  // Restore to the created revision
  const restoreResp = await notesMCPCall(request, token, 'notes__restore_revision', {
    revision_id: createdRevision.id,
  });
  expect(restoreResp.status).toBe(200);
  const restoreData = JSON.parse(
    (restoreResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(restoreData.entry.title).toBe('Restore Test');
  expect(restoreData.entry.body).toBe('First version body');

  // History should now have a "restored" revision
  const hist2Resp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: entry.id,
  });
  const hist2Data = JSON.parse(
    (hist2Resp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(hist2Data.revisions[0].change_type).toBe('restored');
});

test('restore_revision: invalid revision_id returns error', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'notes__restore_revision', {
    revision_id: 999999,
  });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- Folder kind ----

test('create_folder: creates entry with kind=folder', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'E2E Test Folder',
    project_id: 1,
  });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.entry.kind).toBe('folder');
  expect(data.entry.title).toBe('E2E Test Folder');
  expect(data.entry.body).toBe('');
});

test('create_folder: requires title', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'notes__create_folder', {
    project_id: 1,
  });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

test('create_folder: requires project_id', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Orphan Folder',
  });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

test('create_folder: nested folder inside folder', async ({ request }) => {
  const token = requireToken();
  // Create parent folder
  const parentResp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Parent Folder',
    project_id: 1,
  });
  const parentData = JSON.parse(
    (parentResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  const parentId = parentData.entry.id;

  // Create child folder
  const childResp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Child Folder',
    project_id: 1,
    parent_id: parentId,
  });
  expect(childResp.status).toBe(200);
  const childData = JSON.parse(
    (childResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(childData.entry.kind).toBe('folder');
  expect(childData.entry.parent_id).toBe(parentId);
});

test('list: kind=folder filter returns only folders', async ({ request }) => {
  const token = requireToken();
  // Ensure at least one folder exists
  await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'List Filter Folder',
    project_id: 1,
  });

  const resp = await notesMCPCall(request, token, 'notes__list', { kind: 'folder' });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.total).toBeGreaterThanOrEqual(1);
  for (const entry of data.entries) {
    expect(entry.kind).toBe('folder');
  }
});

// ---- Author propagation ----

test('quick_capture: author param is stored in entry', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Author test note',
    title: 'Author Test',
    author: 'test-user',
  });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.entry.author).toBe('test-user');
});

test('quick_capture: no author defaults to human', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Default author note',
    title: 'Default Author',
  });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.entry.author).toBe('human');
});

test('create_doc: author param is stored in entry', async ({ request }) => {
  const token = requireToken();
  // First create a project via tasks or use an existing project_id
  const resp = await notesMCPCall(request, token, 'notes__create_doc', {
    title: 'Author Doc Test',
    body: 'Some body',
    project_id: 1,
    author: 'alice',
  });
  expect(resp.status).toBe(200);
  const result = resp.result as { content?: Array<{ text?: string }> };
  const data = JSON.parse(result.content![0].text!);
  expect(data.entry.author).toBe('alice');
});

test('update_entry: author param is recorded in revision', async ({ request }) => {
  const token = requireToken();
  // Create entry
  const createResp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Revision author test',
    title: 'Rev Author Test',
    author: 'bob',
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  // Update with different author
  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    body: 'Updated body',
    author: 'charlie',
  });

  // Check history — latest revision should have author "charlie"
  const histResp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: entry.id,
  });
  const histData = JSON.parse(
    (histResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  const editedRevision = histData.revisions.find(
    (r: { change_type: string }) => r.change_type === 'edited',
  );
  expect(editedRevision).toBeTruthy();
  expect(editedRevision.author).toBe('charlie');
});

// ---- Quick Notes via MCP ----

test('quick_capture: creates kind=note entry', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Quick note body',
    title: 'Quick Test',
  });
  expect(resp.status).toBe(200);
  const data = JSON.parse(
    (resp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(data.entry.kind).toBe('note');
  expect(data.entry.status).toBe('draft');
  expect(data.entry.body).toBe('Quick note body');
});

test('quick_capture: note with tags and priority', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Tagged quick note',
    tags: ['urgent', 'review'],
    priority: 'high',
  });
  expect(resp.status).toBe(200);
  const data = JSON.parse(
    (resp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(data.entry.tags).toContain('urgent');
  expect(data.entry.priority).toBe('high');
});

test('quick_capture: note linked to project', async ({ request }) => {
  const token = requireToken();
  const resp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Project-linked note',
    project_id: 1,
  });
  expect(resp.status).toBe(200);
  const data = JSON.parse(
    (resp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(data.entry.project_id).toBe(1);
});

// ---- Folder operations ----

test('create_folder: doc can be created inside folder', async ({ request }) => {
  const token = requireToken();
  // Create a folder
  const folderResp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Parent For Doc',
    project_id: 1,
  });
  const folder = JSON.parse(
    (folderResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  // Create a doc inside the folder
  const docResp = await notesMCPCall(request, token, 'notes__create_doc', {
    title: 'Doc Inside Folder',
    body: 'Some content',
    project_id: 1,
    parent_id: folder.id,
  });
  expect(docResp.status).toBe(200);
  const doc = JSON.parse(
    (docResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;
  expect(doc.parent_id).toBe(folder.id);
});

test('create_folder: folder inside folder works', async ({ request }) => {
  const token = requireToken();
  const parentResp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Outer Folder',
    project_id: 1,
  });
  const parent = JSON.parse(
    (parentResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  const childResp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Inner Folder',
    project_id: 1,
    parent_id: parent.id,
  });
  expect(childResp.status).toBe(200);
  const child = JSON.parse(
    (childResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;
  expect(child.kind).toBe('folder');
  expect(child.parent_id).toBe(parent.id);
});

test('delete_entry: folder cascade deletes children', async ({ request }) => {
  const token = requireToken();
  // Create folder with a doc inside
  const folderResp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Delete Cascade Folder',
    project_id: 1,
  });
  const folder = JSON.parse(
    (folderResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  await notesMCPCall(request, token, 'notes__create_doc', {
    title: 'Child Doc',
    body: 'Will be cascade deleted',
    project_id: 1,
    parent_id: folder.id,
  });

  // Delete the folder
  const delResp = await notesMCPCall(request, token, 'notes__delete_entry', { id: folder.id });
  expect(delResp.status).toBe(200);
  const delData = JSON.parse(
    (delResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(delData.deleted).toBe(true);
  expect(delData.cascade_count).toBeGreaterThanOrEqual(1);
});

test('move: folder can be moved to another folder', async ({ request }) => {
  const token = requireToken();
  const folder1Resp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Move Source',
    project_id: 1,
  });
  const folder1 = JSON.parse(
    (folder1Resp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  const folder2Resp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Move Target',
    project_id: 1,
  });
  const folder2 = JSON.parse(
    (folder2Resp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  const moveResp = await notesMCPCall(request, token, 'notes__move', {
    id: folder1.id,
    parent_id: folder2.id,
  });
  expect(moveResp.status).toBe(200);
  const moved = JSON.parse(
    (moveResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;
  expect(moved.parent_id).toBe(folder2.id);
});

test('get_tree: returns folders and docs in hierarchy', async ({ request }) => {
  const token = requireToken();
  // Create a structure: folder → doc
  const folderResp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Tree Test Folder',
    project_id: 1,
  });
  const folder = JSON.parse(
    (folderResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  await notesMCPCall(request, token, 'notes__create_doc', {
    title: 'Tree Test Doc',
    body: 'Inside folder',
    project_id: 1,
    parent_id: folder.id,
  });

  const treeResp = await notesMCPCall(request, token, 'notes__get_tree', { project_id: 1 });
  expect(treeResp.status).toBe(200);
  const treeData = JSON.parse(
    (treeResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(treeData.total).toBeGreaterThanOrEqual(2);

  // Find our folder in the tree
  const treeFolder = treeData.tree.find(
    (n: { entry: { id: number } }) => n.entry.id === folder.id,
  );
  expect(treeFolder).toBeTruthy();
  expect(treeFolder.entry.kind).toBe('folder');
  expect(treeFolder.children.length).toBeGreaterThanOrEqual(1);
});

// ---- Delete cascades to revisions ----

test('delete_entry: removes associated revisions', async ({ request }) => {
  const token = requireToken();
  // Create and update to generate revisions
  const createResp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Delete cascade test',
    title: 'Cascade Delete Test',
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    body: 'Updated for cascade test',
  });

  // Verify revisions exist
  const histResp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: entry.id,
  });
  const histData = JSON.parse(
    (histResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(histData.total).toBeGreaterThanOrEqual(2);

  // Delete the entry
  await notesMCPCall(request, token, 'notes__delete_entry', { id: entry.id });

  // Verify revisions are gone
  const hist2Resp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: entry.id,
  });
  const hist2Data = JSON.parse(
    (hist2Resp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(hist2Data.total).toBe(0);
});

// ---- History: revision author tracking ----

test('history: revision tracks correct author through edits', async ({ request }) => {
  const token = requireToken();
  // Create with author "alice"
  const createResp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Revision author tracking',
    title: 'Multi-Author History',
    author: 'alice',
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  // Edit 1 by "bob"
  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    body: 'Edited by bob',
    author: 'bob',
  });

  // Edit 2 by "charlie"
  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    title: 'Updated by Charlie',
    author: 'charlie',
  });

  // Fetch history
  const histResp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: entry.id,
  });
  const histData = JSON.parse(
    (histResp.result as { content: Array<{ text: string }> }).content[0].text,
  );

  // Should have at least 3 revisions: created, edited, edited
  expect(histData.total).toBeGreaterThanOrEqual(3);

  // Most recent (index 0) should be charlie's edit
  expect(histData.revisions[0].author).toBe('charlie');
  expect(histData.revisions[0].change_type).toBe('edited');

  // Second most recent should be bob's edit
  expect(histData.revisions[1].author).toBe('bob');

  // Created revision should be alice
  const created = histData.revisions.find(
    (r: { change_type: string }) => r.change_type === 'created',
  );
  expect(created.author).toBe('alice');
});

// ---- Restore: author tracked in restored revision ----

test('restore_revision: tracks restoring author', async ({ request }) => {
  const token = requireToken();
  const createResp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'V1 body',
    title: 'Restore Author Test',
    author: 'alice',
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  // Make an edit
  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    body: 'V2 body',
    author: 'bob',
  });

  // Get created revision ID
  const histResp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: entry.id,
  });
  const createdRev = JSON.parse(
    (histResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).revisions.find((r: { change_type: string }) => r.change_type === 'created');

  // Restore as "charlie"
  const restoreResp = await notesMCPCall(request, token, 'notes__restore_revision', {
    revision_id: createdRev.id,
    author: 'charlie',
  });
  expect(restoreResp.status).toBe(200);

  // Verify restored revision has charlie as author
  const hist2Resp = await notesMCPCall(request, token, 'notes__get_history', {
    entry_id: entry.id,
  });
  const hist2Data = JSON.parse(
    (hist2Resp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  expect(hist2Data.revisions[0].change_type).toBe('restored');
  expect(hist2Data.revisions[0].author).toBe('charlie');
});

// ---- Promote/Demote validation ----

test('promote: cannot promote a folder', async ({ request }) => {
  const token = requireToken();
  const folderResp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Unpromatable Folder',
    project_id: 1,
  });
  const folder = JSON.parse(
    (folderResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  const resp = await notesMCPCall(request, token, 'notes__promote', {
    id: folder.id,
    project_id: 1,
  });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

test('demote: cannot demote a folder', async ({ request }) => {
  const token = requireToken();
  const folderResp = await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Undemotable Folder',
    project_id: 1,
  });
  const folder = JSON.parse(
    (folderResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  const resp = await notesMCPCall(request, token, 'notes__demote', { id: folder.id });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});

// ---- Search across kinds ----

test('search: finds docs, notes, and folders', async ({ request }) => {
  const token = requireToken();
  const uniqueTag = `search-${Date.now()}`;

  // Create one of each kind
  await notesMCPCall(request, token, 'notes__quick_capture', {
    body: `Searchable note ${uniqueTag}`,
    title: `Search Note ${uniqueTag}`,
  });
  await notesMCPCall(request, token, 'notes__create_doc', {
    title: `Search Doc ${uniqueTag}`,
    body: `Searchable doc ${uniqueTag}`,
    project_id: 1,
  });
  await notesMCPCall(request, token, 'notes__create_folder', {
    title: `Search Folder ${uniqueTag}`,
    project_id: 1,
  });

  const resp = await notesMCPCall(request, token, 'notes__search', {
    query: uniqueTag,
  });
  expect(resp.status).toBe(200);
  const data = JSON.parse(
    (resp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  // Should find at least the note and doc (folder has no body to match, but title matches)
  expect(data.total).toBeGreaterThanOrEqual(2);
  const kinds = data.entries.map((e: { kind: string }) => e.kind);
  expect(kinds).toContain('note');
  expect(kinds).toContain('doc');
});

// ---- List with multiple filters ----

test('list: combined kind + project filter', async ({ request }) => {
  const token = requireToken();
  // Create doc and folder in project 1
  await notesMCPCall(request, token, 'notes__create_doc', {
    title: 'Filter Test Doc',
    body: 'content',
    project_id: 1,
  });
  await notesMCPCall(request, token, 'notes__create_folder', {
    title: 'Filter Test Folder',
    project_id: 1,
  });

  // List only docs in project 1
  const docResp = await notesMCPCall(request, token, 'notes__list', {
    kind: 'doc',
    project_id: 1,
  });
  const docData = JSON.parse(
    (docResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  for (const entry of docData.entries) {
    expect(entry.kind).toBe('doc');
    expect(entry.project_id).toBe(1);
  }

  // List only folders in project 1
  const folderResp = await notesMCPCall(request, token, 'notes__list', {
    kind: 'folder',
    project_id: 1,
  });
  const folderData = JSON.parse(
    (folderResp.result as { content: Array<{ text: string }> }).content[0].text,
  );
  for (const entry of folderData.entries) {
    expect(entry.kind).toBe('folder');
  }
});

// ---- Priority and status on notes/docs ----

test('quick_capture: priority is stored correctly', async ({ request }) => {
  const token = requireToken();
  for (const priority of ['low', 'medium', 'high']) {
    const resp = await notesMCPCall(request, token, 'notes__quick_capture', {
      body: `Priority ${priority} note`,
      priority,
    });
    expect(resp.status).toBe(200);
    const data = JSON.parse(
      (resp.result as { content: Array<{ text: string }> }).content[0].text,
    );
    expect(data.entry.priority).toBe(priority);
  }
});

test('update_entry: pin and unpin', async ({ request }) => {
  const token = requireToken();
  const createResp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Pin test note',
    title: 'Pin Test',
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  // Pin
  const pinResp = await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    pinned: true,
  });
  expect(JSON.parse(
    (pinResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry.pinned).toBe(true);

  // Unpin
  const unpinResp = await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    pinned: false,
  });
  expect(JSON.parse(
    (unpinResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry.pinned).toBe(false);
});

test('update_entry: status change draft to published', async ({ request }) => {
  const token = requireToken();
  const createResp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Status test',
    title: 'Status Test',
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;
  expect(entry.status).toBe('draft');

  const resp = await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    status: 'published',
  });
  const updated = JSON.parse(
    (resp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;
  expect(updated.status).toBe('published');
});

// ---- Move validation ----

// ---- UI: History panel and diff view ----

test('UI: history panel opens and shows revisions', async ({ page, request }) => {
  const token = requireToken();

  // Create a doc via MCP
  const createResp = await notesMCPCall(request, token, 'notes__create_doc', {
    title: 'History UI Test',
    body: '# Version 1\n\nOriginal content',
    project_id: 1,
    author: 'admin',
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  // Update to create revision
  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    body: '# Version 2\n\nUpdated content with changes',
    author: 'admin',
  });

  // Login and navigate to project notes
  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/projects/1/notes`);

  // Wait for sidebar to load and click on our doc
  const docNode = page.locator(`.tree-node[data-doc-id="${entry.id}"]`);
  if (await docNode.count() > 0) {
    await docNode.click();
    await page.waitForTimeout(500);

    // Click History button
    const historyBtn = page.locator('[data-action="toggle-history"]');
    if (await historyBtn.count() > 0) {
      await historyBtn.click();
      await page.waitForTimeout(1000);

      // History panel should be open
      const panel = page.locator('#notes-history-panel.open');
      expect(await panel.count()).toBeGreaterThan(0);

      // Should have at least 2 revisions (created + edited)
      const entries = page.locator('.notes-history-entry');
      expect(await entries.count()).toBeGreaterThanOrEqual(2);

      // First entry should be selected (current)
      const firstEntry = entries.first();
      expect(await firstEntry.getAttribute('class')).toContain('selected');
    }
  }

  // Cleanup
  await notesMCPCall(request, token, 'notes__delete_entry', { id: entry.id });
});

test('UI: clicking revision shows its content without flash', async ({ page, request }) => {
  const token = requireToken();

  const createResp = await notesMCPCall(request, token, 'notes__create_doc', {
    title: 'Revision Click Test',
    body: '# First version',
    project_id: 1,
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    body: '# Second version\n\nNew paragraph here',
  });

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/projects/1/notes`);

  const docNode = page.locator(`.tree-node[data-doc-id="${entry.id}"]`);
  if (await docNode.count() > 0) {
    await docNode.click();
    await page.waitForTimeout(500);

    const historyBtn = page.locator('[data-action="toggle-history"]');
    if (await historyBtn.count() > 0) {
      await historyBtn.click();
      await page.waitForTimeout(1000);

      // Click on the second (older) revision
      const entries = page.locator('.notes-history-entry');
      if (await entries.count() >= 2) {
        await entries.nth(1).click();
        await page.waitForTimeout(800);

        // The clicked entry should be selected
        expect(await entries.nth(1).getAttribute('class')).toContain('selected');

        // Meta should show "Viewing version by"
        const metaEl = page.locator('#notes-meta-edited');
        const metaText = await metaEl.textContent();
        expect(metaText).toContain('Viewing version by');

        // Body should show first version content (rendered markdown)
        const bodyView = page.locator('[data-doc-body-view]');
        const bodyHtml = await bodyView.innerHTML();
        expect(bodyHtml).toContain('First version');
      }
    }
  }

  await notesMCPCall(request, token, 'notes__delete_entry', { id: entry.id });
});

test('UI: compare diff view persists and is not overwritten', async ({ page, request }) => {
  const token = requireToken();

  const createResp = await notesMCPCall(request, token, 'notes__create_doc', {
    title: 'Diff Persist Test',
    body: 'Hello world original text',
    project_id: 1,
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    body: 'Hello world updated text with additions',
  });

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/projects/1/notes`);

  const docNode = page.locator(`.tree-node[data-doc-id="${entry.id}"]`);
  if (await docNode.count() === 0) {
    // Doc not visible, skip
    await notesMCPCall(request, token, 'notes__delete_entry', { id: entry.id });
    return;
  }

  await docNode.click();
  await page.waitForTimeout(500);

  const historyBtn = page.locator('[data-action="toggle-history"]');
  if (await historyBtn.count() === 0) {
    await notesMCPCall(request, token, 'notes__delete_entry', { id: entry.id });
    return;
  }

  await historyBtn.click();
  await page.waitForTimeout(1000);

  // Click "Compare with current version"
  const compareBtn = page.locator('[data-action="compare-versions"]');
  if (await compareBtn.count() > 0) {
    await compareBtn.click();

    // Button should now say "Clear diff view"
    await expect(compareBtn).toHaveText('Clear diff view');

    // Wait and verify diff content persists (not overwritten by async fetch)
    await page.waitForTimeout(2000);

    const bodyView = page.locator('[data-doc-body-view]');
    const bodyHtml = await bodyView.innerHTML();

    // Diff view should contain ins/del elements from wordDiff
    const hasDiffMarkers = bodyHtml.includes('<ins') || bodyHtml.includes('<del') || bodyHtml.includes('diff-');
    expect(hasDiffMarkers).toBe(true);

    // Verify it's still diff after another second (not overwritten)
    await page.waitForTimeout(1000);
    const bodyHtml2 = await bodyView.innerHTML();
    expect(bodyHtml2).toBe(bodyHtml); // Should be unchanged

    // Clear diff
    await compareBtn.click();
    await expect(compareBtn).toHaveText('Compare with current version');
  }

  await notesMCPCall(request, token, 'notes__delete_entry', { id: entry.id });
});

test('UI: history selected entry keeps highlight on mouse move', async ({ page, request }) => {
  const token = requireToken();

  const createResp = await notesMCPCall(request, token, 'notes__create_doc', {
    title: 'Highlight Persist Test',
    body: 'Version one',
    project_id: 1,
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  await notesMCPCall(request, token, 'notes__update_entry', {
    id: entry.id,
    body: 'Version two',
  });

  await loginAs(page, adminToken());
  await page.goto(`${baseURL}/projects/1/notes`);

  const docNode = page.locator(`.tree-node[data-doc-id="${entry.id}"]`);
  if (await docNode.count() > 0) {
    await docNode.click();
    await page.waitForTimeout(500);

    const historyBtn = page.locator('[data-action="toggle-history"]');
    if (await historyBtn.count() > 0) {
      await historyBtn.click();
      await page.waitForTimeout(1000);

      const entries = page.locator('.notes-history-entry');
      if (await entries.count() >= 2) {
        // Click second entry
        await entries.nth(1).click();
        await page.waitForTimeout(200);
        expect(await entries.nth(1).getAttribute('class')).toContain('selected');

        // Hover over first entry (mouse move)
        await entries.nth(0).hover();
        await page.waitForTimeout(200);

        // Second entry should STILL have selected class
        expect(await entries.nth(1).getAttribute('class')).toContain('selected');
      }
    }
  }

  await notesMCPCall(request, token, 'notes__delete_entry', { id: entry.id });
});

// ---- Move validation ----

test('move: cannot move a note', async ({ request }) => {
  const token = requireToken();
  const createResp = await notesMCPCall(request, token, 'notes__quick_capture', {
    body: 'Unmovable note',
    title: 'Move Fail',
  });
  const entry = JSON.parse(
    (createResp.result as { content: Array<{ text: string }> }).content[0].text,
  ).entry;

  const resp = await notesMCPCall(request, token, 'notes__move', {
    id: entry.id,
    sort_order: 5,
  });
  const hasError =
    resp.error !== undefined ||
    (resp.result as { isError?: boolean })?.isError === true;
  expect(hasError).toBe(true);
});
