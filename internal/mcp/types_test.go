package mcp

import (
	"encoding/json"
	"testing"
)

func TestRequest_Marshal(t *testing.T) {
	cases := []struct {
		name    string
		input   Request
		wantID  string
		wantMet string
	}{
		{
			name: "full request",
			input: Request{
				JSONRPC: "2.0",
				ID:      json.RawMessage(`1`),
				Method:  "tools/list",
				Params:  json.RawMessage(`{"cursor":""}`),
			},
			wantID:  "1",
			wantMet: "tools/list",
		},
		{
			name: "no params",
			input: Request{
				JSONRPC: "2.0",
				ID:      json.RawMessage(`"abc"`),
				Method:  "initialize",
			},
			wantID:  `"abc"`,
			wantMet: "initialize",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.input)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got Request
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got.JSONRPC != "2.0" {
				t.Errorf("JSONRPC = %q, want 2.0", got.JSONRPC)
			}
			if string(got.ID) != tc.wantID {
				t.Errorf("ID = %s, want %s", got.ID, tc.wantID)
			}
			if got.Method != tc.wantMet {
				t.Errorf("Method = %q, want %q", got.Method, tc.wantMet)
			}
		})
	}
}

func TestErrorResponse_Structure(t *testing.T) {
	resp := ErrorResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`42`),
		Error: RPCError{
			Code:    CodeMethodNotFound,
			Message: "method not found",
		},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	if _, ok := m["error"]; !ok {
		t.Error("expected 'error' key in ErrorResponse JSON")
	}
	if _, ok := m["result"]; ok {
		t.Error("ErrorResponse must not have 'result' key")
	}

	var errObj RPCError
	if err := json.Unmarshal(m["error"], &errObj); err != nil {
		t.Fatalf("Unmarshal error obj: %v", err)
	}
	if errObj.Code != CodeMethodNotFound {
		t.Errorf("Code = %d, want %d", errObj.Code, CodeMethodNotFound)
	}
	if errObj.Message != "method not found" {
		t.Errorf("Message = %q, want %q", errObj.Message, "method not found")
	}
}

func TestResponse_HasResult(t *testing.T) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Result:  json.RawMessage(`{"ok":true}`),
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := m["result"]; !ok {
		t.Error("expected 'result' key in Response JSON")
	}
}

func TestConstants_HTTPStatus(t *testing.T) {
	// Verify HTTP status constants have expected values.
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"Unauthorized", HTTPStatusUnauthorized, 401},
		{"PluginNotReady", HTTPStatusPluginNotReady, 503},
		{"MethodNotFound", HTTPStatusMethodNotFound, 404},
		{"InternalError", HTTPStatusInternalError, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
			}
		})
	}
}
