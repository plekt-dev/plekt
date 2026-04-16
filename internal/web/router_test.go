package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plekt-dev/plekt/internal/web"
)

func TestNewWebRouter_Build_RegistersRoutes(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"},
	}
	csrf := &stubCSRFProvider{token: "tok"}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:      auth,
		Sessions:  store,
		CSRF:      csrf,
		StaticDir: "",
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(http.NewServeMux())
	if mux == nil {
		t.Fatal("Build returned nil mux")
	}

	// GET /login should work (no auth required)
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		t.Error("GET /login returned 404: route not registered")
	}
}

func TestNewWebRouter_Build_ProtectedRouteRedirects(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	csrf := &stubCSRFProvider{}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(http.NewServeMux())

	// GET / without session should redirect to /login
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("GET / without auth: status = %d, want 302 (redirect to /login)", w.Code)
	}
}

func TestNewWebRouter_Build_AcceptsNilMux(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{}
	csrf := &stubCSRFProvider{}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
	}
	router := web.NewWebRouter(cfg)
	// Build with nil mux should create a new one
	mux := router.Build(nil)
	if mux == nil {
		t.Fatal("Build(nil) returned nil mux")
	}
}
