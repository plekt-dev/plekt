package dashboard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/web/dashboard"
)

// stubWidgetRegistry satisfies dashboard.WidgetRegistry for handler tests.
type stubWidgetRegistry struct {
	widgets map[dashboard.WidgetKey]dashboard.Widget
}

func (s *stubWidgetRegistry) Register(_ string, _ loader.DashboardDeclaration) error { return nil }
func (s *stubWidgetRegistry) Unregister(_ string)                                    {}
func (s *stubWidgetRegistry) Get(key dashboard.WidgetKey) (dashboard.Widget, error) {
	w, ok := s.widgets[key]
	if !ok {
		return dashboard.Widget{}, dashboard.ErrWidgetNotFound
	}
	return w, nil
}
func (s *stubWidgetRegistry) List() []dashboard.Widget {
	out := make([]dashboard.Widget, 0, len(s.widgets))
	for _, w := range s.widgets {
		out = append(out, w)
	}
	return out
}

// stubDataProvider satisfies dashboard.DashboardDataProvider.
type stubDataProvider struct {
	result []byte
	err    error
}

func (s *stubDataProvider) Fetch(_ context.Context, req dashboard.WidgetDataRequest) (dashboard.WidgetDataResponse, error) {
	if s.err != nil {
		return dashboard.WidgetDataResponse{}, s.err
	}
	return dashboard.WidgetDataResponse{Key: req.Key, JSON: s.result}, nil
}

func newTestHandler(t *testing.T) (dashboard.DashboardHandler, *stubWidgetRegistry, *stubDataProvider, dashboard.DashboardLayoutStore, *stubEventBus) {
	t.Helper()
	reg := &stubWidgetRegistry{
		widgets: map[dashboard.WidgetKey]dashboard.Widget{
			dashboard.WidgetKey("plug/w1"): {
				Key:        dashboard.WidgetKey("plug/w1"),
				PluginName: "plug",
				Descriptor: loader.WidgetDescriptor{
					ID: "w1", Title: "W1", DataFunction: "fn", RefreshSeconds: 30, Width: "full",
				},
			},
		},
	}
	provider := &stubDataProvider{result: []byte(`{"ok":true}`)}
	store := dashboard.NewDashboardLayoutStore()
	bus := &stubEventBus{}
	handler := dashboard.NewDashboardHandler(reg, provider, store, bus)
	t.Cleanup(func() { store.Close() })
	return handler, reg, provider, store, bus
}

func TestDashboardHandler_HandleDashboardPage_HappyPath(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "sess123"})
	w := httptest.NewRecorder()

	handler.HandleDashboardPage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDashboardHandler_HandleDashboardPage_NoSessionCookie(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := newTestHandler(t)

	// No session cookie: should still render with default layout
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	handler.HandleDashboardPage(w, req)

	// Should render OK with default (all visible) layout
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDashboardHandler_HandleWidgetRefresh_HappyPath(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := newTestHandler(t)

	// Use a mux to route the request with path values
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dashboard/widgets/{key}/refresh", handler.HandleWidgetRefresh)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/widgets/plug%2Fw1/refresh", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDashboardHandler_HandleWidgetRefresh_NotFound(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := newTestHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /dashboard/widgets/{key}/refresh", handler.HandleWidgetRefresh)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/widgets/plug%2Fmissing/refresh", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestDashboardHandler_HandleWidgetRefresh_FetchError(t *testing.T) {
	t.Parallel()
	handler, _, provider, _, _ := newTestHandler(t)
	provider.err = dashboard.ErrWidgetDataFetchFailed

	mux := http.NewServeMux()
	mux.HandleFunc("GET /dashboard/widgets/{key}/refresh", handler.HandleWidgetRefresh)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/widgets/plug%2Fw1/refresh", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Fetch error should render widget with error state, not a 500
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (widget with error)", w.Code)
	}
}

func TestDashboardHandler_HandleLayoutSave_HappyPath(t *testing.T) {
	t.Parallel()
	handler, _, _, store, bus := newTestHandler(t)

	form := url.Values{}
	form.Set("widget_keys[]", "plug/w1")
	form.Set("visible[]", "plug/w1")

	req := httptest.NewRequest(http.MethodPost, "/dashboard/layout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "sess123"})
	w := httptest.NewRecorder()

	handler.HandleLayoutSave(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}

	// Layout should be saved
	layout, err := store.Load("sess123")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(layout.Placements) != 1 {
		t.Errorf("Placements count = %d, want 1", len(layout.Placements))
	}

	// Event should be emitted
	found := false
	for _, e := range bus.emitted {
		if e.Name == eventbus.EventDashboardLayoutSaved {
			found = true
		}
	}
	if !found {
		t.Error("EventDashboardLayoutSaved not emitted")
	}
}

func TestDashboardHandler_HandleLayoutSave_MultipleWidgets(t *testing.T) {
	t.Parallel()
	handler, reg, _, store, _ := newTestHandler(t)
	reg.widgets[dashboard.WidgetKey("plug/w2")] = dashboard.Widget{
		Key: dashboard.WidgetKey("plug/w2"),
		Descriptor: loader.WidgetDescriptor{
			ID: "w2", DataFunction: "fn2",
		},
	}

	form := url.Values{}
	form["widget_keys[]"] = []string{"plug/w1", "plug/w2"}
	form["visible[]"] = []string{"plug/w1"}

	req := httptest.NewRequest(http.MethodPost, "/dashboard/layout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "sessX"})
	w := httptest.NewRecorder()

	handler.HandleLayoutSave(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}

	layout, err := store.Load("sessX")
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.Placements) != 2 {
		t.Errorf("Placements = %d, want 2", len(layout.Placements))
	}
	// w1 visible, w2 not
	for _, p := range layout.Placements {
		if p.Key == dashboard.WidgetKey("plug/w1") && !p.Visible {
			t.Error("plug/w1 should be visible")
		}
		if p.Key == dashboard.WidgetKey("plug/w2") && p.Visible {
			t.Error("plug/w2 should not be visible")
		}
	}
}

func TestDashboardHandler_HandleLayoutSave_EmitsEventWithCount(t *testing.T) {
	t.Parallel()
	handler, _, _, _, bus := newTestHandler(t)

	form := url.Values{}
	form["widget_keys[]"] = []string{"plug/w1"}

	req := httptest.NewRequest(http.MethodPost, "/dashboard/layout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "s"})
	w := httptest.NewRecorder()

	handler.HandleLayoutSave(w, req)

	for _, e := range bus.emitted {
		if e.Name == eventbus.EventDashboardLayoutSaved {
			payload, ok := e.Payload.(eventbus.DashboardLayoutSavedPayload)
			if !ok {
				t.Errorf("payload type = %T, want DashboardLayoutSavedPayload", e.Payload)
				return
			}
			if payload.WidgetCount != 1 {
				t.Errorf("WidgetCount = %d, want 1", payload.WidgetCount)
			}
			if payload.SessionID != "s" {
				t.Errorf("SessionID = %q, want s", payload.SessionID)
			}
			return
		}
	}
	t.Error("EventDashboardLayoutSaved not found in emitted events")
}

func TestDashboardHandler_HandleDashboardPage_WithSavedLayout(t *testing.T) {
	t.Parallel()
	handler, _, _, store, _ := newTestHandler(t)

	// Pre-save a layout
	layout := dashboard.DashboardLayout{
		Placements: []dashboard.WidgetPlacement{
			{Key: dashboard.WidgetKey("plug/w1"), Position: 0, Visible: true},
		},
	}
	if err := store.Save("mysess", layout); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "mysess"})
	w := httptest.NewRecorder()

	handler.HandleDashboardPage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDashboardHandler_HandleWidgetRefresh_WidgetNotFound_Returns404(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := newTestHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /dashboard/widgets/{key}/refresh", handler.HandleWidgetRefresh)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/widgets/does-not-exist/refresh", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestDashboardHandler_HandleLayoutSave_ParseFormError(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := newTestHandler(t)

	// No widget_keys[] posted: empty form
	req := httptest.NewRequest(http.MethodPost, "/dashboard/layout", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "s"})
	w := httptest.NewRecorder()

	handler.HandleLayoutSave(w, req)

	// Should still redirect even with empty layout
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

// TestDashboardHandler_DataFunctionNotFromHTTPRequest verifies security property:
// DataFunction in widget fetch MUST come from the registry, not from HTTP params.
// This is tested more directly in data_provider_test.go but we verify the
// handler does not expose a data_function override path.
func TestDashboardHandler_DataFunctionNotFromHTTPRequest(t *testing.T) {
	t.Parallel()
	// The handler only calls provider.Fetch(key, params): it cannot pass
	// a DataFunction; that is resolved by the provider from the registry.
	// This test ensures HandleWidgetRefresh does not expose such a param.
	handler, _, _, _, _ := newTestHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /dashboard/widgets/{key}/refresh", handler.HandleWidgetRefresh)

	// Try injecting data_function via query param: should be ignored
	req := httptest.NewRequest(http.MethodGet, "/dashboard/widgets/plug%2Fw1/refresh?data_function=evil_fn", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should succeed (200) and use registry-defined function, not evil_fn
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestDashboardHandler_HandleLayoutSave_NoSessionCookie(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := newTestHandler(t)

	form := url.Values{}
	form.Set("widget_keys[]", "plug/w1")

	req := httptest.NewRequest(http.MethodPost, "/dashboard/layout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// No session cookie
	w := httptest.NewRecorder()

	handler.HandleLayoutSave(w, req)

	// Without session, should still redirect (empty sessionID is handled gracefully)
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

// TestDashboardHandler_HandleLayoutSave_UnknownKeysSkipped verifies that
// widget_keys[] values not present in the registry are silently skipped
// and not persisted to the layout store.
func TestDashboardHandler_HandleLayoutSave_UnknownKeysSkipped(t *testing.T) {
	t.Parallel()
	handler, _, _, store, _ := newTestHandler(t)

	// "plug/w1" is registered; "ghost/unknown" is not.
	form := url.Values{}
	form["widget_keys[]"] = []string{"plug/w1", "ghost/unknown"}
	form["visible[]"] = []string{"plug/w1", "ghost/unknown"}

	req := httptest.NewRequest(http.MethodPost, "/dashboard/layout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "sess-filter"})
	w := httptest.NewRecorder()

	handler.HandleLayoutSave(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}

	layout, err := store.Load("sess-filter")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	// Only the known widget should be persisted.
	if len(layout.Placements) != 1 {
		t.Errorf("Placements count = %d, want 1 (unknown key filtered)", len(layout.Placements))
	}
	if layout.Placements[0].Key != dashboard.WidgetKey("plug/w1") {
		t.Errorf("Placement key = %q, want plug/w1", layout.Placements[0].Key)
	}
}

// TestDashboardHandler_HandleDashboardPage_PositionOrdering verifies that
// widgets are rendered in the order defined by the saved layout positions.
func TestDashboardHandler_HandleDashboardPage_PositionOrdering(t *testing.T) {
	t.Parallel()
	handler, reg, _, store, _ := newTestHandler(t)

	// Add a second widget to the registry.
	reg.widgets[dashboard.WidgetKey("plug/w2")] = dashboard.Widget{
		Key:        dashboard.WidgetKey("plug/w2"),
		PluginName: "plug",
		Descriptor: loader.WidgetDescriptor{
			ID: "w2", Title: "W2", DataFunction: "fn2",
		},
	}

	// Save a layout that reverses the natural sort order: w2 first, w1 second.
	layout := dashboard.DashboardLayout{
		Placements: []dashboard.WidgetPlacement{
			{Key: dashboard.WidgetKey("plug/w2"), Position: 0, Visible: true},
			{Key: dashboard.WidgetKey("plug/w1"), Position: 1, Visible: true},
		},
	}
	if err := store.Save("order-sess", layout); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "order-sess"})
	w := httptest.NewRecorder()

	handler.HandleDashboardPage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	// W2 must appear before W1 in the rendered HTML.
	posW2 := strings.Index(body, "W2")
	posW1 := strings.Index(body, "W1")
	if posW2 == -1 || posW1 == -1 {
		t.Fatalf("expected both W1 and W2 in body; W1 at %d, W2 at %d", posW1, posW2)
	}
	if posW2 > posW1 {
		t.Errorf("W2 (pos %d) should appear before W1 (pos %d) based on saved layout", posW2, posW1)
	}
}

// TestDashboardHandler_HandleLayoutSave_NilBusNoPanic verifies that
// HandleLayoutSave does not panic when the event bus is nil.
func TestDashboardHandler_HandleLayoutSave_NilBusNoPanic(t *testing.T) {
	t.Parallel()
	reg := &stubWidgetRegistry{
		widgets: map[dashboard.WidgetKey]dashboard.Widget{
			dashboard.WidgetKey("plug/w1"): {
				Key:        dashboard.WidgetKey("plug/w1"),
				PluginName: "plug",
				Descriptor: loader.WidgetDescriptor{
					ID: "w1", DataFunction: "fn",
				},
			},
		},
	}
	provider := &stubDataProvider{result: []byte(`{}`)}
	store := dashboard.NewDashboardLayoutStore()
	t.Cleanup(func() { store.Close() })

	// bus is nil: must not panic
	handler := dashboard.NewDashboardHandler(reg, provider, store, nil)

	form := url.Values{}
	form.Set("widget_keys[]", "plug/w1")
	form.Set("visible[]", "plug/w1")

	req := httptest.NewRequest(http.MethodPost, "/dashboard/layout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "nil-bus-sess"})
	w := httptest.NewRecorder()

	// Should not panic; should redirect normally.
	handler.HandleLayoutSave(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}
}

// TestDashboardHandler_HandleDashboardPage_WidgetsMetaJSON verifies that
// the rendered page contains a valid WidgetsMeta JSON with correct keys.
func TestDashboardHandler_HandleDashboardPage_WidgetsMetaJSON(t *testing.T) {
	t.Parallel()
	handler, reg, _, _, _ := newTestHandler(t)

	reg.widgets[dashboard.WidgetKey("plug/w2")] = dashboard.Widget{
		Key:        dashboard.WidgetKey("plug/w2"),
		PluginName: "plug",
		Descriptor: loader.WidgetDescriptor{
			ID: "w2", Title: "W2", DataFunction: "fn2",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "meta-sess"})
	w := httptest.NewRecorder()

	handler.HandleDashboardPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	// Extract data-widgets-meta value: it's JSON-encoded in the attribute.
	// The handler produces a JSON array; verify we can find and parse it.
	idx := strings.Index(body, `data-widgets-meta="`)
	if idx == -1 {
		t.Fatal("data-widgets-meta attribute not found in rendered HTML")
	}
	start := idx + len(`data-widgets-meta="`)
	end := strings.Index(body[start:], `"`)
	if end == -1 {
		t.Fatal("could not find closing quote for data-widgets-meta")
	}
	raw := body[start : start+end]
	// Templ HTML-escapes attribute values, so unescape &quot; etc.
	raw = strings.ReplaceAll(raw, "&quot;", `"`)
	raw = strings.ReplaceAll(raw, "&#34;", `"`)
	raw = strings.ReplaceAll(raw, "&amp;", "&")

	var meta []struct {
		Key     string `json:"key"`
		Title   string `json:"title"`
		Visible bool   `json:"visible"`
	}
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("failed to parse WidgetsMeta JSON: %v\nraw: %s", err, raw)
	}
	if len(meta) < 2 {
		t.Errorf("expected at least 2 widgets in meta, got %d", len(meta))
	}
	// All widgets should default to visible when no layout is saved.
	for _, m := range meta {
		if !m.Visible {
			t.Errorf("widget %q should default to visible", m.Key)
		}
	}
}

// TestDashboardHandler_HandleDashboardPage_HiddenWidgetNotRendered verifies that
// a widget marked as invisible in saved layout does not appear in the grid HTML.
func TestDashboardHandler_HandleDashboardPage_HiddenWidgetNotRendered(t *testing.T) {
	t.Parallel()
	handler, reg, _, store, _ := newTestHandler(t)

	reg.widgets[dashboard.WidgetKey("plug/w2")] = dashboard.Widget{
		Key:        dashboard.WidgetKey("plug/w2"),
		PluginName: "plug",
		Descriptor: loader.WidgetDescriptor{
			ID: "w2", Title: "Hidden Widget", DataFunction: "fn2",
		},
	}

	// Save layout: w1 visible, w2 hidden.
	layout := dashboard.DashboardLayout{
		Placements: []dashboard.WidgetPlacement{
			{Key: dashboard.WidgetKey("plug/w1"), Position: 0, Visible: true},
			{Key: dashboard.WidgetKey("plug/w2"), Position: 1, Visible: false},
		},
	}
	if err := store.Save("hidden-sess", layout); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "hidden-sess"})
	w := httptest.NewRecorder()

	handler.HandleDashboardPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	// W1 card should be rendered (it's visible).
	if !strings.Contains(body, "W1") {
		t.Error("visible widget W1 should appear in rendered HTML")
	}
	// The hidden widget's card should NOT appear in the grid.
	// The card has data-widget-key attribute; check it's absent for w2.
	if strings.Contains(body, `data-widget-key="plug/w2"`) {
		t.Error("hidden widget plug/w2 should not be rendered as a card in the grid")
	}
}

// TestDashboardHandler_HandleDashboardPage_SettingsButton verifies the
// dashboard settings gear button is present in the rendered HTML.
func TestDashboardHandler_HandleDashboardPage_SettingsButton(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	handler.HandleDashboardPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "dashboard-settings-btn") {
		t.Error("dashboard-settings-btn class not found in rendered HTML")
	}
	if !strings.Contains(body, "MC.dashboardSettings()") {
		t.Error("MC.dashboardSettings() onclick handler not found")
	}
}

// TestDashboardHandler_HandleDashboardPage_WidgetCardsAreDraggable verifies
// that widget cards have draggable="true" and widget-card class.
func TestDashboardHandler_HandleDashboardPage_WidgetCardsAreDraggable(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	handler.HandleDashboardPage(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `draggable="true"`) {
		t.Error("widget cards should have draggable=\"true\" attribute")
	}
	if !strings.Contains(body, "widget-card") {
		t.Error("widget cards should have widget-card class")
	}
}

// TestDashboardHandler_HandleDashboardPage_NoLayoutFormInHTML verifies
// that the old inline layout form is no longer rendered.
func TestDashboardHandler_HandleDashboardPage_NoLayoutFormInHTML(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	handler.HandleDashboardPage(w, req)

	body := w.Body.String()
	if strings.Contains(body, `action="/dashboard/layout"`) {
		t.Error("old layout form should no longer be rendered inline on the dashboard page")
	}
	if strings.Contains(body, "Dashboard Layout") {
		t.Error("'Dashboard Layout' card title should no longer appear on the page")
	}
}

// Ensure errors.Is works for sentinel errors
func TestDashboardErrors_SentinelValues(t *testing.T) {
	t.Parallel()

	if !errors.Is(fmt.Errorf("wrap: %w", dashboard.ErrWidgetNotFound), dashboard.ErrWidgetNotFound) {
		t.Error("ErrWidgetNotFound should be identifiable via errors.Is")
	}
	if !errors.Is(fmt.Errorf("wrap: %w", dashboard.ErrWidgetDataFetchFailed), dashboard.ErrWidgetDataFetchFailed) {
		t.Error("ErrWidgetDataFetchFailed should be identifiable via errors.Is")
	}
	if !errors.Is(fmt.Errorf("wrap: %w", dashboard.ErrInvalidWidgetDescriptor), dashboard.ErrInvalidWidgetDescriptor) {
		t.Error("ErrInvalidWidgetDescriptor should be identifiable via errors.Is")
	}
	if !errors.Is(fmt.Errorf("wrap: %w", dashboard.ErrLayoutNotFound), dashboard.ErrLayoutNotFound) {
		t.Error("ErrLayoutNotFound should be identifiable via errors.Is")
	}
}
