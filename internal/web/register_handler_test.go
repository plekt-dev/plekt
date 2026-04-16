package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web"
)

// ---------------------------------------------------------------------------
// Stub settings store for register handler tests
// ---------------------------------------------------------------------------

type stubRegisterSettingsStore struct {
	s     settings.Settings
	err   error
	rawKV map[string]string
}

func (s *stubRegisterSettingsStore) Load(_ context.Context) (settings.Settings, error) {
	return s.s, s.err
}

func (s *stubRegisterSettingsStore) Save(_ context.Context, st settings.Settings) error {
	s.s = st
	return nil
}

func (s *stubRegisterSettingsStore) GetRaw(_ context.Context, key string) (string, error) {
	if s.rawKV != nil {
		v, ok := s.rawKV[key]
		if ok {
			return v, nil
		}
	}
	return "", settings.ErrSettingNotFound
}
func (s *stubRegisterSettingsStore) SetRaw(_ context.Context, key, value string) error {
	if s.rawKV == nil {
		s.rawKV = make(map[string]string)
	}
	s.rawKV[key] = value
	return nil
}
func (s *stubRegisterSettingsStore) DeleteRaw(_ context.Context, key string) error {
	if s.rawKV != nil {
		delete(s.rawKV, key)
	}
	return nil
}
func (s *stubRegisterSettingsStore) Close() error { return nil }

// ---------------------------------------------------------------------------
// HandleRegisterPage tests
// ---------------------------------------------------------------------------

func TestHandleRegisterPage_AllowedWhenNoUsers(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService() // no users = registration open
	settingsSvc := &stubRegisterSettingsStore{s: settings.Settings{PasswordMinLength: 12}}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	w := httptest.NewRecorder()
	h.HandleRegisterPage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "register") && !strings.Contains(strings.ToLower(w.Body.String()), "account") {
		t.Error("response should contain registration form")
	}
}

func TestHandleRegisterPage_AllowedWhenRegistrationEnabled(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService()
	svc.addUser(users.User{ID: 1, Username: "existing", Role: users.RoleAdmin})
	settingsSvc := &stubRegisterSettingsStore{s: settings.Settings{RegistrationEnabled: true, PasswordMinLength: 12}}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	w := httptest.NewRecorder()
	h.HandleRegisterPage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (registration enabled)", w.Code)
	}
}

func TestHandleRegisterPage_RedirectsWhenNotAllowed(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService()
	svc.addUser(users.User{ID: 1, Username: "existing", Role: users.RoleAdmin})
	// RegistrationEnabled=false + users exist → redirect
	settingsSvc := &stubRegisterSettingsStore{s: settings.Settings{RegistrationEnabled: false}}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	w := httptest.NewRecorder()
	h.HandleRegisterPage(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

// ---------------------------------------------------------------------------
// HandleRegisterSubmit tests
// ---------------------------------------------------------------------------

func TestHandleRegisterSubmit_SuccessFirstUser(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "pre-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService() // no users: first user gets admin
	settingsSvc := &stubRegisterSettingsStore{s: settings.Settings{PasswordMinLength: 8}}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	body := "username=newadmin&password=mypassword&confirm_password=mypassword&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "pre-session"})
	w := httptest.NewRecorder()
	h.HandleRegisterSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}
}

func TestHandleRegisterSubmit_PasswordMismatch(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "pre-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService()
	settingsSvc := &stubRegisterSettingsStore{s: settings.Settings{PasswordMinLength: 8}}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	body := "username=alice&password=mypassword&confirm_password=different&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "pre-session"})
	w := httptest.NewRecorder()
	h.HandleRegisterSubmit(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (re-render with error)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "match") {
		t.Error("response should contain password mismatch error")
	}
}

func TestHandleRegisterSubmit_NotAllowedRedirectsToLogin(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "pre-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService()
	svc.addUser(users.User{ID: 1, Username: "existing", Role: users.RoleAdmin})
	// Registration disabled and users exist
	settingsSvc := &stubRegisterSettingsStore{s: settings.Settings{RegistrationEnabled: false}}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	body := "username=alice&password=mypassword&confirm_password=mypassword&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "pre-session"})
	w := httptest.NewRecorder()
	h.HandleRegisterSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestHandleRegisterSubmit_NoCSRFCookie(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService() // no users
	settingsSvc := &stubRegisterSettingsStore{s: settings.Settings{}}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	body := "username=alice&password=mypassword&confirm_password=mypassword&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// No cookie → redirect to /register (graceful recovery)
	w := httptest.NewRecorder()
	h.HandleRegisterSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 (redirect to fresh form)", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/register?error=no_cookie" {
		t.Errorf("Location = %q, want /register?error=no_cookie", loc)
	}
}

func TestHandleRegisterSubmit_InvalidCSRF(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "pre-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: web.ErrCSRFTokenInvalid}
	svc := newStubUserService()
	settingsSvc := &stubRegisterSettingsStore{s: settings.Settings{}}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	body := "username=alice&password=mypassword&confirm_password=mypassword&csrf_token=wrongtoken"
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "pre-session"})
	w := httptest.NewRecorder()
	h.HandleRegisterSubmit(w, req)

	// CSRF mismatch → redirect to fresh form (graceful recovery, not blank 403)
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 (redirect to fresh form)", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/register?error=csrf_mismatch" {
		t.Errorf("Location = %q, want /register?error=csrf_mismatch", loc)
	}
}

func TestHandleRegisterSubmit_CreateError(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "pre-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService()
	svc.createErr = users.ErrUserAlreadyExists
	settingsSvc := &stubRegisterSettingsStore{s: settings.Settings{PasswordMinLength: 8}}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	body := "username=existing&password=mypassword&confirm_password=mypassword&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "pre-session"})
	w := httptest.NewRecorder()
	h.HandleRegisterSubmit(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (re-render with error)", w.Code)
	}
}

func TestHandleRegisterSubmit_WithEventBus(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "pre-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService()
	settingsSvc := &stubRegisterSettingsStore{s: settings.Settings{PasswordMinLength: 8}}
	bus := &recordingBus{}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, bus)

	body := "username=newuser&password=mypassword&confirm_password=mypassword&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "pre-session"})
	w := httptest.NewRecorder()
	h.HandleRegisterSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if len(bus.events) == 0 {
		t.Error("expected user created event to be emitted")
	}
}

// ---------------------------------------------------------------------------
// Task 5: VULN-03: Setup token validation tests
// ---------------------------------------------------------------------------

func TestHandleRegisterSubmit_SetupToken_MissingWhenRequired(t *testing.T) {
	t.Parallel()

	// Compute the SHA-256 hash of our test token, same as main.go does.
	const testToken = "aaaa"
	tokenHash := "61be55a8e2f6b4e172338bddf184d6dbee29c98853e0a0485ecee7f27b9af0b4" // sha256("aaaa")

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "pre-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService() // no users: first user
	settingsSvc := &stubRegisterSettingsStore{
		s:     settings.Settings{PasswordMinLength: 8},
		rawKV: map[string]string{"setup_token_hash": tokenHash},
	}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	// Submit WITHOUT setup_token
	body := "username=admin&password=mypassword&confirm_password=mypassword&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "pre-session"})
	w := httptest.NewRecorder()
	h.HandleRegisterSubmit(w, req)

	// Should fail: setup token required but missing
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (re-render with error)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Invalid setup token") {
		t.Error("response should contain 'Invalid setup token' error")
	}

	// No user should have been created
	all, _ := svc.ListUsers(context.Background())
	if len(all) != 0 {
		t.Errorf("users created = %d, want 0", len(all))
	}
}

func TestHandleRegisterSubmit_SetupToken_WrongToken(t *testing.T) {
	t.Parallel()

	tokenHash := "61be55a8e2f6b4e172338bddf184d6dbee29c98853e0a0485ecee7f27b9af0b4" // sha256("aaaa")

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "pre-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService()
	settingsSvc := &stubRegisterSettingsStore{
		s:     settings.Settings{PasswordMinLength: 8},
		rawKV: map[string]string{"setup_token_hash": tokenHash},
	}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	// Submit with WRONG setup_token
	body := "username=admin&password=mypassword&confirm_password=mypassword&csrf_token=valid-csrf&setup_token=wrong"
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "pre-session"})
	w := httptest.NewRecorder()
	h.HandleRegisterSubmit(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (re-render with error)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Invalid setup token") {
		t.Error("response should contain 'Invalid setup token' error")
	}
}

func TestHandleRegisterSubmit_SetupToken_CorrectToken(t *testing.T) {
	t.Parallel()

	tokenHash := "61be55a8e2f6b4e172338bddf184d6dbee29c98853e0a0485ecee7f27b9af0b4" // sha256("aaaa")

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "pre-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	svc := newStubUserService()
	settingsSvc := &stubRegisterSettingsStore{
		s:     settings.Settings{PasswordMinLength: 8},
		rawKV: map[string]string{"setup_token_hash": tokenHash},
	}
	h := web.NewWebRegisterHandler(svc, store, csrf, settingsSvc, nil)

	// Submit with CORRECT setup_token
	body := "username=admin&password=mypassword&confirm_password=mypassword&csrf_token=valid-csrf&setup_token=aaaa"
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "pre-session"})
	w := httptest.NewRecorder()
	h.HandleRegisterSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 (redirect to dashboard)", w.Code)
	}

	// User should have been created
	all, _ := svc.ListUsers(context.Background())
	if len(all) != 1 {
		t.Fatalf("users created = %d, want 1", len(all))
	}
	if all[0].Role != users.RoleAdmin {
		t.Errorf("first user role = %q, want admin", all[0].Role)
	}

	// Setup token hash should have been deleted
	if _, err := settingsSvc.GetRaw(context.Background(), "setup_token_hash"); err == nil {
		t.Error("setup_token_hash should have been deleted after successful registration")
	}
}
