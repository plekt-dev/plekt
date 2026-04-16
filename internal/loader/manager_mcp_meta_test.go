package loader

import (
	"errors"
	"testing"
)

func TestGetMCPMeta_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	_, err := mgr.GetMCPMeta("ghost")
	if !errors.Is(err, ErrPluginNotFound) {
		t.Errorf("expected ErrPluginNotFound, got %v", err)
	}
}

func TestGetMCPMeta_Success(t *testing.T) {
	// Use a fakeManager directly to test GetMCPMeta without WASM.
	m := &managerImpl{
		plugins: make(map[string]*pluginImpl),
	}
	tools := []MCPTool{
		{Name: "do_thing", Description: "Does a thing"},
		{Name: "other_tool", Description: "Another"},
	}
	resources := []MCPResource{
		{URI: "res://example", Name: "example", Description: "an example resource"},
	}
	m.plugins["myplugin"] = &pluginImpl{
		info:         PluginInfo{Name: "myplugin", Status: PluginStatusActive},
		mcpTools:     tools,
		mcpResources: resources,
	}

	meta, err := m.GetMCPMeta("myplugin")
	if err != nil {
		t.Fatalf("GetMCPMeta: %v", err)
	}
	if meta.PluginName != "myplugin" {
		t.Errorf("PluginName = %q, want %q", meta.PluginName, "myplugin")
	}
	if len(meta.Tools) != 2 {
		t.Fatalf("Tools count = %d, want 2", len(meta.Tools))
	}
	if meta.Tools[0].Name != "do_thing" {
		t.Errorf("Tools[0].Name = %q, want %q", meta.Tools[0].Name, "do_thing")
	}
	if len(meta.Resources) != 1 {
		t.Fatalf("Resources count = %d, want 1", len(meta.Resources))
	}
	if meta.Resources[0].URI != "res://example" {
		t.Errorf("Resources[0].URI = %q, want %q", meta.Resources[0].URI, "res://example")
	}

	// Verify defensive copy: mutating returned slice must not affect internal state.
	meta.Tools[0].Name = "MUTATED"
	meta2, _ := m.GetMCPMeta("myplugin")
	if meta2.Tools[0].Name == "MUTATED" {
		t.Error("GetMCPMeta returned a reference to internal slice: expected a copy")
	}
}

func TestGetMCPMeta_EmptyToolsAndResources(t *testing.T) {
	m := &managerImpl{
		plugins: make(map[string]*pluginImpl),
	}
	m.plugins["empty-plugin"] = &pluginImpl{
		info:         PluginInfo{Name: "empty-plugin", Status: PluginStatusActive},
		mcpTools:     nil,
		mcpResources: nil,
	}

	meta, err := m.GetMCPMeta("empty-plugin")
	if err != nil {
		t.Fatalf("GetMCPMeta: %v", err)
	}
	if meta.PluginName != "empty-plugin" {
		t.Errorf("PluginName = %q, want %q", meta.PluginName, "empty-plugin")
	}
	if len(meta.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(meta.Tools))
	}
	if len(meta.Resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(meta.Resources))
	}
}
