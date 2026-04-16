package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/plekt-dev/plekt/internal/loader"
)

// Sentinel errors for the mcp package.
var (
	ErrMissingBearerToken = errors.New("missing bearer token")
	ErrInvalidBearerToken = errors.New("invalid bearer token")
	ErrSessionNotFound    = errors.New("session not found")
	ErrUnsupportedMethod  = errors.New("unsupported method")
	// ErrInvalidPluginName is returned when a plugin name fails character validation.
	// It maps to CodeInvalidParams / HTTP 400, distinct from ErrInvalidBearerToken (401).
	ErrInvalidPluginName = errors.New("invalid plugin name")
)

// ErrorCode maps an error to a JSON-RPC code and HTTP status code.
func ErrorCode(err error) (rpcCode int, httpStatus int) {
	switch {
	case errors.Is(err, loader.ErrPluginNotFound):
		return CodeMethodNotFound, http.StatusNotFound
	case errors.Is(err, loader.ErrPluginNotReady):
		return CodeInternalError, HTTPStatusPluginNotReady
	case errors.Is(err, loader.ErrPermissionDenied):
		return CodeInvalidParams, http.StatusBadRequest
	case errors.Is(err, loader.ErrManifestInvalid):
		return CodeInvalidParams, http.StatusBadRequest
	case errors.Is(err, loader.ErrPluginDirTraversal):
		return CodeInvalidParams, http.StatusBadRequest
	case errors.Is(err, ErrMissingBearerToken):
		return CodeInvalidRequest, http.StatusUnauthorized
	case errors.Is(err, ErrInvalidBearerToken):
		return CodeInvalidRequest, http.StatusUnauthorized
	case errors.Is(err, ErrUnsupportedMethod):
		return CodeMethodNotFound, http.StatusNotFound
	case errors.Is(err, ErrInvalidPluginName):
		return CodeInvalidParams, http.StatusBadRequest
	case isParseError(err):
		return CodeParseError, HTTPStatusParseError
	case isInvalidRequestError(err):
		return CodeInvalidRequest, HTTPStatusInvalidRequest
	case isInvalidParamsError(err):
		return CodeInvalidParams, HTTPStatusInvalidParams
	default:
		return CodeInternalError, http.StatusInternalServerError
	}
}

// isParseError checks for *parseErr via type assertion.
func isParseError(err error) bool {
	var e *parseErr
	return errors.As(err, &e)
}

// isInvalidRequestError checks for *invalidRequestErr.
func isInvalidRequestError(err error) bool {
	var e *invalidRequestErr
	return errors.As(err, &e)
}

// isInvalidParamsError checks for *invalidParamsErr.
func isInvalidParamsError(err error) bool {
	var e *invalidParamsErr
	return errors.As(err, &e)
}

// NewRPCError constructs an RPCError with the given code and message.
func NewRPCError(code int, message string) RPCError {
	return RPCError{Code: code, Message: message}
}

// safeErrorMessage returns a sanitized, human-readable summary for the given error.
// It never includes raw filesystem paths, token values, or internal error strings.
func safeErrorMessage(err error) string {
	switch {
	case errors.Is(err, loader.ErrPluginNotFound):
		return "plugin not found"
	case errors.Is(err, loader.ErrPluginNotReady):
		return "plugin not ready"
	case errors.Is(err, loader.ErrPermissionDenied):
		return "permission denied"
	case errors.Is(err, loader.ErrManifestInvalid):
		return "invalid request parameters"
	case errors.Is(err, loader.ErrPluginDirTraversal):
		return "invalid request parameters"
	case errors.Is(err, ErrMissingBearerToken):
		return "authentication required"
	case errors.Is(err, ErrInvalidBearerToken):
		return "authentication failed"
	case errors.Is(err, ErrUnsupportedMethod):
		return "method not found"
	case errors.Is(err, ErrInvalidPluginName):
		return "invalid plugin name"
	case errors.Is(err, ErrSessionNotFound):
		return "session not found"
	case isParseError(err):
		return "parse error"
	case isInvalidRequestError(err):
		return "invalid request"
	case isInvalidParamsError(err):
		return "invalid parameters"
	default:
		return "internal server error"
	}
}

// NewErrorResponse constructs an ErrorResponse for the given request ID and error.
// The error message is sanitized: no filesystem paths, tokens, or internal details are exposed.
func NewErrorResponse(id RequestID, err error) ErrorResponse {
	rpcCode, _ := ErrorCode(err)
	msg := safeErrorMessage(err)
	return ErrorResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   NewRPCError(rpcCode, msg),
	}
}

// writeError writes a JSON ErrorResponse with the appropriate HTTP status code.
func writeError(w http.ResponseWriter, id RequestID, err error) {
	_, httpStatus := ErrorCode(err)
	resp := NewErrorResponse(id, err)
	b, encErr := json.Marshal(resp)
	if encErr != nil {
		// Fallback: write a static internal error response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal server error"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_, _ = w.Write(b)
}

// writeResult writes a successful JSON-RPC response with HTTP 200.
func writeResult(w http.ResponseWriter, id RequestID, result any) {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		writeError(w, id, errors.New("internal marshal error"))
		return
	}
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  json.RawMessage(resultBytes),
	}
	b, err := json.Marshal(resp)
	if err != nil {
		writeError(w, id, errors.New("internal marshal error"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// isSafeErrorMessage returns false if the message contains patterns that suggest
// sensitive information (filesystem paths, token values, database or WASM files).
func isSafeErrorMessage(msg string) bool {
	lower := strings.ToLower(msg)
	forbidden := []string{"/", `\`, "token", "bearer", ".db", ".wasm"}
	for _, f := range forbidden {
		if strings.Contains(lower, f) {
			return false
		}
	}
	return true
}
