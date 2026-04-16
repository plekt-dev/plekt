package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
)

// fakeBus is a synchronous-recording EventBus for testing.
// Emit records events immediately (no goroutine) so tests can assert without racing.
type fakeBus struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func newFakeBus() *fakeBus { return &fakeBus{} }

func (b *fakeBus) Emit(_ context.Context, ev eventbus.Event) {
	b.mu.Lock()
	b.events = append(b.events, ev)
	b.mu.Unlock()
}

func (b *fakeBus) Subscribe(_ string, _ eventbus.Handler) eventbus.Subscription {
	return eventbus.Subscription{}
}
func (b *fakeBus) Unsubscribe(_ eventbus.Subscription) {}
func (b *fakeBus) Close() error                        { return nil }

func (b *fakeBus) collected() []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]eventbus.Event, len(b.events))
	copy(cp, b.events)
	return cp
}

// waitForEvent polls collected events until the predicate returns true or timeout.
func waitForEvent(b *fakeBus, pred func(eventbus.Event) bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, ev := range b.collected() {
			if pred(ev) {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// --- PluginMCPEndpoint EventBus tests ---

func TestPluginMCPEndpoint_EmitsToolCalledEvent_OnSuccess(t *testing.T) {
	bus := newFakeBus()
	disp := &fakeDispatcher{
		dispatchOut: ToolsCallResult{
			Content: []ContentItem{{Type: "text", Text: "ok"}},
		},
	}
	sessions := NewSessionStore()
	ep := NewPluginMCPEndpoint("testplugin", disp, sessions, bus)

	params := ToolsCallParams{Name: "my_tool", Arguments: json.RawMessage(`{}`)}
	body := buildRequest(t, "tools/call", 1, params)
	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200", w.Code)
	}

	// fakeBus.Emit is synchronous, so no polling needed.
	events := bus.collected()
	var found bool
	for _, ev := range events {
		if ev.Name == eventbus.EventMCPToolCalled {
			found = true
			p, ok := ev.Payload.(eventbus.MCPToolCalledPayload)
			if !ok {
				t.Fatalf("payload type = %T, want MCPToolCalledPayload", ev.Payload)
			}
			if p.ToolName != "my_tool" {
				t.Errorf("ToolName = %q, want my_tool", p.ToolName)
			}
			if p.PluginName != "testplugin" {
				t.Errorf("PluginName = %q, want testplugin", p.PluginName)
			}
			if p.IsError {
				t.Error("IsError should be false on success")
			}
		}
	}
	if !found {
		t.Fatal("expected EventMCPToolCalled to be emitted, but it was not")
	}
}

func TestPluginMCPEndpoint_EmitsToolCalledEvent_OnDispatchError(t *testing.T) {
	bus := newFakeBus()
	disp := &fakeDispatcher{
		dispatchErr: ErrUnsupportedMethod,
	}
	sessions := NewSessionStore()
	ep := NewPluginMCPEndpoint("testplugin", disp, sessions, bus)

	params := ToolsCallParams{Name: "bad_tool"}
	body := buildRequest(t, "tools/call", 2, params)
	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code == http.StatusOK {
		t.Fatal("expected non-200 HTTP status on dispatch error")
	}

	events := bus.collected()
	var found bool
	for _, ev := range events {
		if ev.Name == eventbus.EventMCPToolCalled {
			found = true
			p, ok := ev.Payload.(eventbus.MCPToolCalledPayload)
			if !ok {
				t.Fatalf("payload type = %T, want MCPToolCalledPayload", ev.Payload)
			}
			if !p.IsError {
				t.Error("IsError should be true on dispatch error")
			}
		}
	}
	if !found {
		t.Fatal("expected EventMCPToolCalled to be emitted on error too")
	}
}

func TestPluginMCPEndpoint_NilBus_NoEmitPanic(t *testing.T) {
	// When bus is nil, no event is emitted and no panic occurs.
	disp := &fakeDispatcher{
		dispatchOut: ToolsCallResult{
			Content: []ContentItem{{Type: "text", Text: "ok"}},
		},
	}
	sessions := NewSessionStore()
	ep := NewPluginMCPEndpoint("testplugin", disp, sessions, nil)

	params := ToolsCallParams{Name: "my_tool", Arguments: json.RawMessage(`{}`)}
	body := buildRequest(t, "tools/call", 1, params)
	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()

	// Must not panic.
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want 200", w.Code)
	}
}

// --- FederatedMCPEndpoint EventBus tests ---

func TestFederatedMCPEndpoint_EmitsToolCalledEvent_OnSuccess(t *testing.T) {
	bus := newFakeBus()
	disp := &fakeDispatcher{
		dispatchOut: ToolsCallResult{
			Content: []ContentItem{{Type: "text", Text: "federated"}},
		},
	}
	sessions := NewSessionStore()
	ep := NewFederatedMCPEndpoint(disp, sessions, bus)

	params := ToolsCallParams{Name: "plugin__tool", Arguments: json.RawMessage(`{}`)}
	body := buildRequest(t, "tools/call", 1, params)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200", w.Code)
	}

	// bus.Emit is called from a goroutine; poll until the event arrives.
	if !waitForEvent(bus, func(ev eventbus.Event) bool {
		return ev.Name == eventbus.EventMCPToolCalled
	}, 2*time.Second) {
		t.Fatal("expected EventMCPToolCalled to be emitted")
	}

	events := bus.collected()
	for _, ev := range events {
		if ev.Name == eventbus.EventMCPToolCalled {
			p, ok := ev.Payload.(eventbus.MCPToolCalledPayload)
			if !ok {
				t.Fatalf("payload type = %T, want MCPToolCalledPayload", ev.Payload)
			}
			if p.ToolName != "plugin__tool" {
				t.Errorf("ToolName = %q, want plugin/tool", p.ToolName)
			}
			if p.IsError {
				t.Error("IsError should be false on success")
			}
		}
	}
}

func TestFederatedMCPEndpoint_NilBus_NoEmitPanic(t *testing.T) {
	disp := &fakeDispatcher{
		dispatchOut: ToolsCallResult{
			Content: []ContentItem{{Type: "text", Text: "ok"}},
		},
	}
	sessions := NewSessionStore()
	ep := NewFederatedMCPEndpoint(disp, sessions, nil)

	params := ToolsCallParams{Name: "tool", Arguments: json.RawMessage(`{}`)}
	body := buildRequest(t, "tools/call", 1, params)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want 200", w.Code)
	}
}

// --- ErrInvalidPluginName sentinel tests ---

func TestValidatePluginName_EmptyUsesErrInvalidPluginName(t *testing.T) {
	err := validatePluginName("")
	if err == nil {
		t.Fatal("expected error for empty plugin name, got nil")
	}
	// Must NOT wrap ErrInvalidBearerToken (semantic mismatch: that maps to 401).
	if errors.Is(err, ErrInvalidBearerToken) {
		t.Error("validatePluginName empty: must not wrap ErrInvalidBearerToken (would produce 401 instead of 400)")
	}
	// Must wrap ErrInvalidPluginName.
	if !errors.Is(err, ErrInvalidPluginName) {
		t.Errorf("validatePluginName empty: expected ErrInvalidPluginName, got: %v", err)
	}
}

func TestErrInvalidPluginName_ErrorCode(t *testing.T) {
	// ErrInvalidPluginName must map to CodeInvalidParams / HTTP 400.
	rpcCode, httpStatus := ErrorCode(ErrInvalidPluginName)
	if rpcCode != CodeInvalidParams {
		t.Errorf("ErrorCode(ErrInvalidPluginName).rpcCode = %d, want %d (CodeInvalidParams)", rpcCode, CodeInvalidParams)
	}
	if httpStatus != http.StatusBadRequest {
		t.Errorf("ErrorCode(ErrInvalidPluginName).httpStatus = %d, want 400", httpStatus)
	}
}

// --- RouterConfig Bus field test ---

func TestRouterConfig_BusField_EmitsEvent(t *testing.T) {
	// Verify that RouterConfig.Bus is wired through to plugin endpoints
	// and that events are emitted on tool calls.
	bus := newFakeBus()
	mgr := newFakeManager()
	agentToken := "bus-test-agent-tok"
	mgr.plugins = []loader.PluginInfo{
		{Name: "myplugin", Status: loader.PluginStatusActive},
	}
	mgr.meta["myplugin"] = loader.PluginMCPMeta{
		PluginName: "myplugin",
		Tools:      []loader.MCPTool{{Name: "greet", Description: "says hello"}},
	}
	mgr.callOut["myplugin/greet"] = []byte(`"hello"`)

	svcForBus := newTestAgentService(agentToken, "myplugin")

	sessions := NewSessionStore()
	sysHandler := loader.NewPluginMCPHandler(mgr)
	cfg := RouterConfig{
		Manager:       mgr,
		SystemHandler: sysHandler,
		Sessions:      sessions,
		AgentService:  svcForBus,
		ServerVersion: "1.0.0",
		Bus:           bus,
	}
	router := NewMCPRouter(cfg)
	mux := router.Build(http.NewServeMux())

	// Call tools/call through the plugin endpoint with valid token.
	params := ToolsCallParams{Name: "greet", Arguments: json.RawMessage(`{}`)}
	body := buildRequest(t, "tools/call", 1, params)
	r := httptest.NewRequest(http.MethodPost, "/plugins/myplugin/mcp", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+agentToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	events := bus.collected()
	var found bool
	for _, ev := range events {
		if ev.Name == eventbus.EventMCPToolCalled {
			found = true
		}
	}
	if !found {
		t.Error("expected EventMCPToolCalled to be emitted via router-constructed endpoint")
	}
}
