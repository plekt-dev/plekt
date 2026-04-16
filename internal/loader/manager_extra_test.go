package loader

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// TestPluginImpl_CallWhenNotActive verifies that Call returns ErrPluginNotReady
// when the plugin is not in PluginStatusActive.
// We construct a pluginImpl directly to avoid needing a real WASM binary.
func TestPluginImpl_CallWhenNotActive(t *testing.T) {
	impl := &pluginImpl{
		info: PluginInfo{
			Name:   "test",
			Status: PluginStatusError,
		},
	}
	_, err := impl.Call(context.Background(), "fn", nil)
	if !errors.Is(err, ErrPluginNotReady) {
		t.Errorf("expected ErrPluginNotReady, got %v", err)
	}
}

// TestPluginImpl_CallWhenUnloading verifies ErrPluginNotReady during unloading.
func TestPluginImpl_CallWhenUnloading(t *testing.T) {
	impl := &pluginImpl{
		info: PluginInfo{
			Name:   "test",
			Status: PluginStatusUnloading,
		},
	}
	_, err := impl.Call(context.Background(), "fn", nil)
	if !errors.Is(err, ErrPluginNotReady) {
		t.Errorf("expected ErrPluginNotReady, got %v", err)
	}
}

// TestPluginImpl_CallWhenLoading verifies ErrPluginNotReady during loading.
func TestPluginImpl_CallWhenLoading(t *testing.T) {
	impl := &pluginImpl{
		info: PluginInfo{
			Name:   "test",
			Status: PluginStatusLoading,
		},
	}
	_, err := impl.Call(context.Background(), "fn", nil)
	if !errors.Is(err, ErrPluginNotReady) {
		t.Errorf("expected ErrPluginNotReady, got %v", err)
	}
}

// TestPluginImpl_CloseNilPoolAndDB verifies Close is a no-op when pool and db are nil.
func TestPluginImpl_CloseNilRunnerAndDB(t *testing.T) {
	impl := &pluginImpl{
		info: PluginInfo{Name: "test", Status: PluginStatusActive},
		pool: nil,
		db:   nil,
	}
	if err := impl.Close(); err != nil {
		t.Errorf("Close with nil pool/db should not error, got: %v", err)
	}
}

// TestCallPlugin_PluginNotReady exercises the ErrPluginNotReady path through
// the manager by calling CallPlugin on a plugin that is in error state.
func TestCallPlugin_PluginNotReady(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	m := mgr.(*managerImpl)

	// Inject a plugin directly in error state.
	impl := &pluginImpl{
		info: PluginInfo{
			Name:   "broken",
			Status: PluginStatusError,
		},
	}
	m.mu.Lock()
	m.plugins["broken"] = impl
	m.mu.Unlock()

	_, err := m.CallPlugin(context.Background(), "broken", "fn", nil)
	if !errors.Is(err, ErrPluginNotReady) {
		t.Errorf("expected ErrPluginNotReady, got %v", err)
	}
}

// TestListPlugins_StatusFilter_WithMatch populates a fake active plugin and
// ensures the status filter returns it.
func TestListPlugins_StatusFilter_WithMatch(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	m := mgr.(*managerImpl)

	impl := &pluginImpl{
		info: PluginInfo{Name: "active-one", Status: PluginStatusActive},
	}
	m.mu.Lock()
	m.plugins["active-one"] = impl
	m.mu.Unlock()

	h := NewPluginMCPHandler(mgr)
	result, err := h.ListPlugins(context.Background(), ListPluginsParams{StatusFilter: string(PluginStatusActive)})
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(result.Plugins) != 1 {
		t.Errorf("expected 1 plugin, got %d", len(result.Plugins))
	}
	if result.Plugins[0].Name != "active-one" {
		t.Errorf("unexpected plugin name: %s", result.Plugins[0].Name)
	}
}

// TestListPlugins_StatusFilter_NoMatch ensures filter with unmatched status returns empty.
func TestListPlugins_StatusFilter_NoMatch(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	m := mgr.(*managerImpl)

	impl := &pluginImpl{
		info: PluginInfo{Name: "active-one", Status: PluginStatusActive},
	}
	m.mu.Lock()
	m.plugins["active-one"] = impl
	m.mu.Unlock()

	h := NewPluginMCPHandler(mgr)
	result, err := h.ListPlugins(context.Background(), ListPluginsParams{StatusFilter: string(PluginStatusError)})
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(result.Plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(result.Plugins))
	}
}

// TestGetPlugin_Found verifies GetPlugin returns info for an injected plugin.
func TestGetPlugin_Found(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	m := mgr.(*managerImpl)

	impl := &pluginImpl{
		info: PluginInfo{Name: "found-one", Status: PluginStatusActive},
	}
	m.mu.Lock()
	m.plugins["found-one"] = impl
	m.mu.Unlock()

	h := NewPluginMCPHandler(mgr)
	result, err := h.GetPlugin(context.Background(), GetPluginParams{Name: "found-one"})
	if err != nil {
		t.Fatalf("GetPlugin: %v", err)
	}
	if result.Plugin.Name != "found-one" {
		t.Errorf("unexpected plugin name: %s", result.Plugin.Name)
	}
}

// TestUnloadPlugin_Success verifies UnloadPlugin succeeds for an injected plugin
// (wasm=nil, db=nil for simplicity).
func TestUnloadPlugin_Success(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	m := mgr.(*managerImpl)

	impl := &pluginImpl{
		info: PluginInfo{Name: "to-unload", Status: PluginStatusActive},
	}
	m.mu.Lock()
	m.plugins["to-unload"] = impl
	m.mu.Unlock()

	h := NewPluginMCPHandler(mgr)
	result, err := h.UnloadPlugin(context.Background(), UnloadPluginParams{Name: "to-unload"})
	if err != nil {
		t.Fatalf("UnloadPlugin: %v", err)
	}
	if !result.Success {
		t.Error("expected Success=true")
	}
}

// TestReload_Success exercises Reload via a fake plugin (skips WASM load phase).
// We use Reload from the manager directly; the Load call in Reload will fail
// because there is no valid plugin dir: that is expected and we test the
// error path that includes ErrPluginNotFound after successful Unload.
func TestReload_FromInjectedPlugin(t *testing.T) {
	mgr, pluginRoot, _ := newTestManager(t)
	m := mgr.(*managerImpl)

	// Inject a plugin pointing at a dir that does NOT have valid files.
	// Reload will Unload first (succeeds), then Load (will fail with ErrManifestInvalid
	// or ErrPluginDirTraversal). We just verify the Reload path is exercised.
	fakeDir := filepath.Join(pluginRoot, "fake-reload")
	impl := &pluginImpl{
		info: PluginInfo{
			Name:   "fake-reload",
			Status: PluginStatusActive,
			Dir:    fakeDir,
		},
	}
	m.mu.Lock()
	m.plugins["fake-reload"] = impl
	m.mu.Unlock()

	_, err := mgr.Reload(context.Background(), "fake-reload")
	// We expect an error from the Load phase (manifest missing), not ErrPluginNotFound.
	if errors.Is(err, ErrPluginNotFound) {
		t.Errorf("Reload should not return ErrPluginNotFound when plugin was present, got %v", err)
	}
	if err == nil {
		t.Log("Reload unexpectedly succeeded (fake dir happened to be valid?)")
	}
}

// TestReload_ViaHandler exercises the ReloadPlugin MCP handler error path.
func TestReloadPlugin_ViaHandler_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	_, err := h.ReloadPlugin(context.Background(), ReloadPluginParams{Name: "ghost"})
	if !errors.Is(err, ErrPluginNotFound) {
		t.Errorf("expected ErrPluginNotFound, got %v", err)
	}
}

// TestInstallPlugin_PathTraversal exercises InstallPlugin with a path
// that escapes the plugin root, expecting ErrPluginDirTraversal.
func TestInstallPlugin_PathTraversal(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	h := NewPluginMCPHandler(mgr)
	_, err := h.InstallPlugin(context.Background(), InstallPluginParams{Dir: "/etc/passwd"})
	if !errors.Is(err, ErrPluginDirTraversal) {
		t.Errorf("expected ErrPluginDirTraversal, got %v", err)
	}
}
