package loader

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/plekt-dev/plekt/internal/config"
	"github.com/plekt-dev/plekt/internal/eventbus"
)

// stubRegistryStore is a controllable PluginRegistryStore for bootstrap tests.
type stubRegistryStore struct {
	entries   []PluginRegistryEntry
	upsertErr error
	deleteErr error
	listErr   error
	deleted   []string
	upserted  []PluginRegistryEntry
}

func (s *stubRegistryStore) Upsert(_ context.Context, e PluginRegistryEntry) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upserted = append(s.upserted, e)
	return nil
}

func (s *stubRegistryStore) Delete(_ context.Context, name string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = append(s.deleted, name)
	return nil
}

func (s *stubRegistryStore) List(_ context.Context) ([]PluginRegistryEntry, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.entries, nil
}

func (s *stubRegistryStore) Get(_ context.Context, name string) (PluginRegistryEntry, error) {
	for _, e := range s.entries {
		if e.Name == name {
			return e, nil
		}
	}
	return PluginRegistryEntry{}, ErrRegistryEntryNotFound
}

func (s *stubRegistryStore) Close() error { return nil }

// newBootstrapManager creates a manager pointing at pluginRoot.
func newBootstrapManager(t *testing.T, pluginRoot, dataDir string) PluginManager {
	t.Helper()
	bus := eventbus.NewInMemoryBus()
	cfg := config.Config{
		PluginDir: pluginRoot,
		DataDir:   dataDir,
		Loader: config.LoaderConfig{
			ReloadDrainTimeout:   100,
			WASMMemoryLimitPages: 128,
		},
	}
	return NewManager(cfg, bus)
}

func TestRestoreFromRegistry_EmptyRegistry(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	dataDir := filepath.Join(root, "data")
	_ = os.MkdirAll(pluginRoot, 0o755)

	mgr := newBootstrapManager(t, pluginRoot, dataDir)
	store := &stubRegistryStore{}

	payload, err := RestoreFromRegistry(context.Background(), mgr, store)
	if err != nil {
		t.Fatalf("RestoreFromRegistry: %v", err)
	}
	if payload.Restored != 0 {
		t.Errorf("Restored: want 0, got %d", payload.Restored)
	}
	if payload.Failed != 0 {
		t.Errorf("Failed: want 0, got %d", payload.Failed)
	}
}

func TestRestoreFromRegistry_MissingDirIncrementsFailedAndDeletesEntry(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	dataDir := filepath.Join(root, "data")
	_ = os.MkdirAll(pluginRoot, 0o755)

	mgr := newBootstrapManager(t, pluginRoot, dataDir)

	now := time.Now().UTC()
	store := &stubRegistryStore{
		entries: []PluginRegistryEntry{
			{
				Name:      "missing-plugin",
				Dir:       filepath.Join(pluginRoot, "missing-plugin"),
				Version:   "1.0.0",
				LoadedAt:  now,
				UpdatedAt: now,
			},
		},
	}

	payload, err := RestoreFromRegistry(context.Background(), mgr, store)
	if err != nil {
		t.Fatalf("RestoreFromRegistry: %v", err)
	}
	if payload.Failed != 1 {
		t.Errorf("Failed: want 1, got %d", payload.Failed)
	}
	if payload.Restored != 0 {
		t.Errorf("Restored: want 0, got %d", payload.Restored)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "missing-plugin" {
		t.Errorf("expected missing-plugin to be deleted from registry, deleted=%v", store.deleted)
	}
}

func TestRestoreFromRegistry_LoadFailureIncrementsFailed(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	dataDir := filepath.Join(root, "data")
	_ = os.MkdirAll(pluginRoot, 0o755)

	// Create a plugin dir that exists on disk but has no manifest.json: Load will fail.
	badDir := filepath.Join(pluginRoot, "bad-plugin")
	_ = os.MkdirAll(badDir, 0o755)

	mgr := newBootstrapManager(t, pluginRoot, dataDir)

	now := time.Now().UTC()
	store := &stubRegistryStore{
		entries: []PluginRegistryEntry{
			{
				Name:      "bad-plugin",
				Dir:       badDir,
				Version:   "1.0.0",
				LoadedAt:  now,
				UpdatedAt: now,
			},
		},
	}

	payload, err := RestoreFromRegistry(context.Background(), mgr, store)
	if err != nil {
		t.Fatalf("RestoreFromRegistry: %v", err)
	}
	if payload.Failed != 1 {
		t.Errorf("Failed: want 1, got %d", payload.Failed)
	}
	if payload.Restored != 0 {
		t.Errorf("Restored: want 0, got %d", payload.Restored)
	}
}

func TestRestoreFromRegistry_ListError(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	dataDir := filepath.Join(root, "data")
	_ = os.MkdirAll(pluginRoot, 0o755)

	mgr := newBootstrapManager(t, pluginRoot, dataDir)
	store := &stubRegistryStore{
		listErr: errors.New("db unavailable"),
	}

	_, err := RestoreFromRegistry(context.Background(), mgr, store)
	if err == nil {
		t.Error("expected error when List fails, got nil")
	}
}

func TestRestoreFromRegistry_EmptyPayload(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	dataDir := filepath.Join(root, "data")
	_ = os.MkdirAll(pluginRoot, 0o755)

	mgr := newBootstrapManager(t, pluginRoot, dataDir)
	store := &stubRegistryStore{}

	payload, err := RestoreFromRegistry(context.Background(), mgr, store)
	if err != nil {
		t.Fatalf("RestoreFromRegistry: %v", err)
	}
	if payload.Restored != 0 {
		t.Errorf("Restored = %d, want 0 for empty registry", payload.Restored)
	}
	if payload.Failed != 0 {
		t.Errorf("Failed = %d, want 0 for empty registry", payload.Failed)
	}
}

func TestRestoreFromRegistry_PartialSuccess(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	dataDir := filepath.Join(root, "data")
	_ = os.MkdirAll(pluginRoot, 0o755)

	// Create one missing and one present-but-invalid dir.
	badDir := filepath.Join(pluginRoot, "bad-plugin2")
	_ = os.MkdirAll(badDir, 0o755) // exists but no manifest, Load fails

	now := time.Now().UTC()
	store := &stubRegistryStore{
		entries: []PluginRegistryEntry{
			{
				Name:      "completely-missing",
				Dir:       filepath.Join(pluginRoot, "completely-missing"),
				Version:   "1.0.0",
				LoadedAt:  now,
				UpdatedAt: now,
			},
			{
				Name:      "bad-plugin2",
				Dir:       badDir,
				Version:   "1.0.0",
				LoadedAt:  now,
				UpdatedAt: now,
			},
		},
	}

	mgr := newBootstrapManager(t, pluginRoot, dataDir)

	payload, err := RestoreFromRegistry(context.Background(), mgr, store)
	if err != nil {
		t.Fatalf("RestoreFromRegistry: %v", err)
	}
	if payload.Failed != 2 {
		t.Errorf("Failed: want 2, got %d", payload.Failed)
	}
	if payload.Restored != 0 {
		t.Errorf("Restored: want 0, got %d", payload.Restored)
	}
}

// TestRestoreFromRegistry_SQLiteStore runs RestoreFromRegistry against a real SQLite store.
func TestRestoreFromRegistry_SQLiteStore(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	dataDir := filepath.Join(root, "data")
	_ = os.MkdirAll(pluginRoot, 0o755)

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}
	defer db.Close()

	store, err := NewSQLitePluginRegistryStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePluginRegistryStore: %v", err)
	}
	defer store.Close()

	mgr := newBootstrapManager(t, pluginRoot, dataDir)

	payload, err := RestoreFromRegistry(context.Background(), mgr, store)
	if err != nil {
		t.Fatalf("RestoreFromRegistry with SQLite store: %v", err)
	}
	if payload.Restored != 0 || payload.Failed != 0 {
		t.Errorf("expected 0/0, got Restored=%d Failed=%d", payload.Restored, payload.Failed)
	}
}

// ---------------------------------------------------------------------------
// Helpers shared by topo-sort and parallel-restore tests
// ---------------------------------------------------------------------------

// makeEntry is a helper to create a PluginRegistryEntry pointing at a real dir.
func makeEntry(name, dir string) PluginRegistryEntry {
	now := time.Now().UTC()
	return PluginRegistryEntry{Name: name, Dir: dir, Version: "1.0.0", LoadedAt: now, UpdatedAt: now}
}

// writeManifestDeps writes a minimal manifest.json with only name + deps fields.
func writeManifestDeps(t *testing.T, dir, name string, deps, optDeps map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	md := manifestDeps{Name: name, Dependencies: deps, OptionalDependencies: optDeps}
	data, err := json.Marshal(md)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
}

// makeManifestMap builds the manifests map that buildLoadLevels expects.
func makeManifestMap(entries []PluginRegistryEntry, deps map[string]manifestDeps) map[string]manifestDeps {
	m := make(map[string]manifestDeps, len(entries))
	for _, e := range entries {
		if d, ok := deps[e.Name]; ok {
			m[e.Name] = d
		} else {
			m[e.Name] = manifestDeps{Name: e.Name}
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// TestBuildLoadLevels_TopoSort
// ---------------------------------------------------------------------------

func TestBuildLoadLevels_TopoSort(t *testing.T) {
	// Helper to build fake entries in given name order with no real dirs needed.
	mkEntries := func(names ...string) []PluginRegistryEntry {
		out := make([]PluginRegistryEntry, len(names))
		for i, n := range names {
			out[i] = PluginRegistryEntry{Name: n, Dir: "/fake/" + n}
		}
		return out
	}

	tests := []struct {
		name       string
		entries    []PluginRegistryEntry
		manifests  map[string]manifestDeps
		wantLevels int
		wantLevel0 []string // names expected in level[0] in any order within level
		wantErrNil bool
	}{
		{
			name:    "no deps – 3 independent in 1 level",
			entries: mkEntries("alpha", "beta", "gamma"),
			manifests: map[string]manifestDeps{
				"alpha": {Name: "alpha"},
				"beta":  {Name: "beta"},
				"gamma": {Name: "gamma"},
			},
			wantLevels: 1,
			wantLevel0: []string{"alpha", "beta", "gamma"},
			wantErrNil: true,
		},
		{
			name:    "linear A→B→C – 3 levels",
			entries: mkEntries("A", "B", "C"),
			manifests: map[string]manifestDeps{
				"A": {Name: "A"},
				"B": {Name: "B", Dependencies: map[string]string{"A": ""}},
				"C": {Name: "C", Dependencies: map[string]string{"B": ""}},
			},
			wantLevels: 3,
			wantLevel0: []string{"A"},
			wantErrNil: true,
		},
		{
			name:    "diamond A→[B,C]→D – 3 levels",
			entries: mkEntries("A", "B", "C", "D"),
			manifests: map[string]manifestDeps{
				"A": {Name: "A"},
				"B": {Name: "B", Dependencies: map[string]string{"A": ""}},
				"C": {Name: "C", Dependencies: map[string]string{"A": ""}},
				"D": {Name: "D", Dependencies: map[string]string{"B": "", "C": ""}},
			},
			wantLevels: 3,
			wantLevel0: []string{"A"},
			wantErrNil: true,
		},
		{
			name:    "optional dep present acts like hard for ordering",
			entries: mkEntries("X", "Y"),
			manifests: map[string]manifestDeps{
				"X": {Name: "X"},
				"Y": {Name: "Y", OptionalDependencies: map[string]string{"X": ""}},
			},
			wantLevels: 2,
			wantLevel0: []string{"X"},
			wantErrNil: true,
		},
		{
			name:    "optional dep absent is silently ignored",
			entries: mkEntries("Y"),
			manifests: map[string]manifestDeps{
				"Y": {Name: "Y", OptionalDependencies: map[string]string{"ghost": ""}},
			},
			wantLevels: 1,
			wantLevel0: []string{"Y"},
			wantErrNil: true,
		},
		{
			// Regression for duplicate-dep inDegree inflation bug caught by
			// post-implementation review: a manifest listing the same dep twice
			// must not corrupt level boundaries. Expected: 2 levels (A, then B).
			name:    "duplicate hard dep is deduplicated",
			entries: mkEntries("A", "B"),
			manifests: map[string]manifestDeps{
				"A": {Name: "A"},
				"B": {Name: "B", Dependencies: map[string]string{"A": ""}},
			},
			wantLevels: 2,
			wantLevel0: []string{"A"},
			wantErrNil: true,
		},
		{
			// A hard dep that is also listed as optional must collapse to one
			// in-degree contribution, not two.
			name:    "overlapping hard and optional dep is deduplicated",
			entries: mkEntries("A", "B"),
			manifests: map[string]manifestDeps{
				"A": {Name: "A"},
				"B": {Name: "B", Dependencies: map[string]string{"A": ""}, OptionalDependencies: map[string]string{"A": ""}},
			},
			wantLevels: 2,
			wantLevel0: []string{"A"},
			wantErrNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			levels, err := buildLoadLevels(tc.entries, tc.manifests)
			if tc.wantErrNil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantErrNil && err == nil {
				t.Fatal("expected error, got nil")
			}
			if err != nil {
				return
			}
			if len(levels) != tc.wantLevels {
				t.Errorf("level count: want %d, got %d", tc.wantLevels, len(levels))
			}
			if len(tc.wantLevel0) > 0 && len(levels) > 0 {
				got := make(map[string]bool)
				for _, e := range levels[0] {
					got[e.Name] = true
				}
				for _, name := range tc.wantLevel0 {
					if !got[name] {
						t.Errorf("expected %q in level[0], got %v", name, levels[0])
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestBuildLoadLevels_Cycle
// ---------------------------------------------------------------------------

func TestBuildLoadLevels_Cycle(t *testing.T) {
	entries := []PluginRegistryEntry{
		{Name: "A", Dir: "/fake/A"},
		{Name: "B", Dir: "/fake/B"},
	}
	manifests := map[string]manifestDeps{
		"A": {Name: "A", Dependencies: map[string]string{"B": ""}},
		"B": {Name: "B", Dependencies: map[string]string{"A": ""}},
	}
	_, err := buildLoadLevels(entries, manifests)
	if !errors.Is(err, ErrBootstrapCycle) {
		t.Errorf("want ErrBootstrapCycle, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestBuildLoadLevels_MissingHardDep
// ---------------------------------------------------------------------------

func TestBuildLoadLevels_MissingHardDep(t *testing.T) {
	entries := []PluginRegistryEntry{
		{Name: "A", Dir: "/fake/A"},
	}
	manifests := map[string]manifestDeps{
		"A": {Name: "A", Dependencies: map[string]string{"ghost": ""}},
	}
	_, err := buildLoadLevels(entries, manifests)
	if !errors.Is(err, ErrBootstrapMissingHardDep) {
		t.Errorf("want ErrBootstrapMissingHardDep, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestBuildLoadLevels_RegistryOrderPreserved
// ---------------------------------------------------------------------------

func TestBuildLoadLevels_RegistryOrderPreserved(t *testing.T) {
	// 4 independent plugins in a specific registry order; all end up in level 0.
	// Within level 0 they should appear in registry insertion order.
	names := []string{"delta", "alpha", "gamma", "beta"}
	entries := make([]PluginRegistryEntry, len(names))
	manifests := make(map[string]manifestDeps, len(names))
	for i, n := range names {
		entries[i] = PluginRegistryEntry{Name: n, Dir: "/fake/" + n}
		manifests[n] = manifestDeps{Name: n}
	}

	levels, err := buildLoadLevels(entries, manifests)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(levels) != 1 {
		t.Fatalf("expected 1 level, got %d", len(levels))
	}
	level := levels[0]
	if len(level) != len(names) {
		t.Fatalf("expected %d entries in level, got %d", len(names), len(level))
	}
	for i, want := range names {
		if level[i].Name != want {
			t.Errorf("level[0][%d]: want %q, got %q", i, want, level[i].Name)
		}
	}
}

// ---------------------------------------------------------------------------
// recordingManager – test double for PluginManager
// ---------------------------------------------------------------------------

// recordingManager records the order in which Load is called.
// It implements the full PluginManager interface.
// Unused methods return zero values and nil errors.
type recordingManager struct {
	mu           sync.Mutex
	loadOrder    []string
	sleepPerLoad time.Duration
}

func (r *recordingManager) Load(_ context.Context, pluginDir string) (PluginInfo, error) {
	// Simulate work so goroutines actually interleave.
	if r.sleepPerLoad > 0 {
		time.Sleep(r.sleepPerLoad)
	}

	// Derive plugin name from the manifest.json in the dir.
	// If not present, fall back to dir basename.
	name := filepath.Base(pluginDir)
	mdPath := filepath.Join(pluginDir, "manifest.json")
	if data, err := os.ReadFile(mdPath); err == nil {
		var md manifestDeps
		if jsonErr := json.Unmarshal(data, &md); jsonErr == nil && md.Name != "" {
			name = md.Name
		}
	}

	r.mu.Lock()
	r.loadOrder = append(r.loadOrder, name)
	r.mu.Unlock()
	return PluginInfo{Name: name}, nil
}

func (r *recordingManager) Unload(_ context.Context, _ string) error { return nil }
func (r *recordingManager) Reload(_ context.Context, _ string) (PluginInfo, error) {
	return PluginInfo{}, nil
}
func (r *recordingManager) Get(_ string) (Plugin, error) { return nil, ErrPluginNotFound }
func (r *recordingManager) List() []PluginInfo           { return nil }
func (r *recordingManager) GetMCPMeta(_ string) (PluginMCPMeta, error) {
	return PluginMCPMeta{}, ErrPluginNotFound
}
func (r *recordingManager) CallPlugin(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
	return nil, ErrPluginNotFound
}
func (r *recordingManager) GetManifest(_ string) (Manifest, error) {
	return Manifest{}, ErrPluginNotFound
}
func (r *recordingManager) ScanDir(_ context.Context) ([]DiscoveredPlugin, error) { return nil, nil }
func (r *recordingManager) Shutdown(_ context.Context) error                      { return nil }
func (r *recordingManager) PluginDB(_ string) (*sql.DB, error) {
	return nil, ErrPluginNotFound
}

// ---------------------------------------------------------------------------
// TestRestoreFromRegistry_ParallelTopoSort
// ---------------------------------------------------------------------------

func TestRestoreFromRegistry_ParallelTopoSort(t *testing.T) {
	// Topology: A→B→C (linear chain) plus independent D.
	// Expected load order constraints:
	//   loadOrder[A] < loadOrder[B]
	//   loadOrder[B] < loadOrder[C]
	//   D appears in the first level (before B and C, possibly concurrent with A).
	root := t.TempDir()

	// Create temp dirs for each plugin and write manifests.
	type pluginSpec struct {
		name    string
		deps    map[string]string
		optDeps map[string]string
	}
	specs := []pluginSpec{
		{name: "A", deps: nil, optDeps: nil},
		{name: "B", deps: map[string]string{"A": ""}, optDeps: nil},
		{name: "C", deps: map[string]string{"B": ""}, optDeps: nil},
		{name: "D", deps: nil, optDeps: nil},
	}

	var entries []PluginRegistryEntry
	for _, spec := range specs {
		dir := filepath.Join(root, spec.name)
		writeManifestDeps(t, dir, spec.name, spec.deps, spec.optDeps)
		entries = append(entries, makeEntry(spec.name, dir))
	}

	store := &stubRegistryStore{entries: entries}
	mgr := &recordingManager{sleepPerLoad: 20 * time.Millisecond}

	payload, err := RestoreFromRegistry(context.Background(), mgr, store)
	if err != nil {
		t.Fatalf("RestoreFromRegistry: %v", err)
	}
	if payload.Restored != 4 {
		t.Errorf("Restored: want 4, got %d", payload.Restored)
	}
	if payload.Failed != 0 {
		t.Errorf("Failed: want 0, got %d", payload.Failed)
	}

	mgr.mu.Lock()
	order := mgr.loadOrder
	mgr.mu.Unlock()

	if len(order) != 4 {
		t.Fatalf("expected 4 load calls, got %d: %v", len(order), order)
	}

	// Build index map for assertion.
	idx := make(map[string]int, len(order))
	for i, n := range order {
		idx[n] = i
	}
	for _, name := range []string{"A", "B", "C", "D"} {
		if _, ok := idx[name]; !ok {
			t.Errorf("plugin %q not found in loadOrder %v", name, order)
		}
	}

	// A must load before B.
	if idx["A"] >= idx["B"] {
		t.Errorf("A must load before B, but order=%v", order)
	}
	// B must load before C.
	if idx["B"] >= idx["C"] {
		t.Errorf("B must load before C, but order=%v", order)
	}
	// D is independent; it must appear before B (it's in level 0 with A, before level 1 which has B).
	if idx["D"] >= idx["B"] {
		t.Errorf("D must load before B (independent, in level 0), but order=%v", order)
	}
}

// ---------------------------------------------------------------------------
// TestRestoreFromRegistry_CtxCancellation
// ---------------------------------------------------------------------------

// slowManager blocks inside Load until the context is done, then returns the
// ctx error. This exercises the context-cancellation path in loadLevelParallel.
type slowManager struct {
	recordingManager
}

func (s *slowManager) Load(ctx context.Context, pluginDir string) (PluginInfo, error) {
	<-ctx.Done()
	return PluginInfo{}, ctx.Err()
}

func TestRestoreFromRegistry_CtxCancellation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugin-a")
	writeManifestDeps(t, dir, "plugin-a", nil, nil)

	store := &stubRegistryStore{entries: []PluginRegistryEntry{makeEntry("plugin-a", dir)}}
	mgr := &slowManager{}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := RestoreFromRegistry(ctx, mgr, store)
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestRestoreFromRegistry_CycleReturnsError
// ---------------------------------------------------------------------------

func TestRestoreFromRegistry_CycleReturnsError(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "A")
	dirB := filepath.Join(root, "B")
	writeManifestDeps(t, dirA, "A", map[string]string{"B": ""}, nil)
	writeManifestDeps(t, dirB, "B", map[string]string{"A": ""}, nil)

	store := &stubRegistryStore{
		entries: []PluginRegistryEntry{
			makeEntry("A", dirA),
			makeEntry("B", dirB),
		},
	}
	mgr := &recordingManager{}

	_, err := RestoreFromRegistry(context.Background(), mgr, store)
	if !errors.Is(err, ErrBootstrapCycle) {
		t.Errorf("want ErrBootstrapCycle, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestRestoreFromRegistry_MissingHardDepReturnsError
// ---------------------------------------------------------------------------

func TestRestoreFromRegistry_MissingHardDepReturnsError(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "A")
	writeManifestDeps(t, dirA, "A", map[string]string{"ghost": ""}, nil)

	store := &stubRegistryStore{
		entries: []PluginRegistryEntry{makeEntry("A", dirA)},
	}
	mgr := &recordingManager{}

	_, err := RestoreFromRegistry(context.Background(), mgr, store)
	if !errors.Is(err, ErrBootstrapMissingHardDep) {
		t.Errorf("want ErrBootstrapMissingHardDep, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestRestoreFromRegistry_ManifestReadError
// ---------------------------------------------------------------------------

// TestLoadLevelParallel_PreCancelledCtx verifies that when the context is
// already cancelled before loadLevelParallel is called, goroutines take the
// early-exit branch and errors propagate.
func TestLoadLevelParallel_PreCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	level := loadLevel{
		{Name: "p1", Dir: "/fake/p1"},
		{Name: "p2", Dir: "/fake/p2"},
	}
	mgr := &recordingManager{}

	results, err := loadLevelParallel(ctx, mgr, level, 2)
	if err == nil {
		t.Error("expected error from pre-cancelled context, got nil")
	}
	// Results slice should be fully allocated.
	if len(results) != len(level) {
		t.Errorf("expected %d results, got %d", len(level), len(results))
	}
}

func TestRestoreFromRegistry_ManifestReadError(t *testing.T) {
	root := t.TempDir()
	// Create a dir but do NOT write manifest.json → readManifestDeps will fail.
	dir := filepath.Join(root, "no-manifest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	store := &stubRegistryStore{entries: []PluginRegistryEntry{makeEntry("no-manifest", dir)}}
	mgr := &recordingManager{}

	payload, err := RestoreFromRegistry(context.Background(), mgr, store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.Failed != 1 {
		t.Errorf("want Failed=1 (manifest read error counted as failed), got %d", payload.Failed)
	}
	if payload.Restored != 0 {
		t.Errorf("want Restored=0, got %d", payload.Restored)
	}
}

func (r *recordingManager) InstallFromURL(_ context.Context, _, _ string) (PluginInfo, error) {
	return PluginInfo{}, nil
}
func (r *recordingManager) DownloadAndUnpack(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (s *slowManager) InstallFromURL(_ context.Context, _, _ string) (PluginInfo, error) {
	return PluginInfo{}, nil
}
func (s *slowManager) DownloadAndUnpack(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
