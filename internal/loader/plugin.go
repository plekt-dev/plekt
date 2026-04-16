package loader

import "context"

// PluginStatus represents the lifecycle state of a loaded plugin.
type PluginStatus string

const (
	PluginStatusLoading   PluginStatus = "loading"
	PluginStatusActive    PluginStatus = "active"
	PluginStatusError     PluginStatus = "error"
	PluginStatusUnloading PluginStatus = "unloading"
)

// PluginInfo holds observable metadata for a loaded plugin.
//
// PublicKey and Official are populated by the manager from its registry
// snapshot at Load time (see managerImpl.SetRegistrySnapshot). They are
// surfaced here for the UI badge and for diagnostics.
type PluginInfo struct {
	Name      string
	Version   string
	Status    PluginStatus
	Dir       string
	PublicKey string
	Official  bool
}

// PluginMCPMeta holds MCP tool/resource metadata for a loaded plugin.
type PluginMCPMeta struct {
	PluginName string
	Tools      []MCPTool
	Resources  []MCPResource
}

// Plugin is the runtime interface for an installed plugin.
type Plugin interface {
	// Info returns the current plugin metadata.
	Info() PluginInfo
	// Call invokes a named WASM function with raw input bytes and returns output bytes.
	Call(ctx context.Context, function string, input []byte) ([]byte, error)
	// Close releases all resources held by this plugin instance.
	Close() error
}
