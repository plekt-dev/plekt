package eventbus

import "context"

// Handler is a function that receives an event asynchronously.
// Handlers must not block the bus.
type Handler func(ctx context.Context, event Event)

// Event carries a named payload from a source plugin.
type Event struct {
	Name         string
	SourcePlugin string
	Payload      any
}

// Subscription is an opaque handle returned by Subscribe.
type Subscription struct{ id uint64 }

// EventBus is the async inter-plugin communication channel.
type EventBus interface {
	// Emit publishes an event asynchronously to all subscribers.
	Emit(ctx context.Context, event Event)
	// Subscribe registers a handler for the given event name.
	Subscribe(eventName string, handler Handler) Subscription
	// Unsubscribe removes a previously registered handler.
	Unsubscribe(sub Subscription)
	// Close shuts down the bus, waiting for in-flight deliveries to finish.
	Close() error
}
