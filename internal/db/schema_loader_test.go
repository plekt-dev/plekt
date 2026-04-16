package db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaLoader_Load_ValidSchema(t *testing.T) {
	dir := t.TempDir()
	schemaYAML := []byte(`
version: "1"
tables:
  - name: items
    columns:
      - name: id
        type: INTEGER
        primary_key: true
      - name: label
        type: TEXT
`)
	if err := os.WriteFile(filepath.Join(dir, "schema.yaml"), schemaYAML, 0o600); err != nil {
		t.Fatalf("write schema.yaml: %v", err)
	}

	loader := NewSchemaLoader()
	schema, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if schema.Version != "1" {
		t.Errorf("expected version 1, got %q", schema.Version)
	}
	if len(schema.Tables) != 1 || schema.Tables[0].Name != "items" {
		t.Errorf("unexpected schema: %+v", schema)
	}
}

func TestSchemaLoader_Load_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	loader := NewSchemaLoader()
	_, err := loader.Load(dir)
	if !errors.Is(err, ErrSchemaFileNotFound) {
		t.Errorf("expected ErrSchemaFileNotFound, got %v", err)
	}
}

func TestSchemaLoader_Load_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.yaml"), []byte("\x01\x02\x03 bad yaml"), 0o600); err != nil {
		t.Fatalf("write schema.yaml: %v", err)
	}
	loader := NewSchemaLoader()
	_, err := loader.Load(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestSchemaLoader_Load_InvalidSchema(t *testing.T) {
	dir := t.TempDir()
	// Valid YAML but invalid schema (version != "1").
	schemaYAML := []byte("version: \"2\"\ntables: []\n")
	if err := os.WriteFile(filepath.Join(dir, "schema.yaml"), schemaYAML, 0o600); err != nil {
		t.Fatalf("write schema.yaml: %v", err)
	}
	loader := NewSchemaLoader()
	_, err := loader.Load(dir)
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestNoOpSchemaLoader_AlwaysReturnsNotFound(t *testing.T) {
	loader := NoOpSchemaLoader()
	_, err := loader.Load("/any/path")
	if !errors.Is(err, ErrSchemaFileNotFound) {
		t.Errorf("expected ErrSchemaFileNotFound, got %v", err)
	}
}

func TestNewSchemaLoader_ReturnsSchemaLoader(t *testing.T) {
	loader := NewSchemaLoader()
	if loader == nil {
		t.Error("expected non-nil SchemaLoader")
	}
}

func TestNoOpSchemaLoader_ReturnsSchemaLoader(t *testing.T) {
	loader := NoOpSchemaLoader()
	if loader == nil {
		t.Error("expected non-nil SchemaLoader")
	}
}
