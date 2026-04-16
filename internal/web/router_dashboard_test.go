package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/web"
)

// stubDashboardHandler records which handler methods were called.
type stubDashboardHandler struct {
	dashboardPageCalled bool
	widgetRefreshCalled bool
	layoutSaveCalled    bool
}

func (s *stubDashboardHandler) HandleDashboardPage(w http.ResponseWriter, r *http.Request) {
	s.dashboardPageCalled = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("dashboard"))
}

func (s *stubDashboardHandler) HandleWidgetRefresh(w http.ResponseWriter, r *http.Request) {
	s.widgetRefreshCalled = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("widget"))
}

func (s *stubDashboardHandler) HandleLayoutSave(w http.ResponseWriter, r *http.Request) {
	s.layoutSaveCalled = true
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// buildDashboardMux creates a mux with Dashboard routes registered and
// a session store that accepts "valid-session" as a session ID.
func buildDashboardMux(t *testing.T) (*http.ServeMux, *stubDashboardHandler, *stubSessionStore) {
	t.Helper()
	stub := &stubDashboardHandler{}
	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "valid-session",
			CSRFToken: "valid-csrf",
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:      auth,
		Sessions:  store,
		CSRF:      csrf,
		Dashboard: stub,
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)
	return mux, stub, store
}

// TestRegisterDashboardRoutes_GetDashboard verifies that GET /dashboard
// returns 200 when a valid session cookie is present.
func TestRegisterDashboardRoutes_GetDashboard(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildDashboardMux(t)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /dashboard with valid session: status = %d, want 200", w.Code)
	}
	if !stub.dashboardPageCalled {
		t.Error("HandleDashboardPage was not called")
	}
}

// TestRegisterDashboardRoutes_GetDashboard_NoSession verifies that GET /dashboard
// redirects to /login when no session cookie is present.
func TestRegisterDashboardRoutes_GetDashboard_NoSession(t *testing.T) {
	t.Parallel()
	mux, _, _ := buildDashboardMux(t)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("GET /dashboard without session: status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

// TestRegisterDashboardRoutes_WidgetRefresh verifies GET /dashboard/widgets/{key}/refresh
// calls HandleWidgetRefresh when authenticated.
func TestRegisterDashboardRoutes_WidgetRefresh(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildDashboardMux(t)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/widgets/tasks-plugin%2Ftask-summary/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET widget refresh with valid session: status = %d, want 200", w.Code)
	}
	if !stub.widgetRefreshCalled {
		t.Error("HandleWidgetRefresh was not called")
	}
}

// TestRegisterDashboardRoutes_WidgetRefresh_NoSession verifies the refresh route
// requires authentication.
func TestRegisterDashboardRoutes_WidgetRefresh_NoSession(t *testing.T) {
	t.Parallel()
	mux, _, _ := buildDashboardMux(t)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/widgets/tasks-plugin%2Ftask-summary/refresh", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("GET widget refresh without session: status = %d, want 303", w.Code)
	}
}

// TestRegisterDashboardRoutes_LayoutSave_WithValidCSRF verifies POST /dashboard/layout
// succeeds with a valid session and CSRF token.
func TestRegisterDashboardRoutes_LayoutSave_WithValidCSRF(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildDashboardMux(t)

	form := url.Values{"csrf_token": {"valid-csrf"}, "widget_keys[]": {"plug/w1"}}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/layout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /dashboard/layout with valid CSRF: status = %d, want 303", w.Code)
	}
	if !stub.layoutSaveCalled {
		t.Error("HandleLayoutSave was not called")
	}
}

// TestRegisterDashboardRoutes_LayoutSave_WithoutCSRF verifies POST /dashboard/layout
// returns 403 when no CSRF token is supplied.
func TestRegisterDashboardRoutes_LayoutSave_WithoutCSRF(t *testing.T) {
	t.Parallel()
	// Use a store where CSRF validation will fail.
	stub := &stubDashboardHandler{}
	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "valid-session",
			CSRFToken: "valid-csrf",
		},
	}
	// CSRF provider always returns an error: simulates mismatched token.
	csrf := &stubCSRFProvider{token: "valid-csrf", err: web.ErrCSRFTokenInvalid}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:      auth,
		Sessions:  store,
		CSRF:      csrf,
		Dashboard: stub,
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)

	req := httptest.NewRequest(http.MethodPost, "/dashboard/layout", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("POST /dashboard/layout without CSRF: status = %d, want 403", w.Code)
	}
	if stub.layoutSaveCalled {
		t.Error("HandleLayoutSave must not be called when CSRF validation fails")
	}
}

// TestRegisterDashboardRoutes_NilDashboard verifies that when Dashboard is nil,
// /dashboard returns 404 (route not registered).
func TestRegisterDashboardRoutes_NilDashboard(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:      auth,
		Sessions:  store,
		CSRF:      csrf,
		Dashboard: nil, // dashboard routes must not be registered
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "x"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /dashboard with nil Dashboard: status = %d, want 404", w.Code)
	}
}
