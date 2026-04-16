package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/web"
)

// stubSessionStore is a controllable WebSessionStore for middleware tests.
type stubSessionStore struct {
	entry web.WebSessionEntry
	err   error
}

func (s *stubSessionStore) Create(remoteAddr string, userID int64, role string, username string, mustChangePassword bool) (web.WebSessionEntry, error) {
	return s.entry, s.err
}
func (s *stubSessionStore) Get(id web.WebSessionID) (web.WebSessionEntry, error) {
	if s.err != nil {
		return web.WebSessionEntry{}, s.err
	}
	e := s.entry
	// Default to authenticated session so WebAuthMiddleware accepts it.
	// Tests for pre-login sessions call handlers directly, not via middleware.
	if e.UserID == 0 {
		e.UserID = 1
	}
	return e, nil
}
func (s *stubSessionStore) Delete(id web.WebSessionID)     {}
func (s *stubSessionStore) ListAll() []web.WebSessionEntry { return nil }
func (s *stubSessionStore) SetMustChangePassword(id web.WebSessionID, mustChange bool) error {
	return nil
}
func (s *stubSessionStore) Close() error { return nil }

// stubCSRFProvider is a controllable CSRFProvider for middleware tests.
type stubCSRFProvider struct {
	token string
	err   error
}

func (p *stubCSRFProvider) TokenForSession(e web.WebSessionEntry) string { return p.token }
func (p *stubCSRFProvider) Validate(e web.WebSessionEntry, submitted string) error {
	return p.err
}

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func TestWebAuthMiddleware_NoSession_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	h := web.WebAuthMiddleware(store, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestWebAuthMiddleware_NoCookie_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	h := web.WebAuthMiddleware(store, http.HandlerFunc(okHandler))

	// No cookie set
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestWebAuthMiddleware_ValidSession_CallsNext(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{ID: "abc123", CSRFToken: "tok"}
	store := &stubSessionStore{entry: entry}
	h := web.WebAuthMiddleware(store, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestWebAuthMiddleware_SessionInjectedIntoContext(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{ID: "sessionABC", CSRFToken: "tok"}
	store := &stubSessionStore{entry: entry}

	var capturedSession web.WebSessionEntry
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := web.SessionFromRequest(r)
		if err != nil {
			t.Errorf("SessionFromRequest: %v", err)
		}
		capturedSession = s
		w.WriteHeader(http.StatusOK)
	})

	h := web.WebAuthMiddleware(store, next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if capturedSession.ID != entry.ID {
		t.Errorf("session ID in context = %q, want %q", capturedSession.ID, entry.ID)
	}
}

func TestWebCSRFMiddleware_GET_SkipsValidation(t *testing.T) {
	t.Parallel()
	csrf := &stubCSRFProvider{err: web.ErrCSRFTokenInvalid} // would fail if called
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	h := web.WebCSRFMiddleware(store, csrf, http.HandlerFunc(okHandler))

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, "/", nil)
		req.AddCookie(&http.Cookie{Name: "mc_session", Value: "x"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("method %s: status = %d, want 200", method, w.Code)
		}
	}
}

func TestWebCSRFMiddleware_POST_ValidToken_Passes(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{ID: "sessid", CSRFToken: "goodtoken"}
	store := &stubSessionStore{entry: entry}
	csrf := &stubCSRFProvider{token: "goodtoken", err: nil}
	h := web.WebCSRFMiddleware(store, csrf, http.HandlerFunc(okHandler))

	form := url.Values{"csrf_token": {"goodtoken"}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestWebCSRFMiddleware_POST_InvalidToken_Returns403(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{ID: "sessid", CSRFToken: "goodtoken"}
	store := &stubSessionStore{entry: entry}
	csrf := &stubCSRFProvider{token: "goodtoken", err: web.ErrCSRFTokenInvalid}
	h := web.WebCSRFMiddleware(store, csrf, http.HandlerFunc(okHandler))

	form := url.Values{"csrf_token": {"badtoken"}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestWebCSRFMiddleware_POST_MissingToken_Returns403(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{ID: "sessid", CSRFToken: "goodtoken"}
	store := &stubSessionStore{entry: entry}
	csrf := &stubCSRFProvider{token: "goodtoken", err: web.ErrCSRFTokenInvalid}
	h := web.WebCSRFMiddleware(store, csrf, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodPost, "/action", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestWebCSRFMiddleware_POST_NoSession_Returns403(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	csrf := &stubCSRFProvider{}
	h := web.WebCSRFMiddleware(store, csrf, http.HandlerFunc(okHandler))

	form := url.Values{"csrf_token": {"anytoken"}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "unknown"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestWebAuthMiddleware_CSRFTokenInjectedAsHeader(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{ID: "sess1", CSRFToken: "csrf-header-value"}
	store := &stubSessionStore{entry: entry}

	var capturedCSRF string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCSRF = r.Header.Get("X-CSRF-Token")
		w.WriteHeader(http.StatusOK)
	})

	h := web.WebAuthMiddleware(store, next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if capturedCSRF != entry.CSRFToken {
		t.Errorf("X-CSRF-Token header = %q, want %q", capturedCSRF, entry.CSRFToken)
	}
}

func TestWebAuthMiddleware_CSRFHeaderDoesNotMutateOriginalRequest(t *testing.T) {
	t.Parallel()
	entry := web.WebSessionEntry{ID: "sess2", CSRFToken: "csrf-clone-test"}
	store := &stubSessionStore{entry: entry}

	h := web.WebAuthMiddleware(store, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	originalHeader := req.Header.Get("X-CSRF-Token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// The original request's header map must not have been mutated by Clone+Set.
	if originalHeader != "" {
		t.Errorf("original request X-CSRF-Token was mutated, got %q", originalHeader)
	}
}

func TestSessionFromRequest_NoContext_ReturnsError(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := web.SessionFromRequest(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

type stubUserCounter struct {
	count int
	err   error
}

func (s *stubUserCounter) CountUsers(_ context.Context) (int, error) {
	return s.count, s.err
}

func TestFirstRunMiddleware_NoUsers_RedirectsToRegister(t *testing.T) {
	t.Parallel()
	counter := &stubUserCounter{count: 0}
	h := web.FirstRunMiddleware(counter, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/register" {
		t.Errorf("Location = %q, want /register", loc)
	}
}

func TestFirstRunMiddleware_UsersExist_CallsNext(t *testing.T) {
	t.Parallel()
	counter := &stubUserCounter{count: 1}
	h := web.FirstRunMiddleware(counter, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestFirstRunMiddleware_CountError_CallsNext(t *testing.T) {
	t.Parallel()
	counter := &stubUserCounter{count: 0, err: errors.New("db error")}
	h := web.FirstRunMiddleware(counter, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestFirstRunMiddleware_AllowedPaths_PassThrough(t *testing.T) {
	t.Parallel()
	counter := &stubUserCounter{count: 0}
	h := web.FirstRunMiddleware(counter, http.HandlerFunc(okHandler))

	paths := []string{"/register", "/login", "/logout", "/favicon.ico", "/static/app.css", "/plugins/foo/mcp", "/mcp"}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("path %q: status = %d, want 200", path, w.Code)
		}
	}
}
