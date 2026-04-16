package loader

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/plekt-dev/plekt/internal/registry"
)

// PluginMCPHandler implements the built-in MCP tools that manage the plugin system itself.
// These are registered on the federated /mcp endpoint.
type PluginMCPHandler struct {
	manager  PluginManager
	registry registry.RegistryClient // nil allowed; disables remote install tools
}

// NewPluginMCPHandler constructs a handler backed by manager.
func NewPluginMCPHandler(manager PluginManager) *PluginMCPHandler {
	return &PluginMCPHandler{manager: manager}
}

// SetRegistryClient wires the registry client for remote install tools.
func (h *PluginMCPHandler) SetRegistryClient(rc registry.RegistryClient) {
	h.registry = rc
}

// --- ListPlugins ---

// ListPluginsParams are the input parameters for the list_plugins MCP tool.
type ListPluginsParams struct {
	// StatusFilter optionally filters by PluginStatus. Empty string = all.
	StatusFilter string `json:"status_filter,omitempty"`
}

// ListPluginsResult is the output of the list_plugins MCP tool.
type ListPluginsResult struct {
	Plugins []PluginInfo `json:"plugins"`
}

// ListPlugins returns all currently loaded plugins, optionally filtered by status.
func (h *PluginMCPHandler) ListPlugins(_ context.Context, params ListPluginsParams) (ListPluginsResult, error) {
	all := h.manager.List()
	if params.StatusFilter == "" {
		return ListPluginsResult{Plugins: all}, nil
	}
	status := PluginStatus(params.StatusFilter)
	filtered := make([]PluginInfo, 0)
	for _, p := range all {
		if p.Status == status {
			filtered = append(filtered, p)
		}
	}
	return ListPluginsResult{Plugins: filtered}, nil
}

// --- InstallPlugin ---

// InstallPluginParams are the input parameters for the install_plugin MCP tool.
type InstallPluginParams struct {
	Dir string `json:"dir"`
}

// InstallPluginResult is the output of the install_plugin MCP tool.
type InstallPluginResult struct {
	Plugin PluginInfo `json:"plugin"`
}

// InstallPlugin loads a plugin from the given directory.
func (h *PluginMCPHandler) InstallPlugin(ctx context.Context, params InstallPluginParams) (InstallPluginResult, error) {
	if params.Dir == "" {
		return InstallPluginResult{}, fmt.Errorf("%w: dir is required", ErrManifestInvalid)
	}
	dir := params.Dir
	if !filepath.IsAbs(dir) {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	info, err := h.manager.Load(ctx, dir)
	if err != nil {
		return InstallPluginResult{}, err
	}
	return InstallPluginResult{Plugin: info}, nil
}

// --- UnloadPlugin ---

// UnloadPluginParams are the input parameters for the unload_plugin MCP tool.
type UnloadPluginParams struct {
	Name string `json:"name"`
}

// UnloadPluginResult is the output of the unload_plugin MCP tool.
type UnloadPluginResult struct {
	Success bool `json:"success"`
}

// UnloadPlugin gracefully shuts down a plugin by name.
func (h *PluginMCPHandler) UnloadPlugin(ctx context.Context, params UnloadPluginParams) (UnloadPluginResult, error) {
	if params.Name == "" {
		return UnloadPluginResult{}, fmt.Errorf("%w: name is required", ErrManifestInvalid)
	}
	if err := h.manager.Unload(ctx, params.Name); err != nil {
		return UnloadPluginResult{}, err
	}
	return UnloadPluginResult{Success: true}, nil
}

// --- ReloadPlugin ---

// ReloadPluginParams are the input parameters for the reload_plugin MCP tool.
type ReloadPluginParams struct {
	Name string `json:"name"`
}

// ReloadPluginResult is the output of the reload_plugin MCP tool.
type ReloadPluginResult struct {
	Plugin PluginInfo `json:"plugin"`
}

// ReloadPlugin performs Unload+Load on a named plugin.
func (h *PluginMCPHandler) ReloadPlugin(ctx context.Context, params ReloadPluginParams) (ReloadPluginResult, error) {
	if params.Name == "" {
		return ReloadPluginResult{}, fmt.Errorf("%w: name is required", ErrManifestInvalid)
	}
	info, err := h.manager.Reload(ctx, params.Name)
	if err != nil {
		return ReloadPluginResult{}, err
	}
	return ReloadPluginResult{Plugin: info}, nil
}

// --- GetPlugin ---

// GetPluginParams are the input parameters for the get_plugin MCP tool.
type GetPluginParams struct {
	Name string `json:"name"`
}

// GetPluginResult is the output of the get_plugin MCP tool.
type GetPluginResult struct {
	Plugin PluginInfo `json:"plugin"`
}

// GetPlugin retrieves metadata for a single plugin by name.
func (h *PluginMCPHandler) GetPlugin(_ context.Context, params GetPluginParams) (GetPluginResult, error) {
	if params.Name == "" {
		return GetPluginResult{}, fmt.Errorf("%w: name is required", ErrManifestInvalid)
	}
	p, err := h.manager.Get(params.Name)
	if err != nil {
		return GetPluginResult{}, err
	}
	return GetPluginResult{Plugin: p.Info()}, nil
}

// --- InstallFromRegistry ---

// InstallFromRegistryParams are the input parameters for the install_from_registry MCP tool.
type InstallFromRegistryParams struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"` // optional; empty = latest
}

// InstallFromRegistryResult is the output of the install_from_registry MCP tool.
type InstallFromRegistryResult struct {
	Plugin  PluginInfo `json:"plugin"`
	Version string     `json:"version"`
}

// InstallFromRegistry downloads and installs a plugin from the registry by name.
func (h *PluginMCPHandler) InstallFromRegistry(ctx context.Context, params InstallFromRegistryParams) (InstallFromRegistryResult, error) {
	if params.Name == "" {
		return InstallFromRegistryResult{}, fmt.Errorf("%w: name is required", ErrManifestInvalid)
	}
	if h.registry == nil {
		return InstallFromRegistryResult{}, fmt.Errorf("registry client not configured")
	}

	_, pv, err := h.registry.FindCompatibleVersion(ctx, params.Name)
	if err != nil {
		return InstallFromRegistryResult{}, fmt.Errorf("find compatible plugin version: %w", err)
	}

	info, err := h.manager.InstallFromURL(ctx, pv.DownloadURL, pv.ChecksumSHA256)
	if err != nil {
		return InstallFromRegistryResult{}, err
	}

	return InstallFromRegistryResult{Plugin: info, Version: pv.Version}, nil
}

// --- CheckPluginUpdates ---

// CheckPluginUpdatesParams are the input parameters for the check_plugin_updates MCP tool.
type CheckPluginUpdatesParams struct{}

// CheckPluginUpdatesResult is the output of the check_plugin_updates MCP tool.
type CheckPluginUpdatesResult struct {
	Updates []registry.UpdateInfo `json:"updates"`
}

// CheckPluginUpdates compares installed plugin versions with the registry.
func (h *PluginMCPHandler) CheckPluginUpdates(ctx context.Context, _ CheckPluginUpdatesParams) (CheckPluginUpdatesResult, error) {
	if h.registry == nil {
		return CheckPluginUpdatesResult{}, fmt.Errorf("registry client not configured")
	}

	installed := make(map[string]string)
	for _, p := range h.manager.List() {
		installed[p.Name] = p.Version
	}

	updates, err := h.registry.CheckUpdates(ctx, installed)
	if err != nil {
		return CheckPluginUpdatesResult{}, fmt.Errorf("check updates: %w", err)
	}

	return CheckPluginUpdatesResult{Updates: updates}, nil
}
