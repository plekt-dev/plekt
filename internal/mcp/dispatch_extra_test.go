package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
)

// Test all 5 builtin tool dispatches through the federated dispatcher.
func TestFederatedDispatcher_Dispatch_AllBuiltins(t *testing.T) {
	mgr := newFakeManager()
	handler := &fakeHandlerAdapter{}
	d := NewFederatedDispatcher(mgr, handler)

	builtinCases := []struct {
		name string
		args json.RawMessage
	}{
		{"list_plugins", json.RawMessage(`{}`)},
		{"install_plugin", json.RawMessage(`{"dir":"/some/dir"}`)},
		{"unload_plugin", json.RawMessage(`{"name":"p"}`)},
		{"reload_plugin", json.RawMessage(`{"name":"p"}`)},
		{"get_plugin", json.RawMessage(`{"name":"p"}`)},
	}

	for _, tc := range builtinCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := d.Dispatch(context.Background(), tc.name, tc.args)
			if err != nil {
				t.Fatalf("Dispatch(%q): %v", tc.name, err)
			}
			if len(result.Content) == 0 {
				t.Errorf("Dispatch(%q): expected content, got none", tc.name)
			}
		})
	}
}

// Test builtin dispatch with invalid JSON params.
func TestFederatedDispatcher_Dispatch_BuiltinInvalidParams(t *testing.T) {
	mgr := newFakeManager()
	handler := &fakeHandlerAdapter{}
	d := NewFederatedDispatcher(mgr, handler)

	// All builtins that unmarshal params.
	builtins := []string{
		"list_plugins", "install_plugin", "unload_plugin",
		"reload_plugin", "get_plugin",
	}
	// Builtins use lenient unmarshal (extra fields ok), so test with truly invalid JSON.
	for _, name := range builtins {
		t.Run(name+"_invalid_json", func(t *testing.T) {
			_, err := d.Dispatch(context.Background(), name, json.RawMessage(`{invalid`))
			if err == nil {
				t.Errorf("Dispatch(%q) with invalid JSON: expected error, got nil", name)
			}
		})
	}
}

// Test builtin handler error propagation for all builtins.
func TestFederatedDispatcher_Dispatch_BuiltinHandlerError(t *testing.T) {
	mgr := newFakeManager()
	handler := &errorHandler{}
	d := NewFederatedDispatcher(mgr, handler)

	builtins := []string{
		"list_plugins", "install_plugin", "unload_plugin",
		"reload_plugin", "get_plugin",
	}
	for _, name := range builtins {
		t.Run(name, func(t *testing.T) {
			_, err := d.Dispatch(context.Background(), name, json.RawMessage(`{}`))
			if err == nil {
				t.Fatalf("expected error from errorHandler for %q, got nil", name)
			}
		})
	}
}

// errorHandler returns errors from all methods.
type errorHandler struct{}

func (e *errorHandler) ListPlugins(_ context.Context, _ loader.ListPluginsParams) (loader.ListPluginsResult, error) {
	return loader.ListPluginsResult{}, errors.New("list error")
}
func (e *errorHandler) InstallPlugin(_ context.Context, _ loader.InstallPluginParams) (loader.InstallPluginResult, error) {
	return loader.InstallPluginResult{}, errors.New("install error")
}
func (e *errorHandler) UnloadPlugin(_ context.Context, _ loader.UnloadPluginParams) (loader.UnloadPluginResult, error) {
	return loader.UnloadPluginResult{}, errors.New("unload error")
}
func (e *errorHandler) ReloadPlugin(_ context.Context, _ loader.ReloadPluginParams) (loader.ReloadPluginResult, error) {
	return loader.ReloadPluginResult{}, errors.New("reload error")
}
func (e *errorHandler) GetPlugin(_ context.Context, _ loader.GetPluginParams) (loader.GetPluginResult, error) {
	return loader.GetPluginResult{}, errors.New("get error")
}

// Test ListTools when GetMCPMeta fails (plugin not found).
func TestFederatedDispatcher_ListTools_SkipsFailedMeta(t *testing.T) {
	mgr := newFakeManager()
	mgr.plugins = []loader.PluginInfo{
		{Name: "ghost"}, // no meta registered
	}
	handler := &fakeHandlerAdapter{}
	d := NewFederatedDispatcher(mgr, handler)

	tools := d.ListTools()
	// Should only have builtins (5), no plugin tools.
	pluginPrefixCount := 0
	for _, t := range tools {
		if _, _, ok := pluginNameFromFederatedTool(t.Name); ok {
			pluginPrefixCount++
		}
	}
	if pluginPrefixCount != 0 {
		t.Errorf("expected 0 plugin tools for failed meta, got %d", pluginPrefixCount)
	}
	if len(tools) != 5 {
		t.Errorf("expected 5 builtin tools, got %d", len(tools))
	}
}

// Test pluginDispatcher ListTools with InputSchema.
func TestPluginDispatcher_ListTools_WithInputSchema(t *testing.T) {
	schemaJSON := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	meta := loader.PluginMCPMeta{
		PluginName: "myplugin",
		Tools: []loader.MCPTool{
			{Name: "tool_a", Description: "does a", InputSchema: schemaJSON},
		},
	}
	mgr := newFakeManager()
	d := NewPluginDispatcher(mgr, meta)

	tools := d.ListTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].InputSchema == nil {
		t.Error("expected InputSchema to be set")
	}
}

// Test error messages via Error() methods.
func TestSentinelErrors_ErrorStrings(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&parseErr{}, "parse error"},
		{&invalidRequestErr{}, "invalid request"},
		{&invalidParamsErr{}, "invalid params"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if tc.err.Error() != tc.want {
				t.Errorf("Error() = %q, want %q", tc.err.Error(), tc.want)
			}
		})
	}
}

// Test safeErrorMessage covers remaining branches.
func TestSafeErrorMessage_AllBranches(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{loader.ErrPluginNotReady, "plugin not ready"},
		{loader.ErrPermissionDenied, "permission denied"},
		{loader.ErrManifestInvalid, "invalid request parameters"},
		{loader.ErrPluginDirTraversal, "invalid request parameters"},
		{ErrSessionNotFound, "session not found"},
		{&parseErr{}, "parse error"},
		{&invalidRequestErr{}, "invalid request"},
		{&invalidParamsErr{}, "invalid parameters"},
		{errors.New("unknown"), "internal server error"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			got := safeErrorMessage(tc.err)
			if got != tc.want {
				t.Errorf("safeErrorMessage(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
