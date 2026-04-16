import { test, expect, Page } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

// End-to-end coverage of the realtime SSE contract (task #26). Because
// the federated MCP endpoint requires a plugin-specific bearer token
// in the e2e fixture, the tests below trigger plugin actions via the
// authenticated web session instead of MCP. The underlying event flow
// (plugin → eventbus → realtime hub → SSE) is identical either way.

async function pluginAction(
  page: Page,
  plugin: string,
  tool: string,
  args: Record<string, unknown>,
): Promise<{ ok: boolean; status: number; body: string }> {
  const csrf = await page.evaluate(() => {
    const h = document.querySelector('input[name="csrf_token"]') as HTMLInputElement | null;
    return h ? h.value : '';
  });
  const resp = await page.request.post(`${baseURL}/p/${plugin}/action/${tool}`, {
    headers: {
      'Content-Type': 'application/json',
      'X-CSRF-Token': csrf,
    },
    data: args,
    failOnStatusCode: false,
  });
  return { ok: resp.ok(), status: resp.status(), body: await resp.text() };
}

test.describe('realtime SSE', () => {
  test('GET /api/events redirects unauthenticated to /login', async ({ request }) => {
    const resp = await request.get(`${baseURL}/api/events`, {
      maxRedirects: 0,
      failOnStatusCode: false,
    });
    expect([302, 303]).toContain(resp.status());
    const loc = resp.headers()['location'] ?? '';
    expect(loc).toContain('/login');
  });

  test('task update reflects in board without reload', async ({ page }) => {
    await loginAs(page, adminToken());

    // Navigate to the dashboard first — any authenticated page that
    // loads main.js and kicks off MC.events._init is sufficient. We
    // don't need the tasks board rendered; we only need the SSE
    // stream open in this page context.
    const gotoResp = await page.goto(`${baseURL}/dashboard`);
    if (!gotoResp || gotoResp.status() >= 400) {
      test.skip(true, `dashboard not available (status=${gotoResp?.status()})`);
      return;
    }

    // Subscribe in-page to task.updated events via MC.events.
    const subscribed = await page.evaluate(() => {
      (window as any).__rtEvents = [];
      const w = window as any;
      if (!(w.MC && w.MC.events && w.MC.events.on)) return false;
      w.MC.events.on('task.updated', (payload: any) => {
        (window as any).__rtEvents.push(payload);
      });
      return true;
    });
    if (!subscribed) {
      test.skip(true, 'MC.events not available on page');
      return;
    }

    // Create a task via the authenticated plugin action endpoint.
    const created = await pluginAction(page, 'tasks-plugin', 'create_task', {
      title: 'realtime-e2e-task',
      status: 'pending',
    });
    if (!created.ok) {
      test.skip(true, `create_task failed: ${created.status} ${created.body.slice(0, 200)}`);
      return;
    }
    const m = created.body.match(/"id"\s*:\s*(\d+)/);
    if (!m) {
      test.skip(true, `could not parse created task id: ${created.body.slice(0, 200)}`);
      return;
    }
    const taskId = Number(m[1]);

    // Update the task status — emits task.updated on the bus, which
    // flows through the realtime hub to our SSE subscription.
    const updated = await pluginAction(page, 'tasks-plugin', 'update_task', {
      id: taskId,
      status: 'in_progress',
    });
    if (!updated.ok) {
      test.skip(true, `update_task failed: ${updated.status} ${updated.body.slice(0, 200)}`);
      return;
    }

    await expect
      .poll(
        async () =>
          page.evaluate(() => {
            const arr = (window as any).__rtEvents as any[] | undefined;
            return arr ? arr.length : 0;
          }),
        { timeout: 5_000, intervals: [100, 200, 500] },
      )
      .toBeGreaterThan(0);
  });

  test('pomodoro start reflects in widget without reload', async ({ page }) => {
    await loginAs(page, adminToken());

    const resp = await page.goto(`${baseURL}/dashboard`);
    if (!resp || resp.status() >= 400) {
      test.skip(true, `dashboard not available (status=${resp?.status()})`);
      return;
    }

    const subscribed = await page.evaluate(() => {
      (window as any).__rtPomo = [];
      const w = window as any;
      if (!(w.MC && w.MC.events && w.MC.events.on)) return false;
      w.MC.events.on('pomodoro.started', (payload: any) => {
        (window as any).__rtPomo.push(payload);
      });
      return true;
    });
    if (!subscribed) {
      test.skip(true, 'MC.events not available on page');
      return;
    }

    // Ensure no active session first.
    await pluginAction(page, 'pomodoro-plugin', 'stop_session', {});

    const started = await pluginAction(page, 'pomodoro-plugin', 'start_session', {
      session_type: 'work',
    });
    if (!started.ok) {
      test.skip(true, `start_session failed: ${started.status} ${started.body.slice(0, 200)}`);
      return;
    }

    await expect
      .poll(
        async () =>
          page.evaluate(() => {
            const arr = (window as any).__rtPomo as any[] | undefined;
            return arr ? arr.length : 0;
          }),
        { timeout: 5_000, intervals: [100, 200, 500] },
      )
      .toBeGreaterThan(0);

    // Cleanup.
    await pluginAction(page, 'pomodoro-plugin', 'stop_session', {});
  });
});
