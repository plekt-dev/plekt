package eventbus

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
)

// inMemoryBus is the concrete EventBus implementation.
// Events are delivered asynchronously in individual goroutines.
// A panic inside a handler is recovered and logged so that the bus remains operational.
type inMemoryBus struct {
	mu     sync.RWMutex
	subs   map[string]map[uint64]Handler // eventName → id → handler
	nextID atomic.Uint64
	closed atomic.Bool
	wg     sync.WaitGroup
}

// NewInMemoryBus constructs a ready-to-use in-memory EventBus.
func NewInMemoryBus() EventBus {
	return &inMemoryBus{
		subs: make(map[string]map[uint64]Handler),
	}
}

// Subscribe registers handler for eventName and returns an opaque Subscription.
func (b *inMemoryBus) Subscribe(eventName string, handler Handler) Subscription {
	id := b.nextID.Add(1)
	b.mu.Lock()
	if b.subs[eventName] == nil {
		b.subs[eventName] = make(map[uint64]Handler)
	}
	b.subs[eventName][id] = handler
	b.mu.Unlock()
	return Subscription{id: id}
}

// Unsubscribe removes the handler identified by sub from all event maps.
func (b *inMemoryBus) Unsubscribe(sub Subscription) {
	b.mu.Lock()
	for _, handlers := range b.subs {
		delete(handlers, sub.id)
	}
	b.mu.Unlock()
}

// Emit delivers event asynchronously to all subscribers registered under event.Name
// and to wildcard ("*") subscribers. If the bus is closed, the call is a no-op.
func (b *inMemoryBus) Emit(ctx context.Context, event Event) {
	if b.closed.Load() {
		return
	}

	b.mu.RLock()
	handlers := make([]Handler, 0, len(b.subs[event.Name])+len(b.subs["*"]))
	for _, h := range b.subs[event.Name] {
		handlers = append(handlers, h)
	}
	// Wildcard subscribers receive every event.
	for _, h := range b.subs["*"] {
		handlers = append(handlers, h)
	}
	b.mu.RUnlock()

	// Detach from the caller's context so that handler goroutines are not
	// cancelled when the originating request completes. Values (e.g. trace IDs)
	// are preserved; only the cancellation signal is removed.
	detached := context.WithoutCancel(ctx)
	for _, h := range handlers {
		h := h
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			safeCall(detached, event, h)
		}()
	}
}

// Close waits for all in-flight deliveries to finish and marks the bus as closed.
func (b *inMemoryBus) Close() error {
	b.closed.Store(true)
	b.wg.Wait()
	return nil
}

// safeCall invokes h, recovering any panic so the bus stays operational.
func safeCall(ctx context.Context, event Event, h Handler) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("eventbus: recovered panic in handler for %q: %v", event.Name, r)
		}
	}()
	h(ctx, event)
}
