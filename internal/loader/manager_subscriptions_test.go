package loader

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// makePluginDirWithEvents builds a valid signed plugin dir with a custom events declaration.
// It delegates to makePluginDir for signing, then overwrites manifest.json with the given events.
func makePluginDirWithEvents(t *testing.T, pluginRoot, name string, events EventsDeclaration) string {
	t.Helper()
	dir := makePluginDir(t, pluginRoot, name, true)

	m := Manifest{
		Name:    name,
		Version: "0.1.0",
		Events:  events,
	}
	mb, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("makePluginDirWithEvents: marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o600); err != nil {
		t.Fatalf("makePluginDirWithEvents: write manifest: %v", err)
	}
	return dir
}

// ---------------------------------------------------------------------------
// TestLoad_ValidatesManifestEvents_InvalidEmitName
// ---------------------------------------------------------------------------

func TestLoad_ValidatesManifestEvents_InvalidEmitName(t *testing.T) {
	runner := newFakeRunner(nil)
	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{},
	)

	// Build a plugin dir with an invalid event name in Emits (empty string).
	makePluginDirWithEvents(t, pluginRoot, "bad-emit", EventsDeclaration{
		Emits: []string{"valid.event", ""},
	})
	dir := filepath.Join(pluginRoot, "bad-emit")

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("expected ErrManifestInvalid for invalid emit name, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestLoad_EmitsPluginError_OnWASMInitFail
// ---------------------------------------------------------------------------

func TestLoad_EmitsPluginError_OnWASMInitFail(t *testing.T) {
	wantErr := errors.New("mock wasm init failure")
	mgr, pluginRoot, bus := newFakeManager(t,
		&fakePluginFactory{err: wantErr},
		&fakeDBFactory{},
	)

	var errorEvents []eventbus.Event
	var mu sync.Mutex
	bus.Subscribe(eventbus.EventPluginError, func(_ context.Context, e eventbus.Event) {
		mu.Lock()
		errorEvents = append(errorEvents, e)
		mu.Unlock()
	})

	makePluginDir(t, pluginRoot, "wasm-fail-plugin", true)
	dir := filepath.Join(pluginRoot, "wasm-fail-plugin")

	_, err := mgr.Load(context.Background(), dir)
	if !errors.Is(err, ErrWASMInit) {
		t.Fatalf("expected ErrWASMInit, got %v", err)
	}

	bus.Close() // drain in-flight event goroutines
	mu.Lock()
	defer mu.Unlock()
	if len(errorEvents) == 0 {
		t.Error("expected plugin.error event to be emitted on WASM init failure")
	}
	payload, ok := errorEvents[0].Payload.(eventbus.PluginErrorPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", errorEvents[0].Payload)
	}
	if payload.Name != "wasm-fail-plugin" {
		t.Errorf("payload.Name = %q, want %q", payload.Name, "wasm-fail-plugin")
	}
}

// ---------------------------------------------------------------------------
// TestLoad_EmitsPluginError_OnDBFail
// ---------------------------------------------------------------------------

func TestLoad_EmitsPluginError_OnDBFail(t *testing.T) {
	runner := newFakeRunner(nil)
	mgr, pluginRoot, bus := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{err: errors.New("mock db open failure")},
	)

	var errorEvents []eventbus.Event
	var mu sync.Mutex
	bus.Subscribe(eventbus.EventPluginError, func(_ context.Context, e eventbus.Event) {
		mu.Lock()
		errorEvents = append(errorEvents, e)
		mu.Unlock()
	})

	makePluginDir(t, pluginRoot, "db-fail-plugin", true)
	dir := filepath.Join(pluginRoot, "db-fail-plugin")

	_, err := mgr.Load(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error when DB open fails, got nil")
	}

	bus.Close() // drain in-flight event goroutines
	mu.Lock()
	defer mu.Unlock()
	if len(errorEvents) == 0 {
		t.Error("expected plugin.error event to be emitted on DB open failure")
	}
	payload, ok := errorEvents[0].Payload.(eventbus.PluginErrorPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", errorEvents[0].Payload)
	}
	if payload.Name != "db-fail-plugin" {
		t.Errorf("payload.Name = %q, want %q", payload.Name, "db-fail-plugin")
	}
}

// ---------------------------------------------------------------------------
// TestUnload_UnwiresSubscriptions
// ---------------------------------------------------------------------------

func TestUnload_UnwiresSubscriptions(t *testing.T) {
	runner := newFakeRunner(nil)
	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{},
	)

	// Build a plugin dir that subscribes to a non-system event.
	makePluginDirWithEvents(t, pluginRoot, "sub-plugin", EventsDeclaration{
		Subscribes: []string{"task.created"},
	})
	dir := filepath.Join(pluginRoot, "sub-plugin")

	_, err := mgr.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Unload removes plugin subscriptions.
	if err := mgr.Unload(context.Background(), "sub-plugin"); err != nil {
		t.Fatalf("Unload: %v", err)
	}

	// Verify m.subscriptions["sub-plugin"] is cleared after unload.
	m := mgr.(*managerImpl)
	m.mu.RLock()
	subs := m.subscriptions["sub-plugin"]
	m.mu.RUnlock()
	if len(subs) != 0 {
		t.Errorf("expected subscriptions to be cleared after unload, got %d", len(subs))
	}
}

// ---------------------------------------------------------------------------
// TestEventEmitHostFn_Integration
// ---------------------------------------------------------------------------

func TestEventEmitHostFn_Integration(t *testing.T) {
	localBus := &fakeEventBus{}

	pcc := PluginCallContext{
		PluginName:   "integration-plugin",
		BearerToken:  "should-not-appear-in-event",
		AllowedEmits: []string{"integration.event"},
		Bus:          localBus,
	}

	err := EventEmitHostFn(context.Background(), pcc, EventEmitParams{
		EventName: "integration.event",
		Payload:   map[string]string{"key": "value"},
	})
	if err != nil {
		t.Fatalf("EventEmitHostFn: %v", err)
	}

	emitted := localBus.emitted()
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(emitted))
	}
	got := emitted[0]
	if got.Name != "integration.event" {
		t.Errorf("event Name = %q, want %q", got.Name, "integration.event")
	}
	if got.SourcePlugin != "integration-plugin" {
		t.Errorf("event SourcePlugin = %q, want %q", got.SourcePlugin, "integration-plugin")
	}
	// Bearer token must not appear in any event field.
	if got.SourcePlugin == pcc.BearerToken || got.Name == pcc.BearerToken {
		t.Error("BearerToken leaked into emitted event fields")
	}
}

// ---------------------------------------------------------------------------
// TestLoad_WiresSubscriptions_AfterLoad
// ---------------------------------------------------------------------------

func TestLoad_WiresSubscriptions_AfterLoad(t *testing.T) {
	runner := newFakeRunner(nil)
	mgr, pluginRoot, _ := newFakeManager(t,
		&fakePluginFactory{runner: runner},
		&fakeDBFactory{},
	)

	makePluginDirWithEvents(t, pluginRoot, "wiring-plugin", EventsDeclaration{
		Subscribes: []string{"task.created", "task.updated"},
	})
	dir := filepath.Join(pluginRoot, "wiring-plugin")

	_, err := mgr.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer mgr.Unload(context.Background(), "wiring-plugin") //nolint:errcheck

	// After loading, subscriptions should be tracked.
	m := mgr.(*managerImpl)
	m.mu.RLock()
	subs := m.subscriptions["wiring-plugin"]
	m.mu.RUnlock()
	if len(subs) != 2 {
		t.Errorf("expected 2 subscriptions (one per subscribed event), got %d", len(subs))
	}
}

// ---------------------------------------------------------------------------
// TestMarshalEvent
// ---------------------------------------------------------------------------

func TestMarshalEvent(t *testing.T) {
	cases := []struct {
		name       string
		event      eventbus.Event
		wantNil    bool
		wantInJSON string
	}{
		{
			name: "valid event with payload",
			event: eventbus.Event{
				Name:         "test.event",
				SourcePlugin: "my-plugin",
				Payload:      map[string]string{"key": "val"},
			},
			wantInJSON: "test.event",
		},
		{
			name: "nil payload produces valid JSON",
			event: eventbus.Event{
				Name:    "test.nil",
				Payload: nil,
			},
			wantInJSON: "test.nil",
		},
		{
			name:    "unmarshalable payload (channel) returns nil",
			event:   eventbus.Event{Name: "x", Payload: make(chan int)},
			wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalEvent(tc.event)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil bytes, got %q", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil bytes, got nil")
			}
			if tc.wantInJSON != "" && !strings.Contains(string(got), tc.wantInJSON) {
				t.Errorf("JSON %q does not contain %q", got, tc.wantInJSON)
			}
		})
	}
}
