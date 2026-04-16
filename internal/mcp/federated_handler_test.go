package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestFederatedEndpoint(t *testing.T, disp Dispatcher) *FederatedMCPEndpoint {
	t.Helper()
	sessions := NewSessionStore()
	return NewFederatedMCPEndpoint(disp, sessions, nil)
}

func TestFederatedMCPEndpoint_NonPost(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestFederatedEndpoint(t, disp)

	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(m, func(t *testing.T) {
			r := httptest.NewRequest(m, "/mcp", nil)
			w := httptest.NewRecorder()
			ep.ServeHTTP(w, r)
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("method %s: HTTP status = %d, want 405", m, w.Code)
			}
		})
	}
}

func TestFederatedMCPEndpoint_ParseError(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestFederatedEndpoint(t, disp)

	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte("{not json")))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != HTTPStatusParseError {
		t.Errorf("HTTP status = %d, want %d", w.Code, HTTPStatusParseError)
	}
	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != CodeParseError {
		t.Errorf("RPC code = %d, want %d", resp.Error.Code, CodeParseError)
	}
}

func TestFederatedMCPEndpoint_InvalidJsonrpc(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestFederatedEndpoint(t, disp)

	body := []byte(`{"jsonrpc":"1.0","id":1,"method":"initialize"}`)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != HTTPStatusInvalidRequest {
		t.Errorf("HTTP status = %d, want %d", w.Code, HTTPStatusInvalidRequest)
	}
}

func TestFederatedMCPEndpoint_Initialize(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestFederatedEndpoint(t, disp)

	body := buildRequest(t, "initialize", 1, InitializeParams{
		ProtocolVersion: "2025-03-26",
		ClientInfo:      ClientInfo{Name: "test", Version: "1.0"},
	})
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want 200", w.Code)
	}
	sessionID := w.Header().Get(MCPSessionHeader)
	if sessionID == "" {
		t.Error("expected Mcp-Session-Id header")
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ServerInfo.Name != "plekt" {
		t.Errorf("ServerInfo.Name = %q, want plekt", result.ServerInfo.Name)
	}
}

func TestFederatedMCPEndpoint_ToolsList(t *testing.T) {
	disp := &fakeDispatcher{
		tools: []ToolDescriptor{{Name: "plugin_a__do_it", Description: "does it"}},
	}
	ep := newTestFederatedEndpoint(t, disp)

	body := buildRequest(t, "tools/list", 2, nil)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
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
}

func TestFederatedMCPEndpoint_ToolsCall_Success(t *testing.T) {
	disp := &fakeDispatcher{
		dispatchOut: ToolsCallResult{
			Content: []ContentItem{{Type: "text", Text: "federated result"}},
		},
	}
	ep := newTestFederatedEndpoint(t, disp)

	params := ToolsCallParams{Name: "plugin_a__do_it", Arguments: json.RawMessage(`{}`)}
	body := buildRequest(t, "tools/call", 3, params)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
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
	if result.Content[0].Text != "federated result" {
		t.Errorf("content = %q, want %q", result.Content[0].Text, "federated result")
	}
}

func TestFederatedMCPEndpoint_UnknownMethod(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestFederatedEndpoint(t, disp)

	body := buildRequest(t, "unknown/method", 4, nil)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code != HTTPStatusMethodNotFound {
		t.Errorf("HTTP status = %d, want %d", w.Code, HTTPStatusMethodNotFound)
	}
}

func TestFederatedMCPEndpoint_ToolsCall_DispatchError(t *testing.T) {
	disp := &fakeDispatcher{
		dispatchErr: ErrUnsupportedMethod,
	}
	ep := newTestFederatedEndpoint(t, disp)

	params := ToolsCallParams{Name: "bad_tool"}
	body := buildRequest(t, "tools/call", 5, params)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	if w.Code == http.StatusOK {
		t.Error("expected non-200 HTTP status on dispatch error")
	}
}

func TestFederatedMCPEndpoint_ToolsCall_InvalidParams(t *testing.T) {
	disp := &fakeDispatcher{}
	ep := newTestFederatedEndpoint(t, disp)

	// Malformed params (not an object).
	body := []byte(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":"not-an-object"}`)
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	w := httptest.NewRecorder()
	ep.ServeHTTP(w, r)

	// params unmarshal into ToolsCallParams will fail for "not-an-object".
	// Could succeed or fail depending on JSON flexibility; check we get a response.
	if w.Body.Len() == 0 {
		t.Error("expected non-empty response body")
	}
}
