package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/web"
	"github.com/plekt-dev/plekt/internal/web/audit"
)

// ---------------------------------------------------------------------------
// Stub WebAdminHandler for router tests
// ---------------------------------------------------------------------------

type stubAdminHandler struct {
	profileCalled bool
	revokeCalled  bool
	auditCalled   bool
}

func (s *stubAdminHandler) HandleUserProfile(w http.ResponseWriter, r *http.Request) {
	s.profileCalled = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("user-profile"))
}

func (s *stubAdminHandler) HandleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	s.revokeCalled = true
	http.Redirect(w, r, "/user", http.StatusSeeOther)
}

func (s *stubAdminHandler) HandleAuditLog(w http.ResponseWriter, r *http.Request) {
	s.auditCalled = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("audit-log"))
}

func (s *stubAdminHandler) HandleUserList(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("user-list"))
}

func (s *stubAdminHandler) HandleUserCreate(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *stubAdminHandler) HandleUserDelete(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *stubAdminHandler) HandleUserChangeRole(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *stubAdminHandler) HandleUserResetPassword(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Build helper
// ---------------------------------------------------------------------------

func buildAdminMux(t *testing.T) (*http.ServeMux, *stubAdminHandler, *stubSessionStore) {
	t.Helper()
	stub := &stubAdminHandler{}
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
		Admin:    stub,
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)
	return mux, stub, store
}

func buildAdminMuxWithRealHandler(t *testing.T) (*http.ServeMux, *listingSessionStore, *stubAuditLogStore) {
	t.Helper()
	now := time.Now()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{
				ID:        "valid-session",
				CSRFToken: "valid-csrf",
				Role:      "admin",
				CreatedAt: now,
				ExpiresAt: now.Add(24 * time.Hour),
			},
		},
		all: []web.WebSessionEntry{
			{ID: "valid-session", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)},
		},
	}
	auditStore := &stubAuditLogStore{}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}

	adminHandler, err := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store,
		AuditLog: auditStore,
		Bus:      bus,
		CSRF:     csrf,
	})
	if err != nil {
		t.Fatalf("NewWebAdminHandler: %v", err)
	}

	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Admin:    adminHandler,
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)
	return mux, store, auditStore
}

// ---------------------------------------------------------------------------
// Route registration tests (stub handler)
// ---------------------------------------------------------------------------

func TestRegisterAdminRoutes_GetUserProfile(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildAdminMux(t)

	req := httptest.NewRequest(http.MethodGet, "/user", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /user: status = %d, want 200", w.Code)
	}
	if !stub.profileCalled {
		t.Error("HandleUserProfile was not called")
	}
}

func TestRegisterAdminRoutes_GetUserProfile_NoSession(t *testing.T) {
	t.Parallel()
	mux, _, _ := buildAdminMux(t)

	req := httptest.NewRequest(http.MethodGet, "/user", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("GET /user without session: status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestRegisterAdminRoutes_PostRevoke_WithCSRF(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildAdminMux(t)

	form := url.Values{"csrf_token": {"valid-csrf"}}
	req := httptest.NewRequest(http.MethodPost, "/user/sessions/some-id/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /user/sessions/{id}/revoke with CSRF: status = %d, want 303", w.Code)
	}
	if !stub.revokeCalled {
		t.Error("HandleSessionRevoke was not called")
	}
}

func TestRegisterAdminRoutes_PostRevoke_WithoutCSRF(t *testing.T) {
	t.Parallel()
	stub := &stubAdminHandler{}
	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf", Role: "admin"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: web.ErrCSRFTokenInvalid}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Admin:    stub,
	}
	mux := web.NewWebRouter(cfg).Build(nil)

	req := httptest.NewRequest(http.MethodPost, "/user/sessions/some-id/revoke", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("POST /user/sessions/revoke without CSRF: status = %d, want 403", w.Code)
	}
	if stub.revokeCalled {
		t.Error("HandleSessionRevoke must not be called when CSRF fails")
	}
}

func TestRegisterAdminRoutes_GetAudit(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildAdminMux(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /admin/audit: status = %d, want 200", w.Code)
	}
	if !stub.auditCalled {
		t.Error("HandleAuditLog was not called")
	}
}

func TestRegisterAdminRoutes_GetAudit_NoSession(t *testing.T) {
	t.Parallel()
	mux, _, _ := buildAdminMux(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("GET /admin/audit without session: status = %d, want 303", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Nil Admin → routes return 404
// ---------------------------------------------------------------------------

func TestRegisterAdminRoutes_NilAdmin_Returns404(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Admin:    nil, // admin routes must not be registered
	}
	mux := web.NewWebRouter(cfg).Build(nil)

	for _, path := range []string{"/user", "/admin/audit"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "mc_session", Value: "x"})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s with nil Admin: status = %d, want 404", path, w.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// Integration: real handler through router
// ---------------------------------------------------------------------------

func TestRegisterAdminRoutes_Integration_UserProfile(t *testing.T) {
	t.Parallel()
	mux, store, _ := buildAdminMuxWithRealHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/user", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /user integration: status = %d, want 200", w.Code)
	}
	if w.Body.String() == "" {
		t.Error("user profile page body should not be empty")
	}
}

func TestRegisterAdminRoutes_Integration_AuditLog(t *testing.T) {
	t.Parallel()
	mux, store, auditStore := buildAdminMuxWithRealHandler(t)

	auditStore.entries = []audit.AuditLogEntry{
		{EventName: "web.auth.login_success", RemoteAddr: "1.1.1.1", OccurredAt: time.Now().UTC()},
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /admin/audit integration: status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "web.auth.login_success") {
		t.Error("audit page should contain event names")
	}
}
