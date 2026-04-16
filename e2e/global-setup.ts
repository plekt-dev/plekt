import fs from 'fs';
import path from 'path';

export default async function globalSetup(): Promise<() => void> {
  const projectRoot = process.cwd();
  const dataDir = path.join(projectRoot, 'e2e', 'test-data');
  const pluginDir = path.join(projectRoot, 'e2e', 'test-plugins');

  // Create runtime directories (gitignored, not committed)
  fs.mkdirSync(dataDir, { recursive: true });
  fs.mkdirSync(pluginDir, { recursive: true });

  // Copy built plugin binaries into test-plugins so they are within the allowed plugin_dir.
  // Files are copied (not symlinked) so the server can read them without path-traversal issues.
  const pluginsToCopy = ['notes-plugin', 'pomodoro-plugin', 'projects-plugin', 'tasks-plugin', 'scheduler-plugin'];
  for (const plugin of pluginsToCopy) {
    const src = path.join(projectRoot, 'plugins', plugin);
    const dst = path.join(pluginDir, plugin);
    fs.rmSync(dst, { recursive: true, force: true });
    fs.mkdirSync(dst, { recursive: true });
    fs.cpSync(src, dst, { recursive: true });
  }

  process.env.E2E_ADMIN_TOKEN = 'e2e-test-secret-token';
  process.env.E2E_ADMIN_USERNAME = 'e2e-admin';
  process.env.E2E_ADMIN_PASSWORD = 'e2e-admin-password-bootstrap';

  return () => {
    try {
      fs.rmSync(dataDir, { recursive: true, force: true });
      fs.rmSync(pluginDir, { recursive: true, force: true });
    } catch {
      // ignore cleanup errors (e.g. file locks on Windows)
    }
  };
}
