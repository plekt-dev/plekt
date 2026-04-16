package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// fakeEngine is a minimal Engine stub: records Start/Stop calls and the last
// context it received. Safe for concurrent use.
type fakeEngine struct {
	started atomic.Int32
	stopped atomic.Int32
}

func (f *fakeEngine) Start(_ context.Context)                               { f.started.Add(1) }
func (f *fakeEngine) Stop()                                                 { f.stopped.Add(1) }
func (f *fakeEngine) TriggerNow(_ context.Context, _ int64) (int64, error)  { return 0, nil }
func (f *fakeEngine) TriggerNowWithRun(_ context.Context, _, _ int64) error { return nil }

// lifecycleHarness builds a LifecycleManager against an in-memory bus and a
// fake engine factory so tests can observe Start/Stop without touching SQLite.
type lifecycleHarness struct {
	bus      eventbus.EventBus
	fake     *fakeEngine
	factory  EngineFactory
	mgr      *LifecycleManager
	dbExists bool
}

func newLifecycleHarness(dbExists bool) *lifecycleHarness {
	bus := eventbus.NewInMemoryBus()
	fake := &fakeEngine{}
	factory := EngineFactory(func(_ *sql.DB) (Engine, error) { return fake, nil })
	var resolve DBResolver
	h := &lifecycleHarness{bus: bus, fake: fake, factory: factory, dbExists: dbExists}
	resolve = func(name string) (*sql.DB, error) {
		if name != PluginName {
			return nil, errors.New("unknown plugin")
		}
		if !h.dbExists {
			return nil, errors.New("plugin not loaded")
		}
		// Return a non-nil *sql.DB sentinel. The factory ignores the value so
		// we can cheat and fabricate one by opening an in-memory database.
		db, err := sql.Open("sqlite", "file:lifecycle_test?mode=memory&cache=shared")
		return db, err
	}
	h.mgr = NewLifecycleManager(bus, factory, resolve, nil)
	return h
}

func TestLifecycle_TryStartNow_PluginAlreadyLoaded(t *testing.T) {
	h := newLifecycleHarness(true)
	defer h.mgr.Shutdown()
	h.mgr.Subscribe()

	if err := h.mgr.TryStartNow(context.Background()); err != nil {
		t.Fatalf("TryStartNow: %v", err)
	}
	if !h.mgr.IsRunning() {
		t.Fatal("engine not running after TryStartNow")
	}
	if h.fake.started.Load() != 1 {
		t.Errorf("Start called %d times, want 1", h.fake.started.Load())
	}
}

func TestLifecycle_TryStartNow_PluginNotLoaded(t *testing.T) {
	h := newLifecycleHarness(false)
	defer h.mgr.Shutdown()
	h.mgr.Subscribe()

	if err := h.mgr.TryStartNow(context.Background()); err != nil {
		t.Fatalf("TryStartNow: %v", err)
	}
	if h.mgr.IsRunning() {
		t.Fatal("engine should not be running when plugin absent")
	}
}

func TestLifecycle_StartsOnPluginLoaded(t *testing.T) {
	h := newLifecycleHarness(false) // not loaded initially
	defer h.mgr.Shutdown()
	h.mgr.Subscribe()

	if err := h.mgr.TryStartNow(context.Background()); err != nil {
		t.Fatalf("TryStartNow: %v", err)
	}
	if h.mgr.IsRunning() {
		t.Fatal("engine should not be running yet")
	}

	// Simulate the plugin showing up later (admin UI path).
	h.dbExists = true
	h.bus.Emit(context.Background(), eventbus.Event{
		Name:    eventbus.EventPluginLoaded,
		Payload: eventbus.PluginLoadedPayload{Name: PluginName},
	})

	// In-memory bus dispatches handlers asynchronously; wait briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.mgr.IsRunning() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !h.mgr.IsRunning() {
		t.Fatal("engine did not start on plugin.loaded")
	}
	if got := h.fake.started.Load(); got != 1 {
		t.Errorf("Start called %d times, want 1", got)
	}
}

func TestLifecycle_IgnoresOtherPluginsLoaded(t *testing.T) {
	h := newLifecycleHarness(false)
	defer h.mgr.Shutdown()
	h.mgr.Subscribe()

	h.dbExists = true // irrelevant, should not be queried
	h.bus.Emit(context.Background(), eventbus.Event{
		Name:    eventbus.EventPluginLoaded,
		Payload: eventbus.PluginLoadedPayload{Name: "notes-plugin"},
	})

	// Give the bus a moment, then confirm nothing happened.
	time.Sleep(100 * time.Millisecond)
	if h.mgr.IsRunning() {
		t.Error("engine started for the wrong plugin")
	}
}

func TestLifecycle_StopsOnPluginUnloaded(t *testing.T) {
	h := newLifecycleHarness(true)
	defer h.mgr.Shutdown()
	h.mgr.Subscribe()

	if err := h.mgr.TryStartNow(context.Background()); err != nil {
		t.Fatalf("TryStartNow: %v", err)
	}
	if !h.mgr.IsRunning() {
		t.Fatal("engine not running")
	}

	h.bus.Emit(context.Background(), eventbus.Event{
		Name:    eventbus.EventPluginUnloaded,
		Payload: eventbus.PluginUnloadedPayload{Name: PluginName},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !h.mgr.IsRunning() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if h.mgr.IsRunning() {
		t.Fatal("engine still running after plugin.unloaded")
	}
	if got := h.fake.stopped.Load(); got != 1 {
		t.Errorf("Stop called %d times, want 1", got)
	}
}

func TestLifecycle_DuplicateLoadedIsNoOp(t *testing.T) {
	h := newLifecycleHarness(true)
	defer h.mgr.Shutdown()
	h.mgr.Subscribe()

	if err := h.mgr.TryStartNow(context.Background()); err != nil {
		t.Fatalf("TryStartNow: %v", err)
	}
	h.bus.Emit(context.Background(), eventbus.Event{
		Name:    eventbus.EventPluginLoaded,
		Payload: eventbus.PluginLoadedPayload{Name: PluginName},
	})
	time.Sleep(100 * time.Millisecond)
	if got := h.fake.started.Load(); got != 1 {
		t.Errorf("Start called %d times, want 1 (duplicate load should be no-op)", got)
	}
}

func TestLifecycle_Shutdown_IsIdempotent(t *testing.T) {
	h := newLifecycleHarness(true)
	h.mgr.Subscribe()
	_ = h.mgr.TryStartNow(context.Background())
	h.mgr.Shutdown()
	h.mgr.Shutdown() // should not panic
	if h.mgr.IsRunning() {
		t.Error("engine still running after Shutdown")
	}
}
