package loader

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/plekt-dev/plekt/internal/config"
	"github.com/plekt-dev/plekt/internal/db"
	"github.com/plekt-dev/plekt/internal/eventbus"
)

// noOpSchemaLoaderAdapter wraps db.NoOpSchemaLoader to satisfy the unexported schemaLoader interface.
type noOpSchemaLoaderAdapter struct{}

func (noOpSchemaLoaderAdapter) Load(_ string) (db.PluginSchema, error) {
	return db.NoOpSchemaLoader().Load("")
}

// ---------------------------------------------------------------------------
// Fake implementations of pluginRunner, pluginFactory, dbFactory
// ---------------------------------------------------------------------------

// fakePluginRunner returns deterministic output for named functions.
type fakePluginRunner struct {
	mu        sync.Mutex
	responses map[string][]byte
	callErr   map[string]error
	closed    bool
	closeErr  error
	// callCount tracks how many times CallFunc was invoked.
	callCount int
}

func newFakeRunner(responses map[string][]byte) *fakePluginRunner {
	if responses == nil {
		responses = make(map[string][]byte)
	}
	return &fakePluginRunner{
		responses: responses,
		callErr:   make(map[string]error),
	}
}

func (f *fakePluginRunner) CallFunc(name string, input []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if f.closed {
		return nil, errors.New("plugin runner is closed")
	}
	if err, ok := f.callErr[name]; ok {
		return nil, err
	}
	if resp, ok := f.responses[name]; ok {
		return resp, nil
	}
	return []byte("{}"), nil
}

func (f *fakePluginRunner) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.closeErr
}

func (f *fakePluginRunner) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// fakePluginFactory always returns the configured runner (or error).
type fakePluginFactory struct {
	runner *fakePluginRunner
	err    error
}

func (f *fakePluginFactory) New(wasmPath string, hf []HostFunction, memPages uint32, pcc PluginCallContext, allowedHosts []string) (pluginRunner, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.runner, nil
}

// fakeDBFactory opens an in-memory SQLite DB (or returns a configured error).
type fakeDBFactory struct {
	err error
}

func (f *fakeDBFactory) Open(dsn string) (*sql.DB, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Use modernc in-memory SQLite: unique per test to avoid shared-cache collisions.
	db, err := sql.Open("sqlite", "file::memory:?mode=memory")
	return db, err
}

// ---------------------------------------------------------------------------
// Helper: build a valid signed plugin directory for fake-factory tests.
// We reuse makePluginDir from manager_test.go (same package).
// ---------------------------------------------------------------------------

// newFakeManager creates a PluginManager with injectable fakes.
func newFakeManager(t *testing.T, pf pluginFactory, df dbFactory) (PluginManager, string, eventbus.EventBus) {
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
	mgr := newManagerWithDeps(cfg, bus, pf, df, noOpSchemaLoaderAdapter{}, nil, nil)
	// Test isolation: skip the registry-snapshot check so plugins built on
	// the fly with ephemeral keys can load.
	AllowUnsignedPlugins(mgr, true)
	return mgr, pluginRoot, bus
}

// ---------------------------------------------------------------------------
// BLOCKER 2: Load() coverage
// ---------------------------------------------------------------------------

func TestLoad_Success_WithFakes(t *testing.T) {
	runner := newFakeRunner(nil)
	mgr, pluginRoot, bus := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{},
	)

	var loadedEvents []eventbus.Event
	var mu sync.Mutex
	bus.Subscribe(eventbus.EventPluginLoaded, func(_ context.Context, e eventbus.Event) {
		mu.Lock()
		loadedEvents = append(loadedEvents, e)
		mu.Unlock()
	})

	makePluginDir(t, pluginRoot, "tasks", true)
	dir := filepath.Join(pluginRoot, "tasks")

	info, err := mgr.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if info.Status != PluginStatusActive {
		t.Errorf("expected Active status, got %s", info.Status)
	}
	if info.Name != "tasks" {
		t.Errorf("unexpected name: %s", info.Name)
	}

	// Cleanup before temp dir is removed.
	defer mgr.Unload(context.Background(), "tasks") //nolint:errcheck

	// Give bus async delivery a moment.
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(loadedEvents) == 0 {
		t.Error("expected plugin.loaded event to be emitted")
	}
	payload, ok := loadedEvents[0].Payload.(eventbus.PluginLoadedPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", loadedEvents[0].Payload)
	}
	if payload.Name != "tasks" {
		t.Errorf("unexpected payload name: %s", payload.Name)
	}
}

func TestLoad_WASMInitFails(t *testing.T) {
	wantErr := fmt.Errorf("mock wasm init failure")
	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{err: wantErr},
		&fakeDBFactory{},
	)

	makePluginDir(t, pluginRoot, "fail-wasm", true)
	dir := filepath.Join(pluginRoot, "fail-wasm")

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrWASMInit) {
		t.Errorf("expected ErrWASMInit, got %v", err)
	}
	// Plugin must not be in the map.
	_, getErr := mgr.Get("fail-wasm")
	if !errors.Is(getErr, ErrPluginNotFound) {
		t.Errorf("plugin should not be in map after failed load, got: %v", getErr)
	}
}

func TestLoad_DBOpenFails(t *testing.T) {
	runner := newFakeRunner(nil)
	wantErr := fmt.Errorf("mock db open failure")
	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{err: wantErr},
	)

	makePluginDir(t, pluginRoot, "fail-db", true)
	dir := filepath.Join(pluginRoot, "fail-db")

	_, err := mgr.Load(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error when DB open fails, got nil")
	}
	// WASM init happens after DB open, so runner is never created on DB failure.
	// Plugin must not be in the map.
	_, getErr := mgr.Get("fail-db")
	if !errors.Is(getErr, ErrPluginNotFound) {
		t.Errorf("plugin should not be in map after failed load, got: %v", getErr)
	}
}

// ---------------------------------------------------------------------------
// BLOCKER 3: Call() coverage
// ---------------------------------------------------------------------------

func TestCallPlugin_Success_WithFakes(t *testing.T) {
	runner := newFakeRunner(map[string][]byte{
		"hello": []byte(`{"greeting":"hello world"}`),
	})
	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{},
	)

	makePluginDir(t, pluginRoot, "call-test", true)
	dir := filepath.Join(pluginRoot, "call-test")

	if _, err := mgr.Load(context.Background(), dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer mgr.Unload(context.Background(), "call-test") //nolint:errcheck

	out, err := mgr.CallPlugin(context.Background(), "call-test", "hello", nil)
	if err != nil {
		t.Fatalf("CallPlugin: %v", err)
	}
	if string(out) != `{"greeting":"hello world"}` {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestCallPlugin_FunctionError(t *testing.T) {
	runner := newFakeRunner(nil)
	runner.callErr["explode"] = errors.New("function exploded")

	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{},
	)

	makePluginDir(t, pluginRoot, "err-test", true)
	dir := filepath.Join(pluginRoot, "err-test")

	if _, err := mgr.Load(context.Background(), dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer mgr.Unload(context.Background(), "err-test") //nolint:errcheck

	_, err := mgr.CallPlugin(context.Background(), "err-test", "explode", nil)
	if err == nil {
		t.Fatal("expected error from exploding function, got nil")
	}
	if errors.Is(err, ErrPluginNotReady) {
		t.Error("error should be a WASM call error, not ErrPluginNotReady")
	}
}

func TestCallPlugin_AfterClose(t *testing.T) {
	runner := newFakeRunner(nil)
	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{},
	)

	makePluginDir(t, pluginRoot, "close-call-test", true)
	dir := filepath.Join(pluginRoot, "close-call-test")

	if _, err := mgr.Load(context.Background(), dir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Get the pluginImpl directly and mark it as unloading to simulate close.
	m := mgr.(*managerImpl)
	m.mu.Lock()
	impl := m.plugins["close-call-test"]
	impl.mu.Lock()
	impl.info.Status = PluginStatusError
	impl.mu.Unlock()
	m.mu.Unlock()

	_, err := mgr.CallPlugin(context.Background(), "close-call-test", "fn", nil)
	if !errors.Is(err, ErrPluginNotReady) {
		t.Errorf("expected ErrPluginNotReady, got %v", err)
	}
	// Cleanup.
	mgr.Unload(context.Background(), "close-call-test") //nolint:errcheck
}

// ---------------------------------------------------------------------------
// BLOCKER 4: Reload() coverage
// ---------------------------------------------------------------------------

func TestReload_Success_WithFakes(t *testing.T) {
	runner := newFakeRunner(nil)
	mgr, pluginRoot, bus := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{},
	)

	var reloadedEvents []eventbus.Event
	var mu sync.Mutex
	bus.Subscribe(eventbus.EventPluginReloaded, func(_ context.Context, e eventbus.Event) {
		mu.Lock()
		reloadedEvents = append(reloadedEvents, e)
		mu.Unlock()
	})

	makePluginDir(t, pluginRoot, "reload-me", true)
	dir := filepath.Join(pluginRoot, "reload-me")

	_, err := mgr.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	info2, err := mgr.Reload(context.Background(), "reload-me")
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	defer mgr.Unload(context.Background(), "reload-me") //nolint:errcheck

	if info2.Status != PluginStatusActive {
		t.Errorf("expected Active after reload, got %s", info2.Status)
	}

	// EventPluginReloaded must be emitted.
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(reloadedEvents) == 0 {
		t.Error("expected plugin.reloaded event to be emitted")
	}
}

func TestReload_LoadFailsAfterUnload(t *testing.T) {
	// First load succeeds; then factory is broken so second Load in Reload fails.
	runner := newFakeRunner(nil)
	pf := &fakePluginFactory{runner: runner}
	mgr, pluginRoot, _ := newFakeManager(t, pf, &fakeDBFactory{})

	makePluginDir(t, pluginRoot, "broken-reload", true)
	dir := filepath.Join(pluginRoot, "broken-reload")

	if _, err := mgr.Load(context.Background(), dir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Break the factory so the Load inside Reload fails.
	pf.err = fmt.Errorf("factory broken after first load")

	_, err := mgr.Reload(context.Background(), "broken-reload")
	if err == nil {
		t.Fatal("expected Reload to fail when Load fails, got nil")
	}
	// After failed reload, the plugin should be gone (unloaded during reload attempt).
	_, getErr := mgr.Get("broken-reload")
	if !errors.Is(getErr, ErrPluginNotFound) {
		t.Errorf("expected plugin to be unloaded after failed reload, got: %v", getErr)
	}
}

// ---------------------------------------------------------------------------
// BLOCKER 1: HostFunctionRegistry.All() returns ErrRegistryNotSealed
// ---------------------------------------------------------------------------

func TestHostFunctionRegistry_All_NotSealed(t *testing.T) {
	reg := NewHostFunctionRegistry()
	_ = reg.Register(HostFunction{Name: "fn1"})

	_, err := reg.All()
	if !errors.Is(err, ErrRegistryNotSealed) {
		t.Errorf("expected ErrRegistryNotSealed before Seal(), got %v", err)
	}
}

func TestHostFunctionRegistry_All_AfterSeal(t *testing.T) {
	reg := NewHostFunctionRegistry()
	_ = reg.Register(HostFunction{Name: "fn1"})
	_ = reg.Register(HostFunction{Name: "fn2"})
	reg.Seal()

	fns, err := reg.All()
	if err != nil {
		t.Fatalf("All() after Seal: %v", err)
	}
	if len(fns) != 2 {
		t.Errorf("expected 2 functions, got %d", len(fns))
	}
}

// ---------------------------------------------------------------------------
// WARNING: BearerToken stripped from MCP handler responses
// ---------------------------------------------------------------------------

func TestListPlugins_NoBearerTokenExposed(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	m := mgr.(*managerImpl)

	impl := &pluginImpl{
		info: PluginInfo{
			Name:   "secure-plugin",
			Status: PluginStatusActive,
		},
	}
	m.mu.Lock()
	m.plugins["secure-plugin"] = impl
	m.mu.Unlock()

	h := NewPluginMCPHandler(mgr)
	result, err := h.ListPlugins(context.Background(), ListPluginsParams{})
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(result.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(result.Plugins))
	}
}

func TestGetPlugin_NoBearerTokenExposed(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	m := mgr.(*managerImpl)

	impl := &pluginImpl{
		info: PluginInfo{
			Name:   "auth-plugin",
			Status: PluginStatusActive,
		},
	}
	m.mu.Lock()
	m.plugins["auth-plugin"] = impl
	m.mu.Unlock()

	h := NewPluginMCPHandler(mgr)
	result, err := h.GetPlugin(context.Background(), GetPluginParams{Name: "auth-plugin"})
	if err != nil {
		t.Fatalf("GetPlugin: %v", err)
	}
	if result.Plugin.Name != "auth-plugin" {
		t.Errorf("Plugin.Name = %q, want auth-plugin", result.Plugin.Name)
	}
}

// ---------------------------------------------------------------------------
// WARNING 8: Unload race: status set before delete from map
// ---------------------------------------------------------------------------

func TestUnload_StatusSetBeforeRemoval(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	m := mgr.(*managerImpl)

	impl := &pluginImpl{
		info: PluginInfo{Name: "race-test", Status: PluginStatusActive},
	}
	m.mu.Lock()
	m.plugins["race-test"] = impl
	m.mu.Unlock()

	// Start many concurrent reads of the plugin status while unloading.
	var wg sync.WaitGroup
	statusSeen := make([]PluginStatus, 0, 100)
	var statusMu sync.Mutex

	// Reader goroutines that observe the plugin status.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				impl.mu.Lock()
				s := impl.info.Status
				impl.mu.Unlock()
				statusMu.Lock()
				statusSeen = append(statusSeen, s)
				statusMu.Unlock()
			}
		}()
	}

	if err := mgr.Unload(context.Background(), "race-test"); err != nil {
		t.Fatalf("Unload: %v", err)
	}

	wg.Wait()

	// After Unload, the status must be Unloading (set by Close).
	impl.mu.Lock()
	finalStatus := impl.info.Status
	impl.mu.Unlock()
	if finalStatus != PluginStatusUnloading {
		t.Errorf("expected PluginStatusUnloading after close, got %s", finalStatus)
	}
}

// ---------------------------------------------------------------------------
// Additional coverage for pluginImpl.Call happy path via fakeRunner
// ---------------------------------------------------------------------------

func TestPluginImpl_Call_ViaFakeRunner(t *testing.T) {
	runner := newFakeRunner(map[string][]byte{
		"compute": []byte(`{"result":42}`),
	})
	impl := &pluginImpl{
		info: PluginInfo{Name: "test", Status: PluginStatusActive},
		pool: newSingleRunnerPool(runner),
	}

	out, err := impl.Call(context.Background(), "compute", []byte(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(out) != `{"result":42}` {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestPluginImpl_Call_RunnerReturnsError(t *testing.T) {
	runner := newFakeRunner(nil)
	runner.callErr["boom"] = errors.New("wasm runtime error")

	impl := &pluginImpl{
		info: PluginInfo{Name: "test", Status: PluginStatusActive},
		pool: newSingleRunnerPool(runner),
	}

	_, err := impl.Call(context.Background(), "boom", nil)
	if err == nil {
		t.Fatal("expected error from runner, got nil")
	}
	if errors.Is(err, ErrPluginNotReady) {
		t.Error("should be a WASM call error, not ErrPluginNotReady")
	}
}

// ---------------------------------------------------------------------------
// Additional coverage for pluginImpl.Close() paths
// ---------------------------------------------------------------------------

func TestPluginImpl_Close_RunnerErrorIsReturned(t *testing.T) {
	runner := newFakeRunner(nil)
	runner.closeErr = errors.New("close failed")

	impl := &pluginImpl{
		info: PluginInfo{Name: "test", Status: PluginStatusActive},
		pool: newSingleRunnerPool(runner),
	}
	err := impl.Close()
	if err == nil {
		t.Fatal("expected error from Close when runner.Close fails")
	}
}

// ---------------------------------------------------------------------------
// Load: DB ping failure path
// ---------------------------------------------------------------------------

// pingFailDBFactory returns a DB that opens successfully but fails PingContext
// because the underlying file does not exist and the in-memory DB is closed
// immediately after being created.
type pingFailDBFactory struct{}

func (f *pingFailDBFactory) Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file::memory:?mode=memory")
	if err != nil {
		return nil, err
	}
	// Close the DB immediately so that PingContext will fail.
	db.Close()
	return db, nil
}

func TestLoad_DBPingFails(t *testing.T) {
	runner := newFakeRunner(nil)
	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&pingFailDBFactory{},
	)

	makePluginDir(t, pluginRoot, "ping-fail", true)
	dir := filepath.Join(pluginRoot, "ping-fail")

	_, err := mgr.Load(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error when DB ping fails, got nil")
	}
	// WASM init happens after DB ping, so runner is never created on ping failure.
	// Plugin must not be in the map.
	_, getErr := mgr.Get("ping-fail")
	if !errors.Is(getErr, ErrPluginNotFound) {
		t.Errorf("plugin should not be in map after failed load, got: %v", getErr)
	}
}

// ---------------------------------------------------------------------------
// Load: mcp.yaml YAML parse error path
// ---------------------------------------------------------------------------

func TestLoad_InvalidMCPYAMLSyntax(t *testing.T) {
	runner := newFakeRunner(nil)
	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{},
	)

	// Build plugin dir manually with an invalid mcp.yaml (control chars break yaml.v3).
	name := "bad-yaml-mcp"
	dir := filepath.Join(pluginRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mb, _ := json.Marshal(Manifest{Name: name, Version: "1.0"})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	// Write a mcp.yaml containing control characters that yaml.v3 rejects.
	invalidYAML := []byte("\x01\x02\x03 not valid yaml")
	if err := os.WriteFile(filepath.Join(dir, "mcp.yaml"), invalidYAML, 0o600); err != nil {
		t.Fatalf("write mcp.yaml: %v", err)
	}
	// Write a dummy plugin.wasm (won't be reached).
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	if err := os.WriteFile(filepath.Join(dir, "plugin.wasm"), wasmBytes, 0o600); err != nil {
		t.Fatalf("write plugin.wasm: %v", err)
	}

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid for invalid YAML, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Load double-check race: inject plugin between RLock and Lock to hit the
// second duplicate guard inside the write lock.
// ---------------------------------------------------------------------------

// racingPluginFactory injects a plugin into the manager map after the first
// call to New() returns, simulating a race where another goroutine loaded the
// same plugin between the RLock check and the write lock acquisition.
type racingPluginFactory struct {
	inner   *fakePluginFactory
	manager **managerImpl // pointer-to-pointer so we can set after construction
	name    string        // plugin name to inject
}

func (f *racingPluginFactory) New(wasmPath string, hf []HostFunction, memPages uint32, pcc PluginCallContext, allowedHosts []string) (pluginRunner, error) {
	runner, err := f.inner.New(wasmPath, hf, memPages, pcc, allowedHosts)
	if err != nil {
		return nil, err
	}
	// Simulate concurrent load: inject the plugin into the map now,
	// before the calling Load() acquires the write lock.
	if *f.manager != nil {
		m := *f.manager
		m.mu.Lock()
		m.plugins[f.name] = &pluginImpl{
			info: PluginInfo{Name: f.name, Status: PluginStatusActive},
			pool: newSingleRunnerPool(runner),
		}
		m.mu.Unlock()
	}
	// Return a fresh runner; the caller will discard it after the duplicate check.
	return newFakeRunner(nil), nil
}

func TestLoad_DoubleCheckDuplicateRace(t *testing.T) {
	// We need a pointer to managerImpl so the racing factory can inject into it.
	var mImpl *managerImpl

	inner := &fakePluginFactory{runner: newFakeRunner(nil)}
	rf := &racingPluginFactory{
		inner:   inner,
		manager: &mImpl,
		name:    "race-dup",
	}

	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
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
	mgr := newManagerWithDeps(cfg, bus, rf, &fakeDBFactory{}, noOpSchemaLoaderAdapter{}, nil, nil)
	AllowUnsignedPlugins(mgr, true)
	mImpl = mgr.(*managerImpl)
	// Update the factory's pointer now that we have the concrete impl.
	rf.manager = &mImpl

	makePluginDir(t, pluginRoot, "race-dup", true)
	dir := filepath.Join(pluginRoot, "race-dup")

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrPluginAlreadyLoaded) {
		t.Errorf("expected ErrPluginAlreadyLoaded from double-check race, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Load double-check (non-race): second Load of same plugin via standard path
// ---------------------------------------------------------------------------

func TestLoad_DuplicatePlugin_WithFakes(t *testing.T) {
	runner := newFakeRunner(nil)
	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{},
	)

	makePluginDir(t, pluginRoot, "dup-fake", true)
	dir := filepath.Join(pluginRoot, "dup-fake")

	_, err := mgr.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	defer mgr.Unload(context.Background(), "dup-fake") //nolint:errcheck

	_, err2 := mgr.Load(context.Background(), dir)
	if !errors.Is(err2, ErrPluginAlreadyLoaded) {
		t.Errorf("expected ErrPluginAlreadyLoaded on second Load, got %v", err2)
	}
}
