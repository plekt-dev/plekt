package loader

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/plekt-dev/plekt/internal/config"
	"github.com/plekt-dev/plekt/internal/db"
	"github.com/plekt-dev/plekt/internal/eventbus"
)

// ---------------------------------------------------------------------------
// Fake implementations of schemaLoader and migrationRunner for migration tests.
// ---------------------------------------------------------------------------

// fakeSchemaLoader returns a preconfigured PluginSchema or error.
type fakeSchemaLoader struct {
	schema db.PluginSchema
	err    error
}

func (f *fakeSchemaLoader) Load(_ string) (db.PluginSchema, error) {
	return f.schema, f.err
}

// fakeMigrationRunner records calls and returns a preconfigured error.
type fakeMigrationRunner struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeMigrationRunner) Migrate(_ context.Context, _ *sql.DB, _ db.PluginSchema) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.err
}

func (f *fakeMigrationRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// newMigrationTestManager creates a PluginManager with injectable schema loader
// and migration runner, reusing the fakePluginFactory and fakeDBFactory pattern.
func newMigrationTestManager(
	t *testing.T,
	sl schemaLoader,
	mr migrationRunner,
) (PluginManager, string, eventbus.EventBus) {
	t.Helper()
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir pluginRoot: %v", err)
	}
	bus := eventbus.NewInMemoryBus()
	cfg := config.Config{
		PluginDir: pluginRoot,
		DataDir:   dataDir,
		Loader: config.LoaderConfig{
			ReloadDrainTimeout:   100 * time.Millisecond,
			WASMMemoryLimitPages: 128,
		},
	}
	mgr := newManagerWithDeps(cfg, bus,
		&fakePluginFactory{runner: newFakeRunner(nil)},
		&fakeDBFactory{},
		sl,
		mr,
		nil,
	)
	// Migration tests don't load real signed plugins, skip strict registry check.
	AllowUnsignedPlugins(mgr, true)
	return mgr, pluginRoot, bus
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestLoad_WithSchema_Success(t *testing.T) {
	schema := db.PluginSchema{
		Version: "1",
		Tables: []db.TableSchema{
			{
				Name: "tasks",
				Columns: []db.ColumnSchema{
					{Name: "id", Type: "INTEGER", PrimaryKey: true},
					{Name: "title", Type: "TEXT"},
				},
				Indexes: []db.IndexSchema{
					{Name: "idx_tasks_title", Columns: []string{"title"}},
				},
			},
		},
	}

	sl := &fakeSchemaLoader{schema: schema}
	mr := &fakeMigrationRunner{}

	mgr, pluginRoot, bus := newMigrationTestManager(t, sl, mr)

	var migratedEvents []eventbus.Event
	var mu sync.Mutex
	bus.Subscribe(eventbus.EventPluginSchemaMigrated, func(_ context.Context, e eventbus.Event) {
		mu.Lock()
		migratedEvents = append(migratedEvents, e)
		mu.Unlock()
	})

	makePluginDir(t, pluginRoot, "schema-plugin", true)
	dir := filepath.Join(pluginRoot, "schema-plugin")

	info, err := mgr.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer mgr.Unload(context.Background(), info.Name) //nolint:errcheck

	if mr.callCount() != 1 {
		t.Errorf("expected migration runner called once, got %d", mr.callCount())
	}

	// Drain in-flight event goroutines before asserting.
	bus.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(migratedEvents) == 0 {
		t.Error("expected EventPluginSchemaMigrated to be emitted")
	}
	payload, ok := migratedEvents[0].Payload.(eventbus.PluginSchemaMigratedPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", migratedEvents[0].Payload)
	}
	if payload.TablesApplied != 1 {
		t.Errorf("expected TablesApplied=1, got %d", payload.TablesApplied)
	}
	if payload.IndexesApplied != 1 {
		t.Errorf("expected IndexesApplied=1, got %d", payload.IndexesApplied)
	}
}

func TestLoad_SchemaFileNotFound(t *testing.T) {
	// NoOpSchemaLoader returns ErrSchemaFileNotFound: migration must be skipped,
	// plugin still loads successfully.
	sl := noOpSchemaLoaderAdapter{}
	mr := &fakeMigrationRunner{}

	mgr, pluginRoot, _ := newMigrationTestManager(t, sl, mr)
	makePluginDir(t, pluginRoot, "no-schema-plugin", true)
	dir := filepath.Join(pluginRoot, "no-schema-plugin")

	info, err := mgr.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("expected Load to succeed when schema file not found, got: %v", err)
	}
	defer mgr.Unload(context.Background(), info.Name) //nolint:errcheck

	// Migration runner must NOT have been called.
	if mr.callCount() != 0 {
		t.Errorf("expected migration runner not called, got %d calls", mr.callCount())
	}
}

func TestLoad_SchemaParseError(t *testing.T) {
	// Schema loader returns a non-ErrSchemaFileNotFound error: Load must return ErrMigration.
	sl := &fakeSchemaLoader{err: db.ErrSchemaInvalid}
	mr := &fakeMigrationRunner{}

	mgr, pluginRoot, _ := newMigrationTestManager(t, sl, mr)
	makePluginDir(t, pluginRoot, "bad-schema-plugin", true)
	dir := filepath.Join(pluginRoot, "bad-schema-plugin")

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrMigration) {
		t.Errorf("expected ErrMigration when schema parse fails, got %v", err)
	}
}

func TestLoad_MigrationFails(t *testing.T) {
	schema := db.PluginSchema{
		Version: "1",
		Tables: []db.TableSchema{
			{
				Name: "t",
				Columns: []db.ColumnSchema{
					{Name: "id", Type: "INTEGER", PrimaryKey: true},
				},
			},
		},
	}
	sl := &fakeSchemaLoader{schema: schema}
	mr := &fakeMigrationRunner{err: db.ErrMigrationFailed}

	mgr, pluginRoot, bus := newMigrationTestManager(t, sl, mr)

	var failedEvents []eventbus.Event
	var mu sync.Mutex
	bus.Subscribe(eventbus.EventPluginMigrationFailed, func(_ context.Context, e eventbus.Event) {
		mu.Lock()
		failedEvents = append(failedEvents, e)
		mu.Unlock()
	})

	makePluginDir(t, pluginRoot, "fail-migration-plugin", true)
	dir := filepath.Join(pluginRoot, "fail-migration-plugin")

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrMigration) {
		t.Errorf("expected ErrMigration when migration fails, got %v", err)
	}

	// Plugin must not be registered.
	_, getErr := mgr.Get("fail-migration-plugin")
	if !errors.Is(getErr, ErrPluginNotFound) {
		t.Errorf("plugin should not be registered after migration failure, got: %v", getErr)
	}

	// Drain in-flight event goroutines before asserting.
	bus.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(failedEvents) == 0 {
		t.Error("expected EventPluginMigrationFailed to be emitted")
	}
	payload, ok := failedEvents[0].Payload.(eventbus.PluginMigrationFailedPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", failedEvents[0].Payload)
	}
	if payload.PluginName != "fail-migration-plugin" {
		t.Errorf("expected plugin name fail-migration-plugin, got %q", payload.PluginName)
	}
	// Error field must be the sanitized sentinel string, not a raw SQL/driver error.
	const wantErrMsg = "migration failed"
	if payload.Error != wantErrMsg {
		t.Errorf("payload.Error = %q, want %q", payload.Error, wantErrMsg)
	}
	if strings.Contains(payload.Error, "sql:") || strings.Contains(payload.Error, "sqlite") {
		t.Errorf("payload.Error leaks raw driver error: %q", payload.Error)
	}
}
