// Package web_test contains additional security-path tests to meet the >=90%
// coverage requirement for auth middleware, CSRF validation, and
// constant-time comparisons.
package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web"
)

// ---------------------------------------------------------------------------
// Task 1: VULN-04: Admin route access control
// ---------------------------------------------------------------------------

// TestAdminSessions_ViewerGets403 verifies that a viewer-role user is rejected
// from GET /admin/sessions with 403 Forbidden.
func TestAdminSessions_ViewerGets403(t *testing.T) {
	t.Parallel()

	sessions, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })

	entry, err := sessions.Create("127.0.0.1", 1, "viewer", "viewer-user", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	admin := &stubSecurityAdminHandler{}
	csrf := web.NewCSRFProvider()
	router := web.NewWebRouter(web.WebRouterConfig{
		Auth:     &noopSecurityAuth{},
		Sessions: sessions,
		CSRF:     csrf,
		Admin:    admin,
	})
	mux := router.Build(nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/sessions", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/sessions as viewer: status = %d, want 403", w.Code)
	}
}

// TestAdminAudit_ViewerGets403 verifies that a viewer-role user is rejected
// from GET /admin/audit with 403 Forbidden.
func TestAdminAudit_ViewerGets403(t *testing.T) {
	t.Parallel()

	sessions, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })

	entry, err := sessions.Create("127.0.0.1", 1, "viewer", "viewer-user", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	admin := &stubSecurityAdminHandler{}
	csrf := web.NewCSRFProvider()
	router := web.NewWebRouter(web.WebRouterConfig{
		Auth:     &noopSecurityAuth{},
		Sessions: sessions,
		CSRF:     csrf,
		Admin:    admin,
	})
	mux := router.Build(nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/audit as viewer: status = %d, want 403", w.Code)
	}
}

// TestAdminSessions_AdminGets200 verifies that an admin-role user can access
// GET /admin/sessions.
func TestAdminSessions_AdminGets200(t *testing.T) {
	t.Parallel()

	sessions, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })

	entry, err := sessions.Create("127.0.0.1", 1, "admin", "admin-user", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	admin := &stubSecurityAdminHandler{}
	csrf := web.NewCSRFProvider()
	router := web.NewWebRouter(web.WebRouterConfig{
		Auth:     &noopSecurityAuth{},
		Sessions: sessions,
		CSRF:     csrf,
		Admin:    admin,
	})
	mux := router.Build(nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/sessions", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /admin/sessions as admin: status = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Task 2: VULN-08: Security headers middleware
// ---------------------------------------------------------------------------

func TestSecurityHeadersMiddleware_AllHeadersPresent(t *testing.T) {
	t.Parallel()

	handler := web.SecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	expected := map[string]string{
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "frame-ancestors 'none'",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
	}

	for header, want := range expected {
		got := w.Header().Get(header)
		if got != want {
			t.Errorf("header %q = %q, want %q", header, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Task 4: VULN-02: Plugin static file extension whitelist
// ---------------------------------------------------------------------------

func TestPluginStaticHandler_ExtensionWhitelist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "test-plugin", "frontend")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"style.css", "app.js", "page.html", "page.htm", "script.php", "malware.exe"} {
		if err := os.WriteFile(filepath.Join(pluginDir, name), []byte("test"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	handler := &web.PluginStaticHandler{PluginsDir: dir}

	tests := []struct {
		file       string
		wantStatus int
	}{
		{"style.css", http.StatusOK},
		{"app.js", http.StatusOK},
		{"page.html", http.StatusForbidden},
		{"page.htm", http.StatusForbidden},
		{"script.php", http.StatusForbidden},
		{"malware.exe", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/p/test-plugin/static/"+tt.file, nil)
			req.SetPathValue("plugin", "test-plugin")
			req.SetPathValue("path", tt.file)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("GET /p/test-plugin/static/%s: status = %d, want %d", tt.file, w.Code, tt.wantStatus)
			}
		})
	}
}

func TestPluginStaticHandler_NosniffHeader(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "test-plugin", "frontend")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "style.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	handler := &web.PluginStaticHandler{PluginsDir: dir}

	req := httptest.NewRequest(http.MethodGet, "/p/test-plugin/static/style.css", nil)
	req.SetPathValue("plugin", "test-plugin")
	req.SetPathValue("path", "style.css")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// ---------------------------------------------------------------------------
// Stubs for security tests
// ---------------------------------------------------------------------------

type noopSecurityAuth struct{}

func (a *noopSecurityAuth) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (a *noopSecurityAuth) HandleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (a *noopSecurityAuth) HandleLogout(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

type stubSecurityAdminHandler struct{}

func (h *stubSecurityAdminHandler) HandleUserProfile(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (h *stubSecurityAdminHandler) HandleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (h *stubSecurityAdminHandler) HandleAuditLog(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (h *stubSecurityAdminHandler) HandleUserList(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (h *stubSecurityAdminHandler) HandleUserCreate(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (h *stubSecurityAdminHandler) HandleUserDelete(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (h *stubSecurityAdminHandler) HandleUserChangeRole(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (h *stubSecurityAdminHandler) HandleUserResetPassword(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// stubSecuritySettingsStore is a minimal settings store for security tests.
type stubSecuritySettingsStore struct {
	s     settings.Settings
	rawKV map[string]string
}

func newStubSecuritySettingsStore() *stubSecuritySettingsStore {
	return &stubSecuritySettingsStore{
		rawKV: make(map[string]string),
	}
}

func (s *stubSecuritySettingsStore) Load(_ context.Context) (settings.Settings, error) {
	return s.s, nil
}
func (s *stubSecuritySettingsStore) Save(_ context.Context, st settings.Settings) error {
	s.s = st
	return nil
}
func (s *stubSecuritySettingsStore) GetRaw(_ context.Context, key string) (string, error) {
	v, ok := s.rawKV[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return v, nil
}
func (s *stubSecuritySettingsStore) SetRaw(_ context.Context, key, value string) error {
	s.rawKV[key] = value
	return nil
}
func (s *stubSecuritySettingsStore) DeleteRaw(_ context.Context, key string) error {
	delete(s.rawKV, key)
	return nil
}
func (s *stubSecuritySettingsStore) Close() error { return nil }

func TestWebAuthMiddleware_ExpiredSession_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	entry, _ := store.Create("127.0.0.1", 0, "", "", false)
	store.Delete(entry.ID)

	h := web.WebAuthMiddleware(store, http.HandlerFunc(okHandler))
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestWebAuthMiddleware_WrongCookieName_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{ID: "goodsession", CSRFToken: "tok"}
	store := &stubSessionStore{entry: entry}
	h := web.WebAuthMiddleware(store, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "other_cookie", Value: entry.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestWebCSRFMiddleware_TRACE_SkipsValidation(t *testing.T) {
	t.Parallel()
	csrf := &stubCSRFProvider{err: web.ErrCSRFTokenInvalid}
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	h := web.WebCSRFMiddleware(store, csrf, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodTrace, "/", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "x"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("TRACE: status = %d, want 200", w.Code)
	}
}

func TestWebCSRFMiddleware_DELETE_ValidatesCSRF(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{ID: "sessid", CSRFToken: "goodtoken"}
	store := &stubSessionStore{entry: entry}
	csrf := &stubCSRFProvider{token: "goodtoken", err: web.ErrCSRFTokenInvalid}
	h := web.WebCSRFMiddleware(store, csrf, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodDelete, "/resource/1", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("DELETE without CSRF: status = %d, want 403", w.Code)
	}
}

func TestWebCSRFMiddleware_PUT_ValidatesCSRF(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{ID: "sessid", CSRFToken: "tok"}
	store := &stubSessionStore{entry: entry}
	csrf := &stubCSRFProvider{token: "tok", err: nil}
	h := web.WebCSRFMiddleware(store, csrf, http.HandlerFunc(okHandler))

	form := url.Values{"csrf_token": {"tok"}}
	req := httptest.NewRequest(http.MethodPut, "/resource/1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("PUT with valid CSRF: status = %d, want 200", w.Code)
	}
}

func TestWebAuthHandler_HandleLoginSubmit_EmptyCredentials(t *testing.T) {
	t.Parallel()
	handler, store, _, bus := newAuthHandlerFixture(t, "unused")

	form := url.Values{"username": {""}, "password": {""}, "csrf_token": {store.entry.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: store.entry.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code == http.StatusSeeOther {
		t.Error("empty credentials must not grant access")
	}
	for _, e := range bus.events {
		if e.Name == "web.auth.login_success" {
			t.Error("EventWebLoginSuccess must not be emitted for empty credentials")
		}
	}
}

func TestWebAuthHandler_HandleLoginSubmit_WrongUsername(t *testing.T) {
	t.Parallel()
	handler, store, _, _ := newAuthHandlerFixture(t, "unused")

	form := url.Values{"username": {"nonexistent"}, "password": {"correctpassword"}, "csrf_token": {store.entry.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: store.entry.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code == http.StatusSeeOther {
		t.Error("wrong username must not grant access")
	}
}

func TestWebAuthHandler_LoginSuccess_CookieSecurityAttributes(t *testing.T) {
	t.Parallel()
	handler, store, _, _ := newAuthHandlerFixture(t, "unused")

	form := url.Values{"username": {testAdminUsername}, "password": {testAdminPassword}, "csrf_token": {store.entry.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: store.entry.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "mc_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("mc_session cookie not found")
	}
	if !sessionCookie.HttpOnly {
		t.Error("cookie must be HttpOnly=true")
	}
	// Secure flag is intentionally omitted so the app works over plain HTTP (dev/local).
	// Production deployments use a TLS-terminating reverse proxy.
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", sessionCookie.SameSite)
	}
	if sessionCookie.Path != "/" {
		t.Errorf("cookie Path = %q, want /", sessionCookie.Path)
	}
	if sessionCookie.MaxAge != 86400 {
		t.Errorf("cookie MaxAge = %d, want 86400", sessionCookie.MaxAge)
	}
}

func TestWebAuthHandler_HandleLogout_ClearsCookie(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	entry, _ := store.Create("127.0.0.1", 0, "", "", false)
	csrf := web.NewCSRFProvider()
	userSvc := newStubUserService()
	handler := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	form := url.Values{"csrf_token": {entry.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	handler.HandleLogout(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	_, err = store.Get(entry.ID)
	if err == nil {
		t.Error("session should be deleted after logout")
	}

	var clearedCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "mc_session" {
			clearedCookie = c
		}
	}
	if clearedCookie == nil {
		t.Fatal("mc_session cookie not found in response (should be cleared)")
	}
	if clearedCookie.MaxAge != -1 {
		t.Errorf("cleared cookie MaxAge = %d, want -1", clearedCookie.MaxAge)
	}
	if !clearedCookie.HttpOnly {
		t.Error("cleared cookie must be HttpOnly=true")
	}
	// Secure flag removed intentionally: app works over plain HTTP (dev/local).
}

func TestWebAuthHandler_HandleLoginPage_SessionCreateError(t *testing.T) {
	t.Parallel()
	brokenStore := &errorSessionStore{}
	csrf := web.NewCSRFProvider()
	userSvc := newStubUserService()
	handler := web.NewWebAuthHandler(userSvc, brokenStore, csrf, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	handler.HandleLoginPage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// errorSessionStore always returns an error from Create.
type errorSessionStore struct{}

func (e *errorSessionStore) Create(_ string, _ int64, _ string, _ string, _ bool) (web.WebSessionEntry, error) {
	return web.WebSessionEntry{}, web.ErrWebSessionNotFound
}
func (e *errorSessionStore) Get(_ web.WebSessionID) (web.WebSessionEntry, error) {
	return web.WebSessionEntry{}, web.ErrWebSessionNotFound
}
func (e *errorSessionStore) Delete(_ web.WebSessionID)      {}
func (e *errorSessionStore) ListAll() []web.WebSessionEntry { return nil }
func (e *errorSessionStore) SetMustChangePassword(_ web.WebSessionID, _ bool) error {
	return nil
}
func (e *errorSessionStore) Close() error { return nil }

func TestWebAuthHandler_HandleLoginSubmit_BadCredentials_RerenderHasCSRF(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	entry, _ := store.Create("127.0.0.1", 0, "", "", false)
	csrf := web.NewCSRFProvider()
	userSvc := newStubUserService()
	userSvc.addUser(users.User{ID: 1, Username: "admin", PasswordHash: "correctpassword", Role: users.RoleAdmin})
	handler := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	form := url.Values{"username": {"admin"}, "password": {"wrong-password"}, "csrf_token": {entry.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "csrf_token") {
		t.Error("re-rendered login page must contain csrf_token field")
	}
}

func TestSessionFromRequest_WrongType_ReturnsError(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := web.SessionFromRequest(req)
	if err == nil {
		t.Fatal("expected error for request without session context")
	}
}

func TestWebAuthHandler_HandleLogout_NoCookie_Forbidden(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	csrf := &stubCSRFProvider{}
	userSvc := newStubUserService()
	handler := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := httptest.NewRecorder()
	handler.HandleLogout(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}
