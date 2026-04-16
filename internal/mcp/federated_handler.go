package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/eventbus"
)

// FederatedMCPEndpoint handles MCP JSON-RPC requests for the federated /mcp endpoint.
// It aggregates tools from all loaded plugins plus 5 built-in system tools.
type FederatedMCPEndpoint struct {
	dispatcher Dispatcher
	sessions   SessionStore
	bus        eventbus.EventBus // optional; if nil, tool-call events are not emitted
}

// NewFederatedMCPEndpoint constructs a FederatedMCPEndpoint.
// bus may be nil: when nil, no events are emitted.
func NewFederatedMCPEndpoint(dispatcher Dispatcher, sessions SessionStore, bus eventbus.EventBus) *FederatedMCPEndpoint {
	return &FederatedMCPEndpoint{
		dispatcher: dispatcher,
		sessions:   sessions,
		bus:        bus,
	}
}

// ServeHTTP implements http.Handler. Accepts POST only; routes by JSON-RPC method.
func (e *FederatedMCPEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 1 MB.
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
		e.handleInitialize(w, req)
	case MethodToolsList:
		e.handleToolsList(w, r, req)
	case MethodToolsCall:
		e.handleToolsCall(w, r, req)
	default:
		writeError(w, req.ID, ErrUnsupportedMethod)
	}
}

func (e *FederatedMCPEndpoint) handleInitialize(w http.ResponseWriter, req Request) {
	sessionID := e.sessions.Create(SessionEntry{
		PluginName: "",
		Scope:      SessionScopeFederated,
	})
	w.Header().Set(MCPSessionHeader, sessionID)

	// Negotiate protocol version: echo the client's requested version.
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
			Name:    "plekt",
			Version: "1.0.0",
		},
		Capabilities: ServerCaps{
			Tools: &ToolsCap{ListChanged: false},
		},
	}
	writeResult(w, req.ID, result)
}

func (e *FederatedMCPEndpoint) handleToolsList(w http.ResponseWriter, r *http.Request, req Request) {
	tools := e.dispatcher.ListTools()

	aa, ok := AgentFromContext(r.Context())
	if ok {
		tools = filterToolsByAgent(tools, aa.Permissions)
	}

	writeResult(w, req.ID, ToolsListResult{Tools: tools})
}

func (e *FederatedMCPEndpoint) handleToolsCall(w http.ResponseWriter, r *http.Request, req Request) {
	var params ToolsCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeError(w, req.ID, &invalidParamsErr{})
			return
		}
	}

	// Permission check: deny if agent lacks permission for this tool.
	if aa, ok := AgentFromContext(r.Context()); ok {
		if err := checkToolPermission(aa.Permissions, params.Name, ""); err != nil {
			writeError(w, req.ID, ErrUnsupportedMethod) // don't leak "permission denied"
			return
		}
	}

	// Inject agent identity into arguments so plugins know who's calling.
	args := params.Arguments
	if aa, ok := AgentFromContext(r.Context()); ok && aa.Agent.Name != "" {
		args = injectAgentAuthor(args, aa.Agent.Name)
	}

	start := time.Now()
	result, err := e.dispatcher.Dispatch(r.Context(), params.Name, args)
	durationMs := time.Since(start).Milliseconds()

	isError := err != nil

	pluginName := ""
	if pn, _, ok := pluginNameFromFederatedTool(params.Name); ok {
		pluginName = pn
	}

	if e.bus != nil {
		ev := eventbus.Event{
			Name:         eventbus.EventMCPToolCalled,
			SourcePlugin: pluginName,
			Payload: eventbus.MCPToolCalledPayload{
				ToolName:   params.Name,
				PluginName: pluginName,
				DurationMs: durationMs,
				IsError:    isError,
			},
		}
		go e.bus.Emit(context.Background(), ev)
	}

	if err != nil {
		writeError(w, req.ID, err)
		return
	}
	writeResult(w, req.ID, result)
}

// injectAgentAuthor merges "_mc_author" into the JSON arguments object.
// If arguments is nil or empty, creates a new object. If "_mc_author" is already
// set by the caller, it is preserved (caller wins).
func injectAgentAuthor(args json.RawMessage, agentName string) json.RawMessage {
	m := make(map[string]json.RawMessage)
	if len(args) > 0 {
		if err := json.Unmarshal(args, &m); err != nil {
			return args // not a JSON object, return as-is
		}
	}
	if _, exists := m["_mc_author"]; !exists {
		quoted, _ := json.Marshal(agentName)
		m["_mc_author"] = quoted
	}
	out, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return out
}

// filterToolsByAgent returns only tools the agent has permission to use.
// For federated tools (format "plugin__tool"), pluginName is extracted from prefix.
// For built-in tools (no prefix), pluginName is agents.BuiltinPluginName.
func filterToolsByAgent(all []ToolDescriptor, perms []agents.AgentPermission) []ToolDescriptor {
	filtered := make([]ToolDescriptor, 0, len(all))
	for _, td := range all {
		pluginName, toolName, ok := pluginNameFromFederatedTool(td.Name)
		if !ok {
			// Built-in tool (no prefix)
			pluginName = agents.BuiltinPluginName
			toolName = td.Name
		}
		// Permissions store original names (hyphens: "tasks-plugin") but
		// federated tool names use sanitized names (underscores: "tasks_plugin").
		// Try sanitized first, then original with hyphens restored.
		if agents.IsToolAllowed(perms, pluginName, toolName) {
			filtered = append(filtered, td)
		} else if ok {
			original := strings.ReplaceAll(pluginName, "_", "-")
			if agents.IsToolAllowed(perms, original, toolName) {
				filtered = append(filtered, td)
			}
		}
	}
	return filtered
}

// checkToolPermission checks if the agent has permission for the named tool.
// scopedPlugin is non-empty for plugin-scoped endpoints.
func checkToolPermission(perms []agents.AgentPermission, name, scopedPlugin string) error {
	var pluginName, toolName string
	if scopedPlugin != "" {
		pluginName = scopedPlugin
		toolName = name
	} else {
		var ok bool
		pluginName, toolName, ok = pluginNameFromFederatedTool(name)
		if !ok {
			pluginName = agents.BuiltinPluginName
			toolName = name
		}
	}
	if agents.IsToolAllowed(perms, pluginName, toolName) {
		return nil
	}
	// Try with hyphens restored (permissions store original names).
	original := strings.ReplaceAll(pluginName, "_", "-")
	if original != pluginName && agents.IsToolAllowed(perms, original, toolName) {
		return nil
	}
	return ErrAgentPermissionDenied
}
