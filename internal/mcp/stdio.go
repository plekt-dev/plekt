package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
)

// StdioServerConfig holds dependencies for the stdio MCP transport.
type StdioServerConfig struct {
	Manager       loader.PluginManager
	SystemHandler *loader.PluginMCPHandler
	AgentService  agents.AgentService
	Bus           eventbus.EventBus
	// Token is the bearer token for agent authentication.
	// If empty, all requests are allowed (useful for local stdio usage).
	Token string
}

// RunStdio starts an MCP server on stdin/stdout using newline-delimited JSON-RPC.
// Blocks until stdin is closed or an error occurs.
func RunStdio(cfg StdioServerConfig) error {
	dispatcher := NewFederatedDispatcher(cfg.Manager, cfg.SystemHandler)
	sessions := NewSessionStore()

	slog.Info("MCP stdio server started", "tools", len(dispatcher.ListTools()))

	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer for large messages.
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		resp := handleStdioMessage(line, dispatcher, sessions, cfg)
		if resp != nil {
			out, err := json.Marshal(resp)
			if err != nil {
				slog.Error("stdio: failed to marshal response", "error", err)
				continue
			}
			fmt.Fprintln(os.Stdout, string(out))
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("stdio scanner error: %w", err)
	}
	return nil
}

func handleStdioMessage(line string, dispatcher Dispatcher, sessions SessionStore, cfg StdioServerConfig) any {
	var req Request
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return ErrorResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   RPCError{Code: CodeParseError, Message: "parse error"},
		}
	}

	// Notifications (no id): accept silently.
	if req.ID == nil || string(req.ID) == "null" {
		return nil
	}

	if req.JSONRPC != "2.0" {
		return ErrorResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   RPCError{Code: CodeInvalidRequest, Message: "invalid request"},
		}
	}

	switch req.Method {
	case MethodInitialize:
		return handleStdioInitialize(req, sessions)
	case MethodToolsList:
		return handleStdioToolsList(req, dispatcher, cfg)
	case MethodToolsCall:
		return handleStdioToolsCall(req, dispatcher, cfg)
	default:
		return ErrorResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   RPCError{Code: CodeMethodNotFound, Message: "method not found"},
		}
	}
}

func handleStdioInitialize(req Request, sessions SessionStore) Response {
	sessions.Create(SessionEntry{
		PluginName: "",
		Scope:      SessionScopeFederated,
	})

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

	resultJSON, _ := json.Marshal(result)
	return Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  resultJSON,
	}
}

func handleStdioToolsList(req Request, dispatcher Dispatcher, cfg StdioServerConfig) Response {
	tools := dispatcher.ListTools()

	// Filter by agent permissions if token is set.
	if cfg.Token != "" && cfg.AgentService != nil {
		if _, perms, err := cfg.AgentService.ResolveByToken(context.Background(), cfg.Token); err == nil {
			tools = filterToolsByAgent(tools, perms)
		}
	}

	result := ToolsListResult{Tools: tools}
	resultJSON, _ := json.Marshal(result)
	return Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  resultJSON,
	}
}

func handleStdioToolsCall(req Request, dispatcher Dispatcher, cfg StdioServerConfig) any {
	var params ToolsCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return ErrorResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   RPCError{Code: CodeInvalidParams, Message: "invalid params"},
			}
		}
	}

	// Permission check.
	if cfg.Token != "" && cfg.AgentService != nil {
		if _, perms, err := cfg.AgentService.ResolveByToken(context.Background(), cfg.Token); err == nil {
			if err := checkToolPermission(perms, params.Name, ""); err != nil {
				return ErrorResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   RPCError{Code: CodeMethodNotFound, Message: "method not found"},
				}
			}
		}
	}

	result, err := dispatcher.Dispatch(context.Background(), params.Name, params.Arguments)
	if err != nil {
		code, _ := ErrorCode(err)
		return ErrorResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   RPCError{Code: code, Message: err.Error()},
		}
	}

	resultJSON, _ := json.Marshal(result)
	return Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  resultJSON,
	}
}
