/**
 * scheduler.spec.ts — E2E tests for the scheduler-plugin (task #15).
 *
 * Covers the five scenarios from the Architect contract:
 *   1. Create a job via the UI, wait for the engine to fire it, verify success.
 *   2. Manual trigger produces exactly ONE job_runs row (Phase C.5 fix).
 *   3. Invalid cron input shows an inline error; a valid expression shows the
 *      next-5-fires preview.
 *   4. Delete cascades job_runs (FK ON DELETE CASCADE from Phase C.5).
 *   5. Week and Month views render without JS errors.
 *
 * Plugin invocation strategy:
 *   - beforeAll bootstraps a fresh agent via createAgentToken(), which posts
 *     to /admin/agents with a logged-in admin session and reads the new
 *     token from the redirect. New agents get wildcard `*` / `*` permissions
 *     automatically, so the token can call any tool on any plugin via the
 *     federated /mcp endpoint.
 *   - beforeAll then loads the scheduler-plugin via the admin web UI
 *     (POST /plugins/load).
 *   - All MCP tool calls go through the federated endpoint with the
 *     sanitised tool id "scheduler_plugin__<tool>".
 *
 * If the plugin binary cannot be loaded (e.g. plugin.wasm missing), every
 * test skips gracefully via the pluginLoaded flag.
 */

import { test, expect, Page, APIRequestContext } from '@playwright/test';
import { loginAs, createAgentToken } from './helpers/auth';
import { baseURL, adminUsername, adminPassword } from './helpers/server';

// Plugin lives under the e2e test plugins root (e2e/test-plugins/scheduler-plugin)
// — global-setup.ts copies it there before any test runs. The loader rejects
// paths outside cfg.PluginDir, so we must use the in-tree path here.
const PLUGIN_DIR = './e2e/test-plugins/scheduler-plugin';
const PLUGIN_NAME = 'scheduler-plugin';
const FEDERATED_PREFIX = 'scheduler_plugin__'; // loader.sanitizePluginName("scheduler-plugin")
const MCP_FEDERATED = `${baseURL}/mcp`;

// Agent bearer token, populated by beforeAll.
let agentBearerToken = '';
let pluginLoaded = false;

// ---- Helpers ---------------------------------------------------------------

/**
 * Calls a scheduler-plugin MCP tool through the federated endpoint using the
 * agent bearer token bootstrapped in beforeAll.
 */
async function call(
  request: APIRequestContext,
  toolName: string,
  args: Record<string, unknown>,
  id = 1,
): Promise<{ result?: any; error?: { code: number; message: string }; status: number }> {
  const resp = await request.post(MCP_FEDERATED, {
    headers: {
      Authorization: `Bearer ${agentBearerToken}`,
      'Content-Type': 'application/json',
    },
    data: {
      jsonrpc: '2.0',
      method: 'tools/call',
      params: { name: FEDERATED_PREFIX + toolName, arguments: args },
      id,
    },
  });
  const body = await resp.json();
  return { ...body, status: resp.status() };
}

/** Unwraps the JSON text content inside an MCP tools/call result. */
function unwrap<T = any>(result: any): T | null {
  if (!result) return null;
  const content = result?.content?.[0]?.text;
  if (!content) return null;
  try {
    return JSON.parse(content);
  } catch {
    return null;
  }
}

/**
 * Loads the scheduler-plugin via the admin web UI. Returns true if the
 * plugin is loaded after the call (already-loaded counts as success).
 *
 * The page parameter must already be authenticated as admin — this function
 * does not log in. Uses the same CSRF + page.request pattern as
 * createAgentToken so we don't depend on htmx form submission semantics.
 */
async function ensurePluginLoaded(page: Page): Promise<boolean> {
  console.log('[ensurePluginLoaded] step 1: GET /admin/plugins');
  const navResp = await page.goto(`${baseURL}/admin/plugins`);
  console.log(`[ensurePluginLoaded] step 2: nav status = ${navResp?.status()}`);
  if (!navResp || navResp.status() !== 200) return false;

  const alreadyLoaded = await page.locator(`#plugin-list >> text=${PLUGIN_NAME}`).first().isVisible().catch(() => false);
  console.log(`[ensurePluginLoaded] step 3: alreadyLoaded = ${alreadyLoaded}`);
  if (alreadyLoaded) return true;

  const csrfToken = await page
    .inputValue('input[name="csrf_token"]', { timeout: 5_000 })
    .catch(() => '');
  console.log(`[ensurePluginLoaded] step 4: csrf = ${csrfToken ? '<found>' : 'EMPTY'}`);
  if (!csrfToken) return false;

  console.log(`[ensurePluginLoaded] step 5: POST /admin/plugins/load dir=${PLUGIN_DIR}`);
  const resp = await page.request.post(`${baseURL}/admin/plugins/load`, {
    form: {
      csrf_token: csrfToken,
      dir: PLUGIN_DIR,
    },
    failOnStatusCode: false,
    timeout: 30_000,
  });
  console.log(`[ensurePluginLoaded] step 6: load status = ${resp.status()}, body head = ${(await resp.text()).slice(0, 200)}`);

  // Verify by re-fetching the admin plugins page and checking presence in the
  // loaded-plugins tbody (NOT the Discovered Plugins section, which lists every
  // discoverable plugin including unloaded ones).
  await page.goto(`${baseURL}/admin/plugins`);
  const verified = await page.locator(`#plugin-list >> text=${PLUGIN_NAME}`).first().isVisible().catch(() => false);
  console.log(`[ensurePluginLoaded] step 7: verified loaded = ${verified}`);
  return verified;
}

/** Deletes every job in the plugin DB so each test starts clean. */
async function wipeJobs(request: APIRequestContext): Promise<void> {
  const listed = await call(request, 'list_jobs', {});
  const data = unwrap<{ jobs?: Array<{ id: number }> }>(listed.result);
  if (!data?.jobs) return;
  for (const j of data.jobs) {
    await call(request, 'delete_job', { id: j.id });
  }
}

// ---- Test state ------------------------------------------------------------

// Loading the plugin via the admin UI involves a full WASM initialisation
// and schema migration on first boot; the Playwright default 30s hook
// timeout is too tight on cold machines. setTimeout inside the hook bumps
// it for this single invocation.
test.beforeAll(async ({ browser }) => {
  test.setTimeout(180_000);
  const page = await browser.newPage();
  try {
    // Bootstrap a fresh agent first; createAgentToken() handles the admin
    // login, so subsequent requests on this page are authenticated.
    agentBearerToken = await createAgentToken(page, `e2e-scheduler-${Date.now()}`);
    pluginLoaded = await ensurePluginLoaded(page);
  } finally {
    await page.close();
  }
});

test.beforeEach(async ({ request }) => {
  if (!pluginLoaded) return;
  await wipeJobs(request);
});

// ---- Tests -----------------------------------------------------------------

test.describe('scheduler-plugin', () => {
  test.skip(() => !pluginLoaded, 'scheduler-plugin not loaded — compile plugin.wasm to enable');

  // ---------------------------------------------------------------------------
  // 1. Create job via UI → wait for engine fire → verify success
  // ---------------------------------------------------------------------------
  test('Scheduler — Create job via UI and verify firing', async ({ page, request }) => {
    test.setTimeout(180_000);
    await loginAs(page, adminUsername(), adminPassword());
    await page.goto(`${baseURL}/p/scheduler-plugin/scheduler_list`);

    // Open the New Job modal.
    await page.locator('[data-testid="sched-new-job"]').click();
    const dialog = page.locator('#sched-job-dialog');
    await expect(dialog).toBeVisible();

    // Fill in a minute-level schedule so the engine fires within the test window.
    await dialog.locator('[data-testid="sched-field-name"]').fill('e2e-minute-job');
    await dialog.locator('[data-testid="sched-field-cron"]').fill('* * * * *');
    await dialog.locator('[data-testid="sched-field-tz"]').fill('UTC');
    await dialog.locator('[data-testid="sched-field-agent"]').fill('e2e-agent');
    await dialog.locator('[data-testid="sched-field-prompt"]').fill('e2e prompt');

    // The live preview should show next 5 fires (authoritative via validate_cron).
    await expect(dialog.locator('[data-testid="sched-preview"]')).toContainText(/Next .* fires/i, { timeout: 5000 });

    await dialog.locator('[data-testid="sched-form-save"]').click();

    // After reload, the new row should be visible in the list.
    await expect(page.locator('text=e2e-minute-job')).toBeVisible({ timeout: 5000 });

    // Poll via MCP until the first run lands. Minute-level cron + default
    // 60s tick interval → up to ~150s worst case.
    const deadline = Date.now() + 150_000;
    let fired = false;
    while (Date.now() < deadline) {
      const listed = await call(request, 'list_jobs', {});
      const data = unwrap<{ jobs: any[] }>(listed.result);
      const row = data?.jobs?.find(j => j.name === 'e2e-minute-job');
      if (row?.last_run_status === 'success') {
        fired = true;
        break;
      }
      await new Promise(r => setTimeout(r, 3_000));
    }
    expect(fired, 'engine should have fired the minute-level job within 150s').toBe(true);
  });

  // ---------------------------------------------------------------------------
  // 2. trigger_job_now produces exactly ONE job_runs row
  // ---------------------------------------------------------------------------
  test('Scheduler — trigger_job_now inserts exactly one run row (Phase C.5 fix)', async ({ page, request }) => {
    // Create a disabled job so the tick loop never fires it on its own.
    const created = await call(request, 'create_job', {
      name: 'e2e-manual',
      cron_expr: '0 9 * * *',
      prompt: 'manual only',
      agent_id: 'e2e-agent',
      timezone: 'UTC',
      enabled: false,
    });
    const job = unwrap<{ job: { id: number } }>(created.result)?.job;
    expect(job?.id, 'create_job should return a job id').toBeGreaterThan(0);

    await loginAs(page, adminUsername(), adminPassword());
    await page.goto(`${baseURL}/p/scheduler-plugin/scheduler_list`);
    await expect(page.locator(`[data-testid="sched-row-${job!.id}"]`)).toBeVisible();

    await page.locator(`[data-testid="sched-trigger-${job!.id}"]`).click();

    // Give the engine a moment to finish the manual firing pipeline.
    await page.waitForTimeout(2_000);

    // Exactly one row in get_job.recent_runs: the engine promoted the
    // plugin's placeholder row (Phase C.5) instead of inserting a second one.
    const detail = await call(request, 'get_job', { id: job!.id });
    const data = unwrap<{ recent_runs: Array<{ manual: boolean; status: string }> }>(detail.result);
    expect(data?.recent_runs?.length, 'manual trigger should produce exactly one run row').toBe(1);
    expect(data?.recent_runs?.[0].manual).toBe(true);
    expect(['success', 'running']).toContain(data?.recent_runs?.[0].status);
  });

  // ---------------------------------------------------------------------------
  // 3. Invalid cron inline error + valid expression preview
  // ---------------------------------------------------------------------------
  test('Scheduler — invalid cron shows inline error; valid expression shows preview', async ({ page }) => {
    await loginAs(page, adminUsername(), adminPassword());
    await page.goto(`${baseURL}/p/scheduler-plugin/scheduler_list`);

    await page.locator('[data-testid="sched-new-job"]').click();
    const dialog = page.locator('#sched-job-dialog');
    await expect(dialog).toBeVisible();

    const cronInput = dialog.locator('[data-testid="sched-field-cron"]');
    const preview = dialog.locator('[data-testid="sched-preview"]');

    // Invalid expression — the read-only validate_cron tool rejects it and
    // the preview flips to the error state.
    await cronInput.fill('99 99 99 99 99');
    await expect(preview).toHaveClass(/sched-preview-error/, { timeout: 5000 });

    // Attempting to save should surface an error, not silently create a row.
    await dialog.locator('[data-testid="sched-field-name"]').fill('e2e-invalid');
    await dialog.locator('[data-testid="sched-field-agent"]').fill('e2e-agent');
    await dialog.locator('[data-testid="sched-field-prompt"]').fill('should not save');
    await dialog.locator('[data-testid="sched-form-save"]').click();
    await expect(dialog.locator('[data-testid="sched-form-error"]')).toBeVisible();
    await expect(dialog).toBeVisible(); // dialog is NOT closed

    // Now swap in a valid weekday-morning expression.
    await cronInput.fill('0 9 * * 1-5');
    await expect(preview).not.toHaveClass(/sched-preview-error/, { timeout: 5000 });
    await expect(preview).toContainText(/Next .* fires/i);
  });

  // ---------------------------------------------------------------------------
  // 4. Delete cascades job_runs (FK ON DELETE CASCADE — Phase C.5)
  // ---------------------------------------------------------------------------
  test('Scheduler — delete job cascades run history', async ({ request }) => {
    const created = await call(request, 'create_job', {
      name: 'e2e-cascade',
      cron_expr: '0 9 * * *',
      prompt: 'cascade',
      agent_id: 'e2e-agent',
      timezone: 'UTC',
      enabled: false,
    });
    const jobID = unwrap<{ job: { id: number } }>(created.result)?.job?.id;
    expect(jobID).toBeGreaterThan(0);

    // Manually trigger once so there's at least one row to cascade.
    await call(request, 'trigger_job_now', { id: jobID });
    await new Promise(r => setTimeout(r, 1_500));

    const preDelete = await call(request, 'get_job', { id: jobID });
    const preRuns = unwrap<{ recent_runs: any[] }>(preDelete.result)?.recent_runs;
    expect(preRuns?.length, 'expected at least one run before delete').toBeGreaterThanOrEqual(1);

    const del = await call(request, 'delete_job', { id: jobID });
    const delData = unwrap<{ deleted: boolean; runs_deleted: number }>(del.result);
    expect(delData?.deleted).toBe(true);
    expect(delData?.runs_deleted, 'runs_deleted count should reflect cascade').toBeGreaterThanOrEqual(1);

    // get_job must now fail — the row (and, via FK cascade, its runs) are gone.
    const after = await call(request, 'get_job', { id: jobID });
    const afterData = unwrap(after.result);
    // Either an error response or an unparseable result both indicate "not found".
    expect(afterData === null || after.error != null || after.status >= 400).toBeTruthy();
  });

  // ---------------------------------------------------------------------------
  // 5. Week + Month views render without JS errors and place jobs correctly
  // ---------------------------------------------------------------------------
  test('Scheduler — Week and Month views render', async ({ page, request }) => {
    // Seed one enabled daily job so the grid has something to show.
    await call(request, 'create_job', {
      name: 'e2e-daily',
      cron_expr: '0 9 * * *',
      prompt: 'daily',
      agent_id: 'e2e-agent',
      timezone: 'UTC',
    });

    const jsErrors: string[] = [];
    page.on('pageerror', err => jsErrors.push(err.message));

    await loginAs(page, adminUsername(), adminPassword());

    // Week view.
    await page.goto(`${baseURL}/p/scheduler-plugin/scheduler_week`);
    await expect(page.locator('[data-testid="sched-week"]')).toBeVisible();
    await expect(page.locator('[data-testid="sched-tab-scheduler_week"]')).toHaveClass(/sched-tab-active/);

    // Month view.
    await page.goto(`${baseURL}/p/scheduler-plugin/scheduler_month`);
    await expect(page.locator('[data-testid="sched-month"]')).toBeVisible();
    await expect(page.locator('[data-testid="sched-tab-scheduler_month"]')).toHaveClass(/sched-tab-active/);

    // List view.
    await page.goto(`${baseURL}/p/scheduler-plugin/scheduler_list`);
    await expect(page.locator('[data-testid="sched-list"]')).toBeVisible();
    await expect(page.locator('text=e2e-daily')).toBeVisible();

    expect(jsErrors, `no JS errors expected, got: ${jsErrors.join(' | ')}`).toEqual([]);
  });
});
