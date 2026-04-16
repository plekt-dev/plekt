package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/loader"
)

// mockSystemHandler is a loader.PluginMCPHandler stand-in for router tests.
// We can't use loader.PluginMCPHandler directly without a real manager, so we
// test with a real PluginMCPHandler backed by the fakeManager.
type mockSystemHandler struct{}

// newTestAgentService creates a fakeAgentService with a single agent that has
// the given token and permissions for the given plugins.
func newTestAgentService(token string, pluginNames ...string) *fakeAgentService {
	svc := newFakeAgentService()
	perms := make([]agents.AgentPermission, 0, len(pluginNames))
	for _, pn := range pluginNames {
		perms = append(perms, agents.AgentPermission{AgentID: 1, PluginName: pn, ToolName: "*"})
	}
	svc.addAgent(agents.Agent{ID: 1, Name: "testbot", Token: token}, perms)
	return svc
}

func TestValidateURLPluginName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "my-plugin", false},
		{"valid underscore", "my_plugin", false},
		{"valid alphanumeric", "plugin123", false},
		{"uppercase", "MyPlugin", true},
		{"space", "my plugin", true},
		{"empty", "", true},
		{"dot", "my.plugin", true},
		{"slash", "my/plugin", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateURLPluginName(tc.input)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestMCPRouter_Build_FederatedEndpoint(t *testing.T) {
	agentToken := "federated-agent-token"
	svc := newTestAgentService(agentToken) // no plugin scope needed for federated
	mgr := newFakeManager()

	// Build a router with a real PluginMCPHandler backed by fakeManager.
	sessions := NewSessionStore()
	sysHandler := loader.NewPluginMCPHandler(mgr)
	cfg := RouterConfig{
		Manager:       mgr,
		SystemHandler: sysHandler,
		Sessions:      sessions,
		AgentService:  svc,
		ServerVersion: "1.0.0",
	}
	router := NewMCPRouter(cfg)
	mux := router.Build(http.NewServeMux())

	// Test: POST /mcp without token → 401.
	t.Run("federated no token", func(t *testing.T) {
		body := buildRequest(t, "initialize", 1, InitializeParams{
			ProtocolVersion: "2025-03-26",
			ClientInfo:      ClientInfo{Name: "test", Version: "1.0"},
		})
		r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("HTTP status = %d, want 401", w.Code)
		}
	})

	// Test: POST /mcp with wrong token → 401.
	t.Run("federated wrong token", func(t *testing.T) {
		body := buildRequest(t, "initialize", 1, InitializeParams{
			ProtocolVersion: "2025-03-26",
			ClientInfo:      ClientInfo{Name: "test", Version: "1.0"},
		})
		r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer wrongtoken")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("HTTP status = %d, want 401", w.Code)
		}
	})

	// Test: POST /mcp with correct token → 200.
	t.Run("federated correct token", func(t *testing.T) {
		body := buildRequest(t, "initialize", 1, InitializeParams{
			ProtocolVersion: "2025-03-26",
			ClientInfo:      ClientInfo{Name: "test", Version: "1.0"},
		})
		r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+agentToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("HTTP status = %d, want 200; body: %s", w.Code, w.Body.String())
		}
	})
}

func TestMCPRouter_Build_PluginEndpoint_InvalidName(t *testing.T) {
	svc := newTestAgentService("any-token")
	mgr := newFakeManager()
	sessions := NewSessionStore()
	sysHandler := loader.NewPluginMCPHandler(mgr)
	cfg := RouterConfig{
		Manager:       mgr,
		SystemHandler: sysHandler,
		Sessions:      sessions,
		AgentService:  svc,
	}
	router := NewMCPRouter(cfg)
	mux := router.Build(http.NewServeMux())

	// Plugin name with uppercase should be rejected.
	body := buildRequest(t, "initialize", 1, nil)
	r := httptest.NewRequest(http.MethodPost, "/plugins/BadPlugin/mcp", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer sometoken")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	// Should get 400 (invalid params from validateURLPluginName).
	if w.Code != http.StatusBadRequest {
		t.Errorf("HTTP status = %d, want 400 for invalid plugin name", w.Code)
	}
}

func TestMCPRouter_Build_PluginEndpoint_NotFound(t *testing.T) {
	agentToken := "my-agent-token"
	svc := newTestAgentService(agentToken, "myplugin")
	mgr := newFakeManager()
	// No plugins loaded: GetMCPMeta("myplugin") returns ErrPluginNotFound.
	sessions := NewSessionStore()
	sysHandler := loader.NewPluginMCPHandler(mgr)
	cfg := RouterConfig{
		Manager:       mgr,
		SystemHandler: sysHandler,
		Sessions:      sessions,
		AgentService:  svc,
	}
	router := NewMCPRouter(cfg)
	mux := router.Build(http.NewServeMux())

	body := buildRequest(t, "initialize", 1, nil)
	r := httptest.NewRequest(http.MethodPost, "/plugins/myplugin/mcp", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+agentToken)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("HTTP status = %d, want 404 for unknown plugin", w.Code)
	}
}

func TestMCPRouter_Build_PluginEndpoint_AuthAndDispatch(t *testing.T) {
	pluginToken := "plugin-agent-token-xyz"

	mgr := newFakeManager()
	mgr.plugins = []loader.PluginInfo{
		{Name: "myplugin", Status: loader.PluginStatusActive},
	}
	mgr.meta["myplugin"] = loader.PluginMCPMeta{
		PluginName: "myplugin",
		Tools:      []loader.MCPTool{{Name: "greet", Description: "says hello"}},
	}
	mgr.callOut["myplugin/greet"] = []byte(`"hello"`)

	svc := newTestAgentService(pluginToken, "myplugin")

	sessions := NewSessionStore()
	sysHandler := loader.NewPluginMCPHandler(mgr)
	cfg := RouterConfig{
		Manager:       mgr,
		SystemHandler: sysHandler,
		Sessions:      sessions,
		AgentService:  svc,
	}
	router := NewMCPRouter(cfg)
	mux := router.Build(http.NewServeMux())

	// Without token → 401.
	t.Run("no token", func(t *testing.T) {
		body := buildRequest(t, "initialize", 1, nil)
		r := httptest.NewRequest(http.MethodPost, "/plugins/myplugin/mcp", bytes.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("HTTP status = %d, want 401", w.Code)
		}
	})

	// Wrong token → 401.
	t.Run("wrong token", func(t *testing.T) {
		body := buildRequest(t, "initialize", 1, nil)
		r := httptest.NewRequest(http.MethodPost, "/plugins/myplugin/mcp", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer wrongtoken")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("HTTP status = %d, want 401", w.Code)
		}
	})

	// Correct token + initialize → 200.
	t.Run("correct token initialize", func(t *testing.T) {
		body := buildRequest(t, "initialize", 1, InitializeParams{
			ProtocolVersion: "2025-03-26",
			ClientInfo:      ClientInfo{Name: "test", Version: "1.0"},
		})
		r := httptest.NewRequest(http.MethodPost, "/plugins/myplugin/mcp", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+pluginToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("HTTP status = %d, want 200; body: %s", w.Code, w.Body.String())
		}
	})

	// tools/call with correct token → dispatch result.
	t.Run("tools call greet", func(t *testing.T) {
		params := ToolsCallParams{Name: "greet", Arguments: json.RawMessage(`{}`)}
		body := buildRequest(t, "tools/call", 2, params)
		r := httptest.NewRequest(http.MethodPost, "/plugins/myplugin/mcp", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+pluginToken)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("HTTP status = %d, want 200; body: %s", w.Code, w.Body.String())
		}
		var resp Response
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var result ToolsCallResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if len(result.Content) == 0 {
			t.Fatal("expected content items")
		}
		if result.Content[0].Text != `"hello"` {
			t.Errorf("content = %q, want %q", result.Content[0].Text, `"hello"`)
		}
	})
}

// fakeManager also needs to implement loader.PluginManager fully: verify CallPlugin is routed.
func TestFakeManager_CallPlugin(t *testing.T) {
	mgr := newFakeManager()
	mgr.callOut["myplugin/fn"] = []byte("output")
	out, err := mgr.CallPlugin(context.Background(), "myplugin", "fn", nil)
	if err != nil {
		t.Fatalf("CallPlugin: %v", err)
	}
	if string(out) != "output" {
		t.Errorf("output = %q, want %q", out, "output")
	}
}
