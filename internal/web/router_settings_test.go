package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/web"
)

// --- stub settings handler ---

type stubSettingsHandler struct {
	pageCalled bool
	saveCalled bool
}

func (s *stubSettingsHandler) HandleSettingsPage(w http.ResponseWriter, r *http.Request) {
	s.pageCalled = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("settings-page"))
}

func (s *stubSettingsHandler) HandleSettingsSave(w http.ResponseWriter, r *http.Request) {
	s.saveCalled = true
	http.Redirect(w, r, "/admin/settings?saved=1", http.StatusSeeOther)
}

// --- helpers ---

func buildSettingsMux(t *testing.T) (*http.ServeMux, *stubSettingsHandler, *stubSessionStore) {
	t.Helper()
	stub := &stubSettingsHandler{}
	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "valid-session",
			CSRFToken: "valid-csrf",
			Role:      "admin",
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Settings: stub,
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)
	return mux, stub, store
}

// --- route registration tests ---

func TestRegisterSettingsRoutes_GetSettingsPage(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildSettingsMux(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /admin/settings: status = %d, want 200", w.Code)
	}
	if !stub.pageCalled {
		t.Error("HandleSettingsPage was not called")
	}
}

func TestRegisterSettingsRoutes_GetSettings_NoSession(t *testing.T) {
	t.Parallel()
	mux, _, _ := buildSettingsMux(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("GET /admin/settings without session: status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestRegisterSettingsRoutes_PostSettings_WithCSRF(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildSettingsMux(t)

	form := url.Values{"csrf_token": {"valid-csrf"}, "admin_email": {"test@example.com"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /admin/settings with CSRF: status = %d, want 303", w.Code)
	}
	if !stub.saveCalled {
		t.Error("HandleSettingsSave was not called")
	}
}

func TestRegisterSettingsRoutes_PostSettings_WithoutCSRF(t *testing.T) {
	t.Parallel()
	stub := &stubSettingsHandler{}
	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf", Role: "admin"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: web.ErrCSRFTokenInvalid}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Settings: stub,
	}
	mux := web.NewWebRouter(cfg).Build(nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/settings", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("POST /admin/settings without CSRF: status = %d, want 403", w.Code)
	}
	if stub.saveCalled {
		t.Error("HandleSettingsSave must not be called when CSRF fails")
	}
}

func TestRegisterSettingsRoutes_NilSettings_Returns404(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Settings: nil, // settings routes must not be registered
	}
	mux := web.NewWebRouter(cfg).Build(nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "x"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /admin/settings with nil Settings: status = %d, want 404", w.Code)
	}
}
