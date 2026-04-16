package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
)

// --- fakeManager implements loader.PluginManager for dispatch tests ---

type fakeManager struct {
	plugins []loader.PluginInfo
	meta    map[string]loader.PluginMCPMeta
	callOut map[string][]byte
	callErr map[string]error
}

func newFakeManager() *fakeManager {
	return &fakeManager{
		meta:    make(map[string]loader.PluginMCPMeta),
		callOut: make(map[string][]byte),
		callErr: make(map[string]error),
	}
}

func (f *fakeManager) Load(_ context.Context, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (f *fakeManager) Unload(_ context.Context, _ string) error { return nil }
func (f *fakeManager) Reload(_ context.Context, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (f *fakeManager) Get(_ string) (loader.Plugin, error) { return nil, loader.ErrPluginNotFound }
func (f *fakeManager) List() []loader.PluginInfo           { return f.plugins }
func (f *fakeManager) GetMCPMeta(name string) (loader.PluginMCPMeta, error) {
	m, ok := f.meta[name]
	if !ok {
		return loader.PluginMCPMeta{}, loader.ErrPluginNotFound
	}
	return m, nil
}
func (f *fakeManager) CallPlugin(_ context.Context, name, fn string, input []byte) ([]byte, error) {
	key := name + "/" + fn
	if err, ok := f.callErr[key]; ok {
		return nil, err
	}
	if out, ok := f.callOut[key]; ok {
		return out, nil
	}
	return nil, loader.ErrPluginNotFound
}
func (f *fakeManager) GetManifest(_ string) (loader.Manifest, error) {
	return loader.Manifest{}, loader.ErrPluginNotFound
}
func (f *fakeManager) ScanDir(_ context.Context) ([]loader.DiscoveredPlugin, error) {
	return nil, nil
}
func (f *fakeManager) Shutdown(_ context.Context) error { return nil }
func (f *fakeManager) PluginDB(_ string) (*sql.DB, error) {
	return nil, loader.ErrPluginNotFound
}

func (f *fakeManager) InstallFromURL(_ context.Context, _, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (f *fakeManager) DownloadAndUnpack(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

// --- fakeHandler stubs PluginMCPHandler methods ---

type fakeHandler struct{}

func (fakeHandler) ListPlugins(_ context.Context, _ loader.ListPluginsParams) (loader.ListPluginsResult, error) {
	return loader.ListPluginsResult{Plugins: []loader.PluginInfo{{Name: "p1"}}}, nil
}
func (fakeHandler) InstallPlugin(_ context.Context, _ loader.InstallPluginParams) (loader.InstallPluginResult, error) {
	return loader.InstallPluginResult{Plugin: loader.PluginInfo{Name: "installed"}}, nil
}
func (fakeHandler) UnloadPlugin(_ context.Context, _ loader.UnloadPluginParams) (loader.UnloadPluginResult, error) {
	return loader.UnloadPluginResult{Success: true}, nil
}
func (fakeHandler) ReloadPlugin(_ context.Context, _ loader.ReloadPluginParams) (loader.ReloadPluginResult, error) {
	return loader.ReloadPluginResult{Plugin: loader.PluginInfo{Name: "reloaded"}}, nil
}
func (fakeHandler) GetPlugin(_ context.Context, _ loader.GetPluginParams) (loader.GetPluginResult, error) {
	return loader.GetPluginResult{Plugin: loader.PluginInfo{Name: "myplugin"}}, nil
}

// --- tests ---

func TestPluginNameFromFederatedTool(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantPlugin string
		wantTool   string
		wantOK     bool
	}{
		{
			name:       "valid prefix",
			input:      "myplugin__do_thing",
			wantPlugin: "myplugin",
			wantTool:   "do_thing",
			wantOK:     true,
		},
		{
			name:       "no prefix",
			input:      "list_plugins",
			wantPlugin: "",
			wantTool:   "",
			wantOK:     false,
		},
		{
			name:       "multiple underscores in tool name",
			input:      "myplugin__some_complex_tool_name",
			wantPlugin: "myplugin",
			wantTool:   "some_complex_tool_name",
			wantOK:     true,
		},
		{
			name:       "double underscore only",
			input:      "__",
			wantPlugin: "",
			wantTool:   "",
			wantOK:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plugin, tool, ok := pluginNameFromFederatedTool(tc.input)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok {
				if plugin != tc.wantPlugin {
					t.Errorf("pluginName = %q, want %q", plugin, tc.wantPlugin)
				}
				if tool != tc.wantTool {
					t.Errorf("toolName = %q, want %q", tool, tc.wantTool)
				}
			}
		})
	}
}

func TestValidatePluginName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid lowercase", "myplugin", false},
		{"valid with dash", "my-plugin", false},
		{"valid with underscore", "my_plugin", false},
		{"valid with numbers", "plugin123", false},
		{"uppercase rejected", "MyPlugin", true},
		{"space rejected", "my plugin", true},
		{"dot rejected", "my.plugin", true},
		{"slash rejected", "my/plugin", true},
		{"empty rejected", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePluginName(tc.input)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			// All errors must wrap ErrInvalidPluginName so ErrorCode
			// maps them to CodeInvalidParams / HTTP 400 (not 500).
			if err != nil && !errors.Is(err, ErrInvalidPluginName) {
				t.Errorf("error does not wrap ErrInvalidPluginName: %v", err)
			}
		})
	}
}

func TestPluginDispatcher_ListTools(t *testing.T) {
	meta := loader.PluginMCPMeta{
		PluginName: "myplugin",
		Tools: []loader.MCPTool{
			{Name: "tool_a", Description: "does a"},
			{Name: "tool_b", Description: "does b"},
		},
	}
	mgr := newFakeManager()
	d := NewPluginDispatcher(mgr, meta)

	tools := d.ListTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "tool_a" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "tool_a")
	}
	if tools[1].Description != "does b" {
		t.Errorf("tools[1].Description = %q, want %q", tools[1].Description, "does b")
	}
}

func TestPluginDispatcher_Dispatch_Success(t *testing.T) {
	meta := loader.PluginMCPMeta{
		PluginName: "myplugin",
		Tools:      []loader.MCPTool{{Name: "do_thing", Description: "does thing"}},
	}
	mgr := newFakeManager()
	mgr.callOut["myplugin/do_thing"] = []byte(`{"result":"ok"}`)

	d := NewPluginDispatcher(mgr, meta)
	result, err := d.Dispatch(context.Background(), "do_thing", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content items, got none")
	}
	if result.Content[0].Type != "text" {
		t.Errorf("Content[0].Type = %q, want text", result.Content[0].Type)
	}
	if result.Content[0].Text != `{"result":"ok"}` {
		t.Errorf("Content[0].Text = %q, want %q", result.Content[0].Text, `{"result":"ok"}`)
	}
	if result.IsError {
		t.Error("IsError should be false on success")
	}
}

func TestPluginDispatcher_Dispatch_ToolNotFound(t *testing.T) {
	meta := loader.PluginMCPMeta{
		PluginName: "myplugin",
		Tools:      []loader.MCPTool{{Name: "do_thing", Description: "does thing"}},
	}
	mgr := newFakeManager()
	// callOut key for "myplugin__no_such_tool" doesn't exist → ErrPluginNotFound from fakeManager.
	d := NewPluginDispatcher(mgr, meta)
	_, err := d.Dispatch(context.Background(), "no_such_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
}

func TestPluginDispatcher_Dispatch_ManagerError(t *testing.T) {
	meta := loader.PluginMCPMeta{
		PluginName: "myplugin",
		Tools:      []loader.MCPTool{{Name: "fail_tool", Description: "fails"}},
	}
	mgr := newFakeManager()
	mgr.callErr["myplugin__fail_tool"] = errors.New("wasm crash")

	d := NewPluginDispatcher(mgr, meta)
	_, err := d.Dispatch(context.Background(), "fail_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when manager returns error")
	}
}

func TestFederatedDispatcher_ListTools(t *testing.T) {
	mgr := newFakeManager()
	mgr.plugins = []loader.PluginInfo{
		{Name: "plugin-a"},
		{Name: "plugin-b"},
	}
	mgr.meta["plugin-a"] = loader.PluginMCPMeta{
		PluginName: "plugin-a",
		Tools:      []loader.MCPTool{{Name: "tool_x", Description: "x"}},
	}
	mgr.meta["plugin-b"] = loader.PluginMCPMeta{
		PluginName: "plugin-b",
		Tools:      []loader.MCPTool{{Name: "tool_y", Description: "y"}},
	}

	handler := &fakeHandlerAdapter{}
	d := NewFederatedDispatcher(mgr, handler)
	tools := d.ListTools()

	// Should contain plugin_a__tool_x, plugin_b__tool_y, plus 5 builtins.
	toolNames := make(map[string]bool)
	for _, t := range tools {
		toolNames[t.Name] = true
	}

	if !toolNames["plugin_a__tool_x"] {
		t.Error("expected plugin_a__tool_x in federated tool list")
	}
	if !toolNames["plugin_b__tool_y"] {
		t.Error("expected plugin_b__tool_y in federated tool list")
	}
	// Builtins should be present without prefix.
	builtins := []string{"list_plugins", "install_plugin", "unload_plugin", "reload_plugin", "get_plugin"}
	for _, b := range builtins {
		if !toolNames[b] {
			t.Errorf("expected builtin %q in federated tool list", b)
		}
	}
}

func TestFederatedDispatcher_Dispatch_Plugin(t *testing.T) {
	mgr := newFakeManager()
	mgr.plugins = []loader.PluginInfo{{Name: "plugin-a"}}
	mgr.meta["plugin-a"] = loader.PluginMCPMeta{
		PluginName: "plugin-a",
		Tools:      []loader.MCPTool{{Name: "do_it", Description: "does it"}},
	}
	mgr.callOut["plugin-a/do_it"] = []byte(`"success"`)

	handler := &fakeHandlerAdapter{}
	d := NewFederatedDispatcher(mgr, handler)

	result, err := d.Dispatch(context.Background(), "plugin_a__do_it", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content items")
	}
	if result.Content[0].Text != `"success"` {
		t.Errorf("Content[0].Text = %q, want %q", result.Content[0].Text, `"success"`)
	}
}

func TestFederatedDispatcher_Dispatch_Builtin_ListPlugins(t *testing.T) {
	mgr := newFakeManager()
	handler := &fakeHandlerAdapter{}
	d := NewFederatedDispatcher(mgr, handler)

	args := json.RawMessage(`{}`)
	result, err := d.Dispatch(context.Background(), "list_plugins", args)
	if err != nil {
		t.Fatalf("Dispatch list_plugins: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content from list_plugins")
	}
}

func TestFederatedDispatcher_Dispatch_UnknownBuiltin(t *testing.T) {
	mgr := newFakeManager()
	handler := &fakeHandlerAdapter{}
	d := NewFederatedDispatcher(mgr, handler)

	_, err := d.Dispatch(context.Background(), "unknown_builtin", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown builtin, got nil")
	}
	if !errors.Is(err, ErrUnsupportedMethod) {
		t.Errorf("expected ErrUnsupportedMethod, got %v", err)
	}
}

// fakeHandlerAdapter wraps fakeHandler to implement the SystemHandler interface used by dispatch.
type fakeHandlerAdapter struct{}

func (f *fakeHandlerAdapter) ListPlugins(ctx context.Context, p loader.ListPluginsParams) (loader.ListPluginsResult, error) {
	return fakeHandler{}.ListPlugins(ctx, p)
}
func (f *fakeHandlerAdapter) InstallPlugin(ctx context.Context, p loader.InstallPluginParams) (loader.InstallPluginResult, error) {
	return fakeHandler{}.InstallPlugin(ctx, p)
}
func (f *fakeHandlerAdapter) UnloadPlugin(ctx context.Context, p loader.UnloadPluginParams) (loader.UnloadPluginResult, error) {
	return fakeHandler{}.UnloadPlugin(ctx, p)
}
func (f *fakeHandlerAdapter) ReloadPlugin(ctx context.Context, p loader.ReloadPluginParams) (loader.ReloadPluginResult, error) {
	return fakeHandler{}.ReloadPlugin(ctx, p)
}
func (f *fakeHandlerAdapter) GetPlugin(ctx context.Context, p loader.GetPluginParams) (loader.GetPluginResult, error) {
	return fakeHandler{}.GetPlugin(ctx, p)
}
