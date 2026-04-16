// Package eventbus provides the async inter-plugin communication channel for
// Plekt. Plugins communicate exclusively through the EventBus; direct
// cross-plugin calls are prohibited.
//
// Usage:
//
//	bus := eventbus.NewInMemoryBus()
//	sub := bus.Subscribe("plugin.loaded", func(ctx context.Context, e eventbus.Event) {
//	    // handle
//	})
//	defer bus.Unsubscribe(sub)
//	defer bus.Close()
package eventbus
