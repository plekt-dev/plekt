package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
)

func TestErrorCode(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantRPCCode    int
		wantHTTPStatus int
	}{
		{"ErrPluginNotFound", loader.ErrPluginNotFound, CodeMethodNotFound, 404},
		{"ErrPluginNotReady", loader.ErrPluginNotReady, CodeInternalError, 503},
		{"ErrPermissionDenied", loader.ErrPermissionDenied, CodeInvalidParams, 400},
		{"ErrManifestInvalid", loader.ErrManifestInvalid, CodeInvalidParams, 400},
		{"ErrPluginDirTraversal", loader.ErrPluginDirTraversal, CodeInvalidParams, 400},
		{"ErrMissingBearerToken", ErrMissingBearerToken, CodeInvalidRequest, 401},
		{"ErrInvalidBearerToken", ErrInvalidBearerToken, CodeInvalidRequest, 401},
		{"ErrUnsupportedMethod", ErrUnsupportedMethod, CodeMethodNotFound, 404},
		{"default/unknown", ErrSessionNotFound, CodeInternalError, 500},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rpcCode, httpStatus := ErrorCode(tc.err)
			if rpcCode != tc.wantRPCCode {
				t.Errorf("ErrorCode(%v) rpcCode = %d, want %d", tc.err, rpcCode, tc.wantRPCCode)
			}
			if httpStatus != tc.wantHTTPStatus {
				t.Errorf("ErrorCode(%v) httpStatus = %d, want %d", tc.err, httpStatus, tc.wantHTTPStatus)
			}
		})
	}
}

func TestIsSafeErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"safe message", "plugin not found", true},
		{"safe with numbers", "error code 42", true},
		{"contains forward slash", "path /etc/passwd", false},
		{"contains backslash", "path C:\\Users\\data", false},
		{"contains token", "token invalid", false},
		{"contains bearer", "bearer auth failed", false},
		{"contains .db", "file data.db access", false},
		{"contains .wasm", "loading plugin.wasm failed", false},
		{"empty string", "", true},
		{"only letters", "methodnotfound", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isSafeErrorMessage(tc.msg)
			if got != tc.want {
				t.Errorf("isSafeErrorMessage(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

func TestNewErrorResponse_Sanitized(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"ErrPluginNotFound", loader.ErrPluginNotFound},
		{"ErrInvalidBearerToken", ErrInvalidBearerToken},
		{"ErrMissingBearerToken", ErrMissingBearerToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := json.RawMessage(`1`)
			resp := NewErrorResponse(id, tc.err)
			if !isSafeErrorMessage(resp.Error.Message) {
				t.Errorf("error message %q is not safe (contains sensitive path/token info)", resp.Error.Message)
			}
			if resp.JSONRPC != "2.0" {
				t.Errorf("JSONRPC = %q, want 2.0", resp.JSONRPC)
			}
		})
	}
}

func TestNewRPCError(t *testing.T) {
	e := NewRPCError(CodeInternalError, "internal error")
	if e.Code != CodeInternalError {
		t.Errorf("Code = %d, want %d", e.Code, CodeInternalError)
	}
	if e.Message != "internal error" {
		t.Errorf("Message = %q, want %q", e.Message, "internal error")
	}
}

func TestWriteError_SetsCorrectHTTPStatus(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantHTTPStatus int
	}{
		{"ErrMissingBearerToken", ErrMissingBearerToken, 401},
		{"ErrInvalidBearerToken", ErrInvalidBearerToken, 401},
		{"ErrUnsupportedMethod", ErrUnsupportedMethod, 404},
		{"ErrPluginNotFound", loader.ErrPluginNotFound, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			id := json.RawMessage(`1`)
			writeError(w, id, tc.err)
			if w.Code != tc.wantHTTPStatus {
				t.Errorf("HTTP status = %d, want %d", w.Code, tc.wantHTTPStatus)
			}
			// Verify Content-Type is JSON.
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			// Verify response is valid JSON.
			var resp ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Errorf("response is not valid JSON: %v", err)
			}
		})
	}
}

func TestWriteResult_HTTP200(t *testing.T) {
	w := httptest.NewRecorder()
	id := json.RawMessage(`1`)
	result := map[string]string{"key": "value"}
	writeResult(w, id, result)

	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want 200", w.Code)
	}
	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Errorf("response is not valid JSON: %v", err)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("JSONRPC = %q, want 2.0", resp.JSONRPC)
	}
}
