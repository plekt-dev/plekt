package dashboard

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
)

// WidgetKey is the unique identifier for a widget, formatted as "pluginName/widgetID".
type WidgetKey string

// Widget is a dashboard widget instance held in the registry.
type Widget struct {
	Key        WidgetKey
	PluginName string
	Descriptor loader.WidgetDescriptor
}

// WidgetRegistry manages the set of registered dashboard widgets across all plugins.
type WidgetRegistry interface {
	// Register replaces all widgets for the given pluginName atomically.
	// Returns ErrInvalidWidgetDescriptor if any widget has an empty ID or DataFunction.
	Register(pluginName string, decl loader.DashboardDeclaration) error
	// Unregister removes all widgets for the given pluginName.
	Unregister(pluginName string)
	// Get returns the Widget for the given key or ErrWidgetNotFound.
	Get(key WidgetKey) (Widget, error)
	// List returns all registered widgets sorted by key.
	List() []Widget
}

// Sentinel errors for the dashboard package.
var (
	ErrWidgetNotFound          = errors.New("widget not found")
	ErrInvalidWidgetDescriptor = errors.New("invalid widget descriptor")
	ErrWidgetDataFetchFailed   = errors.New("widget data fetch failed")
	ErrLayoutNotFound          = errors.New("dashboard layout not found")
)

// inMemoryWidgetRegistry is the default WidgetRegistry implementation.
type inMemoryWidgetRegistry struct {
	mu      sync.RWMutex
	widgets map[WidgetKey]Widget
	// pluginKeys maps pluginName → slice of WidgetKeys for atomic replace.
	pluginKeys map[string][]WidgetKey
	bus        eventbus.EventBus
}

// NewWidgetRegistry constructs an empty in-memory WidgetRegistry.
// bus is used to emit registration/unregistration events; it may be nil
// (no events are emitted when bus is nil).
func NewWidgetRegistry(bus eventbus.EventBus) WidgetRegistry {
	return &inMemoryWidgetRegistry{
		widgets:    make(map[WidgetKey]Widget),
		pluginKeys: make(map[string][]WidgetKey),
		bus:        bus,
	}
}

// Register validates and registers all widgets in decl for pluginName,
// atomically replacing any previously registered widgets for that plugin.
func (r *inMemoryWidgetRegistry) Register(pluginName string, decl loader.DashboardDeclaration) error {
	// Validate all descriptors before mutating state.
	for i, d := range decl.Widgets {
		if d.ID == "" {
			return fmt.Errorf("%w: widget[%d] has empty ID", ErrInvalidWidgetDescriptor, i)
		}
		if d.DataFunction == "" {
			return fmt.Errorf("%w: widget[%d] %q has empty DataFunction", ErrInvalidWidgetDescriptor, i, d.ID)
		}
	}

	// Clamp RefreshSeconds: values > 0 and < 10 are raised to 10.
	widgets := make([]Widget, 0, len(decl.Widgets))
	for _, d := range decl.Widgets {
		if d.RefreshSeconds > 0 && d.RefreshSeconds < 10 {
			d.RefreshSeconds = 10
		}
		key := WidgetKey(pluginName + "/" + d.ID)
		widgets = append(widgets, Widget{
			Key:        key,
			PluginName: pluginName,
			Descriptor: d,
		})
	}

	r.mu.Lock()

	// Remove all previously registered widgets for this plugin.
	for _, k := range r.pluginKeys[pluginName] {
		delete(r.widgets, k)
	}

	// Insert the new widgets.
	keys := make([]WidgetKey, 0, len(widgets))
	for _, w := range widgets {
		r.widgets[w.Key] = w
		keys = append(keys, w.Key)
	}
	r.pluginKeys[pluginName] = keys

	r.mu.Unlock()

	if r.bus != nil {
		widgetIDs := make([]string, 0, len(widgets))
		for _, w := range widgets {
			widgetIDs = append(widgetIDs, w.Descriptor.ID)
		}
		r.bus.Emit(context.Background(), eventbus.Event{
			Name: eventbus.EventDashboardWidgetRegistered,
			Payload: eventbus.DashboardWidgetRegisteredPayload{
				PluginName: pluginName,
				WidgetIDs:  widgetIDs,
			},
		})
	}

	return nil
}

// Unregister removes all widgets for the given pluginName.
func (r *inMemoryWidgetRegistry) Unregister(pluginName string) {
	r.mu.Lock()

	for _, k := range r.pluginKeys[pluginName] {
		delete(r.widgets, k)
	}
	delete(r.pluginKeys, pluginName)

	r.mu.Unlock()

	if r.bus != nil {
		r.bus.Emit(context.Background(), eventbus.Event{
			Name: eventbus.EventDashboardWidgetUnregistered,
			Payload: eventbus.DashboardWidgetUnregisteredPayload{
				PluginName: pluginName,
			},
		})
	}
}

// Get returns the Widget for the given key or ErrWidgetNotFound.
func (r *inMemoryWidgetRegistry) Get(key WidgetKey) (Widget, error) {
	r.mu.RLock()
	w, ok := r.widgets[key]
	r.mu.RUnlock()
	if !ok {
		return Widget{}, fmt.Errorf("%w: %q", ErrWidgetNotFound, key)
	}
	return w, nil
}

// List returns all registered widgets sorted by key.
func (r *inMemoryWidgetRegistry) List() []Widget {
	r.mu.RLock()
	out := make([]Widget, 0, len(r.widgets))
	for _, w := range r.widgets {
		out = append(out, w)
	}
	r.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}
