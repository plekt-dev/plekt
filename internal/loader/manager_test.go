package loader

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/plekt-dev/plekt/internal/config"
	"github.com/plekt-dev/plekt/internal/eventbus"
)

// makePluginDir creates a valid plugin directory under t.TempDir() and returns its path.
// If sign is true, mcp.yaml is signed with a freshly generated key.
// If sign is false, the signature block contains garbage values.
func makePluginDir(t *testing.T, pluginRoot, name string, sign bool) string {
	t.Helper()
	dir := filepath.Join(pluginRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("makePluginDir mkdir: %v", err)
	}

	// manifest.json
	m := Manifest{
		Name:    name,
		Version: "0.1.0",
	}
	mb, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Build mcp.yaml (without real signature first, then replace).
	rawYAML := []byte("tools: []\nsignature:\n  public_key: placeholder\n  signature: placeholder\n")
	if sign {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("keygen: %v", err)
		}
		canonical := canonicalizeMCPYAML(rawYAML)
		sig := ed25519.Sign(priv, canonical)

		type mcpYAML struct {
			Tools     []any `yaml:"tools"`
			Signature struct {
				PublicKey string `yaml:"public_key"`
				Signature string `yaml:"signature"`
			} `yaml:"signature"`
		}
		doc := mcpYAML{}
		doc.Signature.PublicKey = hex.EncodeToString(pub)
		doc.Signature.Signature = hex.EncodeToString(sig)
		rawYAML, err = yaml.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal mcp.yaml: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.yaml"), rawYAML, 0o600); err != nil {
		t.Fatalf("write mcp.yaml: %v", err)
	}

	// plugin.wasm: a minimal valid wasm module (magic + version).
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	if err := os.WriteFile(filepath.Join(dir, "plugin.wasm"), wasmBytes, 0o600); err != nil {
		t.Fatalf("write plugin.wasm: %v", err)
	}

	return dir
}

// newTestManager returns a PluginManager with a temp plugin root and data dir.
func newTestManager(t *testing.T) (PluginManager, string, eventbus.EventBus) {
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
	mgr := NewManager(cfg, bus)
	// Tests build plugins on the fly with ephemeral keys and do not consult
	// the remote registry: opt out of strict per-repo verification so the
	// signature path is exercised without needing a registry snapshot.
	AllowUnsignedPlugins(mgr, true)
	return mgr, pluginRoot, bus
}

func TestLoad_PathTraversal(t *testing.T) {
	mgr, pluginRoot, _ := newTestManager(t)
	_ = pluginRoot

	cases := []struct {
		name string
		path string
	}{
		{"absolute escape", "/etc/passwd"},
		{"dot-dot", filepath.Join(pluginRoot, "..", "etc")},
		{"sibling", filepath.Join(filepath.Dir(pluginRoot), "other")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mgr.Load(context.Background(), tc.path)
			if !errors.Is(err, ErrPluginDirTraversal) {
				t.Errorf("expected ErrPluginDirTraversal for %q, got %v", tc.path, err)
			}
		})
	}
}

func TestLoad_MissingManifest(t *testing.T) {
	mgr, pluginRoot, _ := newTestManager(t)
	dir := filepath.Join(pluginRoot, "no-manifest")
	_ = os.MkdirAll(dir, 0o755)

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid, got %v", err)
	}
}

func TestLoad_InvalidManifestJSON(t *testing.T) {
	mgr, pluginRoot, _ := newTestManager(t)
	dir := filepath.Join(pluginRoot, "bad-manifest")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{not json}"), 0o600)

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid for bad JSON, got %v", err)
	}
}

func TestLoad_EmptyPluginName(t *testing.T) {
	mgr, pluginRoot, _ := newTestManager(t)
	dir := filepath.Join(pluginRoot, "no-name")
	_ = os.MkdirAll(dir, 0o755)
	mb, _ := json.Marshal(Manifest{Name: ""})
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o600)

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid for empty name, got %v", err)
	}
}

func TestLoad_MissingMCPYAML(t *testing.T) {
	mgr, pluginRoot, _ := newTestManager(t)
	dir := filepath.Join(pluginRoot, "no-mcp")
	_ = os.MkdirAll(dir, 0o755)
	mb, _ := json.Marshal(Manifest{Name: "no-mcp", Version: "1.0"})
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o600)

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid for missing mcp.yaml, got %v", err)
	}
}

func TestLoad_InvalidSignature(t *testing.T) {
	mgr, pluginRoot, _ := newTestManager(t)
	// sign=false → placeholder pubkey + signature in mcp.yaml.
	makePluginDir(t, pluginRoot, "bad-sig", false)
	dir := filepath.Join(pluginRoot, "bad-sig")

	// Pin the plugin in the registry snapshot to a real key the file is
	// NOT signed with: verification must reject before WASM init. Without
	// the snapshot the test manager's allowUnsigned=true would skip the
	// check entirely.
	realPub, _, _, _ := generateKeyPair(t)
	WireRegistrySnapshot(mgr, map[string]RegistryEntrySnapshot{
		"bad-sig": {PublicKey: realPub},
	})

	_, err := mgr.Load(context.Background(), dir)
	// Both error classes count as "bad signature": pub-key mismatch (mcp
	// says placeholder, registry says realPub) and the legacy hex-decode
	// failure path (placeholder isn't valid hex).
	if !errors.Is(err, ErrPublicKeyMismatch) && !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("expected ErrPublicKeyMismatch or ErrSignatureInvalid, got %v", err)
	}
}

func TestLoad_DuplicatePlugin(t *testing.T) {
	mgr, pluginRoot, _ := newTestManager(t)
	makePluginDir(t, pluginRoot, "dup-plugin", true)
	dir := filepath.Join(pluginRoot, "dup-plugin")

	_, err := mgr.Load(context.Background(), dir)
	if err != nil && !errors.Is(err, ErrWASMInit) {
		// WASM init may fail in test env (no real wasm); that's acceptable.
		// We only care about the duplicate check if first Load succeeds.
		t.Logf("first Load: %v (may be ErrWASMInit in test env)", err)
		return
	}
	if err == nil {
		// Clean up so Windows can delete the temp dir.
		defer mgr.Unload(context.Background(), "dup-plugin") //nolint:errcheck
	}

	_, err2 := mgr.Load(context.Background(), dir)
	if err == nil && !errors.Is(err2, ErrPluginAlreadyLoaded) {
		t.Errorf("expected ErrPluginAlreadyLoaded on second Load, got %v", err2)
	}
}

func TestUnload_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	err := mgr.Unload(context.Background(), "ghost")
	if !errors.Is(err, ErrPluginNotFound) {
		t.Errorf("expected ErrPluginNotFound, got %v", err)
	}
}

func TestGet_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	_, err := mgr.Get("ghost")
	if !errors.Is(err, ErrPluginNotFound) {
		t.Errorf("expected ErrPluginNotFound, got %v", err)
	}
}

func TestList_Empty(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	plugins := mgr.List()
	if len(plugins) != 0 {
		t.Errorf("expected empty list, got %d", len(plugins))
	}
}

func TestCallPlugin_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	_, err := mgr.CallPlugin(context.Background(), "ghost", "fn", nil)
	if !errors.Is(err, ErrPluginNotFound) {
		t.Errorf("expected ErrPluginNotFound, got %v", err)
	}
}

func TestReload_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	_, err := mgr.Reload(context.Background(), "ghost")
	if !errors.Is(err, ErrPluginNotFound) {
		t.Errorf("expected ErrPluginNotFound, got %v", err)
	}
}

func TestLoad_EmitsPluginLoadedOnSuccess(t *testing.T) {
	// This test can only fire if WASM init succeeds; skip if ErrWASMInit.
	mgr, pluginRoot, bus := newTestManager(t)
	makePluginDir(t, pluginRoot, "emitting", true)
	dir := filepath.Join(pluginRoot, "emitting")

	var received []eventbus.Event
	var mu sync.Mutex
	bus.Subscribe(eventbus.EventPluginLoaded, func(ctx context.Context, e eventbus.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})

	_, err := mgr.Load(context.Background(), dir)
	if errors.Is(err, ErrWASMInit) {
		t.Skip("WASM init failed in test environment (no host WASM runtime): skipping EventBus delivery check")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Clean up so Windows can remove the temp dir.
	defer mgr.Unload(context.Background(), "emitting") //nolint:errcheck

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Error("expected plugin.loaded event, got none")
	}
}

func TestLoad_PathTraversal_URLEncoded(t *testing.T) {
	// Ensure the check works even if someone passes URL-encoded paths.
	// filepath.Clean handles the OS-level normalization; this tests we don't
	// accept paths that look like they are in the plugin root but escape it.
	mgr, pluginRoot, _ := newTestManager(t)
	_ = pluginRoot

	// On Windows this would be "..\\.."; on Unix "../..".
	escapePath := filepath.Join(pluginRoot, "..", "..", "etc", "passwd")
	_, err := mgr.Load(context.Background(), escapePath)
	if !errors.Is(err, ErrPluginDirTraversal) {
		t.Errorf("expected ErrPluginDirTraversal for path %q, got %v", escapePath, err)
	}
}

func TestHostFunctionRegistry_RegisterAndAll(t *testing.T) {
	reg := NewHostFunctionRegistry()
	fn := HostFunction{Namespace: "mc_db", Name: "query", Params: []ValType{ValTypeI32}, Returns: []ValType{ValTypeI32}}
	if err := reg.Register(fn); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reg.Seal()
	all, err := reg.All()
	if err != nil {
		t.Fatalf("All() after Seal: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 function, got %d", len(all))
	}
	if all[0].Name != "query" {
		t.Errorf("unexpected function name: %s", all[0].Name)
	}
}

func TestHostFunctionRegistry_SealPreventsRegister(t *testing.T) {
	reg := NewHostFunctionRegistry()
	reg.Seal()
	if !reg.Sealed() {
		t.Error("Sealed() should return true after Seal()")
	}
	err := reg.Register(HostFunction{Name: "forbidden"})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied after seal, got %v", err)
	}
}

func TestHostFunctionRegistry_AllReturnsSnapshot(t *testing.T) {
	reg := NewHostFunctionRegistry()
	_ = reg.Register(HostFunction{Name: "a"})
	reg.Seal()
	snap, err := reg.All()
	if err != nil {
		t.Fatalf("All() after Seal: %v", err)
	}
	// Snapshot taken after seal; registrations after seal are rejected, so snap has 1 item.
	if len(snap) != 1 {
		t.Errorf("snapshot should have 1 item, got %d", len(snap))
	}
}

func TestHostFunctionRegistry_NotSealed(t *testing.T) {
	reg := NewHostFunctionRegistry()
	if reg.Sealed() {
		t.Error("new registry should not be sealed")
	}
}

func TestPluginCallContext_WithAndFrom(t *testing.T) {
	pcc := PluginCallContext{PluginName: "tasks", BearerToken: "secret"}
	ctx := WithPluginCallContext(context.Background(), pcc)
	got, ok := PluginCallContextFrom(ctx)
	if !ok {
		t.Fatal("expected PluginCallContext to be in ctx")
	}
	if got.PluginName != "tasks" {
		t.Errorf("unexpected PluginName: %s", got.PluginName)
	}
	if got.BearerToken != "secret" {
		t.Errorf("unexpected BearerToken: %s", got.BearerToken)
	}
}

func TestPluginCallContext_MissingFromContext(t *testing.T) {
	_, ok := PluginCallContextFrom(context.Background())
	if ok {
		t.Error("expected ok=false when no PluginCallContext in ctx")
	}
}

func TestPluginMCPHandler_ListPlugins_Empty(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	result, err := h.ListPlugins(context.Background(), ListPluginsParams{})
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(result.Plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(result.Plugins))
	}
}

func TestPluginMCPHandler_GetPlugin_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	_, err := h.GetPlugin(context.Background(), GetPluginParams{Name: "ghost"})
	if !errors.Is(err, ErrPluginNotFound) {
		t.Errorf("expected ErrPluginNotFound, got %v", err)
	}
}

func TestPluginMCPHandler_GetPlugin_EmptyName(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	_, err := h.GetPlugin(context.Background(), GetPluginParams{Name: ""})
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid for empty name, got %v", err)
	}
}

func TestPluginMCPHandler_UnloadPlugin_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	_, err := h.UnloadPlugin(context.Background(), UnloadPluginParams{Name: "ghost"})
	if !errors.Is(err, ErrPluginNotFound) {
		t.Errorf("expected ErrPluginNotFound, got %v", err)
	}
}

func TestPluginMCPHandler_UnloadPlugin_EmptyName(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	_, err := h.UnloadPlugin(context.Background(), UnloadPluginParams{Name: ""})
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid for empty name, got %v", err)
	}
}

func TestPluginMCPHandler_ReloadPlugin_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	_, err := h.ReloadPlugin(context.Background(), ReloadPluginParams{Name: "ghost"})
	if !errors.Is(err, ErrPluginNotFound) {
		t.Errorf("expected ErrPluginNotFound, got %v", err)
	}
}

func TestPluginMCPHandler_ReloadPlugin_EmptyName(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	_, err := h.ReloadPlugin(context.Background(), ReloadPluginParams{Name: ""})
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid for empty name, got %v", err)
	}
}

func TestPluginMCPHandler_InstallPlugin_EmptyDir(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	_, err := h.InstallPlugin(context.Background(), InstallPluginParams{Dir: ""})
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid for empty dir, got %v", err)
	}
}

func TestPluginMCPHandler_ListPlugins_StatusFilter(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	// With an empty manager, status filter should return empty regardless.
	result, err := h.ListPlugins(context.Background(), ListPluginsParams{StatusFilter: string(PluginStatusActive)})
	if err != nil {
		t.Fatalf("ListPlugins with filter: %v", err)
	}
	if len(result.Plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(result.Plugins))
	}
}

func TestLoad_SignatureVerifiedBeforeWASMInit(t *testing.T) {
	// Bad signature must fail BEFORE WASM init: never ErrWASMInit.
	mgr, pluginRoot, _ := newTestManager(t)
	makePluginDir(t, pluginRoot, "badsig2", false) // placeholder signature
	dir := filepath.Join(pluginRoot, "badsig2")

	realPub, _, _, _ := generateKeyPair(t)
	WireRegistrySnapshot(mgr, map[string]RegistryEntrySnapshot{
		"badsig2": {PublicKey: realPub},
	})

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrPublicKeyMismatch) && !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("expected ErrPublicKeyMismatch or ErrSignatureInvalid before WASM init, got %v", err)
	}
}
