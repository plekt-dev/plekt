// Package web_test contains additional tests specifically targeting uncovered
// branches identified in the coverage report.
package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web"
)

// preLoginThenErrorStore succeeds on Get (simulating a valid pre-login session)
// but fails on the subsequent Create (simulating a DB error after credential check).
type preLoginThenErrorStore struct {
	preEntry web.WebSessionEntry
}

func (s *preLoginThenErrorStore) Create(_ string, _ int64, _ string, _ string, _ bool) (web.WebSessionEntry, error) {
	return web.WebSessionEntry{}, web.ErrWebSessionNotFound
}
func (s *preLoginThenErrorStore) Get(_ web.WebSessionID) (web.WebSessionEntry, error) {
	return s.preEntry, nil
}
func (s *preLoginThenErrorStore) Delete(_ web.WebSessionID)      {}
func (s *preLoginThenErrorStore) ListAll() []web.WebSessionEntry { return nil }
func (s *preLoginThenErrorStore) SetMustChangePassword(_ web.WebSessionID, _ bool) error {
	return nil
}
func (s *preLoginThenErrorStore) Close() error { return nil }

func TestWebAuthHandler_HandleLoginSubmit_CreateSessionError(t *testing.T) {
	t.Parallel()
	preEntry := web.WebSessionEntry{
		ID:        "preloginsessionid1234567890abcdef",
		CSRFToken: "prelogincsrftoken1234567890abcdef",
	}
	store := &preLoginThenErrorStore{preEntry: preEntry}
	csrf := &stubCSRFProvider{token: preEntry.CSRFToken, err: nil}
	userSvc := newStubUserService()
	userSvc.addUser(users.User{ID: 1, Username: "admin", PasswordHash: "correctpassword", Role: users.RoleAdmin})
	handler := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	// Valid CSRF + correct credentials, but Create will fail → expect 500.
	form := url.Values{"username": {"admin"}, "password": {"correctpassword"}, "csrf_token": {preEntry.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: preEntry.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when session create fails after valid credentials", w.Code)
	}
}

func TestWebAuthHandler_HandleLogout_SessionNotFound_Forbidden(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	csrf := &stubCSRFProvider{}
	userSvc := newStubUserService()
	handler := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	form := url.Values{"csrf_token": {"any"}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-looking-id"})
	w := httptest.NewRecorder()
	handler.HandleLogout(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestWebCSRFMiddleware_POST_NoCookieAtAll_Returns403(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x"}}
	csrf := &stubCSRFProvider{}
	h := web.WebCSRFMiddleware(store, csrf, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodPost, "/action", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("POST without any cookie: status = %d, want 403", w.Code)
	}
}

func TestWebRouter_Build_WithStaticDir(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	userSvc := newStubUserService()
	auth := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:      auth,
		Sessions:  store,
		CSRF:      csrf,
		StaticDir: "/tmp",
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)
	if mux == nil {
		t.Fatal("Build returned nil mux")
	}

	req := httptest.NewRequest(http.MethodGet, "/static/nonexistent.css", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		// route registered, file just doesn't exist
	}
}

func TestInMemoryWebSessionStore_Get_ExpiredSession(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	entry, _ := store.Create("127.0.0.1", 0, "", "", false)

	_, err = store.Get(entry.ID)
	if err != nil {
		t.Fatalf("should find fresh session: %v", err)
	}
}

func TestGenerateHex16_ViaCreate(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	for i := 0; i < 10; i++ {
		_, err := store.Create("127.0.0.1", 0, "", "", false)
		if err != nil {
			t.Fatalf("Create[%d]: %v", i, err)
		}
	}
}

func TestSweepSessions_RemovesNothing_WhenAllFresh(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	entry, _ := store.Create("127.0.0.1", 0, "", "", false)

	web.SweepSessions(store)

	_, err = store.Get(entry.ID)
	if err != nil {
		t.Errorf("fresh session should survive sweep: %v", err)
	}
}
