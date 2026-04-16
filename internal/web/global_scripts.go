// Package web: global script registry.
//
// A plugin that declares ui.global_frontend in its manifest registers a
// JavaScript (and optional CSS) asset that is injected into every page of the
// Web UI. The registry stores one entry per plugin and is consulted by the
// base layout render middleware.
package web

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/plekt-dev/plekt/internal/loader"
)

// GlobalScriptEntry is one registered plugin-global asset.
type GlobalScriptEntry struct {
	PluginName string
	URL        string
	CSSURL     string
}

// ErrInvalidGlobalAsset is returned when a manifest supplies an asset
// filename that contains a path component, path-traversal segment, or is
// otherwise unsafe to embed in a URL.
var ErrInvalidGlobalAsset = errors.New("invalid global frontend asset filename")

// GlobalScriptRegistry stores the set of globally-injected plugin scripts.
// All methods are safe for concurrent use.
type GlobalScriptRegistry interface {
	Register(pluginName string, asset loader.FrontendAssets) error
	Unregister(pluginName string)
	List() []GlobalScriptEntry
}

// defaultGlobalScriptRegistry is the in-memory implementation.
type defaultGlobalScriptRegistry struct {
	mu      sync.RWMutex
	entries map[string]GlobalScriptEntry
}

// NewGlobalScriptRegistry returns an empty, concurrency-safe registry.
func NewGlobalScriptRegistry() GlobalScriptRegistry {
	return &defaultGlobalScriptRegistry{
		entries: make(map[string]GlobalScriptEntry),
	}
}

// Register validates the asset filenames and stores the entry under
// pluginName, overwriting any prior entry for that plugin.
//
// JSFile is required and must be a bare filename (no slashes, no "..",
// no leading dot). CSSFile, when non-empty, follows the same rules.
func (r *defaultGlobalScriptRegistry) Register(pluginName string, asset loader.FrontendAssets) error {
	if strings.TrimSpace(pluginName) == "" {
		return errors.New("plugin name must not be empty")
	}
	if err := validateAssetName(asset.JSFile); err != nil {
		return err
	}
	entry := GlobalScriptEntry{
		PluginName: pluginName,
		URL:        "/p/" + pluginName + "/static/" + asset.JSFile,
	}
	if asset.CSSFile != "" {
		if err := validateAssetName(asset.CSSFile); err != nil {
			return err
		}
		entry.CSSURL = "/p/" + pluginName + "/static/" + asset.CSSFile
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[pluginName] = entry
	return nil
}

// Unregister removes any entry for pluginName. No-op if absent.
func (r *defaultGlobalScriptRegistry) Unregister(pluginName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, pluginName)
}

// List returns a snapshot of all registered entries sorted by PluginName.
func (r *defaultGlobalScriptRegistry) List() []GlobalScriptEntry {
	r.mu.RLock()
	out := make([]GlobalScriptEntry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].PluginName < out[j].PluginName })
	return out
}

// validateAssetName enforces the bare-filename rule. Mirrors the defensive
// style of plugin_static_handler's path-traversal guard.
func validateAssetName(name string) error {
	if name == "" {
		return ErrInvalidGlobalAsset
	}
	if strings.ContainsAny(name, "/\\") {
		return ErrInvalidGlobalAsset
	}
	if strings.Contains(name, "..") {
		return ErrInvalidGlobalAsset
	}
	if filepath.IsAbs(name) {
		return ErrInvalidGlobalAsset
	}
	if strings.HasPrefix(name, ".") {
		return ErrInvalidGlobalAsset
	}
	if filepath.Base(name) != name {
		return ErrInvalidGlobalAsset
	}
	return nil
}
