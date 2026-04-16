// Package loader implements plugin lifecycle management for Plekt.
//
// Initialization order:
//  1. Load Config
//  2. Initialize EventBus
//  3. Initialize HostFunctionRegistry → Seal()
//  4. Scan plugin directory (path traversal protection)
//  5. For each plugin:
//     a. Read/parse manifest.json
//     b. Read/parse mcp.yaml
//     c. Verify Ed25519 signature on mcp.yaml → abort if invalid
//     d. Load plugin.wasm via Extism
//     e. Open per-plugin SQLite DB (modernc.org/sqlite)
//     f. Register host functions on Extism instance
//     g. Register plugin's MCP tools
//     h. Emit plugin.loaded on EventBus
//  6. Mark system ready
package loader
