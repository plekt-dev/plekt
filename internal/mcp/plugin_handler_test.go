package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
)

// --- fakeDispatcher ---

type fakeDispatcher struct {
	tools       []ToolDescriptor
	dispatchOut ToolsCallResult
	dispatchErr error
}

func (f *fakeDispatcher) Dispatch(_ context.Context, name string, _ json.RawMessage) (ToolsCallResult, error) {
	if f.dispatchErr != nil {
		return ToolsCallResult{}, f.dispatchErr
	}
	return f.dispatchOut, nil
}

func (f *fakeDispatcher) ListTools() []ToolDescriptor {
	return f.tools
}

// helper to build a JSON-RPC request body
func buildRequest(t *testing.T, method string, id any, params any) []byte {
	t.Helper()
	idJSON, _ := json.Marshal(id)
	req := map[string]json.RawMessage{
		"jsonrpc": json.RawMessage(`"2.0"`),
		"id":      idJSON,
		"method":  json.RawMessage(`"` + method + `"`),
	}
	if params != nil {
		p, _ := json.Marshal(params)
		req["params"] = p
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	return b
}

func newTestPluginEndpoint(t *testing.T, disp Dispatcher) *PluginMCPEndpoint {
	t.Helper()
	sessions := NewSessionStore()
	return NewPluginMCPEndpoint("testplugin", disp, sessions, nil)
}

func TestPluginMCPEndpoint_NonPost(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestPluginEndpoint(t, disp)

	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, m := range methods {
		t.Run(m, func(t *testing.T) {
			r := httptest.NewRequest(m, "/plugins/testplugin/mcp", nil)
			w := httptest.NewRecorder()
			ep.ServeHTTP(w, r)
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("method %s: HTTP status = %d, want 405", m, w.Code)
			}
		})
	}
}

func TestPluginMCPEndpoint_ParseError(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestPluginEndpoint(t, disp)

	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader([]byte("not json {")))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != HTTPStatusParseError {
		t.Errorf("HTTP status = %d, want %d", w.Code, HTTPStatusParseError)
	}
	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Code != CodeParseError {
		t.Errorf("RPC code = %d, want %d", resp.Error.Code, CodeParseError)
	}
}

func TestPluginMCPEndpoint_InvalidJsonrpc(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestPluginEndpoint(t, disp)

	body := []byte(`{"jsonrpc":"1.0","id":1,"method":"initialize"}`)
	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != HTTPStatusInvalidRequest {
		t.Errorf("HTTP status = %d, want %d", w.Code, HTTPStatusInvalidRequest)
	}
	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != CodeInvalidRequest {
		t.Errorf("RPC code = %d, want %d", resp.Error.Code, CodeInvalidRequest)
	}
}

func TestPluginMCPEndpoint_Initialize(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestPluginEndpoint(t, disp)

	body := buildRequest(t, "initialize", 1, InitializeParams{
		ProtocolVersion: "2025-03-26",
		ClientInfo:      ClientInfo{Name: "test", Version: "1.0"},
	})
	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want 200", w.Code)
	}
	// Must have Mcp-Session-Id header.
	sessionID := w.Header().Get(MCPSessionHeader)
	if sessionID == "" {
		t.Error("expected Mcp-Session-Id header, got empty string")
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ProtocolVersion == "" {
		t.Error("ProtocolVersion should not be empty")
	}
	if result.ServerInfo.Name == "" {
		t.Error("ServerInfo.Name should not be empty")
	}
}

func TestPluginMCPEndpoint_ToolsList(t *testing.T) {
	disp := &fakeDispatcher{
		tools: []ToolDescriptor{
			{Name: "tool_a", Description: "does a"},
		},
	}
	ep := newTestPluginEndpoint(t, disp)

	body := buildRequest(t, "tools/list", 2, nil)
	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want 200", w.Code)
	}
	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "tool_a" {
		t.Errorf("tool name = %q, want tool_a", result.Tools[0].Name)
	}
}

func TestPluginMCPEndpoint_ToolsCall_Success(t *testing.T) {
	disp := &fakeDispatcher{
		dispatchOut: ToolsCallResult{
			Content: []ContentItem{{Type: "text", Text: "all good"}},
		},
	}
	ep := newTestPluginEndpoint(t, disp)

	params := ToolsCallParams{
		Name:      "do_thing",
		Arguments: json.RawMessage(`{}`),
	}
	body := buildRequest(t, "tools/call", 3, params)
	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want 200", w.Code)
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
	if result.Content[0].Text != "all good" {
		t.Errorf("content text = %q, want %q", result.Content[0].Text, "all good")
	}
}

func TestPluginMCPEndpoint_ToolsCall_DispatchError(t *testing.T) {
	disp := &fakeDispatcher{
		dispatchErr: loader.ErrPluginNotFound,
	}
	ep := newTestPluginEndpoint(t, disp)

	params := ToolsCallParams{Name: "bad_tool"}
	body := buildRequest(t, "tools/call", 4, params)
	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	// Should return an error response (not 200).
	if w.Code == http.StatusOK {
		t.Error("expected non-200 HTTP status on dispatch error")
	}
	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
}

func TestPluginMCPEndpoint_UnknownMethod(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestPluginEndpoint(t, disp)

	body := buildRequest(t, "unknown/method", 5, nil)
	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != HTTPStatusMethodNotFound {
		t.Errorf("HTTP status = %d, want %d", w.Code, HTTPStatusMethodNotFound)
	}
	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("RPC code = %d, want %d", resp.Error.Code, CodeMethodNotFound)
	}
}

func TestPluginMCPEndpoint_BodySizeLimit(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestPluginEndpoint(t, disp)

	// Create body larger than 1MB.
	large := make([]byte, 1<<20+1)
	for i := range large {
		large[i] = 'a'
	}
	r := httptest.NewRequest(http.MethodPost, "/plugins/testplugin/mcp", bytes.NewReader(large))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	// Should fail to parse (truncated JSON).
	if w.Code == http.StatusOK {
		t.Error("expected error for oversized body, got 200")
	}
}
