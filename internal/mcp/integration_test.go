package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
)

// TestFederatedEndpoint_ToolsCall_SourcePlugin_PluginTool verifies that when a
// plugin-scoped tool is called through the federated endpoint, the emitted
// MCPToolCalledPayload carries the correct PluginName (not "").
func TestFederatedEndpoint_ToolsCall_SourcePlugin_PluginTool(t *testing.T) {
	mgr := newFakeManager()
	mgr.plugins = []loader.PluginInfo{{Name: "test-plugin"}}
	mgr.meta["test-plugin"] = loader.PluginMCPMeta{
		PluginName: "test-plugin",
		Tools:      []loader.MCPTool{{Name: "do_thing", Description: "does a thing"}},
	}
	mgr.callOut["test-plugin/do_thing"] = []byte(`{"ok":true}`)

	bus := newFakeBus()
	handler := &fakeHandlerAdapter{}
	disp := NewFederatedDispatcher(mgr, handler)
	sessions := NewSessionStore()
	ep := NewFederatedMCPEndpoint(disp, sessions, bus)

	params := ToolsCallParams{Name: "test_plugin__do_thing", Arguments: json.RawMessage(`{}`)}
	body := buildRequest(t, "tools/call", 1, params)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// bus.Emit is called from a goroutine; poll until the event arrives.
	if !waitForEvent(bus, func(ev eventbus.Event) bool {
		return ev.Name == eventbus.EventMCPToolCalled
	}, 2*time.Second) {
		t.Fatal("expected EventMCPToolCalled to be emitted")
	}

	events := bus.collected()
	for _, ev := range events {
		if ev.Name != eventbus.EventMCPToolCalled {
			continue
		}
		if ev.SourcePlugin != "test_plugin" {
			t.Errorf("Event.SourcePlugin = %q, want %q", ev.SourcePlugin, "test_plugin")
		}
		p, ok := ev.Payload.(eventbus.MCPToolCalledPayload)
		if !ok {
			t.Fatalf("payload type = %T, want MCPToolCalledPayload", ev.Payload)
		}
		if p.PluginName != "test_plugin" {
			t.Errorf("Payload.PluginName = %q, want %q", p.PluginName, "test_plugin")
		}
	}
}

// TestFederatedEndpoint_ToolsCall_SourcePlugin_BuiltinTool verifies that
// built-in tools (no "/" separator) emit PluginName == "".
func TestFederatedEndpoint_ToolsCall_SourcePlugin_BuiltinTool(t *testing.T) {
	mgr := newFakeManager()
	bus := newFakeBus()
	handler := &fakeHandlerAdapter{}
	disp := NewFederatedDispatcher(mgr, handler)
	sessions := NewSessionStore()
	ep := NewFederatedMCPEndpoint(disp, sessions, bus)

	params := ToolsCallParams{Name: "list_plugins", Arguments: json.RawMessage(`{}`)}
	body := buildRequest(t, "tools/call", 1, params)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// bus.Emit is called from a goroutine; poll until the event arrives.
	if !waitForEvent(bus, func(ev eventbus.Event) bool {
		return ev.Name == eventbus.EventMCPToolCalled
	}, 2*time.Second) {
		t.Fatal("expected EventMCPToolCalled to be emitted")
	}

	events := bus.collected()
	for _, ev := range events {
		if ev.Name != eventbus.EventMCPToolCalled {
			continue
		}
		if ev.SourcePlugin != "" {
			t.Errorf("Event.SourcePlugin = %q, want empty for built-in", ev.SourcePlugin)
		}
		p, ok := ev.Payload.(eventbus.MCPToolCalledPayload)
		if !ok {
			t.Fatalf("payload type = %T, want MCPToolCalledPayload", ev.Payload)
		}
		if p.PluginName != "" {
			t.Errorf("Payload.PluginName = %q, want empty for built-in", p.PluginName)
		}
	}
}

// TestFederatedDispatcher_ListTools_SkippedPlugin_NoToolsLeaked verifies that
// when GetMCPMeta fails for one plugin, only the healthy plugin's tools (plus
// builtins) appear: no panic, no leaked tools.
func TestFederatedDispatcher_ListTools_SkippedPlugin_NoToolsLeaked(t *testing.T) {
	mgr := newFakeManager()
	mgr.plugins = []loader.PluginInfo{
		{Name: "healthy-plugin"},
		{Name: "broken-plugin"},
	}
	mgr.meta["healthy-plugin"] = loader.PluginMCPMeta{
		PluginName: "healthy-plugin",
		Tools:      []loader.MCPTool{{Name: "good_tool", Description: "works"}},
	}
	// broken-plugin has no meta entry -> GetMCPMeta returns ErrPluginNotFound.

	handler := &fakeHandlerAdapter{}
	disp := NewFederatedDispatcher(mgr, handler)
	tools := disp.ListTools()

	toolNames := make(map[string]bool, len(tools))
	for _, td := range tools {
		toolNames[td.Name] = true
	}

	if !toolNames["healthy_plugin__good_tool"] {
		t.Error("expected healthy_plugin__good_tool in tool list")
	}
	// Verify no broken-plugin tools leaked.
	for name := range toolNames {
		if pn, _, ok := pluginNameFromFederatedTool(name); ok && pn == "broken-plugin" {
			t.Errorf("broken-plugin tool %q leaked into tool list", name)
		}
	}
	// Builtins must still be present.
	if !toolNames["list_plugins"] {
		t.Error("expected builtin list_plugins in tool list")
	}
}

// TestFederatedEndpoint_ToolsCall_PauseSession verifies that a
// pomodoro-plugin pause_session call dispatches correctly.
func TestFederatedEndpoint_ToolsCall_PauseSession(t *testing.T) {
	mgr := newFakeManager()
	mgr.plugins = []loader.PluginInfo{{Name: "pomodoro-plugin"}}
	mgr.meta["pomodoro-plugin"] = loader.PluginMCPMeta{
		PluginName: "pomodoro-plugin",
		Tools:      []loader.MCPTool{{Name: "pause_session", Description: "Pause an active session."}},
	}
	mgr.callOut["pomodoro-plugin/pause_session"] = []byte(`{"paused":true}`)

	handler := &fakeHandlerAdapter{}
	disp := NewFederatedDispatcher(mgr, handler)
	sessions := NewSessionStore()
	ep := NewFederatedMCPEndpoint(disp, sessions, nil)

	params := ToolsCallParams{Name: "pomodoro_plugin__pause_session", Arguments: json.RawMessage(`{}`)}
	body := buildRequest(t, "tools/call", 1, params)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Result) == 0 {
		t.Fatal("expected non-empty result")
	}
}

// TestFederatedEndpoint_ToolsCall_ResumeSession verifies that a
// pomodoro-plugin resume_session call dispatches correctly.
func TestFederatedEndpoint_ToolsCall_ResumeSession(t *testing.T) {
	mgr := newFakeManager()
	mgr.plugins = []loader.PluginInfo{{Name: "pomodoro-plugin"}}
	mgr.meta["pomodoro-plugin"] = loader.PluginMCPMeta{
		PluginName: "pomodoro-plugin",
		Tools:      []loader.MCPTool{{Name: "resume_session", Description: "Resume a paused session."}},
	}
	mgr.callOut["pomodoro-plugin/resume_session"] = []byte(`{"resumed":true}`)

	handler := &fakeHandlerAdapter{}
	disp := NewFederatedDispatcher(mgr, handler)
	sessions := NewSessionStore()
	ep := NewFederatedMCPEndpoint(disp, sessions, nil)

	params := ToolsCallParams{Name: "pomodoro_plugin__resume_session", Arguments: json.RawMessage(`{}`)}
	body := buildRequest(t, "tools/call", 1, params)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Result) == 0 {
		t.Fatal("expected non-empty result")
	}
}

// TestFederatedEndpoint_ToolsCall_ToggleFavourite verifies that a
// projects-plugin toggle_favourite call dispatches correctly.
func TestFederatedEndpoint_ToolsCall_ToggleFavourite(t *testing.T) {
	mgr := newFakeManager()
	mgr.plugins = []loader.PluginInfo{{Name: "projects-plugin"}}
	mgr.meta["projects-plugin"] = loader.PluginMCPMeta{
		PluginName: "projects-plugin",
		Tools:      []loader.MCPTool{{Name: "toggle_favourite", Description: "Toggle the favourite flag."}},
	}
	mgr.callOut["projects-plugin/toggle_favourite"] = []byte(`{"favourite":true}`)

	handler := &fakeHandlerAdapter{}
	disp := NewFederatedDispatcher(mgr, handler)
	sessions := NewSessionStore()
	ep := NewFederatedMCPEndpoint(disp, sessions, nil)

	params := ToolsCallParams{Name: "projects_plugin__toggle_favourite", Arguments: json.RawMessage(`{"id":1}`)}
	body := buildRequest(t, "tools/call", 1, params)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Result) == 0 {
		t.Fatal("expected non-empty result")
	}
}
