package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/eventbus"
)

// PluginMCPEndpoint handles MCP JSON-RPC requests for a single plugin.
type PluginMCPEndpoint struct {
	pluginName string
	dispatcher Dispatcher
	sessions   SessionStore
	bus        eventbus.EventBus // optional; if nil, tool-call events are not emitted
}

// NewPluginMCPEndpoint constructs a PluginMCPEndpoint.
// bus may be nil: when nil, no events are emitted.
func NewPluginMCPEndpoint(pluginName string, dispatcher Dispatcher, sessions SessionStore, bus eventbus.EventBus) *PluginMCPEndpoint {
	return &PluginMCPEndpoint{
		pluginName: pluginName,
		dispatcher: dispatcher,
		sessions:   sessions,
		bus:        bus,
	}
}

// ServeHTTP implements http.Handler. Accepts POST only; routes by JSON-RPC method.
func (e *PluginMCPEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 1 MB to prevent abuse.
	limited := io.LimitReader(r.Body, 1<<20)
	body, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, nil, ErrUnsupportedMethod)
		return
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, nil, &parseErr{})
		return
	}

	if req.JSONRPC != "2.0" {
		writeError(w, req.ID, &invalidRequestErr{})
		return
	}

	// Notifications (no id): accept silently per JSON-RPC 2.0 spec.
	if req.ID == nil || string(req.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case MethodInitialize:
		e.handleInitialize(w, r, req)
	case MethodToolsList:
		e.handleToolsList(w, r, req)
	case MethodToolsCall:
		e.handleToolsCall(w, r, req)
	default:
		writeError(w, req.ID, ErrUnsupportedMethod)
	}
}

// handleInitialize processes an "initialize" request, creates a session, and returns server info.
func (e *PluginMCPEndpoint) handleInitialize(w http.ResponseWriter, _ *http.Request, req Request) {
	sessionID := e.sessions.Create(SessionEntry{
		PluginName: e.pluginName,
		Scope:      SessionScopePlugin,
	})
	w.Header().Set(MCPSessionHeader, sessionID)

	protoVersion := "2025-03-26"
	if len(req.Params) > 0 {
		var params InitializeParams
		if err := json.Unmarshal(req.Params, &params); err == nil && params.ProtocolVersion != "" {
			protoVersion = params.ProtocolVersion
		}
	}

	result := InitializeResult{
		ProtocolVersion: protoVersion,
		ServerInfo: ServerInfo{
			Name:    "plekt/" + e.pluginName,
			Version: "1.0.0",
		},
		Capabilities: ServerCaps{
			Tools: &ToolsCap{ListChanged: false},
		},
	}
	writeResult(w, req.ID, result)
}

// handleToolsList processes a "tools/list" request, filtered by agent permissions.
func (e *PluginMCPEndpoint) handleToolsList(w http.ResponseWriter, r *http.Request, req Request) {
	tools := e.dispatcher.ListTools()

	aa, ok := AgentFromContext(r.Context())
	if ok {
		pluginPerms := make([]agents.AgentPermission, 0)
		for _, p := range aa.Permissions {
			if p.PluginName == e.pluginName {
				pluginPerms = append(pluginPerms, p)
			}
		}
		filtered := make([]ToolDescriptor, 0, len(tools))
		for _, td := range tools {
			if agents.IsToolAllowed(pluginPerms, e.pluginName, td.Name) {
				filtered = append(filtered, td)
			}
		}
		tools = filtered
	}

	writeResult(w, req.ID, ToolsListResult{Tools: tools})
}

// handleToolsCall processes a "tools/call" request and emits EventMCPToolCalled.
func (e *PluginMCPEndpoint) handleToolsCall(w http.ResponseWriter, r *http.Request, req Request) {
	var params ToolsCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeError(w, req.ID, &invalidParamsErr{})
			return
		}
	}

	// Permission check: deny if agent lacks permission for this tool.
	if aa, ok := AgentFromContext(r.Context()); ok {
		if err := checkToolPermission(aa.Permissions, params.Name, e.pluginName); err != nil {
			writeError(w, req.ID, ErrUnsupportedMethod) // don't leak "permission denied"
			return
		}
	}

	start := time.Now()
	result, err := e.dispatcher.Dispatch(r.Context(), params.Name, params.Arguments)
	durationMs := time.Since(start).Milliseconds()

	isError := err != nil
	if e.bus != nil {
		e.bus.Emit(r.Context(), eventbus.Event{
			Name:         eventbus.EventMCPToolCalled,
			SourcePlugin: "",
			Payload: eventbus.MCPToolCalledPayload{
				ToolName:   params.Name,
				PluginName: e.pluginName,
				DurationMs: durationMs,
				IsError:    isError,
			},
		})
	}

	if err != nil {
		writeError(w, req.ID, err)
		return
	}
	writeResult(w, req.ID, result)
}

// --- internal sentinel error types for parse/invalid request ---

// parseErr maps to CodeParseError in ErrorCode.
type parseErr struct{}

func (e *parseErr) Error() string { return "parse error" }

// invalidRequestErr maps to CodeInvalidRequest.
type invalidRequestErr struct{}

func (e *invalidRequestErr) Error() string { return "invalid request" }

// invalidParamsErr maps to CodeInvalidParams.
type invalidParamsErr struct{}

func (e *invalidParamsErr) Error() string { return "invalid params" }
