package db

import (
	"fmt"
	"os"
	"path/filepath"
)

// SchemaLoader loads and parses a plugin's schema.yaml.
type SchemaLoader interface {
	Load(pluginDir string) (PluginSchema, error)
}

type fsSchemaLoader struct{}

// NewSchemaLoader returns a SchemaLoader that reads schema.yaml from the
// plugin directory using the real filesystem.
func NewSchemaLoader() SchemaLoader {
	return &fsSchemaLoader{}
}

// Load reads {pluginDir}/schema.yaml and parses it.
// Returns ErrSchemaFileNotFound if the file does not exist.
// Returns ErrSchemaInvalid (wrapped) if the file is present but invalid.
func (l *fsSchemaLoader) Load(pluginDir string) (PluginSchema, error) {
	schemaPath := filepath.Join(pluginDir, "schema.yaml")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return PluginSchema{}, fmt.Errorf("%w: %s", ErrSchemaFileNotFound, schemaPath)
		}
		return PluginSchema{}, fmt.Errorf("read schema.yaml: %w", err)
	}
	return ParsePluginSchema(data)
}

type noOpSchemaLoader struct{}

// NoOpSchemaLoader returns a SchemaLoader that always returns ErrSchemaFileNotFound.
// Useful for plugins that do not define a schema.
func NoOpSchemaLoader() SchemaLoader {
	return &noOpSchemaLoader{}
}

func (l *noOpSchemaLoader) Load(_ string) (PluginSchema, error) {
	return PluginSchema{}, ErrSchemaFileNotFound
}
