package dashboard_test

import (
	"sort"
	"testing"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/web/dashboard"
)

func makeRegistry(t *testing.T) dashboard.WidgetRegistry {
	t.Helper()
	return dashboard.NewWidgetRegistry(nil)
}

func makeRegistryWithBus(t *testing.T) (dashboard.WidgetRegistry, *stubEventBus) {
	t.Helper()
	bus := &stubEventBus{}
	return dashboard.NewWidgetRegistry(bus), bus
}

func TestWidgetRegistry_Register_HappyPath(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "tasks-count", Title: "Tasks", DataFunction: "get_task_count", RefreshSeconds: 30, Width: "full"},
		},
	}
	if err := reg.Register("tasks", decl); err != nil {
		t.Fatalf("Register returned unexpected error: %v", err)
	}

	w, err := reg.Get(dashboard.WidgetKey("tasks/tasks-count"))
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if w.PluginName != "tasks" {
		t.Errorf("PluginName = %q, want %q", w.PluginName, "tasks")
	}
	if w.Descriptor.DataFunction != "get_task_count" {
		t.Errorf("DataFunction = %q, want %q", w.Descriptor.DataFunction, "get_task_count")
	}
}

func TestWidgetRegistry_Register_MultipleWidgets(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w1", Title: "W1", DataFunction: "fn1"},
			{ID: "w2", Title: "W2", DataFunction: "fn2"},
		},
	}
	if err := reg.Register("plug", decl); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	all := reg.List()
	if len(all) != 2 {
		t.Fatalf("List returned %d widgets, want 2", len(all))
	}
}

func TestWidgetRegistry_Register_EmptyIDReturnsError(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "", Title: "Bad", DataFunction: "fn"},
		},
	}
	if err := reg.Register("plug", decl); err == nil {
		t.Fatal("expected error for empty ID, got nil")
	}
}

func TestWidgetRegistry_Register_EmptyDataFunctionReturnsError(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w1", Title: "T", DataFunction: ""},
		},
	}
	if err := reg.Register("plug", decl); err == nil {
		t.Fatal("expected error for empty DataFunction, got nil")
	}
}

func TestWidgetRegistry_Register_RefreshSecondsClamped(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w1", Title: "T", DataFunction: "fn", RefreshSeconds: 3}, // below 10
		},
	}
	if err := reg.Register("plug", decl); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	w, _ := reg.Get(dashboard.WidgetKey("plug/w1"))
	if w.Descriptor.RefreshSeconds != 10 {
		t.Errorf("RefreshSeconds = %d, want 10 (clamped)", w.Descriptor.RefreshSeconds)
	}
}

func TestWidgetRegistry_Register_RefreshSecondsZeroNotClamped(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w1", Title: "T", DataFunction: "fn", RefreshSeconds: 0},
		},
	}
	if err := reg.Register("plug", decl); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	w, _ := reg.Get(dashboard.WidgetKey("plug/w1"))
	if w.Descriptor.RefreshSeconds != 0 {
		t.Errorf("RefreshSeconds = %d, want 0 (not clamped)", w.Descriptor.RefreshSeconds)
	}
}

func TestWidgetRegistry_Register_ReplacesExistingPlugin(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	first := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w1", Title: "Old", DataFunction: "old_fn"},
		},
	}
	second := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w2", Title: "New", DataFunction: "new_fn"},
		},
	}

	if err := reg.Register("plug", first); err != nil {
		t.Fatalf("first Register error: %v", err)
	}
	if err := reg.Register("plug", second); err != nil {
		t.Fatalf("second Register error: %v", err)
	}

	all := reg.List()
	if len(all) != 1 {
		t.Fatalf("List returned %d widgets after replace, want 1", len(all))
	}
	if all[0].Key != dashboard.WidgetKey("plug/w2") {
		t.Errorf("Key = %q, want plug/w2", all[0].Key)
	}
	// old widget should be gone
	if _, err := reg.Get(dashboard.WidgetKey("plug/w1")); err == nil {
		t.Error("old widget plug/w1 still found after replace")
	}
}

func TestWidgetRegistry_Unregister(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w1", Title: "T", DataFunction: "fn"},
		},
	}
	if err := reg.Register("plug", decl); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	reg.Unregister("plug")

	if _, err := reg.Get(dashboard.WidgetKey("plug/w1")); err == nil {
		t.Error("Get returned nil error after Unregister")
	}
	if len(reg.List()) != 0 {
		t.Errorf("List returned %d widgets after Unregister, want 0", len(reg.List()))
	}
}

func TestWidgetRegistry_Unregister_NonExistentPlugin(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)
	// Should not panic
	reg.Unregister("nonexistent")
}

func TestWidgetRegistry_Get_NotFound(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	if _, err := reg.Get(dashboard.WidgetKey("missing/widget")); err == nil {
		t.Fatal("expected ErrWidgetNotFound, got nil")
	}
}

func TestWidgetRegistry_List_SortedByKey(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	if err := reg.Register("beta", loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{{ID: "z", Title: "Z", DataFunction: "fn"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register("alpha", loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{{ID: "a", Title: "A", DataFunction: "fn"}},
	}); err != nil {
		t.Fatal(err)
	}

	all := reg.List()
	if len(all) != 2 {
		t.Fatalf("List returned %d, want 2", len(all))
	}
	keys := []string{string(all[0].Key), string(all[1].Key)}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("List not sorted: %v", keys)
	}
}

func TestWidgetRegistry_List_EmptyRegistry(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)
	if l := reg.List(); l == nil || len(l) != 0 {
		t.Errorf("empty List should return empty slice, got %v", l)
	}
}

func TestWidgetRegistry_Register_EmptyDeclaration(t *testing.T) {
	t.Parallel()
	reg := makeRegistry(t)

	// Empty Widgets slice should succeed: plugin has no widgets
	if err := reg.Register("plug", loader.DashboardDeclaration{}); err != nil {
		t.Fatalf("Register with empty declaration: %v", err)
	}
	if len(reg.List()) != 0 {
		t.Errorf("expected 0 widgets after registering empty decl, got %d", len(reg.List()))
	}
}

func TestWidgetRegistry_Register_EmitsEventDashboardWidgetRegistered(t *testing.T) {
	t.Parallel()
	reg, bus := makeRegistryWithBus(t)

	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w1", Title: "W1", DataFunction: "fn1"},
			{ID: "w2", Title: "W2", DataFunction: "fn2"},
		},
	}
	if err := reg.Register("myplugin", decl); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	var found *eventbus.Event
	for i, e := range bus.emitted {
		if e.Name == eventbus.EventDashboardWidgetRegistered {
			found = &bus.emitted[i]
			break
		}
	}
	if found == nil {
		t.Fatal("EventDashboardWidgetRegistered not emitted")
	}

	payload, ok := found.Payload.(eventbus.DashboardWidgetRegisteredPayload)
	if !ok {
		t.Fatalf("payload type = %T, want DashboardWidgetRegisteredPayload", found.Payload)
	}
	if payload.PluginName != "myplugin" {
		t.Errorf("PluginName = %q, want %q", payload.PluginName, "myplugin")
	}
	if len(payload.WidgetIDs) != 2 {
		t.Errorf("WidgetIDs length = %d, want 2", len(payload.WidgetIDs))
	}
}

func TestWidgetRegistry_Register_NilBus_NoEmit(t *testing.T) {
	t.Parallel()
	// NewWidgetRegistry(nil) must not panic when registering widgets.
	reg := dashboard.NewWidgetRegistry(nil)
	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w1", Title: "W1", DataFunction: "fn"},
		},
	}
	if err := reg.Register("plug", decl); err != nil {
		t.Fatalf("Register error: %v", err)
	}
}

func TestWidgetRegistry_Unregister_EmitsEventDashboardWidgetUnregistered(t *testing.T) {
	t.Parallel()
	reg, bus := makeRegistryWithBus(t)

	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w1", Title: "T", DataFunction: "fn"},
		},
	}
	if err := reg.Register("myplugin", decl); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	// Clear emitted so we only see the Unregister event.
	bus.emitted = nil

	reg.Unregister("myplugin")

	var found *eventbus.Event
	for i, e := range bus.emitted {
		if e.Name == eventbus.EventDashboardWidgetUnregistered {
			found = &bus.emitted[i]
			break
		}
	}
	if found == nil {
		t.Fatal("EventDashboardWidgetUnregistered not emitted")
	}

	payload, ok := found.Payload.(eventbus.DashboardWidgetUnregisteredPayload)
	if !ok {
		t.Fatalf("payload type = %T, want DashboardWidgetUnregisteredPayload", found.Payload)
	}
	if payload.PluginName != "myplugin" {
		t.Errorf("PluginName = %q, want %q", payload.PluginName, "myplugin")
	}
}

func TestWidgetRegistry_Unregister_NilBus_NoEmit(t *testing.T) {
	t.Parallel()
	// NewWidgetRegistry(nil) must not panic when unregistering.
	reg := dashboard.NewWidgetRegistry(nil)
	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w1", Title: "T", DataFunction: "fn"},
		},
	}
	if err := reg.Register("plug", decl); err != nil {
		t.Fatalf("Register error: %v", err)
	}
	reg.Unregister("plug") // must not panic
}

func TestWidgetRegistry_Register_EventContainsWidgetIDsNotKeys(t *testing.T) {
	t.Parallel()
	reg, bus := makeRegistryWithBus(t)

	decl := loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "task-summary", Title: "T", DataFunction: "fn"},
		},
	}
	if err := reg.Register("tasks-plugin", decl); err != nil {
		t.Fatalf("Register error: %v", err)
	}

	for _, e := range bus.emitted {
		if e.Name == eventbus.EventDashboardWidgetRegistered {
			payload := e.Payload.(eventbus.DashboardWidgetRegisteredPayload)
			// WidgetIDs should contain "task-summary", not "tasks-plugin/task-summary"
			for _, id := range payload.WidgetIDs {
				if id != "task-summary" {
					t.Errorf("WidgetID = %q, want bare ID %q", id, "task-summary")
				}
			}
			return
		}
	}
	t.Fatal("event not emitted")
}
