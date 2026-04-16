package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plekt-dev/plekt/internal/web"
)

// buildUserMgmtMux constructs a mux with a stub admin handler and a session
// whose Role field is controlled by the caller.
func buildUserMgmtMux(t *testing.T, role string) (*http.ServeMux, *stubAdminHandler) {
	t.Helper()
	stub := &stubAdminHandler{}
	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "test-session",
			CSRFToken: "test-csrf",
			Role:      role,
		},
	}
	csrf := &stubCSRFProvider{token: "test-csrf", err: nil}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Admin:    stub,
	}
	mux := web.NewWebRouter(cfg).Build(nil)
	return mux, stub
}

// buildUserMgmtMuxNoSession builds a mux that always rejects session lookups.
func buildUserMgmtMuxNoSession(t *testing.T) *http.ServeMux {
	t.Helper()
	stub := &stubAdminHandler{}
	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	csrf := &stubCSRFProvider{token: "test-csrf", err: nil}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Admin:    stub,
	}
	return web.NewWebRouter(cfg).Build(nil)
}

// ---------------------------------------------------------------------------
// RequireRole integration tests for /admin/users
// ---------------------------------------------------------------------------

func TestRequireRole_AdminSession_GetAdminUsers_Returns200(t *testing.T) {
	t.Parallel()
	mux, _ := buildUserMgmtMux(t, "admin")

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "test-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("admin role GET /admin/users: status = %d, want 200", w.Code)
	}
	if w.Body.String() != "user-list" {
		t.Errorf("body = %q, want %q", w.Body.String(), "user-list")
	}
}

func TestRequireRole_UserSession_GetAdminUsers_Returns403(t *testing.T) {
	t.Parallel()
	mux, _ := buildUserMgmtMux(t, "user")

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "test-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("user role GET /admin/users: status = %d, want 403", w.Code)
	}
	if w.Body.String() == "user-list" {
		t.Error("HandleUserList must not be called for user role")
	}
}

func TestRequireRole_NoSession_GetAdminUsers_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	mux := buildUserMgmtMuxNoSession(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	// No mc_session cookie: WebAuthMiddleware should redirect.
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("no session GET /admin/users: status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestRequireRole_EmptyRole_GetAdminUsers_Returns403(t *testing.T) {
	t.Parallel()
	// A session with an empty role must not access admin routes.
	mux, _ := buildUserMgmtMux(t, "")

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "test-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("empty role GET /admin/users: status = %d, want 403", w.Code)
	}
	if w.Body.String() == "user-list" {
		t.Error("HandleUserList must not be called for empty role")
	}
}

// Session revoke moved to /user/sessions/{id}/revoke: available to all
// authenticated users (no admin role required), tested in router_admin_test.go.
