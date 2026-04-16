package eventbus

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newBus is a helper that creates a concrete bus for tests.
// It will be wired to the real implementation once bus.go is written.
func newBus() EventBus {
	return NewInMemoryBus()
}

func TestEmitAndSubscribe(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	var received []Event
	var mu sync.Mutex

	sub := bus.Subscribe(EventPluginLoaded, func(ctx context.Context, e Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})
	_ = sub

	evt := Event{Name: EventPluginLoaded, SourcePlugin: "test-plugin", Payload: PluginLoadedPayload{Name: "test-plugin"}}
	bus.Emit(context.Background(), evt)

	// Allow async delivery.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].Name != EventPluginLoaded {
		t.Errorf("unexpected event name: %s", received[0].Name)
	}
}

func TestSubscribeMultipleHandlers(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	var count atomic.Int64

	bus.Subscribe(EventPluginLoaded, func(ctx context.Context, e Event) {
		count.Add(1)
	})
	bus.Subscribe(EventPluginLoaded, func(ctx context.Context, e Event) {
		count.Add(1)
	})

	bus.Emit(context.Background(), Event{Name: EventPluginLoaded})
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 2 {
		t.Errorf("expected count=2, got %d", count.Load())
	}
}

func TestUnsubscribe(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	var count atomic.Int64

	sub := bus.Subscribe(EventPluginLoaded, func(ctx context.Context, e Event) {
		count.Add(1)
	})

	bus.Emit(context.Background(), Event{Name: EventPluginLoaded})
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 1 {
		t.Fatalf("expected 1 delivery before unsubscribe, got %d", count.Load())
	}

	bus.Unsubscribe(sub)
	bus.Emit(context.Background(), Event{Name: EventPluginLoaded})
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 1 {
		t.Errorf("expected no delivery after unsubscribe, got %d", count.Load())
	}
}

func TestPanicInHandlerDoesNotCrashBus(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	var count atomic.Int64

	// Panicking handler.
	bus.Subscribe(EventPluginLoaded, func(ctx context.Context, e Event) {
		panic("intentional panic in handler")
	})
	// Second handler must still be called.
	bus.Subscribe(EventPluginLoaded, func(ctx context.Context, e Event) {
		count.Add(1)
	})

	bus.Emit(context.Background(), Event{Name: EventPluginLoaded})
	time.Sleep(100 * time.Millisecond)

	if count.Load() != 1 {
		t.Errorf("expected second handler to run after panic, got count=%d", count.Load())
	}
}

func TestEmitWrongEventNameNotDelivered(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	var count atomic.Int64
	bus.Subscribe(EventPluginLoaded, func(ctx context.Context, e Event) {
		count.Add(1)
	})

	bus.Emit(context.Background(), Event{Name: EventPluginError})
	time.Sleep(50 * time.Millisecond)

	if count.Load() != 0 {
		t.Errorf("expected no delivery for different event name, got %d", count.Load())
	}
}

func TestClose(t *testing.T) {
	bus := newBus()
	if err := bus.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

func TestEmitAfterClose(t *testing.T) {
	bus := newBus()
	bus.Close()
	// Must not panic.
	bus.Emit(context.Background(), Event{Name: EventPluginLoaded})
}

func TestSubscribeZeroValue(t *testing.T) {
	bus := newBus()
	defer bus.Close()
	// Subscribe with empty event name: should be harmless.
	sub := bus.Subscribe("", func(ctx context.Context, e Event) {})
	bus.Unsubscribe(sub)
}

func TestUnsubscribeUnknownSubscription(t *testing.T) {
	bus := newBus()
	defer bus.Close()
	// Unsubscribing a Subscription that was never registered must not panic.
	bus.Unsubscribe(Subscription{id: 9999999})
}

func TestEmitContextCancelled(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	var count atomic.Int64
	bus.Subscribe(EventPluginLoaded, func(ctx context.Context, e Event) {
		count.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Already cancelled.

	// Emit must still deliver (cancellation of caller ctx does not drop events).
	bus.Emit(ctx, Event{Name: EventPluginLoaded})
	time.Sleep(50 * time.Millisecond)
	if count.Load() != 1 {
		t.Errorf("expected event delivery even with cancelled ctx, got %d", count.Load())
	}
}
