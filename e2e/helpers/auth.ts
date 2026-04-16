import { Page } from '@playwright/test';
import { baseURL, adminUsername, adminPassword } from './server';

// Gets CSRF token from the login page (reads hidden input value)
export async function getCSRFToken(page: Page): Promise<string> {
  await page.goto(`${baseURL}/login`);
  return await page.inputValue('input[name="csrf_token"]');
}

// Logs in using username and password credentials.
// If only password is provided (legacy call), uses adminUsername() for username.
export async function loginAs(page: Page, usernameOrPassword: string, password?: string): Promise<void> {
  await page.goto(`${baseURL}/login`);
  if (password !== undefined) {
    // Called as loginAs(page, username, password)
    await page.fill('input[name="username"]', usernameOrPassword);
    await page.fill('input[name="password"]', password);
  } else {
    // Legacy: called as loginAs(page, adminToken()) — treat as password with default admin username
    await page.fill('input[name="username"]', adminUsername());
    await page.fill('input[name="password"]', adminPassword());
  }
  await page.click('button[type="submit"]');
  // Wait for navigation away from login. Cap explicitly so failed logins do
  // not eat the full hook timeout — surface a diagnostic instead.
  try {
    await page.waitForURL(url => !url.pathname.includes('/login'), { timeout: 10_000 });
  } catch (e) {
    const body = (await page.content()).slice(0, 500);
    throw new Error(`loginAs failed: still on /login after submit. URL=${page.url()}. Body head: ${body}`);
  }
}

// Logs out by clicking the logout button in the sidebar footer
export async function logout(page: Page): Promise<void> {
  await page.locator('form[action="/logout"] button[type="submit"]').click();
  await page.waitForURL(/\/login/);
}

/**
 * Creates a fresh agent via the admin web UI and returns its bearer token.
 *
 * Used by plugin e2e specs that need to call MCP tools through the
 * federated /mcp endpoint, which is protected by AgentAuthMiddleware. New
 * agents created via agents.AgentService.Create receive wildcard
 * `*` / `*` permissions automatically, so the returned token can call any
 * tool on any plugin.
 *
 * Implementation: log in as admin, scrape the CSRF token from the agent
 * list page (it carries the same form), POST `name=...` to /admin/agents,
 * and parse the `new_token` query parameter from the resulting redirect.
 *
 * The caller is responsible for cleanup (DELETE /admin/agents/{id}/delete)
 * if it cares about test isolation across runs. For self-contained specs
 * that recreate state in each beforeEach, leakage is harmless.
 */
export async function createAgentToken(page: Page, agentName: string): Promise<string> {
  console.log('[createAgentToken] step 1: loginAs');
  await loginAs(page, adminUsername(), adminPassword());
  const afterLogin = page.url();
  console.log(`[createAgentToken] step 2: post-login URL = ${afterLogin}`);

  console.log('[createAgentToken] step 3: GET /admin/agents');
  const navResp = await page.goto(`${baseURL}/admin/agents`);
  console.log(`[createAgentToken] step 4: nav status = ${navResp?.status()}`);
  if (!navResp || navResp.status() !== 200) {
    const body = (await page.content()).slice(0, 400);
    throw new Error(`createAgentToken: GET /admin/agents returned ${navResp?.status()} (after login URL=${afterLogin}). Body head: ${body}`);
  }
  console.log('[createAgentToken] step 5: read CSRF token from form');
  const csrfToken = await page.inputValue('form[action="/admin/agents"] input[name="csrf_token"]', { timeout: 5_000 }).catch(async err => {
    const body = (await page.content()).slice(0, 800);
    throw new Error(`createAgentToken: CSRF input not found: ${err.message}. Body head: ${body}`);
  });
  console.log(`[createAgentToken] step 6: csrf=${csrfToken ? '<' + csrfToken.length + ' chars>' : 'EMPTY'}`);
  if (!csrfToken) {
    throw new Error('createAgentToken: could not find CSRF token on /admin/agents page');
  }

  // Use the page session cookies for the POST so the auth + CSRF middlewares
  // accept the request. Disable redirect-following so we can read the
  // Location header that carries `new_token=...`.
  console.log('[createAgentToken] step 7: POST /admin/agents');
  const postResp = await page.request.post(`${baseURL}/admin/agents`, {
    form: {
      csrf_token: csrfToken,
      name: agentName,
    },
    maxRedirects: 0,
    failOnStatusCode: false,
    timeout: 10_000,
  });
  console.log(`[createAgentToken] step 8: post status = ${postResp.status()}`);

  if (postResp.status() !== 303 && postResp.status() !== 302) {
    throw new Error(`createAgentToken: expected redirect, got ${postResp.status()}: ${await postResp.text()}`);
  }

  const location = postResp.headers()['location'] || '';
  console.log(`[createAgentToken] step 9: location = ${location}`);
  const url = new URL(location, baseURL);
  const token = url.searchParams.get('new_token');
  if (!token) {
    throw new Error(`createAgentToken: redirect did not contain new_token (${location})`);
  }
  console.log(`[createAgentToken] step 10: token bootstrap done`);
  return token;
}
