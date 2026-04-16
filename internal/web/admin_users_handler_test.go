package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web"
)

// newAdminHandlerWithUsers builds a WebAdminHandler fixture with a stub user service.
func newAdminHandlerWithUsers(t *testing.T, userSvc users.UserService) web.WebAdminHandler {
	t.Helper()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{
				ID:        "valid-session",
				CSRFToken: "valid-csrf",
				Role:      "admin",
			},
		},
		all: nil,
	}
	auditStore := &stubAuditLogStore{}
	bus := &recordingBus{}
	csrf := &stubCSRFProvider{token: "valid-csrf"}

	h, err := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store,
		AuditLog: auditStore,
		Bus:      bus,
		CSRF:     csrf,
		Users:    userSvc,
	})
	if err != nil {
		t.Fatalf("NewWebAdminHandler: %v", err)
	}
	return h
}

func injectAdminSession(req *http.Request) *http.Request {
	entry := web.WebSessionEntry{
		ID:        "valid-session",
		CSRFToken: "valid-csrf",
		Role:      "admin",
	}
	return injectSession(req, entry)
}

// ---------------------------------------------------------------------------
// HandleUserList
// ---------------------------------------------------------------------------

func TestHandleUserList_OK(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	svc.addUser(users.User{ID: 1, Username: "alice", Role: users.RoleAdmin})
	h := newAdminHandlerWithUsers(t, svc)

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	w := httptest.NewRecorder()
	h.HandleUserList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "alice") {
		t.Error("response should contain username 'alice'")
	}
}

func TestHandleUserList_NoSession(t *testing.T) {
	t.Parallel()
	h := newAdminHandlerWithUsers(t, newStubUserService())

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	w := httptest.NewRecorder()
	h.HandleUserList(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestHandleUserList_NilUsers(t *testing.T) {
	t.Parallel()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"},
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	bus := &recordingBus{}
	h, err := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store,
		AuditLog: &stubAuditLogStore{},
		Bus:      bus,
		CSRF:     csrf,
		Users:    nil, // nil user service
	})
	if err != nil {
		t.Fatalf("NewWebAdminHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	w := httptest.NewRecorder()
	h.HandleUserList(w, req)

	// With nil users, page renders with empty user list
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (nil users renders empty)", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandleUserCreate
// ---------------------------------------------------------------------------

func TestHandleUserCreate_Success(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	h := newAdminHandlerWithUsers(t, svc)

	body := "username=newuser&password=validpassword123&role=viewer&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	w := httptest.NewRecorder()
	h.HandleUserCreate(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestHandleUserCreate_NoSession(t *testing.T) {
	t.Parallel()
	h := newAdminHandlerWithUsers(t, newStubUserService())

	body := "username=newuser&password=validpassword123&role=viewer"
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleUserCreate(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestHandleUserCreate_NilUsers(t *testing.T) {
	t.Parallel()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"},
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	h, _ := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store,
		AuditLog: &stubAuditLogStore{},
		CSRF:     csrf,
		Users:    nil,
	})

	body := "username=newuser&password=validpassword123&role=viewer"
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	w := httptest.NewRecorder()
	h.HandleUserCreate(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHandleUserCreate_AdminRole(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	h := newAdminHandlerWithUsers(t, svc)

	body := "username=adminuser&password=validpassword123&role=admin&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	w := httptest.NewRecorder()
	h.HandleUserCreate(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestHandleUserCreate_CreateError(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	svc.createErr = users.ErrUserAlreadyExists
	h := newAdminHandlerWithUsers(t, svc)

	body := "username=existing&password=validpassword123&role=viewer&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	w := httptest.NewRecorder()
	h.HandleUserCreate(w, req)

	// On create error, re-renders the form with the error message
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (re-render with error)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Failed to create user") {
		t.Error("response should contain error message")
	}
}

// ---------------------------------------------------------------------------
// HandleUserDelete
// ---------------------------------------------------------------------------

func TestHandleUserDelete_Success(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	svc.addUser(users.User{ID: 1, Username: "alice", Role: users.RoleAdmin})
	svc.addUser(users.User{ID: 2, Username: "bob", Role: users.RoleUser})
	h := newAdminHandlerWithUsers(t, svc)

	body := "csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/admin/users/2/delete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	req.SetPathValue("id", "2")
	w := httptest.NewRecorder()
	h.HandleUserDelete(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestHandleUserDelete_InvalidID(t *testing.T) {
	t.Parallel()
	h := newAdminHandlerWithUsers(t, newStubUserService())

	body := "csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/admin/users/abc/delete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()
	h.HandleUserDelete(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleUserDelete_NoSession(t *testing.T) {
	t.Parallel()
	h := newAdminHandlerWithUsers(t, newStubUserService())

	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/delete", nil)
	w := httptest.NewRecorder()
	h.HandleUserDelete(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestHandleUserDelete_NilUsers(t *testing.T) {
	t.Parallel()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"},
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	h, _ := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store, AuditLog: &stubAuditLogStore{}, CSRF: csrf,
		Users: nil,
	})

	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/delete", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.HandleUserDelete(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandleUserChangeRole
// ---------------------------------------------------------------------------

func TestHandleUserChangeRole_Success(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	svc.addUser(users.User{ID: 1, Username: "alice", Role: users.RoleUser})
	h := newAdminHandlerWithUsers(t, svc)

	body := "role=admin&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/change-role", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.HandleUserChangeRole(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestHandleUserChangeRole_InvalidID(t *testing.T) {
	t.Parallel()
	h := newAdminHandlerWithUsers(t, newStubUserService())

	body := "role=admin&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/admin/users/abc/change-role", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()
	h.HandleUserChangeRole(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleUserChangeRole_NilUsers(t *testing.T) {
	t.Parallel()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"},
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	h, _ := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store, AuditLog: &stubAuditLogStore{}, CSRF: csrf,
		Users: nil,
	})

	body := "role=admin"
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/change-role", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.HandleUserChangeRole(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandleUserResetPassword
// ---------------------------------------------------------------------------

func TestHandleUserResetPassword_Success(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	svc.addUser(users.User{ID: 1, Username: "alice", Role: users.RoleUser})
	h := newAdminHandlerWithUsers(t, svc)

	body := "new_password=validnewpassword123&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.HandleUserResetPassword(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

func TestHandleUserResetPassword_MissingPassword(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	h := newAdminHandlerWithUsers(t, svc)

	body := "csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.HandleUserResetPassword(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleUserResetPassword_InvalidID(t *testing.T) {
	t.Parallel()
	h := newAdminHandlerWithUsers(t, newStubUserService())

	body := "new_password=validnewpassword123"
	req := httptest.NewRequest(http.MethodPost, "/admin/users/bad/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectAdminSession(req)
	req.SetPathValue("id", "bad")
	w := httptest.NewRecorder()
	h.HandleUserResetPassword(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleUserResetPassword_NilUsers(t *testing.T) {
	t.Parallel()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"},
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	h, _ := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store, AuditLog: &stubAuditLogStore{}, CSRF: csrf,
		Users: nil,
	})

	body := "new_password=validnewpassword123"
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.HandleUserResetPassword(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHandleUserResetPassword_NoSession(t *testing.T) {
	t.Parallel()
	h := newAdminHandlerWithUsers(t, newStubUserService())

	body := "new_password=validnewpassword123"
	req := httptest.NewRequest(http.MethodPost, "/admin/users/1/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.HandleUserResetPassword(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

// ---------------------------------------------------------------------------
// parseUserID helper
// ---------------------------------------------------------------------------

func TestParseUserID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  int64
	}{
		{"1", 1},
		{"42", 42},
		{"0", 0},
		{"", 0},
		{"abc", 0},
		{"1a", 0},
		{"99999", 99999},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := web.ParseUserIDForTest(tc.input)
			if got != tc.want {
				t.Errorf("ParseUserID(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Event emission tests
// ---------------------------------------------------------------------------

func TestHandleUserCreate_EmitsEvent(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf", Role: "admin"},
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	bus := &recordingBus{}
	h, err := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store,
		AuditLog: &stubAuditLogStore{},
		Bus:      bus,
		CSRF:     csrf,
		Users:    svc,
	})
	if err != nil {
		t.Fatalf("NewWebAdminHandler: %v", err)
	}

	body := "username=newuser&password=validpassword123&role=viewer&csrf_token=valid-csrf"
	req := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req = injectSession(req, store.entry)
	w := httptest.NewRecorder()
	h.HandleUserCreate(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if len(bus.events) == 0 {
		t.Error("expected user created event to be emitted")
	}
}
