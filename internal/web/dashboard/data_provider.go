package dashboard

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
)

// WidgetDataRequest carries the parameters for a widget data fetch.
type WidgetDataRequest struct {
	Key    WidgetKey
	Params []byte
}

// WidgetDataResponse carries the JSON payload returned for a widget.
type WidgetDataResponse struct {
	Key  WidgetKey
	JSON []byte
}

// DashboardDataProvider fetches data for dashboard widgets by calling the
// appropriate plugin WASM function.
type DashboardDataProvider interface {
	// Fetch retrieves data for the widget identified by req.Key.
	// The DataFunction is always sourced from the WidgetRegistry: never from req.
	Fetch(ctx context.Context, req WidgetDataRequest) (WidgetDataResponse, error)
}

// dashboardDataProvider is the concrete DashboardDataProvider.
type dashboardDataProvider struct {
	plugins  loader.PluginManager
	registry WidgetRegistry
	bus      eventbus.EventBus
}

// NewDashboardDataProvider constructs a DashboardDataProvider backed by the
// given PluginManager, WidgetRegistry, and EventBus.
func NewDashboardDataProvider(plugins loader.PluginManager, registry WidgetRegistry, bus eventbus.EventBus) DashboardDataProvider {
	return &dashboardDataProvider{
		plugins:  plugins,
		registry: registry,
		bus:      bus,
	}
}

// Fetch looks up the widget in the registry to get the DataFunction name,
// then calls the plugin WASM function with req.Params.
// DataFunction is NEVER taken from the HTTP request: only from the registry.
func (p *dashboardDataProvider) Fetch(ctx context.Context, req WidgetDataRequest) (WidgetDataResponse, error) {
	widget, err := p.registry.Get(req.Key)
	if err != nil {
		return WidgetDataResponse{}, fmt.Errorf("%w: %v", ErrWidgetNotFound, err)
	}

	// DataFunction comes from the registry (manifest), never from req.Params.
	dataFunction := widget.Descriptor.DataFunction

	result, err := p.plugins.CallPlugin(ctx, widget.PluginName, dataFunction, req.Params)
	if err != nil {
		slog.Error("widget data fetch failed",
			"widget_key", string(req.Key),
			"plugin", widget.PluginName,
			"function", dataFunction,
			"error", err,
		)
		p.bus.Emit(ctx, eventbus.Event{
			Name:         eventbus.EventDashboardWidgetFetchError,
			SourcePlugin: widget.PluginName,
			Payload: eventbus.DashboardWidgetFetchErrorPayload{
				WidgetKey:  string(req.Key),
				PluginName: widget.PluginName,
				Error:      err.Error(),
			},
		})
		return WidgetDataResponse{}, fmt.Errorf("%w: plugin %q function %q: %v",
			ErrWidgetDataFetchFailed, widget.PluginName, dataFunction, err)
	}

	return WidgetDataResponse{
		Key:  req.Key,
		JSON: result,
	}, nil
}
