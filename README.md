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

**Recommended: native binary.** Download from [Releases](https://github.com/plekt-dev/plekt/releases), unpack, run `plekt-core.exe` (Windows) or `./plekt-core` (Linux/macOS). Open <http://localhost:8080>.

**Docker** also works:

```bash
docker pull ghcr.io/plekt-dev/plekt:latest
docker run -d --name plekt -p 8080:8080 \
  -v plekt_data:/app/data \
  -v plekt_plugins:/app/plugins \
  -v $PWD/config.yaml:/app/config.yaml:ro \
  ghcr.io/plekt-dev/plekt:latest
```

> Linux native binary builds, but isn't battle-tested yet. Reports welcome.

## Connect an MCP client

After creating an agent in `/admin/agents`, copy the registration command shown on the page. For Claude Code:

```bash
claude mcp add --transport http plekt http://localhost:8080/mcp \
  --header "Authorization: Bearer <agent token>"
```

Restart your client. Plekt's tools and the tools of every installed plugin become available, scoped to whatever permissions you gave that agent.

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

[AGPL-3.0-only](LICENSE) © 2026 Yaroslav Temper.
