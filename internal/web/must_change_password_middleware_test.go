package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plekt-dev/plekt/internal/web"
)

func TestMustChangePasswordMiddleware_MustChange_RedirectsToChangePw(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{
		ID:                 "sess-mustchange",
		UserID:             1,
		CSRFToken:          "tok",
		MustChangePassword: true,
	}
	store := &stubSessionStore{entry: entry}
	mw := web.MustChangePasswordMiddleware(store)

	// Use the existing injectSession helper (declared in token_handler_test.go)
	// to inject the session into the request context.
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req = injectSession(req, entry)

	w := httptest.NewRecorder()
	mw(http.HandlerFunc(okHandler)).ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 when MustChangePassword=true", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/change-password" {
		t.Errorf("Location = %q, want /change-password", loc)
	}
}

func TestMustChangePasswordMiddleware_NoMustChange_CallsNext(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{
		ID:                 "sess-normal",
		UserID:             1,
		CSRFToken:          "tok",
		MustChangePassword: false,
	}
	store := &stubSessionStore{entry: entry}
	mw := web.MustChangePasswordMiddleware(store)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req = injectSession(req, entry)

	w := httptest.NewRecorder()
	mw(http.HandlerFunc(okHandler)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when MustChangePassword=false", w.Code)
	}
}

func TestMustChangePasswordMiddleware_PathChangePassword_PassesThrough(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{
		ID:                 "sess-mustchange-cp",
		UserID:             1,
		CSRFToken:          "tok",
		MustChangePassword: true,
	}
	store := &stubSessionStore{entry: entry}
	mw := web.MustChangePasswordMiddleware(store)

	// /change-password itself must always pass through even when MustChangePassword=true.
	req := httptest.NewRequest(http.MethodGet, "/change-password", nil)
	req = injectSession(req, entry)

	w := httptest.NewRecorder()
	mw(http.HandlerFunc(okHandler)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for /change-password path", w.Code)
	}
}

func TestMustChangePasswordMiddleware_PathLogout_PassesThrough(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{
		ID:                 "sess-mustchange-logout",
		UserID:             1,
		CSRFToken:          "tok",
		MustChangePassword: true,
	}
	store := &stubSessionStore{entry: entry}
	mw := web.MustChangePasswordMiddleware(store)

	// /logout must always pass through even when MustChangePassword=true.
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req = injectSession(req, entry)

	w := httptest.NewRecorder()
	mw(http.HandlerFunc(okHandler)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for /logout path", w.Code)
	}
}

func TestMustChangePasswordMiddleware_NoSessionInContext_PassesThrough(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x"}}
	mw := web.MustChangePasswordMiddleware(store)

	// Request has no session in context (not passed through WebAuthMiddleware).
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)

	w := httptest.NewRecorder()
	mw(http.HandlerFunc(okHandler)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when no session in context", w.Code)
	}
}

func TestMustChangePasswordMiddleware_MustChange_VariousPaths_Redirect(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{
		ID:                 "sess-mustchange-paths",
		UserID:             1,
		CSRFToken:          "tok",
		MustChangePassword: true,
	}
	store := &stubSessionStore{entry: entry}
	mw := web.MustChangePasswordMiddleware(store)

	paths := []string{"/dashboard", "/admin/plugins", "/user", "/tokens"}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = injectSession(req, entry)

		w := httptest.NewRecorder()
		mw(http.HandlerFunc(okHandler)).ServeHTTP(w, req)

		if w.Code != http.StatusSeeOther {
			t.Errorf("path %q: status = %d, want 303 when MustChangePassword=true", path, w.Code)
		}
	}
}

func TestRequireRole_CorrectRole_PassesThrough(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{
		ID:        "sess-admin-role",
		UserID:    1,
		CSRFToken: "tok",
		Role:      "admin",
	}
	store := &stubSessionStore{entry: entry}
	requireAdmin := web.RequireRole("admin", store)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = injectSession(req, entry)

	w := httptest.NewRecorder()
	requireAdmin(http.HandlerFunc(okHandler)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for admin role", w.Code)
	}
}

func TestRequireRole_WrongRole_Returns403(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{
		ID:        "sess-user-role",
		UserID:    1,
		CSRFToken: "tok",
		Role:      "user",
	}
	store := &stubSessionStore{entry: entry}
	requireAdmin := web.RequireRole("admin", store)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = injectSession(req, entry)

	w := httptest.NewRecorder()
	requireAdmin(http.HandlerFunc(okHandler)).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for viewer role accessing admin route", w.Code)
	}
}

func TestRequireRole_NoSessionInContext_Returns403(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x"}}
	requireAdmin := web.RequireRole("admin", store)

	// No session in context: request was not processed by WebAuthMiddleware.
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)

	w := httptest.NewRecorder()
	requireAdmin(http.HandlerFunc(okHandler)).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when no session in context", w.Code)
	}
}

func TestWebAuthMiddleware_PreLoginSession_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	// A pre-login session has UserID==0 and must be rejected by WebAuthMiddleware.
	entry := web.WebSessionEntry{
		ID:        "prelogin-sess-direct",
		UserID:    0,
		CSRFToken: "csrf-pre",
	}

	store := &mcpPreLoginStore{entry: entry}
	h := web.WebAuthMiddleware(store, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: string(entry.ID)})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 for pre-login session (UserID==0)", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

// mcpPreLoginStore returns exactly the configured entry without UserID fix-up.
// (stubSessionStore in middleware_test.go forces UserID=1 when entry.UserID==0)
type mcpPreLoginStore struct {
	entry web.WebSessionEntry
}

func (s *mcpPreLoginStore) Create(_ string, _ int64, _ string, _ string, _ bool) (web.WebSessionEntry, error) {
	return s.entry, nil
}
func (s *mcpPreLoginStore) Get(_ web.WebSessionID) (web.WebSessionEntry, error) {
	return s.entry, nil
}
func (s *mcpPreLoginStore) Delete(_ web.WebSessionID)                              {}
func (s *mcpPreLoginStore) ListAll() []web.WebSessionEntry                         { return nil }
func (s *mcpPreLoginStore) SetMustChangePassword(_ web.WebSessionID, _ bool) error { return nil }
func (s *mcpPreLoginStore) Close() error                                           { return nil }

func TestMustChangePasswordMiddleware_IntegratedStack_MustChange_Redirects(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{
		ID:                 "sess-integrated-mcp",
		UserID:             2,
		CSRFToken:          "tok",
		MustChangePassword: true,
	}
	store := &mcpPreLoginStore{entry: entry}
	mw := web.MustChangePasswordMiddleware(store)

	// Stack: WebAuthMiddleware → MustChangePasswordMiddleware → handler
	h := web.WebAuthMiddleware(store, mw(http.HandlerFunc(okHandler)))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: string(entry.ID)})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 via integrated middleware stack", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/change-password" {
		t.Errorf("Location = %q, want /change-password", loc)
	}
}

func TestMustChangePasswordMiddleware_IntegratedStack_NoMustChange_Passes(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{
		ID:                 "sess-integrated-mcp2",
		UserID:             2,
		CSRFToken:          "tok",
		MustChangePassword: false,
	}
	store := &mcpPreLoginStore{entry: entry}
	mw := web.MustChangePasswordMiddleware(store)

	h := web.WebAuthMiddleware(store, mw(http.HandlerFunc(okHandler)))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: string(entry.ID)})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when MustChangePassword=false", w.Code)
	}
}

func TestMustChangePasswordMiddleware_BackgroundContext_NoSession_Passes(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x"}}
	mw := web.MustChangePasswordMiddleware(store)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	mw(http.HandlerFunc(okHandler)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when no session in context", w.Code)
	}
}
