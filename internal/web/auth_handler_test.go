package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web"
)

// recordingBus records emitted events for assertion.
type recordingBus struct {
	events []eventbus.Event
}

func (b *recordingBus) Emit(_ context.Context, e eventbus.Event) { b.events = append(b.events, e) }
func (b *recordingBus) Subscribe(name string, h eventbus.Handler) eventbus.Subscription {
	return eventbus.Subscription{}
}
func (b *recordingBus) Unsubscribe(eventbus.Subscription) {}
func (b *recordingBus) Close() error                      { return nil }

const (
	testAdminUsername = "admin"
	testAdminPassword = "correctpassword"
)

func newAuthHandlerFixture(t *testing.T, _ string) (web.WebAuthHandler, *stubSessionStore, *stubCSRFProvider, *recordingBus) {
	t.Helper()
	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:         "newsessionid1234567890123456789",
			CSRFToken:  "csrftoken12345678901234567890123",
			CreatedAt:  time.Now(),
			ExpiresAt:  time.Now().Add(24 * time.Hour),
			RemoteAddr: "127.0.0.1",
		},
	}
	csrf := &stubCSRFProvider{token: "csrftoken12345678901234567890123", err: nil}
	bus := &recordingBus{}
	userSvc := newStubUserService()
	userSvc.addUser(users.User{
		ID:           1,
		Username:     testAdminUsername,
		PasswordHash: testAdminPassword, // stub compares plaintext
		Role:         users.RoleAdmin,
	})
	handler := web.NewWebAuthHandler(userSvc, store, csrf, bus, nil)
	return handler, store, csrf, bus
}

func TestWebAuthHandler_HandleLoginPage_GET(t *testing.T) {
	t.Parallel()
	handler, _, _, _ := newAuthHandlerFixture(t, "unused")

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	handler.HandleLoginPage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "username") {
		t.Error("login page should contain username input")
	}
	if !strings.Contains(body, "csrf_token") {
		t.Error("login page should contain csrf_token hidden input")
	}
}

func TestWebAuthHandler_HandleLoginSubmit_ValidCredentials(t *testing.T) {
	t.Parallel()
	handler, store, _, bus := newAuthHandlerFixture(t, "unused")

	form := url.Values{"username": {testAdminUsername}, "password": {testAdminPassword}, "csrf_token": {store.entry.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: store.entry.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}

	// Cookie must be set
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "mc_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("mc_session cookie not set")
	}
	if !sessionCookie.HttpOnly {
		t.Error("mc_session cookie should be HttpOnly")
	}
	// Secure flag removed intentionally: app works over plain HTTP (dev/local).
	if sessionCookie.Secure {
		t.Error("mc_session cookie must NOT be Secure (app runs over HTTP)")
	}

	// Events: attempt + success
	eventNames := make([]string, 0, len(bus.events))
	for _, e := range bus.events {
		eventNames = append(eventNames, e.Name)
	}
	if !contains(eventNames, eventbus.EventWebLoginAttempt) {
		t.Errorf("EventWebLoginAttempt not emitted; got %v", eventNames)
	}
	if !contains(eventNames, eventbus.EventWebLoginSuccess) {
		t.Errorf("EventWebLoginSuccess not emitted; got %v", eventNames)
	}
}

func TestWebAuthHandler_HandleLoginSubmit_InvalidCredentials(t *testing.T) {
	t.Parallel()
	handler, store, _, bus := newAuthHandlerFixture(t, "unused")

	form := url.Values{"username": {testAdminUsername}, "password": {"wrongpassword"}, "csrf_token": {store.entry.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.168.1.1:4321"
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: store.entry.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (re-render login page)", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "username") {
		t.Error("should re-render login form on failure")
	}

	eventNames := make([]string, 0, len(bus.events))
	for _, e := range bus.events {
		eventNames = append(eventNames, e.Name)
	}
	if !contains(eventNames, eventbus.EventWebLoginAttempt) {
		t.Errorf("EventWebLoginAttempt not emitted; got %v", eventNames)
	}
	if !contains(eventNames, eventbus.EventWebLoginFailed) {
		t.Errorf("EventWebLoginFailed not emitted; got %v", eventNames)
	}
	if contains(eventNames, eventbus.EventWebLoginSuccess) {
		t.Error("EventWebLoginSuccess must NOT be emitted on failure")
	}
}

func TestWebAuthHandler_HandleLogout(t *testing.T) {
	t.Parallel()
	handler, store, csrf, bus := newAuthHandlerFixture(t, "unused")

	csrf.err = nil

	form := url.Values{"csrf_token": {csrf.token}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "127.0.0.1:9000"
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: store.entry.ID})
	w := httptest.NewRecorder()
	handler.HandleLogout(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}

	eventNames := make([]string, 0, len(bus.events))
	for _, e := range bus.events {
		eventNames = append(eventNames, e.Name)
	}
	if !contains(eventNames, eventbus.EventWebLogout) {
		t.Errorf("EventWebLogout not emitted; got %v", eventNames)
	}
}

func TestWebAuthHandler_HandleLogout_NoCookie_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	handler, _, _, _ := newAuthHandlerFixture(t, "unused")

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.HandleLogout(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
	// clearSessionCookie must have been called (MaxAge = -1).
	var cleared bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "mc_session" && c.MaxAge == -1 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("expected session cookie to be cleared (MaxAge=-1) when no cookie present")
	}
}

func TestWebAuthHandler_HandleLogout_SessionNotFound_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	handler, store, _, _ := newAuthHandlerFixture(t, "unused")
	store.err = web.ErrWebSessionNotFound

	form := url.Values{"csrf_token": {"any"}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "stale-session"})
	w := httptest.NewRecorder()
	handler.HandleLogout(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
	var cleared bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "mc_session" && c.MaxAge == -1 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("expected session cookie to be cleared (MaxAge=-1) when session not found")
	}
}

func TestWebAuthHandler_HandleLogout_CSRFInvalid_Forbidden(t *testing.T) {
	t.Parallel()
	handler, store, csrf, _ := newAuthHandlerFixture(t, "unused")

	csrf.err = web.ErrCSRFTokenInvalid

	form := url.Values{"csrf_token": {"badcsrf"}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: store.entry.ID})
	w := httptest.NewRecorder()
	handler.HandleLogout(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestWebAuthHandler_LoginSubmit_NilBus_DoesNotPanic(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "sessionid123456789012345678901",
			CSRFToken: "csrftoken1234567890123456789012",
			ExpiresAt: time.Now().Add(24 * time.Hour),
		},
	}
	csrf := &stubCSRFProvider{token: "csrftoken1234567890123456789012"}
	userSvc := newStubUserService()
	handler := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	form := url.Values{"username": {"admin"}, "password": {"wrong"}, "csrf_token": {"c"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	// Should not panic
	handler.HandleLoginSubmit(w, req)
}

func TestWebAuthHandler_HandleLoginSubmit_NoCSRFToken_Returns403(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	preSession, _ := store.Create("127.0.0.1", 0, "", "", false)
	csrf := web.NewCSRFProvider()
	userSvc := newStubUserService()
	userSvc.addUser(users.User{ID: 1, Username: "admin", PasswordHash: "correctpassword", Role: users.RoleAdmin})
	handler := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	form := url.Values{"username": {"admin"}, "password": {"correctpassword"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: preSession.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when CSRF token is missing", w.Code)
	}
}

func TestWebAuthHandler_HandleLoginSubmit_WrongCSRFToken_Returns403(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	preSession, _ := store.Create("127.0.0.1", 0, "", "", false)
	csrf := web.NewCSRFProvider()
	userSvc := newStubUserService()
	handler := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	form := url.Values{"username": {"admin"}, "password": {"correctpassword"}, "csrf_token": {"wrongcsrftoken"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: preSession.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when CSRF token is wrong", w.Code)
	}
}

func TestWebAuthHandler_HandleLoginSubmit_ValidCSRF_WrongCredentials_RerendersForm(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	preSession, _ := store.Create("127.0.0.1", 0, "", "", false)
	csrf := web.NewCSRFProvider()
	userSvc := newStubUserService()
	userSvc.addUser(users.User{ID: 1, Username: "admin", PasswordHash: "correctpassword", Role: users.RoleAdmin})
	handler := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	form := url.Values{"username": {"admin"}, "password": {"wrong-password"}, "csrf_token": {preSession.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: preSession.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (re-render) when CSRF valid but credentials wrong", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "username") {
		t.Error("should re-render login form with username input")
	}
}

func TestWebAuthHandler_HandleLoginSubmit_ValidCSRF_CorrectCredentials_Redirects(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	preSession, _ := store.Create("127.0.0.1", 0, "", "", false)
	csrf := web.NewCSRFProvider()
	bus := &recordingBus{}
	userSvc := newStubUserService()
	userSvc.addUser(users.User{ID: 1, Username: "admin", PasswordHash: "correctpassword", Role: users.RoleAdmin})
	handler := web.NewWebAuthHandler(userSvc, store, csrf, bus, nil)

	form := url.Values{"username": {"admin"}, "password": {"correctpassword"}, "csrf_token": {preSession.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "127.0.0.1:5000"
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: preSession.ID})
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}

	// After login the pre-login session must be gone (session fixation prevention).
	_, err = store.Get(preSession.ID)
	if err == nil {
		t.Error("pre-login session must be deleted after successful login (session fixation prevention)")
	}

	// New session cookie must be different from the pre-login session ID.
	var newCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "mc_session" {
			newCookie = c
		}
	}
	if newCookie == nil {
		t.Fatal("mc_session cookie not set after login")
	}
	if newCookie.Value == preSession.ID {
		t.Error("new session ID must differ from pre-login session ID (session fixation prevention)")
	}
}

func TestWebAuthHandler_HandleLoginSubmit_NoCookieNoCSRF_Returns403(t *testing.T) {
	t.Parallel()
	store, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer store.Close()

	csrf := web.NewCSRFProvider()
	userSvc := newStubUserService()
	handler := web.NewWebAuthHandler(userSvc, store, csrf, nil, nil)

	form := url.Values{"username": {"admin"}, "password": {"correctpassword"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.HandleLoginSubmit(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when no session cookie present", w.Code)
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
