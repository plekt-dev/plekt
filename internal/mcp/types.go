package mcp

import "encoding/json"

// RequestID is a JSON-RPC 2.0 request identifier. May be a number, string, or null.
type RequestID = json.RawMessage

// Request is a JSON-RPC 2.0 request message.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      RequestID       `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a successful JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      RequestID       `json:"id"`
	Result  json.RawMessage `json:"result"`
}

// ErrorResponse is a JSON-RPC 2.0 error response.
type ErrorResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      RequestID `json:"id"`
	Error   RPCError  `json:"error"`
}

// RPCError is the error object within an ErrorResponse.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// JSON-RPC 2.0 standard error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// HTTP status codes mapped to MCP error scenarios.
const (
	HTTPStatusParseError     = 400
	HTTPStatusInvalidRequest = 400
	HTTPStatusMethodNotFound = 404
	HTTPStatusInvalidParams  = 400
	HTTPStatusInternalError  = 500
	HTTPStatusPluginNotReady = 503
	HTTPStatusUnauthorized   = 401
)

// MCP method names.
const (
	MethodInitialize = "initialize"
	MethodToolsList  = "tools/list"
	MethodToolsCall  = "tools/call"
)

// InitializeParams is the parameter for the initialize method.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ClientInfo      ClientInfo     `json:"clientInfo"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
}

// ClientInfo describes the connecting MCP client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the response to an initialize request.
type InitializeResult struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ServerInfo      ServerInfo `json:"serverInfo"`
	Capabilities    ServerCaps `json:"capabilities"`
}

// ServerInfo describes this MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerCaps declares MCP server capabilities.
type ServerCaps struct {
	Tools *ToolsCap `json:"tools,omitempty"`
}

// ToolsCap declares tool-related capabilities.
type ToolsCap struct {
	ListChanged bool `json:"listChanged"`
}

// ToolsListParams is the parameter for tools/list.
type ToolsListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

// ToolsListResult is the response to tools/list.
type ToolsListResult struct {
	Tools []ToolDescriptor `json:"tools"`
}

// ToolDescriptor describes a single MCP tool.
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ToolsCallParams is the parameter for tools/call.
type ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolsCallResult is the response to tools/call.
type ToolsCallResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem is a single item within a ToolsCallResult.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// SessionID is the type of a session identifier.
type SessionID = string

// MCPSessionHeader is the HTTP header name carrying the session ID.
const MCPSessionHeader = "Mcp-Session-Id"
