# Plekt

[![CI](https://github.com/plekt-dev/plekt/actions/workflows/ci.yml/badge.svg)](https://github.com/plekt-dev/plekt/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/plekt-dev/plekt/graph/badge.svg)](https://codecov.io/gh/plekt-dev/plekt)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Go](https://img.shields.io/badge/go-1.25-00ADD8.svg)](https://go.dev/)

Self-hosted workspace where you and your AI work together. One place, every plugin signed and pinned.

## Why?

Today MCP servers live scattered across the disk. Each one is a console connection, configured by hand, with no UI and no shared state. You glue them together yourself.

Plekt flips that. Add an MCP and you also get a full UI for it, like a WordPress plugin. Each plugin is a mini-app: its own pages, its own data, its own MCP endpoint. Everything lives in the same workspace, so plugins can share state and build on each other.

The vision: a small team works together with their AI on real projects. State stays in one place. Access is shared, not copy-pasted between machines. Permissions per agent, per tool. (Sharing/RBAC is still maturing.)

Think WordPress, but the operator is you and your AI together.

## Quick Start

Pick whichever install fits your setup.

### Native binary

Download from [Releases](https://github.com/plekt-dev/plekt/releases), unpack, run:

```bash
./plekt-core       # Linux / macOS
plekt-core.exe     # Windows
```

Open <http://localhost:8080>. Plekt prints a one-time setup token to stdout on first run; copy it from the terminal and paste it into the register form.

### Docker Compose

Put [`docker-compose.yml`](docker-compose.yml) and [`config.yaml`](config.yaml) in the same directory:

```bash
docker-compose up -d
```

State persists in `./data` (SQLite DBs) and `./plugins` (installed bundles) next to the compose file.

Grab the first-run setup token from the container logs:

```bash
docker logs plekt 2>&1 | grep -oE '[a-f0-9]{64}' | head -1
```

Open <http://localhost:8080>, paste the token, create the admin account.

Update:

```bash
docker-compose pull && docker-compose up -d
```

> Linux and macOS native binaries build, but aren't battle-tested yet. Only Windows is exercised regularly. Reports welcome.

## Connect an MCP client

Plekt exposes one HTTP MCP endpoint:

```
POST http://<host>:8080/mcp
Authorization: Bearer <agent token>
```

Create an agent in `/admin/agents`, copy its token, point any MCP-capable client at the URL above. Tools available to that token = the permissions you granted in the UI.

Each client wires this up its own way. We don't ship per-client integrations (at least not yet).

**Example for the Claude Code CLI:**

```bash
claude mcp add --transport http plekt http://localhost:8080/mcp --header "Authorization: Bearer <agent token>"
```

Restart the client to pick up the new server.

## Stack

Go 1.25, Extism (WASM plugins), templ, htmx, modernc.org/sqlite (CGO-free), Ed25519 plugin signing.

## Development

```bash
# templ CLI must match go.mod version exactly
TEMPL_VERSION="$(go list -m -f '{{.Version}}' github.com/a-h/templ)"
go install github.com/a-h/templ/cmd/templ@${TEMPL_VERSION}

# Generated *_templ.go files are gitignored: regen after every .templ change
templ generate ./internal/web/templates

go run ./cmd/plekt-core/
```

CI runs gofmt, vet, race tests, coverage on every push.

## Contributing

Project is early. Issues, PRs, ideas all welcome. Open one on GitHub.

## License

[AGPL-3.0-only](LICENSE) 

© 2026 Yaroslav Temper
