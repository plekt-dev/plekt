package dashboard_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/web/dashboard"
)

// stubPluginManager satisfies loader.PluginManager for testing.
type stubPluginManager struct {
	callResult []byte
	callErr    error
}

func (s *stubPluginManager) Load(_ context.Context, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (s *stubPluginManager) Unload(_ context.Context, _ string) error { return nil }
func (s *stubPluginManager) Reload(_ context.Context, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (s *stubPluginManager) Get(_ string) (loader.Plugin, error) { return nil, nil }
func (s *stubPluginManager) List() []loader.PluginInfo           { return nil }
func (s *stubPluginManager) GetMCPMeta(_ string) (loader.PluginMCPMeta, error) {
	return loader.PluginMCPMeta{}, nil
}
func (s *stubPluginManager) CallPlugin(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
	return s.callResult, s.callErr
}
func (s *stubPluginManager) GetManifest(_ string) (loader.Manifest, error) {
	return loader.Manifest{}, nil
}
func (s *stubPluginManager) ScanDir(_ context.Context) ([]loader.DiscoveredPlugin, error) {
	return nil, nil
}
func (s *stubPluginManager) Shutdown(_ context.Context) error { return nil }
func (s *stubPluginManager) PluginDB(_ string) (*sql.DB, error) {
	return nil, loader.ErrPluginNotFound
}

func (s *stubPluginManager) InstallFromURL(_ context.Context, _, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (s *stubPluginManager) DownloadAndUnpack(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

// stubEventBus collects emitted events for assertion.
type stubEventBus struct {
	emitted []eventbus.Event
}

func (s *stubEventBus) Emit(_ context.Context, e eventbus.Event) {
	s.emitted = append(s.emitted, e)
}
func (s *stubEventBus) Subscribe(_ string, _ eventbus.Handler) eventbus.Subscription {
	return eventbus.Subscription{}
}
func (s *stubEventBus) Unsubscribe(_ eventbus.Subscription) {}
func (s *stubEventBus) Close() error                        { return nil }

func setupProviderRegistry(t *testing.T) (dashboard.WidgetRegistry, loader.PluginManager, *stubEventBus) {
	t.Helper()
	reg := dashboard.NewWidgetRegistry(nil)
	if err := reg.Register("tasks", loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "count", Title: "Count", DataFunction: "get_count", RefreshSeconds: 30},
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	bus := &stubEventBus{}
	pm := &stubPluginManager{}
	return reg, pm, bus
}

func TestDashboardDataProvider_Fetch_HappyPath(t *testing.T) {
	t.Parallel()
	reg, pm, bus := setupProviderRegistry(t)
	pm.(*stubPluginManager).callResult = []byte(`{"count":5}`)

	provider := dashboard.NewDashboardDataProvider(pm, reg, bus)
	resp, err := provider.Fetch(context.Background(), dashboard.WidgetDataRequest{
		Key:    dashboard.WidgetKey("tasks/count"),
		Params: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if string(resp.JSON) != `{"count":5}` {
		t.Errorf("JSON = %q, want %q", resp.JSON, `{"count":5}`)
	}
	if resp.Key != dashboard.WidgetKey("tasks/count") {
		t.Errorf("Key = %q, want tasks/count", resp.Key)
	}
}

func TestDashboardDataProvider_Fetch_WidgetNotFound(t *testing.T) {
	t.Parallel()
	reg, pm, bus := setupProviderRegistry(t)

	provider := dashboard.NewDashboardDataProvider(pm, reg, bus)
	_, err := provider.Fetch(context.Background(), dashboard.WidgetDataRequest{
		Key: dashboard.WidgetKey("tasks/missing"),
	})
	if err == nil {
		t.Fatal("expected error for missing widget key")
	}
	if !errors.Is(err, dashboard.ErrWidgetNotFound) {
		t.Errorf("error = %v, want ErrWidgetNotFound", err)
	}
}

func TestDashboardDataProvider_Fetch_PluginCallFails(t *testing.T) {
	t.Parallel()
	reg, pm, bus := setupProviderRegistry(t)
	pm.(*stubPluginManager).callErr = errors.New("wasm error")

	provider := dashboard.NewDashboardDataProvider(pm, reg, bus)
	_, err := provider.Fetch(context.Background(), dashboard.WidgetDataRequest{
		Key:    dashboard.WidgetKey("tasks/count"),
		Params: nil,
	})
	if err == nil {
		t.Fatal("expected error when plugin call fails")
	}
	if !errors.Is(err, dashboard.ErrWidgetDataFetchFailed) {
		t.Errorf("error = %v, want ErrWidgetDataFetchFailed", err)
	}
}

func TestDashboardDataProvider_Fetch_EmitsEventOnFailure(t *testing.T) {
	t.Parallel()
	reg, pm, bus := setupProviderRegistry(t)
	pm.(*stubPluginManager).callErr = errors.New("wasm error")

	provider := dashboard.NewDashboardDataProvider(pm, reg, bus)
	_, _ = provider.Fetch(context.Background(), dashboard.WidgetDataRequest{
		Key: dashboard.WidgetKey("tasks/count"),
	})

	if len(bus.emitted) == 0 {
		t.Fatal("expected event emitted on fetch failure, got none")
	}
	found := false
	for _, e := range bus.emitted {
		if e.Name == eventbus.EventDashboardWidgetFetchError {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("EventDashboardWidgetFetchError not emitted, got: %v", bus.emitted)
	}
}

func TestDashboardDataProvider_Fetch_DataFunctionFromRegistry_NotHTTPParams(t *testing.T) {
	t.Parallel()
	// This test verifies that DataFunction comes from the registry, not from
	// any caller-supplied input. We register with "safe_fn" and if the call
	// succeeds, the registry-provided function name was used.
	reg := dashboard.NewWidgetRegistry(nil)
	if err := reg.Register("plug", loader.DashboardDeclaration{
		Widgets: []loader.WidgetDescriptor{
			{ID: "w", Title: "T", DataFunction: "safe_fn"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var capturedFn string
	spy := &spyPluginManager{fn: func(_, function string, _ []byte) {
		capturedFn = function
	}}
	bus := &stubEventBus{}
	provider := dashboard.NewDashboardDataProvider(spy, reg, bus)

	_, _ = provider.Fetch(context.Background(), dashboard.WidgetDataRequest{
		Key: dashboard.WidgetKey("plug/w"),
		// Params could contain anything: DataFunction must be ignored
		Params: []byte(`{"data_function":"evil_fn"}`),
	})

	if capturedFn != "safe_fn" {
		t.Errorf("DataFunction called = %q, want %q (from registry)", capturedFn, "safe_fn")
	}
}

// spyPluginManager captures the function name passed to CallPlugin.
type spyPluginManager struct {
	fn func(name, function string, input []byte)
}

func (s *spyPluginManager) Load(_ context.Context, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (s *spyPluginManager) Unload(_ context.Context, _ string) error { return nil }
func (s *spyPluginManager) Reload(_ context.Context, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (s *spyPluginManager) Get(_ string) (loader.Plugin, error) { return nil, nil }
func (s *spyPluginManager) List() []loader.PluginInfo           { return nil }
func (s *spyPluginManager) GetMCPMeta(_ string) (loader.PluginMCPMeta, error) {
	return loader.PluginMCPMeta{}, nil
}
func (s *spyPluginManager) CallPlugin(_ context.Context, name, function string, input []byte) ([]byte, error) {
	if s.fn != nil {
		s.fn(name, function, input)
	}
	return []byte(`{}`), nil
}
func (s *spyPluginManager) SetBearerToken(_ context.Context, _ string, _ string) error { return nil }
func (s *spyPluginManager) GetManifest(_ string) (loader.Manifest, error) {
	return loader.Manifest{}, nil
}
func (s *spyPluginManager) ScanDir(_ context.Context) ([]loader.DiscoveredPlugin, error) {
	return nil, nil
}
func (s *spyPluginManager) RotateToken(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (s *spyPluginManager) Shutdown(_ context.Context) error { return nil }
func (s *spyPluginManager) PluginDB(_ string) (*sql.DB, error) {
	return nil, loader.ErrPluginNotFound
}

func (s *spyPluginManager) InstallFromURL(_ context.Context, _, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (s *spyPluginManager) DownloadAndUnpack(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
