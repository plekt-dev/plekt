# 1.1.0 (17.04.2026)

## Features
- `/admin/agents` page shows an "MCP endpoint" card with the absolute server URL, Bearer auth header, copy-pasteable Claude Code / Claude Desktop config, and a curl sanity check.
- Sidebar nav reorganized: `Plugins` and `Agents` moved from the Workspace section into Administration, where every other `/admin/*` link already lives.

## Fixes
- Apply WAL + 5s `busy_timeout` to all system SQLite databases (`audit.db`, `settings.db`, `plugins.db`, `host_grants.db`, tokens, users). Previously concurrent writers hit `SQLITE_BUSY` and audit entries were silently dropped under load.
- Vendored frontend assets (htmx, mermaid) ship in the Docker image; the over-broad `vendor/` `.gitignore` rule was excluding them from the build context.

## Security
- `SetPermissions` now invalidates the agent token cache. Previously a narrowed agent kept its old wildcard permissions on `/mcp` for up to 30s after the admin restricted them.
- Pin exact npm devDependency versions (`@playwright/test`, `typescript`) instead of `^` ranges so a compromised upstream release cannot be pulled in automatically.

# 1.0.3 (17.04.2026)
## Fixes
- Bundle IANA tzdata into the binary so cron timezones (e.g. `Europe/Berlin`) work on Windows and minimal container images.

# 1.0.2 (17.04.2026)

## Fixes
- Default UI language is now always English

# 1.0.1 (16.04.2026)

## Fixes
- Release workflow now opens the post-release PR against `plekt-dev/registry`

# 1.0.0 (16.04.2026)

## Initial release
