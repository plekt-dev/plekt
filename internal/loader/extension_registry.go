package loader

import "sync"

// RegisteredExtension is a resolved extension: source plugin + descriptor.
type RegisteredExtension struct {
	SourcePlugin string
	Descriptor   ExtensionDescriptor
}

// ExtensionRegistry maps extension points to registered extensions.
// Thread-safe for concurrent reads/writes during plugin load/unload.
type ExtensionRegistry struct {
	mu sync.RWMutex
	// key: "target_plugin:point_id" -> extensions from other plugins
	extensions map[string][]RegisteredExtension
}

// NewExtensionRegistry creates a new empty ExtensionRegistry.
func NewExtensionRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{
		extensions: make(map[string][]RegisteredExtension),
	}
}

// Register adds all extensions declared by a plugin's manifest.
func (r *ExtensionRegistry) Register(sourcePlugin string, exts []ExtensionDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ext := range exts {
		key := ext.TargetPlugin + ":" + ext.Point
		r.extensions[key] = append(r.extensions[key], RegisteredExtension{
			SourcePlugin: sourcePlugin,
			Descriptor:   ext,
		})
	}
}

// Unregister removes all extensions registered by a source plugin.
func (r *ExtensionRegistry) Unregister(sourcePlugin string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, exts := range r.extensions {
		filtered := exts[:0]
		for _, ext := range exts {
			if ext.SourcePlugin != sourcePlugin {
				filtered = append(filtered, ext)
			}
		}
		if len(filtered) == 0 {
			delete(r.extensions, key)
		} else {
			r.extensions[key] = filtered
		}
	}
}

// ForPoint returns all extensions registered for a specific target plugin + point.
func (r *ExtensionRegistry) ForPoint(targetPlugin, pointID string) []RegisteredExtension {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := targetPlugin + ":" + pointID
	result := make([]RegisteredExtension, len(r.extensions[key]))
	copy(result, r.extensions[key])
	return result
}

// ForPlugin returns all extensions targeting any point of the given plugin.
func (r *ExtensionRegistry) ForPlugin(targetPlugin string) []RegisteredExtension {
	r.mu.RLock()
	defer r.mu.RUnlock()
	prefix := targetPlugin + ":"
	var result []RegisteredExtension
	for key, exts := range r.extensions {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			result = append(result, exts...)
		}
	}
	return result
}
