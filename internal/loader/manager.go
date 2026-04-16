package loader

import (
	"context"
	"database/sql"
)

// PluginManager manages the lifecycle of all loaded plugins.
type PluginManager interface {
	// Load installs and activates a plugin from the given directory path.
	// Returns ErrPluginAlreadyLoaded if the plugin name is already active.
	// Returns ErrPluginDirTraversal if the path escapes the configured plugin root.
	Load(ctx context.Context, pluginDir string) (PluginInfo, error)

	// Unload gracefully shuts down a plugin by name.
	// Returns ErrPluginNotFound if the plugin is not loaded.
	Unload(ctx context.Context, name string) error

	// Reload performs Unload followed by Load from the same directory.
	// Returns ErrPluginNotFound if the plugin is not loaded.
	Reload(ctx context.Context, name string) (PluginInfo, error)

	// Get returns the Plugin instance for the given name.
	// Returns ErrPluginNotFound if not loaded.
	Get(name string) (Plugin, error)

	// List returns PluginInfo for all currently loaded plugins.
	List() []PluginInfo

	// GetMCPMeta returns the MCP tool/resource metadata for the named plugin.
	// Returns ErrPluginNotFound if the plugin is not loaded.
	GetMCPMeta(name string) (PluginMCPMeta, error)

	// CallPlugin invokes a WASM function on a named plugin.
	// Returns ErrPluginNotFound or ErrPluginNotReady appropriately.
	CallPlugin(ctx context.Context, name, function string, input []byte) ([]byte, error)

	// GetManifest returns the parsed Manifest for the named plugin.
	// Returns ErrPluginNotFound if the plugin is not loaded.
	GetManifest(name string) (Manifest, error)

	// ScanDir scans the configured plugin directory for subdirectories containing plugin manifests.
	// Returns a DiscoveredPlugin for each subdirectory found (valid or not).
	// Does NOT load plugins or mutate state. Safe to call concurrently.
	ScanDir(ctx context.Context) ([]DiscoveredPlugin, error)

	// Shutdown gracefully closes all loaded plugins without removing them from the registry.
	// Use this on server shutdown so that plugins are automatically restored on the next startup.
	// Unlike Unload, Shutdown does NOT delete registry entries, ensuring that
	// RestoreFromRegistry (or auto_load_on_startup) will reload plugins after restart.
	Shutdown(ctx context.Context) error

	// PluginDB returns the per-plugin *sql.DB handle for in-process Go
	// callers. Returns ErrPluginNotFound if the plugin is not loaded.
	PluginDB(name string) (*sql.DB, error)

	// InstallFromURL downloads a .mcpkg archive from downloadURL, verifies its
	// SHA256 checksum, unpacks it into the plugin directory, and loads it.
	// Returns the loaded plugin info or an error.
	InstallFromURL(ctx context.Context, downloadURL, checksumSHA256 string) (PluginInfo, error)

	// DownloadAndUnpack downloads a .mcpkg archive, verifies checksum, and
	// unpacks it into the plugin directory WITHOUT loading. Returns the
	// unpacked plugin directory path. Use Load() separately after review.
	DownloadAndUnpack(ctx context.Context, downloadURL, checksumSHA256 string) (string, error)
}
