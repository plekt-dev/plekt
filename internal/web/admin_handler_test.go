package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/web"
	"github.com/plekt-dev/plekt/internal/web/audit"
)

// ---------------------------------------------------------------------------
// Stubs for admin handler tests
// ---------------------------------------------------------------------------

// listingSessionStore embeds stubSessionStore and adds ListAll support.
type listingSessionStore struct {
	stubSessionStore
	all []web.WebSessionEntry
}

func (s *listingSessionStore) ListAll() []web.WebSessionEntry {
	return s.all
}

// stubAuditLogStore is a controllable AuditLogStore for admin handler tests.
type stubAuditLogStore struct {
	entries   []audit.AuditLogEntry
	appendErr error
	listErr   error
}

func (s *stubAuditLogStore) Append(_ context.Context, e audit.AuditLogEntry) error {
	s.entries = append(s.entries, e)
	return s.appendErr
}

func (s *stubAuditLogStore) ListRecent(_ context.Context, n int) ([]audit.AuditLogEntry, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if len(s.entries) <= n {
		return s.entries, nil
	}
	return s.entries[:n], nil
}

func (s *stubAuditLogStore) ListFiltered(_ context.Context, f audit.AuditFilter) ([]audit.AuditLogEntry, int, error) {
	if s.listErr != nil {
		return nil, 0, s.listErr
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(s.entries) <= limit {
		return s.entries, len(s.entries), nil
	}
	return s.entries[:limit], len(s.entries), nil
}

func (s *stubAuditLogStore) CountByPrefixes(_ context.Context, prefixes [][]string) ([]int, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return make([]int, len(prefixes)), nil
}

func (s *stubAuditLogStore) Close() error { return nil }

// ---------------------------------------------------------------------------
// Constructor validation tests
// ---------------------------------------------------------------------------

func TestNewWebAdminHandler_NilFields(t *testing.T) {
	t.Parallel()

	now := time.Now()
	validStore := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{ID: "sess", CSRFToken: "tok"},
		},
		all: []web.WebSessionEntry{
			{ID: "sess", CreatedAt: now, ExpiresAt: now.Add(time.Hour), RemoteAddr: "127.0.0.1"},
		},
	}
	validAudit := &stubAuditLogStore{}
	validCSRF := &stubCSRFProvider{token: "tok"}

	tests := []struct {
		name string
		cfg  web.WebAdminHandlerConfig
	}{
		{
			name: "nil Sessions",
			cfg: web.WebAdminHandlerConfig{
				Sessions: nil,
				AuditLog: validAudit,
				CSRF:     validCSRF,
			},
		},
		{
			name: "nil AuditLog",
			cfg: web.WebAdminHandlerConfig{
				Sessions: validStore,
				AuditLog: nil,
				CSRF:     validCSRF,
			},
		},
		{
			name: "nil CSRF",
			cfg: web.WebAdminHandlerConfig{
				Sessions: validStore,
				AuditLog: validAudit,
				CSRF:     nil,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := web.NewWebAdminHandler(tc.cfg)
			if err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestNewWebAdminHandler_ValidConfig(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newAdminHandlerFixture(t)
	if h == nil {
		t.Fatal("handler is nil")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newAdminHandlerFixture(t *testing.T) (web.WebAdminHandler, *listingSessionStore, *stubAuditLogStore, *recordingBus) {
	t.Helper()
	now := time.Now()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{
				ID:         "valid-session",
				CSRFToken:  "valid-csrf",
				CreatedAt:  now,
				ExpiresAt:  now.Add(24 * time.Hour),
				RemoteAddr: "127.0.0.1",
			},
		},
		all: []web.WebSessionEntry{
			{
				ID:         "valid-session",
				CreatedAt:  now,
				ExpiresAt:  now.Add(24 * time.Hour),
				RemoteAddr: "127.0.0.1",
			},
			{
				ID:         "other-session",
				CreatedAt:  now.Add(-time.Hour),
				ExpiresAt:  now.Add(23 * time.Hour),
				RemoteAddr: "10.0.0.1",
			},
		},
	}
	auditStore := &stubAuditLogStore{}
	bus := &recordingBus{}
	csrf := &stubCSRFProvider{token: "valid-csrf"}

	h, err := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store,
		AuditLog: auditStore,
		Bus:      bus,
		CSRF:     csrf,
	})
	if err != nil {
		t.Fatalf("NewWebAdminHandler: %v", err)
	}
	return h, store, auditStore, bus
}

func newAdminRequest(t *testing.T, method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	return req, httptest.NewRecorder()
}

// ---------------------------------------------------------------------------
// HandleUserProfile
// ---------------------------------------------------------------------------

func TestHandleUserProfile_OK(t *testing.T) {
	t.Parallel()
	h, store, _, _ := newAdminHandlerFixture(t)

	req, w := newAdminRequest(t, http.MethodGet, "/user", "")
	req = injectSession(req, store.entry)
	h.HandleUserProfile(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("page body should not be empty")
	}
	// Should show current session info
	if !strings.Contains(body, "valid-session") {
		t.Error("page should show current session ID")
	}
	// Should list all sessions including other
	if !strings.Contains(body, "other-session") {
		t.Error("page should list other sessions")
	}
	// Current session should be marked
	if !strings.Contains(body, "current") {
		t.Error("current session row must be highlighted")
	}
}

// ---------------------------------------------------------------------------
// HandleSessionRevoke
// ---------------------------------------------------------------------------

func TestHandleSessionRevoke_OtherSession(t *testing.T) {
	t.Parallel()
	h, store, _, bus := newAdminHandlerFixture(t)

	req, w := newAdminRequest(t, http.MethodPost, "/user/sessions/other-session/revoke", "csrf_token=valid-csrf")
	req.SetPathValue("id", "other-session")
	req = injectSession(req, store.entry)
	h.HandleSessionRevoke(w, req)

	// Should redirect to /user (not /login)
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/user" {
		t.Errorf("Location = %q, want /user", loc)
	}

	// Event should be emitted
	found := false
	for _, e := range bus.events {
		if e.Name == eventbus.EventAdminSessionRevoked {
			found = true
		}
	}
	if !found {
		t.Error("EventAdminSessionRevoked should be emitted")
	}
}

func TestHandleSessionRevoke_OwnSession(t *testing.T) {
	t.Parallel()
	h, store, _, _ := newAdminHandlerFixture(t)

	req, w := newAdminRequest(t, http.MethodPost, "/user/sessions/valid-session/revoke", "csrf_token=valid-csrf")
	req.SetPathValue("id", "valid-session")
	req = injectSession(req, store.entry)
	h.HandleSessionRevoke(w, req)

	// Should redirect to /login when revoking own session
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}

	// Cookie should be cleared (MaxAge=-1)
	var clearedCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "mc_session" {
			clearedCookie = c
		}
	}
	if clearedCookie == nil {
		t.Fatal("mc_session cookie should be present in response (cleared)")
	}
	if clearedCookie.MaxAge != -1 {
		t.Errorf("cookie MaxAge = %d, want -1 (cleared)", clearedCookie.MaxAge)
	}
}

func TestHandleSessionRevoke_NilBus_NoEventPanic(t *testing.T) {
	t.Parallel()
	now := time.Now()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{
				ID:        "valid-session",
				CSRFToken: "valid-csrf",
				CreatedAt: now,
				ExpiresAt: now.Add(24 * time.Hour),
			},
		},
	}
	auditStore := &stubAuditLogStore{}
	csrf := &stubCSRFProvider{token: "valid-csrf"}

	h, err := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store,
		AuditLog: auditStore,
		Bus:      nil, // nil bus allowed
		CSRF:     csrf,
	})
	if err != nil {
		t.Fatalf("NewWebAdminHandler: %v", err)
	}

	req, w := newAdminRequest(t, http.MethodPost, "/user/sessions/other-sess/revoke", "csrf_token=valid-csrf")
	req.SetPathValue("id", "other-sess")
	req = injectSession(req, store.entry)
	h.HandleSessionRevoke(w, req) // must not panic with nil bus
}

// ---------------------------------------------------------------------------
// HandleAuditLog
// ---------------------------------------------------------------------------

func TestHandleAuditLog_OK(t *testing.T) {
	t.Parallel()
	h, store, auditStore, _ := newAdminHandlerFixture(t)

	now := time.Now().UTC()
	auditStore.entries = []audit.AuditLogEntry{
		{EventName: "web.auth.login_success", RemoteAddr: "1.2.3.4", SessionID: "s1", OccurredAt: now},
		{EventName: "web.auth.logout", RemoteAddr: "1.2.3.4", SessionID: "s1", OccurredAt: now.Add(-time.Minute)},
	}

	req, w := newAdminRequest(t, http.MethodGet, "/admin/audit", "")
	req = injectSession(req, store.entry)
	h.HandleAuditLog(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "web.auth.login_success") {
		t.Error("page should contain audit log entry event names")
	}
}

func TestHandleAuditLog_StoreError(t *testing.T) {
	t.Parallel()
	now := time.Now()
	store := &listingSessionStore{
		stubSessionStore: stubSessionStore{
			entry: web.WebSessionEntry{
				ID:        "valid-session",
				CSRFToken: "valid-csrf",
				CreatedAt: now,
				ExpiresAt: now.Add(24 * time.Hour),
			},
		},
	}
	auditStore := &stubAuditLogStore{listErr: audit.ErrAuditLogUnavailable}
	csrf := &stubCSRFProvider{token: "valid-csrf"}

	h, err := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: store,
		AuditLog: auditStore,
		CSRF:     csrf,
	})
	if err != nil {
		t.Fatalf("NewWebAdminHandler: %v", err)
	}

	req, w := newAdminRequest(t, http.MethodGet, "/admin/audit", "")
	req = injectSession(req, store.entry)
	h.HandleAuditLog(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (error rendered in page)", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "unavailable") {
		t.Error("page should contain error banner when audit log unavailable")
	}
}

func TestHandleAuditLog_EmptyEntries(t *testing.T) {
	t.Parallel()
	h, store, _, _ := newAdminHandlerFixture(t)

	req, w := newAdminRequest(t, http.MethodGet, "/admin/audit", "")
	req = injectSession(req, store.entry)
	h.HandleAuditLog(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// No-session cases (handler's own SessionFromRequest fails)
// ---------------------------------------------------------------------------

// newRequestNoSession builds a request to path with no mc_session cookie.
func newRequestNoSession(t *testing.T, method, path string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	return req, httptest.NewRecorder()
}

func TestHandleUserProfile_NoSession(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newAdminHandlerFixture(t)

	req, w := newRequestNoSession(t, http.MethodGet, "/user")
	h.HandleUserProfile(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestHandleSessionRevoke_NoSession(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newAdminHandlerFixture(t)

	req, w := newRequestNoSession(t, http.MethodPost, "/user/sessions/some-id/revoke")
	req.SetPathValue("id", "some-id")
	h.HandleSessionRevoke(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestHandleAuditLog_NoSession(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newAdminHandlerFixture(t)

	req, w := newRequestNoSession(t, http.MethodGet, "/admin/audit")
	h.HandleAuditLog(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestHandleSessionRevoke_EmptyID(t *testing.T) {
	t.Parallel()
	h, store, _, _ := newAdminHandlerFixture(t)

	req, w := newAdminRequest(t, http.MethodPost, "/user/sessions//revoke", "csrf_token=valid-csrf")
	req = injectSession(req, store.entry)
	// PathValue("id") returns "": no SetPathValue call
	h.HandleSessionRevoke(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
