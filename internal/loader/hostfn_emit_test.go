package loader

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// fakeEventBus is a minimal EventBus that captures emitted events for assertion.
type fakeEventBus struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func (f *fakeEventBus) Emit(_ context.Context, e eventbus.Event) {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
}

func (f *fakeEventBus) Subscribe(eventName string, handler eventbus.Handler) eventbus.Subscription {
	return eventbus.Subscription{}
}

func (f *fakeEventBus) Unsubscribe(_ eventbus.Subscription) {}

func (f *fakeEventBus) Close() error { return nil }

func (f *fakeEventBus) emitted() []eventbus.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]eventbus.Event, len(f.events))
	copy(cp, f.events)
	return cp
}

// ---------------------------------------------------------------------------
// EventEmitHostFn tests
// ---------------------------------------------------------------------------

func TestEventEmitHostFn(t *testing.T) {
	type testCase struct {
		name          string
		pcc           PluginCallContext
		params        EventEmitParams
		wantErr       error
		wantEmitted   bool
		wantSrcPlugin string
	}

	fakeBus := &fakeEventBus{}

	cases := []testCase{
		{
			name: "nil Bus returns ErrEventBusUnavailable",
			pcc: PluginCallContext{
				PluginName:   "test-plugin",
				AllowedEmits: []string{"some.event"},
				Bus:          nil,
			},
			params:  EventEmitParams{EventName: "some.event"},
			wantErr: ErrEventBusUnavailable,
		},
		{
			name: "event not in AllowedEmits returns ErrEventNotDeclared",
			pcc: PluginCallContext{
				PluginName:   "test-plugin",
				AllowedEmits: []string{"allowed.event"},
				Bus:          fakeBus,
			},
			params:  EventEmitParams{EventName: "not.allowed"},
			wantErr: ErrEventNotDeclared,
		},
		{
			name: "empty AllowedEmits returns ErrEventNotDeclared",
			pcc: PluginCallContext{
				PluginName:   "test-plugin",
				AllowedEmits: []string{},
				Bus:          fakeBus,
			},
			params:  EventEmitParams{EventName: "any.event"},
			wantErr: ErrEventNotDeclared,
		},
		{
			name: "event in AllowedEmits emits with correct SourcePlugin",
			pcc: PluginCallContext{
				PluginName:   "my-plugin",
				AllowedEmits: []string{"task.created", "task.deleted"},
				Bus:          fakeBus,
			},
			params:        EventEmitParams{EventName: "task.created", Payload: "some-payload"},
			wantEmitted:   true,
			wantSrcPlugin: "my-plugin",
		},
		{
			name: "BearerToken not leaked in emitted event",
			pcc: PluginCallContext{
				PluginName:   "secure-plugin",
				BearerToken:  "super-secret-bearer-token",
				AllowedEmits: []string{"data.ready"},
				Bus:          fakeBus,
			},
			params:        EventEmitParams{EventName: "data.ready"},
			wantEmitted:   true,
			wantSrcPlugin: "secure-plugin",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Reset bus for each subtest that emits.
			localBus := &fakeEventBus{}
			if tc.pcc.Bus == fakeBus {
				tc.pcc.Bus = localBus
			}

			err := EventEmitHostFn(context.Background(), tc.pcc, tc.params)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("EventEmitHostFn() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantEmitted {
				emitted := localBus.emitted()
				if len(emitted) == 0 {
					t.Fatal("expected event to be emitted, got none")
				}
				got := emitted[0]
				if got.Name != tc.params.EventName {
					t.Errorf("emitted event name = %q, want %q", got.Name, tc.params.EventName)
				}
				if got.SourcePlugin != tc.wantSrcPlugin {
					t.Errorf("emitted event SourcePlugin = %q, want %q", got.SourcePlugin, tc.wantSrcPlugin)
				}
				// BearerToken must NOT appear anywhere in the emitted event.
				if tc.pcc.BearerToken != "" {
					// Verify the emitted payload does not contain the bearer token string.
					// The event struct has Name, SourcePlugin, Payload: none should equal BearerToken.
					if got.Name == tc.pcc.BearerToken {
						t.Error("BearerToken leaked into event Name")
					}
					if got.SourcePlugin == tc.pcc.BearerToken {
						t.Error("BearerToken leaked into event SourcePlugin")
					}
				}
			}
		})
	}
}
