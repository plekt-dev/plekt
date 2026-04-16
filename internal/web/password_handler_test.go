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
// Stub settings store for password handler tests
// ---------------------------------------------------------------------------

type stubPasswordSettingsStore struct {
	s   settings.Settings
	err error
}

func (s *stubPasswordSettingsStore) Load(_ context.Context) (settings.Settings, error) {
	return s.s, s.err
}

func (s *stubPasswordSettingsStore) Save(_ context.Context, st settings.Settings) error {
	s.s = st
	return nil
}

func (s *stubPasswordSettingsStore) GetRaw(_ context.Context, key string) (string, error) {
	return "", settings.ErrSettingNotFound
}
func (s *stubPasswordSettingsStore) SetRaw(_ context.Context, key, value string) error { return nil }
func (s *stubPasswordSettingsStore) DeleteRaw(_ context.Context, key string) error     { return nil }
func (s *stubPasswordSettingsStore) Close() error                                      { return nil }

// ---------------------------------------------------------------------------
// HandleChangePasswordPage tests
// ---------------------------------------------------------------------------

func TestHandleChangePasswordPage_OK(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "valid-session",
			CSRFToken: "valid-csrf",
			UserID:    1,
			Role:      "admin",
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	settingsSvc := &stubPasswordSettingsStore{s: settings.Settings{PasswordMinLength: 12}}
	svc := newStubUserService()
	h := web.NewWebPasswordHandler(svc, store, csrf, settingsSvc)

	req := httptest.NewRequest(http.MethodGet, "/change-password", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	w := httptest.NewRecorder()
	h.HandleChangePasswordPage(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "change-password") {
		t.Error("response should contain change-password form")
	}
}

func TestHandleChangePasswordPage_NoSession(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	h := web.NewWebPasswordHandler(newStubUserService(), store, csrf, nil)

	req := httptest.NewRequest(http.MethodGet, "/change-password", nil)
	w := httptest.NewRecorder()
	h.HandleChangePasswordPage(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

// ---------------------------------------------------------------------------
// HandleChangePasswordSubmit tests
// ---------------------------------------------------------------------------

func TestHandleChangePasswordSubmit_Success(t *testing.T) {
	t.Parallel()

	svc := newStubUserService()
	svc.addUser(users.User{ID: 1, Username: "alice", PasswordHash: "currentpass123", Role: users.RoleAdmin})

	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "valid-session",
			CSRFToken: "valid-csrf",
			UserID:    1,
			Role:      "admin",
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	settingsSvc := &stubPasswordSettingsStore{s: settings.Settings{PasswordMinLength: 8}}
	h := web.NewWebPasswordHandler(svc, store, csrf, settingsSvc)

	body := "current_password=currentpass123&new_password=newpass1234&confirm_password=newpass1234&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	w := httptest.NewRecorder()
	h.HandleChangePasswordSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}
}

func TestHandleChangePasswordSubmit_PasswordMismatch(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "valid-session",
			CSRFToken: "valid-csrf",
			UserID:    1,
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	h := web.NewWebPasswordHandler(newStubUserService(), store, csrf, nil)

	body := "current_password=currentpass&new_password=newpass1234&confirm_password=different1234"
	req := httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	w := httptest.NewRecorder()
	h.HandleChangePasswordSubmit(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (re-render with error)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "match") {
		t.Error("response should contain password mismatch error")
	}
}

func TestHandleChangePasswordSubmit_NoSession(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	h := web.NewWebPasswordHandler(newStubUserService(), store, csrf, nil)

	body := "current_password=x&new_password=y&confirm_password=y"
	req := httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleChangePasswordSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestHandleChangePasswordSubmit_NilSettings(t *testing.T) {
	t.Parallel()

	svc := newStubUserService()
	svc.addUser(users.User{ID: 1, Username: "alice", PasswordHash: "currentpass123", Role: users.RoleAdmin})

	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "valid-session",
			CSRFToken: "valid-csrf",
			UserID:    1,
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	// nil settings store: should default to minLength=12
	h := web.NewWebPasswordHandler(svc, store, csrf, nil)

	body := "current_password=currentpass123&new_password=newpassword12345&confirm_password=newpassword12345"
	req := httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	w := httptest.NewRecorder()
	h.HandleChangePasswordSubmit(w, req)

	// ChangePassword uses stub which returns nil: should succeed
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}
