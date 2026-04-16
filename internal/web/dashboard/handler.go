package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// DashboardHandler handles HTTP requests for the dashboard UI.
type DashboardHandler interface {
	// HandleDashboardPage renders the full dashboard page.
	HandleDashboardPage(w http.ResponseWriter, r *http.Request)
	// HandleWidgetRefresh renders a single widget partial for htmx refresh.
	HandleWidgetRefresh(w http.ResponseWriter, r *http.Request)
	// HandleLayoutSave persists the dashboard layout from a form POST.
	HandleLayoutSave(w http.ResponseWriter, r *http.Request)
}

// dashboardHandler is the concrete DashboardHandler.
type dashboardHandler struct {
	registry WidgetRegistry
	provider DashboardDataProvider
	layout   DashboardLayoutStore
	bus      eventbus.EventBus
}

// NewDashboardHandler constructs a DashboardHandler with all dependencies.
func NewDashboardHandler(
	registry WidgetRegistry,
	provider DashboardDataProvider,
	layout DashboardLayoutStore,
	bus eventbus.EventBus,
) DashboardHandler {
	return &dashboardHandler{
		registry: registry,
		provider: provider,
		layout:   layout,
		bus:      bus,
	}
}

// sessionIDFromRequest extracts the session_id cookie value or returns empty string.
func sessionIDFromRequest(r *http.Request) string {
	c, err := r.Cookie("session_id")
	if err != nil {
		return ""
	}
	return c.Value
}

// HandleDashboardPage renders the full dashboard page.
// It loads the saved layout for the session (defaulting to all visible)
// and fetches data for each visible widget.
func (h *dashboardHandler) HandleDashboardPage(w http.ResponseWriter, r *http.Request) {
	sessionID := sessionIDFromRequest(r)

	// Load saved layout; default to all-visible if not found.
	savedLayout, err := h.layout.Load(sessionID)
	if err != nil && !errors.Is(err, ErrLayoutNotFound) {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	allWidgets := h.registry.List()

	// Build visibility and position maps from saved layout.
	visibleMap := make(map[WidgetKey]bool)
	positionMap := make(map[WidgetKey]int)
	if errors.Is(err, ErrLayoutNotFound) {
		// Default: all widgets visible, registry order determines position.
		for i, w := range allWidgets {
			visibleMap[w.Key] = true
			positionMap[w.Key] = i
		}
	} else {
		for _, p := range savedLayout.Placements {
			visibleMap[p.Key] = p.Visible
			positionMap[p.Key] = p.Position
		}
		// Any new widgets not in layout default to visible at the end.
		nextPos := len(savedLayout.Placements)
		for _, w := range allWidgets {
			if _, known := visibleMap[w.Key]; !known {
				visibleMap[w.Key] = true
				positionMap[w.Key] = nextPos
				nextPos++
			}
		}
	}

	// Sort widgets by saved position so the page reflects the user's ordering.
	sort.Slice(allWidgets, func(i, j int) bool {
		pi := positionMap[allWidgets[i].Key]
		pj := positionMap[allWidgets[j].Key]
		if pi != pj {
			return pi < pj
		}
		// Tie-break by key for deterministic output.
		return allWidgets[i].Key < allWidgets[j].Key
	})

	renderWidgets := make([]templates.WidgetRenderData, 0, len(allWidgets))

	type widgetMeta struct {
		Key     string `json:"key"`
		Title   string `json:"title"`
		Visible bool   `json:"visible"`
	}
	metaList := make([]widgetMeta, 0, len(allWidgets))

	for _, widget := range allWidgets {
		visible := visibleMap[widget.Key]
		rd := templates.WidgetRenderData{
			Key:            string(widget.Key),
			Title:          widget.Descriptor.Title,
			Width:          widget.Descriptor.Width,
			RefreshSeconds: widget.Descriptor.RefreshSeconds,
			RefreshURL:     widgetRefreshURL(widget.Key),
			LinkTemplate:   widget.Descriptor.LinkTemplate,
			Visible:        visible,
		}
		if visible {
			resp, fetchErr := h.provider.Fetch(r.Context(), WidgetDataRequest{
				Key: widget.Key,
			})
			if fetchErr != nil {
				rd.Error = "data unavailable"
			} else {
				rd.DataJSON = string(resp.JSON)
			}
		}
		renderWidgets = append(renderWidgets, rd)
		metaList = append(metaList, widgetMeta{
			Key:     string(widget.Key),
			Title:   widget.Descriptor.Title,
			Visible: visible,
		})
	}

	metaJSON, _ := json.Marshal(metaList)

	// CSRF token for the layout form: read from header (injected by middleware)
	// or from the form value on page reload. Empty string is safe: the CSRF
	// middleware validates on POST, not on GET.
	csrfToken := r.Header.Get("X-CSRF-Token")
	if csrfToken == "" {
		csrfToken = r.FormValue("csrf_token")
	}

	data := templates.DashboardPageData{
		CSRFToken:   csrfToken,
		Widgets:     renderWidgets,
		WidgetsMeta: string(metaJSON),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.DashboardPage(data).Render(r.Context(), w); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// HandleWidgetRefresh renders a single widget partial for htmx polling.
// The key is extracted via r.PathValue("key"): NOT from query params.
// Returns 404 if the widget key is not found in the registry.
func (h *dashboardHandler) HandleWidgetRefresh(w http.ResponseWriter, r *http.Request) {
	rawKey := r.PathValue("key")
	key := WidgetKey(rawKey)

	widget, err := h.registry.Get(key)
	if err != nil {
		if errors.Is(err, ErrWidgetNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rd := templates.WidgetRenderData{
		Key:            string(widget.Key),
		Title:          widget.Descriptor.Title,
		Width:          widget.Descriptor.Width,
		RefreshSeconds: widget.Descriptor.RefreshSeconds,
		RefreshURL:     widgetRefreshURL(widget.Key),
		LinkTemplate:   widget.Descriptor.LinkTemplate,
	}

	resp, fetchErr := h.provider.Fetch(r.Context(), WidgetDataRequest{
		Key: key,
	})
	if fetchErr != nil {
		rd.Error = "data unavailable"
	} else {
		rd.DataJSON = string(resp.JSON)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.WidgetInner(rd).Render(r.Context(), w); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// HandleLayoutSave parses the layout form and persists it.
// Expects form fields: widget_keys[] (all widget keys) and visible[] (visible keys).
// Redirects to /dashboard on success.
func (h *dashboardHandler) HandleLayoutSave(w http.ResponseWriter, r *http.Request) {
	sessionID := sessionIDFromRequest(r)

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	keys := r.Form["widget_keys[]"]
	visibleSet := make(map[string]bool)
	for _, k := range r.Form["visible[]"] {
		visibleSet[k] = true
	}

	placements := make([]WidgetPlacement, 0, len(keys))
	for i, k := range keys {
		// Skip keys that are not registered: prevents storing unknown widget keys.
		if _, err := h.registry.Get(WidgetKey(k)); err != nil {
			continue
		}
		placements = append(placements, WidgetPlacement{
			Key:      WidgetKey(k),
			Position: i,
			Visible:  visibleSet[k],
		})
	}

	layout := DashboardLayout{
		Placements: placements,
		UpdatedAt:  time.Now().UTC(),
	}

	if err := h.layout.Save(sessionID, layout); err != nil {
		http.Error(w, "save error", http.StatusInternalServerError)
		return
	}

	if h.bus != nil {
		h.bus.Emit(r.Context(), eventbus.Event{
			Name: eventbus.EventDashboardLayoutSaved,
			Payload: eventbus.DashboardLayoutSavedPayload{
				SessionID:   sessionID,
				WidgetCount: len(placements),
			},
		})
	}

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// widgetRefreshURL builds the refresh URL for a widget key.
func widgetRefreshURL(key WidgetKey) string {
	return fmt.Sprintf("/dashboard/widgets/%s/refresh", url.PathEscape(string(key)))
}
