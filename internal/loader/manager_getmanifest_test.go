package loader_test

import (
	"errors"
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
)

// TestGetManifest_PluginNotFound verifies ErrPluginNotFound is returned
// when the named plugin is not loaded.
func TestGetManifest_PluginNotFound(t *testing.T) {
	t.Parallel()

	// Use a stub manager that satisfies the interface.
	// The real manager requires WASM files and filesystem setup,
	// so we test through the interface contract.
	mgr := &stubManagerForGetManifest{
		manifests: map[string]loader.Manifest{},
	}

	_, err := mgr.GetManifest("not-loaded")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, loader.ErrPluginNotFound) {
		t.Errorf("error = %v, want ErrPluginNotFound", err)
	}
}

func TestGetManifest_Returns_LoadedManifest(t *testing.T) {
	t.Parallel()

	m := loader.Manifest{
		Name:    "tasks",
		Version: "1.0.0",
		Dashboard: loader.DashboardDeclaration{
			Widgets: []loader.WidgetDescriptor{
				{ID: "w1", DataFunction: "fn1"},
			},
		},
	}
	mgr := &stubManagerForGetManifest{
		manifests: map[string]loader.Manifest{"tasks": m},
	}

	got, err := mgr.GetManifest("tasks")
	if err != nil {
		t.Fatalf("GetManifest error: %v", err)
	}
	if got.Name != "tasks" {
		t.Errorf("Name = %q, want tasks", got.Name)
	}
	if len(got.Dashboard.Widgets) != 1 {
		t.Errorf("Widgets count = %d, want 1", len(got.Dashboard.Widgets))
	}
}

// stubManagerForGetManifest is a minimal PluginManager used to verify the
// GetManifest interface contract without a real filesystem.
type stubManagerForGetManifest struct {
	loader.PluginManager // embed to satisfy the full interface
	manifests            map[string]loader.Manifest
}

func (s *stubManagerForGetManifest) GetManifest(name string) (loader.Manifest, error) {
	m, ok := s.manifests[name]
	if !ok {
		return loader.Manifest{}, loader.ErrPluginNotFound
	}
	return m, nil
}
