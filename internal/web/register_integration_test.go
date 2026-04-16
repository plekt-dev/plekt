package web_test

// Full end-to-end registration flow tests using real HTTP server,
// real InMemoryWebSessionStore, real CSRFProvider, and real UserService stub.
// These tests catch middleware-level bugs (cookie handling, CSRF validation)
// that unit tests with stubs cannot detect.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// buildRegistrationMux wires a minimal mux with GET/POST /register using real
// InMemoryWebSessionStore + CSRFProvider + stub UserService.
func buildRegistrationMux(t *testing.T, svc users.UserService) (*http.ServeMux, web.WebSessionStore) {
	t.Helper()
	sessions, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })

	csrf := web.NewCSRFProvider()
	bus := eventbus.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	settingsStore := &stubRegisterSettingsStore{s: settings.Settings{PasswordMinLength: 8}}
	regHandler := web.NewWebRegisterHandler(svc, sessions, csrf, settingsStore, bus)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /register", regHandler.HandleRegisterPage)
	// POST /register: no WebCSRFMiddleware wrapper: handler validates CSRF itself
	// so stale-session failures redirect back to the form gracefully.
	mux.HandleFunc("POST /register", regHandler.HandleRegisterSubmit)

	return mux, sessions
}

// cookieJar is a minimal single-domain jar for test clients.
type cookieJar struct {
	cookies []*http.Cookie
}

func (j *cookieJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	j.cookies = append(j.cookies, cookies...)
}

func (j *cookieJar) Cookies(_ *url.URL) []*http.Cookie {
	return j.cookies
}

func (j *cookieJar) named(name string) *http.Cookie {
	for _, c := range j.cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// newTestClient returns an *http.Client that follows redirects and stores cookies.
func newTestClient() (*http.Client, *cookieJar) {
	jar := &cookieJar{}
	return &http.Client{Jar: jar}, jar
}

// noRedirectClient does NOT follow redirects so we can assert the 303 itself.
func newNoRedirectClient() (*http.Client, *cookieJar) {
	jar := &cookieJar{}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, jar
}

// extractCSRFFromBody finds the first hidden csrf_token value in HTML.
func extractCSRFFromBody(body string) string {
	const needle = `name="csrf_token" value="`
	idx := strings.Index(body, needle)
	if idx == -1 {
		return ""
	}
	rest := body[idx+len(needle):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		return ""
	}
	return rest[:end]
}

// ---------------------------------------------------------------------------
// integration tests
// ---------------------------------------------------------------------------

// TestRegisterFlow_GetSetsSessionCookie verifies that GET /register always
// creates a session and sets the mc_session cookie without Secure flag (HTTP-safe).
func TestRegisterFlow_GetSetsSessionCookie(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	mux, _ := buildRegistrationMux(t, svc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, jar := newTestClient()
	resp, err := client.Get(srv.URL + "/register")
	if err != nil {
		t.Fatalf("GET /register: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	c := jar.named("mc_session")
	if c == nil {
		t.Fatal("mc_session cookie not set after GET /register")
	}
	if c.Value == "" {
		t.Error("mc_session cookie value is empty")
	}
	if c.Secure {
		t.Error("mc_session cookie must NOT be Secure (app runs over HTTP)")
	}
	if !c.HttpOnly {
		t.Error("mc_session cookie must be HttpOnly")
	}
}

// TestRegisterFlow_GetEmbedsCsrfToken verifies that the registration form
// contains a non-empty csrf_token hidden input.
func TestRegisterFlow_GetEmbedsCsrfToken(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	mux, _ := buildRegistrationMux(t, svc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, _ := newTestClient()
	resp, err := client.Get(srv.URL + "/register")
	if err != nil {
		t.Fatalf("GET /register: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	csrf := extractCSRFFromBody(string(body))
	if csrf == "" {
		t.Fatal("csrf_token not found in register form HTML")
	}
	if len(csrf) < 8 {
		t.Errorf("csrf_token looks too short: %q", csrf)
	}
}

// TestRegisterFlow_PostWithoutCookieRedirects checks that POST /register with
// no session cookie redirects back to /register (graceful recovery).
func TestRegisterFlow_PostWithoutCookieRedirects(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	mux, _ := buildRegistrationMux(t, svc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// POST with no cookie at all: no prior GET
	client, _ := newNoRedirectClient()
	resp, err := client.PostForm(srv.URL+"/register", url.Values{
		"username":         {"alice"},
		"password":         {"password123"},
		"confirm_password": {"password123"},
		"csrf_token":       {"somefaketoken"},
	})
	if err != nil {
		t.Fatalf("POST /register: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 (redirect to fresh form)", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/register?error=no_cookie" {
		t.Errorf("Location = %q, want /register?error=no_cookie", loc)
	}
}

// TestRegisterFlow_PostWithWrongCsrfRedirects checks that a valid session
// cookie but wrong CSRF token redirects back to /register (graceful recovery).
func TestRegisterFlow_PostWithWrongCsrfRedirects(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	mux, _ := buildRegistrationMux(t, svc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// First GET to get a real session cookie
	client, jar := newNoRedirectClient()
	resp, err := client.Get(srv.URL + "/register")
	if err != nil {
		t.Fatalf("GET /register: %v", err)
	}
	resp.Body.Close()

	if jar.named("mc_session") == nil {
		t.Fatal("no mc_session cookie after GET")
	}

	// POST with wrong CSRF token → 303 redirect back to form
	resp, err = client.PostForm(srv.URL+"/register", url.Values{
		"username":         {"alice"},
		"password":         {"password123"},
		"confirm_password": {"password123"},
		"csrf_token":       {"wrongtoken"},
	})
	if err != nil {
		t.Fatalf("POST /register: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 (redirect to fresh form)", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/register?error=csrf_mismatch" {
		t.Errorf("Location = %q, want /register?error=csrf_mismatch", loc)
	}
}

// TestRegisterFlow_FullSuccess is the happy-path: GET /register → extract CSRF →
// POST with correct CSRF + cookie → redirect to /dashboard and user created.
func TestRegisterFlow_FullSuccess(t *testing.T) {
	t.Parallel()
	svc := newStubUserService() // empty: first user gets admin
	mux, _ := buildRegistrationMux(t, svc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, jar := newNoRedirectClient()

	// Step 1: GET /register: get session cookie + CSRF token
	resp, err := client.Get(srv.URL + "/register")
	if err != nil {
		t.Fatalf("GET /register: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", resp.StatusCode)
	}

	sessionCookie := jar.named("mc_session")
	if sessionCookie == nil {
		t.Fatal("mc_session cookie not set by GET /register")
	}

	csrfToken := extractCSRFFromBody(string(body))
	if csrfToken == "" {
		t.Fatalf("csrf_token not found in page HTML; body snippet: %s", string(body)[:min(500, len(body))])
	}

	// Step 2: POST /register with the real CSRF token (cookie sent automatically via jar)
	resp, err = client.PostForm(srv.URL+"/register", url.Values{
		"username":         {"admin"},
		"password":         {"securepassword"},
		"confirm_password": {"securepassword"},
		"csrf_token":       {csrfToken},
	})
	if err != nil {
		t.Fatalf("POST /register: %v", err)
	}
	postBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST status = %d, want 303; body: %s", resp.StatusCode, string(postBody))
	}
	if loc := resp.Header.Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}
}

// TestRegisterFlow_FirstUserGetsAdminRole verifies that the first registered
// user receives the admin role.
func TestRegisterFlow_FirstUserGetsAdminRole(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	mux, _ := buildRegistrationMux(t, svc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, jar := newNoRedirectClient()

	resp, err := client.Get(srv.URL + "/register")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := extractCSRFFromBody(string(body))
	if csrf == "" {
		t.Fatal("no csrf_token in page")
	}
	_ = jar // cookie sent automatically

	resp, err = client.PostForm(srv.URL+"/register", url.Values{
		"username":         {"firstuser"},
		"password":         {"mypassword"},
		"confirm_password": {"mypassword"},
		"csrf_token":       {csrf},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", resp.StatusCode)
	}

	users, _ := svc.ListUsers(context.Background())
	if len(users) == 0 {
		t.Fatal("no users created")
	}
	if users[0].Role != "admin" {
		t.Errorf("first user role = %q, want admin", users[0].Role)
	}
}

// TestRegisterFlow_PasswordMismatchReturns422 verifies that a password mismatch
// re-renders the form (422) rather than redirecting.
func TestRegisterFlow_PasswordMismatchReturns422(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	mux, _ := buildRegistrationMux(t, svc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, _ := newNoRedirectClient()

	resp, err := client.Get(srv.URL + "/register")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := extractCSRFFromBody(string(body))

	resp, err = client.PostForm(srv.URL+"/register", url.Values{
		"username":         {"bob"},
		"password":         {"password1"},
		"confirm_password": {"different"},
		"csrf_token":       {csrf},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	postBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (form re-render)", resp.StatusCode)
	}
	if !strings.Contains(strings.ToLower(string(postBody)), "match") {
		t.Error("response should contain password mismatch message")
	}
}

// TestRegisterFlow_SessionFixationNewCookieAfterSuccess verifies that a NEW
// session cookie is issued after successful registration (session fixation prevention).
func TestRegisterFlow_SessionFixationNewCookieAfterSuccess(t *testing.T) {
	t.Parallel()
	svc := newStubUserService()
	mux, _ := buildRegistrationMux(t, svc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, jar := newNoRedirectClient()

	resp, err := client.Get(srv.URL + "/register")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := extractCSRFFromBody(string(body))

	preSessionID := jar.named("mc_session").Value

	resp, err = client.PostForm(srv.URL+"/register", url.Values{
		"username":         {"charlie"},
		"password":         {"strongpassword"},
		"confirm_password": {"strongpassword"},
		"csrf_token":       {csrf},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	postSessionID := ""
	for _, c := range resp.Cookies() {
		if c.Name == "mc_session" {
			postSessionID = c.Value
		}
	}
	if postSessionID == "" {
		t.Fatal("no new mc_session cookie in POST /register response")
	}
	if postSessionID == preSessionID {
		t.Error("session ID unchanged after registration: session fixation risk")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
