package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/web"
)

// stubWebPluginAdminHandler records which handler methods were called.
type stubWebPluginAdminHandler struct {
	listCalled   bool
	detailCalled bool
	loadCalled   bool
	unloadCalled bool
	reloadCalled bool
}

func (s *stubWebPluginAdminHandler) HandlePluginList(w http.ResponseWriter, r *http.Request) {
	s.listCalled = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("plugin-list"))
}

func (s *stubWebPluginAdminHandler) HandlePluginDetail(w http.ResponseWriter, r *http.Request) {
	s.detailCalled = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("plugin-detail"))
}

func (s *stubWebPluginAdminHandler) HandlePluginLoad(w http.ResponseWriter, r *http.Request) {
	s.loadCalled = true
	w.Header().Set("HX-Redirect", "/admin/plugins")
	w.WriteHeader(http.StatusOK)
}

func (s *stubWebPluginAdminHandler) HandlePluginUnload(w http.ResponseWriter, r *http.Request) {
	s.unloadCalled = true
	w.Header().Set("HX-Redirect", "/admin/plugins")
	w.WriteHeader(http.StatusOK)
}

func (s *stubWebPluginAdminHandler) HandlePluginReload(w http.ResponseWriter, r *http.Request) {
	s.reloadCalled = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("plugin-row"))
}

// Slice 3 permissions handler stubs: no-op for router wiring tests.
func (s *stubWebPluginAdminHandler) HandleInspectPluginDir(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (s *stubWebPluginAdminHandler) HandlePluginPermissionsPage(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (s *stubWebPluginAdminHandler) HandleGrantHost(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (s *stubWebPluginAdminHandler) HandleRevokeHost(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (s *stubWebPluginAdminHandler) HandlePluginInstallRemote(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (s *stubWebPluginAdminHandler) HandlePluginDelete(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// buildPluginAdminMux creates a mux with plugin admin routes registered using
// a stub handler and a session store that accepts "valid-session".
func buildPluginAdminMux(t *testing.T) (*http.ServeMux, *stubWebPluginAdminHandler, *stubSessionStore) {
	t.Helper()
	stub := &stubWebPluginAdminHandler{}
	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "valid-session",
			CSRFToken: "valid-csrf",
			Role:      "admin",
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Plugins:  stub,
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)
	return mux, stub, store
}

// buildPluginAdminMuxWithRealHandler creates a mux using a real plugin admin
// handler backed by a stub plugin manager.
func buildPluginAdminMuxWithRealHandler(t *testing.T) (*http.ServeMux, *stubSessionStore, *stubCSRFProvider) {
	t.Helper()
	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "valid-session",
			CSRFToken: "valid-csrf",
			Role:      "admin",
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{
		plugins: []loader.PluginInfo{
			{Name: "tasks-plugin", Version: "1.0.0", Status: loader.PluginStatusActive, Dir: "/plugins/tasks-plugin"},
		},
		manifest: loader.Manifest{Name: "tasks-plugin", Version: "1.0.0"},
		mcpMeta:  loader.PluginMCPMeta{PluginName: "tasks-plugin"},
		reloadInfo: loader.PluginInfo{
			Name:    "tasks-plugin",
			Version: "1.0.0",
			Status:  loader.PluginStatusActive,
		},
	}
	pluginsHandler, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("NewWebPluginAdminHandler: %v", err)
	}

	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Plugins:  pluginsHandler,
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)
	return mux, store, csrf
}

// ---------------------------------------------------------------------------
// TestRegisterPluginAdminRoutes_AllRoutes
// ---------------------------------------------------------------------------

func TestRegisterPluginAdminRoutes_GetPluginList(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildPluginAdminMux(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /plugins with valid session: status = %d, want 200", w.Code)
	}
	if !stub.listCalled {
		t.Error("HandlePluginList was not called")
	}
}

func TestRegisterPluginAdminRoutes_GetPluginList_NoSession(t *testing.T) {
	t.Parallel()
	mux, _, _ := buildPluginAdminMux(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("GET /plugins without session: status = %d, want 303", w.Code)
	}
}

func TestRegisterPluginAdminRoutes_GetPluginDetail_NoSession(t *testing.T) {
	t.Parallel()
	mux, _, _ := buildPluginAdminMux(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/tasks-plugin", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("GET /admin/plugins/tasks-plugin without session: status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestRegisterPluginAdminRoutes_GetPluginDetail(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildPluginAdminMux(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/tasks-plugin", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /admin/plugins/{name} with valid session: status = %d, want 200", w.Code)
	}
	if !stub.detailCalled {
		t.Error("HandlePluginDetail was not called")
	}
}

func TestRegisterPluginAdminRoutes_PostPluginLoad_WithCSRF(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildPluginAdminMux(t)

	form := url.Values{"csrf_token": {"valid-csrf"}, "dir": {"/plugins/new-plugin"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/load", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("POST /admin/plugins/load with valid CSRF: status = %d, want 200", w.Code)
	}
	if !stub.loadCalled {
		t.Error("HandlePluginLoad was not called")
	}
}

func TestRegisterPluginAdminRoutes_PostPluginLoad_WithoutCSRF(t *testing.T) {
	t.Parallel()
	// Use a store where CSRF validation will fail.
	stub := &stubWebPluginAdminHandler{}
	store := &stubSessionStore{
		entry: web.WebSessionEntry{
			ID:        "valid-session",
			CSRFToken: "valid-csrf",
			Role:      "admin",
		},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: web.ErrCSRFTokenInvalid}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Plugins:  stub,
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/load", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("POST /admin/plugins/load without CSRF: status = %d, want 403", w.Code)
	}
	if stub.loadCalled {
		t.Error("HandlePluginLoad must not be called when CSRF fails")
	}
}

func TestRegisterPluginAdminRoutes_PostPluginUnload_WithCSRF(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildPluginAdminMux(t)

	form := url.Values{"csrf_token": {"valid-csrf"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/tasks-plugin/unload", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("POST /admin/plugins/{name}/unload with valid CSRF: status = %d, want 200", w.Code)
	}
	if !stub.unloadCalled {
		t.Error("HandlePluginUnload was not called")
	}
}

func TestRegisterPluginAdminRoutes_PostPluginReload_WithCSRF(t *testing.T) {
	t.Parallel()
	mux, stub, _ := buildPluginAdminMux(t)

	form := url.Values{"csrf_token": {"valid-csrf"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/tasks-plugin/reload", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("POST /admin/plugins/{name}/reload with valid CSRF: status = %d, want 200", w.Code)
	}
	if !stub.reloadCalled {
		t.Error("HandlePluginReload was not called")
	}
}

// ---------------------------------------------------------------------------
// TestRegisterPluginAdminRoutes_NilPlugins
// ---------------------------------------------------------------------------

func TestRegisterPluginAdminRoutes_NilPlugins(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:     auth,
		Sessions: store,
		CSRF:     csrf,
		Plugins:  nil, // plugin admin routes must not be registered
	}
	router := web.NewWebRouter(cfg)
	mux := router.Build(nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "x"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /plugins with nil Plugins: status = %d, want 404", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Integration: real handler through router
// ---------------------------------------------------------------------------

func TestRegisterPluginAdminRoutes_Integration_PluginList(t *testing.T) {
	t.Parallel()
	mux, _, _ := buildPluginAdminMuxWithRealHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /plugins integration: status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "tasks-plugin") {
		t.Error("plugin list should contain 'tasks-plugin'")
	}
}

func TestRegisterPluginAdminRoutes_Integration_PostLoad(t *testing.T) {
	t.Parallel()
	mux, _, _ := buildPluginAdminMuxWithRealHandler(t)

	form := url.Values{"csrf_token": {"valid-csrf"}, "dir": {"/plugins/tasks-plugin"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/load", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should succeed and set HX-Redirect
	if w.Code != http.StatusOK {
		t.Errorf("POST /admin/plugins/load integration: status = %d, want 200", w.Code)
	}
	if w.Header().Get("HX-Redirect") != "/admin/plugins" {
		t.Errorf("HX-Redirect = %q, want /admin/plugins", w.Header().Get("HX-Redirect"))
	}
}
