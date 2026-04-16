package loader

import (
	"context"
	"database/sql"

	"github.com/plekt-dev/plekt/internal/db"
)

// pluginRunner wraps an Extism plugin instance for testability.
// The real implementation delegates to extism.Plugin; tests inject a fake.
type pluginRunner interface {
	// CallFunc invokes a named WASM export with input bytes and returns output bytes.
	CallFunc(name string, input []byte) ([]byte, error)
	// Close releases the WASM runtime resources.
	Close() error
}

// pluginFactory creates a pluginRunner from a .wasm file path and plugin context.
// The real implementation wraps extism.NewPlugin and wires host functions.
// Tests can inject a fake that returns deterministic output.
type pluginFactory interface {
	New(wasmPath string, hostFunctions []HostFunction, memoryLimitPages uint32, pcc PluginCallContext, allowedHosts []string) (pluginRunner, error)
}

// dbFactory opens a per-plugin SQLite database.
// The real implementation uses sql.Open("sqlite", path).
// Tests can inject a fake or use an in-memory SQLite.
type dbFactory interface {
	Open(dataSourceName string) (*sql.DB, error)
}

// schemaLoader loads and parses a plugin's schema.yaml.
type schemaLoader interface {
	Load(pluginDir string) (db.PluginSchema, error)
}

// migrationRunner applies a PluginSchema to a SQLite database.
type migrationRunner interface {
	Migrate(ctx context.Context, sqlDB *sql.DB, schema db.PluginSchema) error
}
