# Plekt

[![CI](https://github.com/plekt-dev/plekt/actions/workflows/ci.yml/badge.svg)](https://github.com/plekt-dev/plekt/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/plekt-dev/plekt/graph/badge.svg)](https://codecov.io/gh/plekt-dev/plekt)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Go](https://img.shields.io/badge/go-1.25-00ADD8.svg)](https://go.dev/)

Self-hosted workspace where you and your AI work together. 

WordPress-style plugin platform with one MCP endpoint on top.

## Why?

Most MCP setups give the AI tools but no home for the data those tools touch, and no UI for the human. 
Plekt is both.

Each plugin is a mini-app with its own pages, its own SQLite file, and its own tools. The tools all merge into one federated `/mcp` endpoint. 
You control per-agent who can call what. Plugins live in the same process so they can share events, build on each other, and the human gets a web UI over all of them.


Every plugin runs inside a WASM sandbox (Extism + Wazero), so a broken or malicious one can't touch the host beyond what it was granted.

Project vision: a small team works together with their AI on real projects, state in one place, access shared per-agent instead of copy-pasted between machines. Sharing/RBAC still maturing.

## Quick Start

Pick whichever install fits your setup.

### Native binary

Download from [Releases](https://github.com/plekt-dev/plekt/releases), unpack, run plekt-core.

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

| Layer | Tech |
|---|---|
| Core | Go 1.25, single static binary, no CGO |
| Plugins | Extism + Wazero (WASM sandbox), SQLite per plugin |
| Storage | modernc.org/sqlite (pure Go) |
| Web UI | templ + htmx, JetBrains Mono, embedded CSS/JS |
| MCP | Streamable HTTP (MCP spec 2025-03-26), JSON-RPC 2.0 |
| Auth | Per-agent Bearer tokens, Ed25519 plugin signatures |

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

Project is early. Issues, PRs, ideas all welcome. Open one on GitHub ❤️

## License

[AGPL-3.0-only](LICENSE) 

© 2026 Yaroslav Temper
