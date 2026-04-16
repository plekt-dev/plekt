export const baseURL = 'http://localhost:18080';

export function adminToken(): string {
  return process.env.E2E_ADMIN_TOKEN ?? 'e2e-test-secret-token';
}

export function adminUsername(): string {
  return process.env.E2E_ADMIN_USERNAME ?? 'e2e-admin';
}

export function adminPassword(): string {
  return process.env.E2E_ADMIN_PASSWORD ?? 'e2e-admin-password-bootstrap';
}
