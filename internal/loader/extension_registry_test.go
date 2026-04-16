package loader_test

import (
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
)

func TestExtensionRegistry(t *testing.T) {
	t.Run("Register and ForPoint returns extensions", func(t *testing.T) {
		reg := loader.NewExtensionRegistry()
		reg.Register("plugin-a", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn_a", Type: "section"},
		})
		reg.Register("plugin-b", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn_b", Type: "section"},
		})

		exts := reg.ForPoint("core", "core.admin.settings.general")
		if len(exts) != 2 {
			t.Fatalf("expected 2 extensions, got %d", len(exts))
		}
	})

	t.Run("ForPoint returns empty slice for unknown point", func(t *testing.T) {
		reg := loader.NewExtensionRegistry()
		exts := reg.ForPoint("core", "nonexistent.point")
		if exts == nil {
			// nil vs empty is acceptable: just check length
		}
		if len(exts) != 0 {
			t.Errorf("expected 0 extensions for unknown point, got %d", len(exts))
		}
	})

	t.Run("ForPoint returns empty for wrong target plugin", func(t *testing.T) {
		reg := loader.NewExtensionRegistry()
		reg.Register("plugin-a", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn", Type: "section"},
		})
		exts := reg.ForPoint("other-plugin", "core.admin.settings.general")
		if len(exts) != 0 {
			t.Errorf("expected 0 extensions for wrong target plugin, got %d", len(exts))
		}
	})

	t.Run("ForPlugin returns all extensions targeting a plugin", func(t *testing.T) {
		reg := loader.NewExtensionRegistry()
		reg.Register("plugin-a", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn1", Type: "section"},
			{Point: "core.admin.settings.advanced", TargetPlugin: "core", DataFunction: "fn2", Type: "section"},
		})
		reg.Register("plugin-b", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.integrations", TargetPlugin: "core", DataFunction: "fn3", Type: "section"},
		})

		exts := reg.ForPlugin("core")
		if len(exts) != 3 {
			t.Errorf("expected 3 extensions targeting 'core', got %d", len(exts))
		}
	})

	t.Run("ForPlugin returns empty for plugin with no extensions", func(t *testing.T) {
		reg := loader.NewExtensionRegistry()
		exts := reg.ForPlugin("nobody")
		if len(exts) != 0 {
			t.Errorf("expected 0 extensions, got %d", len(exts))
		}
	})

	t.Run("Unregister removes all extensions for source plugin", func(t *testing.T) {
		reg := loader.NewExtensionRegistry()
		reg.Register("plugin-a", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn", Type: "section"},
		})
		reg.Register("plugin-b", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn", Type: "section"},
		})

		reg.Unregister("plugin-a")

		exts := reg.ForPoint("core", "core.admin.settings.general")
		if len(exts) != 1 {
			t.Errorf("expected 1 extension after unregister, got %d", len(exts))
		}
		if exts[0].SourcePlugin != "plugin-b" {
			t.Errorf("expected remaining extension to be plugin-b, got %s", exts[0].SourcePlugin)
		}
	})

	t.Run("Unregister removes only target plugin extensions", func(t *testing.T) {
		reg := loader.NewExtensionRegistry()
		reg.Register("plugin-a", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn", Type: "section"},
			{Point: "core.admin.settings.integrations", TargetPlugin: "core", DataFunction: "fn2", Type: "section"},
		})
		reg.Register("plugin-b", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn", Type: "section"},
		})

		reg.Unregister("plugin-a")

		// plugin-a's extensions should be gone
		allForCore := reg.ForPlugin("core")
		for _, ext := range allForCore {
			if ext.SourcePlugin == "plugin-a" {
				t.Error("plugin-a extension should have been removed")
			}
		}
		// plugin-b's extensions should still be there
		if len(allForCore) != 1 || allForCore[0].SourcePlugin != "plugin-b" {
			t.Errorf("expected 1 extension from plugin-b remaining, got %d", len(allForCore))
		}
	})

	t.Run("Unregister on non-existent plugin is a no-op", func(t *testing.T) {
		reg := loader.NewExtensionRegistry()
		reg.Register("plugin-a", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn", Type: "section"},
		})

		// Should not panic or error
		reg.Unregister("nonexistent")

		exts := reg.ForPoint("core", "core.admin.settings.general")
		if len(exts) != 1 {
			t.Errorf("expected 1 extension, got %d after no-op unregister", len(exts))
		}
	})

	t.Run("Register multiple extension points for one plugin", func(t *testing.T) {
		reg := loader.NewExtensionRegistry()
		reg.Register("voice-plugin", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn1", Type: "section"},
			{Point: "core.admin.settings.integrations", TargetPlugin: "core", DataFunction: "fn2", Type: "section"},
		})

		general := reg.ForPoint("core", "core.admin.settings.general")
		if len(general) != 1 {
			t.Errorf("expected 1 extension at general point, got %d", len(general))
		}
		integrations := reg.ForPoint("core", "core.admin.settings.integrations")
		if len(integrations) != 1 {
			t.Errorf("expected 1 extension at integrations point, got %d", len(integrations))
		}
	})

	t.Run("ForPoint returns copy, mutations do not affect registry", func(t *testing.T) {
		reg := loader.NewExtensionRegistry()
		reg.Register("plugin-a", []loader.ExtensionDescriptor{
			{Point: "core.admin.settings.general", TargetPlugin: "core", DataFunction: "fn", Type: "section"},
		})

		exts1 := reg.ForPoint("core", "core.admin.settings.general")
		exts1[0].SourcePlugin = "mutated"

		exts2 := reg.ForPoint("core", "core.admin.settings.general")
		if exts2[0].SourcePlugin == "mutated" {
			t.Error("ForPoint should return a copy: mutation must not affect registry")
		}
	})
}
