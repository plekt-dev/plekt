import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: 'list',
  use: {
    baseURL: 'http://localhost:18080',
    ignoreHTTPSErrors: true,
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: {
    command: 'go run ./cmd/plekt-core ./e2e/test-config.yaml',
    url: 'http://localhost:18080/login',
    reuseExistingServer: false,
    timeout: 180_000,
    env: {
      // Disable login rate limiter for CI/E2E. Tests hammer /login
      // many times in parallel and would otherwise hit the 5/15min limit.
      PLEKT_LOGIN_RATE_LIMIT_DISABLED: '1',
    },
  },
});
