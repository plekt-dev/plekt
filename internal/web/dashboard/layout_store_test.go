package dashboard_test

import (
	"errors"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/web/dashboard"
)

func TestDashboardLayoutStore_Save_Load_HappyPath(t *testing.T) {
	t.Parallel()
	store := dashboard.NewDashboardLayoutStore()
	defer store.Close()

	layout := dashboard.DashboardLayout{
		Placements: []dashboard.WidgetPlacement{
			{Key: dashboard.WidgetKey("plug/w1"), Position: 0, Visible: true},
			{Key: dashboard.WidgetKey("plug/w2"), Position: 1, Visible: false},
		},
		UpdatedAt: time.Now().UTC(),
	}

	if err := store.Save("sess1", layout); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	got, err := store.Load("sess1")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(got.Placements) != 2 {
		t.Errorf("Placements count = %d, want 2", len(got.Placements))
	}
	if got.Placements[0].Key != dashboard.WidgetKey("plug/w1") {
		t.Errorf("Placement[0].Key = %q, want plug/w1", got.Placements[0].Key)
	}
}

func TestDashboardLayoutStore_Load_NotFound(t *testing.T) {
	t.Parallel()
	store := dashboard.NewDashboardLayoutStore()
	defer store.Close()

	_, err := store.Load("nonexistent")
	if err == nil {
		t.Fatal("expected ErrLayoutNotFound, got nil")
	}
	if !errors.Is(err, dashboard.ErrLayoutNotFound) {
		t.Errorf("error = %v, want ErrLayoutNotFound", err)
	}
}

func TestDashboardLayoutStore_Save_OverwritesExisting(t *testing.T) {
	t.Parallel()
	store := dashboard.NewDashboardLayoutStore()
	defer store.Close()

	first := dashboard.DashboardLayout{
		Placements: []dashboard.WidgetPlacement{
			{Key: dashboard.WidgetKey("p/w1"), Position: 0, Visible: true},
		},
	}
	second := dashboard.DashboardLayout{
		Placements: []dashboard.WidgetPlacement{
			{Key: dashboard.WidgetKey("p/w2"), Position: 0, Visible: false},
		},
	}

	if err := store.Save("s", first); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("s", second); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Placements) != 1 || got.Placements[0].Key != dashboard.WidgetKey("p/w2") {
		t.Errorf("overwrite failed: got %v", got.Placements)
	}
}

func TestDashboardLayoutStore_Save_EmptySessionID(t *testing.T) {
	t.Parallel()
	store := dashboard.NewDashboardLayoutStore()
	defer store.Close()

	// Empty session ID is technically allowed: store should handle it
	layout := dashboard.DashboardLayout{}
	if err := store.Save("", layout); err != nil {
		t.Fatalf("Save with empty sessionID error: %v", err)
	}
	_, err := store.Load("")
	if err != nil {
		t.Fatalf("Load with empty sessionID error: %v", err)
	}
}

func TestDashboardLayoutStore_Close(t *testing.T) {
	t.Parallel()
	store := dashboard.NewDashboardLayoutStore()
	if err := store.Close(); err != nil {
		t.Errorf("Close error: %v", err)
	}
}

func TestDashboardLayoutStore_MultipleSessionsIsolated(t *testing.T) {
	t.Parallel()
	store := dashboard.NewDashboardLayoutStore()
	defer store.Close()

	layout1 := dashboard.DashboardLayout{
		Placements: []dashboard.WidgetPlacement{{Key: dashboard.WidgetKey("a/1"), Position: 0, Visible: true}},
	}
	layout2 := dashboard.DashboardLayout{
		Placements: []dashboard.WidgetPlacement{{Key: dashboard.WidgetKey("b/2"), Position: 0, Visible: false}},
	}

	if err := store.Save("user1", layout1); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("user2", layout2); err != nil {
		t.Fatal(err)
	}

	got1, _ := store.Load("user1")
	got2, _ := store.Load("user2")

	if got1.Placements[0].Key != dashboard.WidgetKey("a/1") {
		t.Errorf("session1 contaminated: got %v", got1.Placements[0].Key)
	}
	if got2.Placements[0].Key != dashboard.WidgetKey("b/2") {
		t.Errorf("session2 contaminated: got %v", got2.Placements[0].Key)
	}
}
