package loader

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/config"
	"github.com/plekt-dev/plekt/internal/eventbus"
)

// newTestManagerForDiscovery creates a manager with given pluginRoot for ScanDir tests.
func newTestManagerForDiscovery(t *testing.T, pluginRoot string) PluginManager {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
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

// makeDiscoverableDir creates a plugin directory with only manifest.json (no mcp.yaml or wasm).
// Suitable for ScanDir discovery (not for Load).
func makeDiscoverableDir(t *testing.T, pluginRoot, name, version, description string) string {
	t.Helper()
	dir := filepath.Join(pluginRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	m := Manifest{
		Name:        name,
		Version:     version,
		Description: description,
	}
	mb, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func TestScanDir_EmptyDir(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := newTestManagerForDiscovery(t, pluginRoot)

	results, err := mgr.ScanDir(context.Background())
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty dir, got %d", len(results))
	}
}

func TestScanDir_ValidPlugin(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := newTestManagerForDiscovery(t, pluginRoot)

	makeDiscoverableDir(t, pluginRoot, "my-plugin", "1.2.3", "A test plugin")

	results, err := mgr.ScanDir(context.Background())
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	got := results[0]
	if got.Name != "my-plugin" {
		t.Errorf("Name: want my-plugin, got %q", got.Name)
	}
	if got.Version != "1.2.3" {
		t.Errorf("Version: want 1.2.3, got %q", got.Version)
	}
	if got.Description != "A test plugin" {
		t.Errorf("Description: want 'A test plugin', got %q", got.Description)
	}
	if got.Dir == "" {
		t.Error("Dir should not be empty")
	}
	if got.ScannedAt.IsZero() {
		t.Error("ScannedAt should not be zero")
	}
}

func TestScanDir_ManifestValid_True_For_ValidManifest(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := newTestManagerForDiscovery(t, pluginRoot)

	makeDiscoverableDir(t, pluginRoot, "valid-plugin", "1.0.0", "")

	results, err := mgr.ScanDir(context.Background())
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].ManifestValid {
		t.Error("ManifestValid should be true for valid manifest")
	}
}

func TestScanDir_MissingManifest(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Create a subdir with no manifest.json.
	emptyDir := filepath.Join(pluginRoot, "no-manifest")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := newTestManagerForDiscovery(t, pluginRoot)

	results, err := mgr.ScanDir(context.Background())
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for dir without manifest, got %d", len(results))
	}
	if results[0].ManifestValid {
		t.Error("ManifestValid should be false when manifest.json is missing")
	}
}

func TestScanDir_InvalidManifestJSON(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	badDir := filepath.Join(pluginRoot, "bad-json")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "manifest.json"), []byte("{not valid json}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	mgr := newTestManagerForDiscovery(t, pluginRoot)

	results, err := mgr.ScanDir(context.Background())
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for invalid manifest, got %d", len(results))
	}
	if results[0].ManifestValid {
		t.Error("ManifestValid should be false for invalid JSON manifest")
	}
}

func TestScanDir_SkipsFiles(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Place a file (not a directory) in the plugin root.
	if err := os.WriteFile(filepath.Join(pluginRoot, "not-a-dir.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	makeDiscoverableDir(t, pluginRoot, "real-plugin", "1.0.0", "")

	mgr := newTestManagerForDiscovery(t, pluginRoot)

	results, err := mgr.ScanDir(context.Background())
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	// Only the directory should be returned.
	if len(results) != 1 {
		t.Errorf("expected 1 result (file should be skipped), got %d", len(results))
	}
}

func TestScanDir_MultiplePlugins(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := newTestManagerForDiscovery(t, pluginRoot)

	makeDiscoverableDir(t, pluginRoot, "plugin-a", "1.0.0", "")
	makeDiscoverableDir(t, pluginRoot, "plugin-b", "2.0.0", "")
	makeDiscoverableDir(t, pluginRoot, "plugin-c", "3.0.0", "")

	results, err := mgr.ScanDir(context.Background())
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestScanDir_ScannedAtIsRecent(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := newTestManagerForDiscovery(t, pluginRoot)

	makeDiscoverableDir(t, pluginRoot, "timed", "1.0.0", "")

	before := time.Now()
	results, err := mgr.ScanDir(context.Background())
	after := time.Now()

	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].ScannedAt.Before(before) || results[0].ScannedAt.After(after) {
		t.Errorf("ScannedAt %v is outside [%v, %v]", results[0].ScannedAt, before, after)
	}
}

func TestScanDir_NonExistentPluginDir(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins", "does-not-exist")
	mgr := newTestManagerForDiscovery(t, pluginRoot)

	_, err := mgr.ScanDir(context.Background())
	if err == nil {
		t.Error("expected error for non-existent plugin dir, got nil")
	}
}

func TestScanDir_DoesNotMutateState(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := newTestManagerForDiscovery(t, pluginRoot)

	makeDiscoverableDir(t, pluginRoot, "passive", "1.0.0", "")

	_, err := mgr.ScanDir(context.Background())
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	// Manager state should be unchanged: no plugins loaded.
	loaded := mgr.List()
	if len(loaded) != 0 {
		t.Errorf("ScanDir must not load plugins; got %d loaded", len(loaded))
	}
}

func TestScanDir_PathSafety(t *testing.T) {
	// ScanDir should only return dirs within the configured PluginDir.
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mgr := newTestManagerForDiscovery(t, pluginRoot)
	makeDiscoverableDir(t, pluginRoot, "safe-plugin", "1.0.0", "")

	results, err := mgr.ScanDir(context.Background())
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	for _, dp := range results {
		// All dirs must be within the configured plugin root.
		rel, err := filepath.Rel(pluginRoot, dp.Dir)
		if err != nil || len(rel) > 0 && rel[0] == '.' {
			t.Errorf("discovered dir %q is outside plugin root %q", dp.Dir, pluginRoot)
		}
	}
}

func TestDiscoveredPlugin_Fields(t *testing.T) {
	dp := DiscoveredPlugin{
		Dir:           "/plugins/test",
		Name:          "test",
		Version:       "1.0.0",
		Description:   "desc",
		ManifestValid: true,
		ScannedAt:     time.Now(),
	}
	if dp.Dir != "/plugins/test" {
		t.Errorf("Dir mismatch")
	}
	if !dp.ManifestValid {
		t.Error("ManifestValid should be true")
	}
}

func TestScanDir_InvalidManifestName(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Manifest with empty name.
	emptyNameDir := filepath.Join(pluginRoot, "empty-name")
	if err := os.MkdirAll(emptyNameDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mb, _ := json.Marshal(Manifest{Name: "", Version: "1.0.0"})
	if err := os.WriteFile(filepath.Join(emptyNameDir, "manifest.json"), mb, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	mgr := newTestManagerForDiscovery(t, pluginRoot)

	results, err := mgr.ScanDir(context.Background())
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// ManifestValid should be false because name is empty.
	if results[0].ManifestValid {
		t.Error("ManifestValid should be false for manifest with empty name")
	}
}

// Verify ScanDir is concurrent-safe (read-only, can be called from multiple goroutines).
func TestScanDir_ConcurrentSafe(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := newTestManagerForDiscovery(t, pluginRoot)

	makeDiscoverableDir(t, pluginRoot, "concurrent-plugin", "1.0.0", "")

	done := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() {
			_, err := mgr.ScanDir(context.Background())
			done <- err
		}()
	}
	for i := 0; i < 5; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent ScanDir: %v", err)
		}
	}
}

// Verify that PluginManager interface has ScanDir method.
func TestPluginManager_HasScanDir(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var mgr PluginManager = newTestManagerForDiscovery(t, pluginRoot)
	// If this compiles, the interface has ScanDir.
	_, err := mgr.ScanDir(context.Background())
	// Non-existent dir errors are fine: we just verify the method exists.
	_ = err
	_ = errors.New("test") // suppress unused import
}
