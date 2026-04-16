package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/plekt-dev/plekt/internal/loader"
)

// BuiltinToolFunc is a function type for dispatching built-in system tools.
type BuiltinToolFunc func(ctx context.Context, args json.RawMessage) (json.RawMessage, error)

// ToolRoute describes how to route a tool call.
type ToolRoute struct {
	PluginName     string
	WASMFunction   string
	BuiltinHandler BuiltinToolFunc
}

// Dispatcher routes MCP tool calls to WASM plugins or built-in handlers.
type Dispatcher interface {
	// Dispatch calls the named tool with the given arguments.
	Dispatch(ctx context.Context, name string, arguments json.RawMessage) (ToolsCallResult, error)
	// ListTools returns all tools available through this dispatcher.
	ListTools() []ToolDescriptor
}

// SystemHandler is the interface satisfied by *loader.PluginMCPHandler for builtin dispatch.
type SystemHandler interface {
	ListPlugins(ctx context.Context, params loader.ListPluginsParams) (loader.ListPluginsResult, error)
	InstallPlugin(ctx context.Context, params loader.InstallPluginParams) (loader.InstallPluginResult, error)
	UnloadPlugin(ctx context.Context, params loader.UnloadPluginParams) (loader.UnloadPluginResult, error)
	ReloadPlugin(ctx context.Context, params loader.ReloadPluginParams) (loader.ReloadPluginResult, error)
	GetPlugin(ctx context.Context, params loader.GetPluginParams) (loader.GetPluginResult, error)
}

// FederatedToolSeparator is the delimiter between plugin name and tool name
// in federated MCP tool identifiers. Double underscore is used because
// Claude validates tool names against ^[a-zA-Z0-9_]{1,64}$.
const FederatedToolSeparator = "__"

// sanitizePluginName replaces characters not allowed in MCP tool names
// (Claude enforces ^[a-zA-Z0-9_]{1,64}$). Hyphens are replaced with underscores.
func sanitizePluginName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// pluginNameFromFederatedTool splits a federated tool name of the form "plugin__tool"
// into the plugin name and tool name. Returns ok=false if the name contains no "__"
// separator or if either part is empty.
// Note: returns the sanitized plugin name; callers that need the original plugin name
// for routing should use unsanitizePluginName.
func pluginNameFromFederatedTool(name string) (pluginName, toolName string, ok bool) {
	idx := strings.Index(name, FederatedToolSeparator)
	if idx <= 0 || idx >= len(name)-len(FederatedToolSeparator) {
		return "", "", false
	}
	return name[:idx], name[idx+len(FederatedToolSeparator):], true
}

// unsanitizePluginName reverses sanitizePluginName for plugin routing.
// Tries the sanitized name first, then the original with hyphens.
func unsanitizePluginName(sanitized string, manager loader.PluginManager) string {
	// Try sanitized name first (might be the actual name)
	if _, err := manager.GetMCPMeta(sanitized); err == nil {
		return sanitized
	}
	// Try with hyphens restored
	original := strings.ReplaceAll(sanitized, "_", "-")
	if _, err := manager.GetMCPMeta(original); err == nil {
		return original
	}
	return sanitized
}

// validatePluginName returns an error if name contains characters outside [a-z0-9_-].
// An empty name or a name with disallowed characters wraps ErrInvalidPluginName,
// which maps to CodeInvalidParams / HTTP 400.
func validatePluginName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: plugin name must not be empty", ErrInvalidPluginName)
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return fmt.Errorf("%w: plugin name %q contains disallowed character %q", ErrInvalidPluginName, name, c)
		}
	}
	return nil
}

// --- Plugin dispatcher ---

// pluginDispatcher routes tool calls to a single WASM plugin.
type pluginDispatcher struct {
	manager    loader.PluginManager
	pluginName string
	tools      []loader.MCPTool
}

// NewPluginDispatcher constructs a Dispatcher backed by a single WASM plugin.
func NewPluginDispatcher(manager loader.PluginManager, meta loader.PluginMCPMeta) Dispatcher {
	return &pluginDispatcher{
		manager:    manager,
		pluginName: meta.PluginName,
		tools:      meta.Tools,
	}
}

// ListTools returns ToolDescriptors for all tools in this plugin's metadata.
func (d *pluginDispatcher) ListTools() []ToolDescriptor {
	descs := make([]ToolDescriptor, 0, len(d.tools))
	for _, t := range d.tools {
		desc := ToolDescriptor{
			Name:        t.Name,
			Description: t.Description,
		}
		if t.InputSchema != nil {
			schema, err := json.Marshal(t.InputSchema)
			if err == nil {
				desc.InputSchema = json.RawMessage(schema)
			}
		}
		descs = append(descs, desc)
	}
	return descs
}

// Dispatch calls the named tool function on the WASM plugin.
func (d *pluginDispatcher) Dispatch(ctx context.Context, name string, arguments json.RawMessage) (ToolsCallResult, error) {
	out, err := d.manager.CallPlugin(ctx, d.pluginName, name, arguments)
	if err != nil {
		return ToolsCallResult{}, err
	}
	return ToolsCallResult{
		Content: []ContentItem{
			{Type: "text", Text: string(out)},
		},
	}, nil
}

// --- Federated dispatcher ---

// federatedDispatcher routes tool calls to per-plugin dispatchers or built-in handlers.
type federatedDispatcher struct {
	manager  loader.PluginManager
	handler  SystemHandler
	builtins map[string]BuiltinToolFunc
}

// NewFederatedDispatcher constructs a Dispatcher that aggregates all loaded plugins
// plus built-in system tools.
func NewFederatedDispatcher(manager loader.PluginManager, handler SystemHandler) Dispatcher {
	d := &federatedDispatcher{
		manager:  manager,
		handler:  handler,
		builtins: make(map[string]BuiltinToolFunc),
	}
	d.registerBuiltins()
	return d
}

// registerBuiltins wires the system tool functions.
func (d *federatedDispatcher) registerBuiltins() {
	d.builtins["list_plugins"] = func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var params loader.ListPluginsParams
		if len(args) > 0 {
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("%w: list_plugins params: %v", ErrUnsupportedMethod, err)
			}
		}
		result, err := d.handler.ListPlugins(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}

	d.builtins["install_plugin"] = func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var params loader.InstallPluginParams
		if len(args) > 0 {
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("%w: install_plugin params: %v", ErrUnsupportedMethod, err)
			}
		}
		result, err := d.handler.InstallPlugin(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}

	d.builtins["unload_plugin"] = func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var params loader.UnloadPluginParams
		if len(args) > 0 {
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("%w: unload_plugin params: %v", ErrUnsupportedMethod, err)
			}
		}
		result, err := d.handler.UnloadPlugin(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}

	d.builtins["reload_plugin"] = func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var params loader.ReloadPluginParams
		if len(args) > 0 {
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("%w: reload_plugin params: %v", ErrUnsupportedMethod, err)
			}
		}
		result, err := d.handler.ReloadPlugin(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}

	d.builtins["get_plugin"] = func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var params loader.GetPluginParams
		if len(args) > 0 {
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("%w: get_plugin params: %v", ErrUnsupportedMethod, err)
			}
		}
		result, err := d.handler.GetPlugin(ctx, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}

}

// ListTools aggregates tools from all loaded plugins (prefixed with "{plugin}__") and built-ins.
func (d *federatedDispatcher) ListTools() []ToolDescriptor {
	var descs []ToolDescriptor

	// Collect plugin tools.
	for _, info := range d.manager.List() {
		meta, err := d.manager.GetMCPMeta(info.Name)
		if err != nil {
			slog.Warn("federated dispatcher: skipping plugin, GetMCPMeta failed",
				"plugin", info.Name,
				"error", err,
			)
			continue
		}
		for _, t := range meta.Tools {
			desc := ToolDescriptor{
				Name:        sanitizePluginName(info.Name) + FederatedToolSeparator + t.Name,
				Description: t.Description,
			}
			if t.InputSchema != nil {
				schema, err := json.Marshal(t.InputSchema)
				if err == nil {
					desc.InputSchema = json.RawMessage(schema)
				}
			}
			// Claude Desktop requires inputSchema on every tool.
			if desc.InputSchema == nil {
				desc.InputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			descs = append(descs, desc)
		}
	}

	// Add built-in system tools without prefix.
	builtinNames := []string{"list_plugins", "install_plugin", "unload_plugin", "reload_plugin", "get_plugin"}
	for _, name := range builtinNames {
		descs = append(descs, ToolDescriptor{
			Name:        name,
			Description: "System tool: " + name,
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		})
	}

	return descs
}

// Dispatch routes the tool call to the appropriate plugin or built-in handler.
func (d *federatedDispatcher) Dispatch(ctx context.Context, name string, arguments json.RawMessage) (ToolsCallResult, error) {
	// Plugin tool: prefixed with "{sanitized_plugin}__".
	if pluginName, toolName, ok := pluginNameFromFederatedTool(name); ok {
		// Reverse sanitization: plugin might be "notes-plugin" but name has "notes_plugin".
		actualPlugin := unsanitizePluginName(pluginName, d.manager)
		out, err := d.manager.CallPlugin(ctx, actualPlugin, toolName, arguments)
		if err != nil {
			return ToolsCallResult{}, err
		}
		return ToolsCallResult{
			Content: []ContentItem{
				{Type: "text", Text: string(out)},
			},
		}, nil
	}

	// Built-in system tool.
	fn, ok := d.builtins[name]
	if !ok {
		return ToolsCallResult{}, fmt.Errorf("%w: %q", ErrUnsupportedMethod, name)
	}
	result, err := fn(ctx, arguments)
	if err != nil {
		return ToolsCallResult{}, err
	}
	return ToolsCallResult{
		Content: []ContentItem{
			{Type: "text", Text: string(result)},
		},
	}, nil
}
