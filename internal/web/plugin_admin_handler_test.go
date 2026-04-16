package web_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/web"
)

// ---------------------------------------------------------------------------
// Stubs for PluginManager (plugin admin tests need their own to control
// GetManifest, GetMCPMeta etc.: token_meta_test.go stub is in a different
// test file but same package; reuse where possible).
// ---------------------------------------------------------------------------

// stubPluginAdminManager is a full PluginManager stub for plugin admin tests.
type stubPluginAdminManager struct {
	plugins     []loader.PluginInfo
	manifest    loader.Manifest
	mcpMeta     loader.PluginMCPMeta
	loadErr     error
	unloadErr   error
	reloadErr   error
	getErr      error
	manifestErr error
	mcpMetaErr  error
	reloadInfo  loader.PluginInfo
	scanResult  []loader.DiscoveredPlugin
	scanErr     error
}

func (m *stubPluginAdminManager) Load(_ context.Context, _ string) (loader.PluginInfo, error) {
	if m.loadErr != nil {
		return loader.PluginInfo{}, m.loadErr
	}
	if len(m.plugins) > 0 {
		return m.plugins[0], nil
	}
	return loader.PluginInfo{Name: "loaded-plugin", Version: "1.0.0", Status: loader.PluginStatusActive}, nil
}
func (m *stubPluginAdminManager) Unload(_ context.Context, _ string) error { return m.unloadErr }
func (m *stubPluginAdminManager) Reload(_ context.Context, _ string) (loader.PluginInfo, error) {
	if m.reloadErr != nil {
		return loader.PluginInfo{}, m.reloadErr
	}
	return m.reloadInfo, nil
}
func (m *stubPluginAdminManager) Get(_ string) (loader.Plugin, error) {
	return nil, m.getErr
}
func (m *stubPluginAdminManager) List() []loader.PluginInfo { return m.plugins }
func (m *stubPluginAdminManager) GetMCPMeta(_ string) (loader.PluginMCPMeta, error) {
	return m.mcpMeta, m.mcpMetaErr
}
func (m *stubPluginAdminManager) CallPlugin(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
	return nil, nil
}
func (m *stubPluginAdminManager) GetManifest(_ string) (loader.Manifest, error) {
	return m.manifest, m.manifestErr
}
func (m *stubPluginAdminManager) ScanDir(_ context.Context) ([]loader.DiscoveredPlugin, error) {
	return m.scanResult, m.scanErr
}
func (m *stubPluginAdminManager) Shutdown(_ context.Context) error { return nil }
func (m *stubPluginAdminManager) PluginDB(_ string) (*sql.DB, error) {
	return nil, loader.ErrPluginNotFound
}

func (m *stubPluginAdminManager) InstallFromURL(_ context.Context, _, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (m *stubPluginAdminManager) DownloadAndUnpack(_ context.Context, _, _ string) (string, error) {
	return "/tmp/test-plugin", nil
}

// ---------------------------------------------------------------------------
// ValidatePluginPath tests
// ---------------------------------------------------------------------------

func TestValidatePluginPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		allowedRoot string
		candidate   string
		wantErr     bool
	}{
		{
			name:        "exact root match",
			allowedRoot: "/plugins",
			candidate:   "/plugins",
			wantErr:     false,
		},
		{
			name:        "valid subdirectory",
			allowedRoot: "/plugins",
			candidate:   "/plugins/my-plugin",
			wantErr:     false,
		},
		{
			name:        "valid nested subdirectory",
			allowedRoot: "/plugins",
			candidate:   "/plugins/org/my-plugin",
			wantErr:     false,
		},
		{
			name:        "traversal attempt dot-dot",
			allowedRoot: "/plugins",
			candidate:   "/plugins/../etc/passwd",
			wantErr:     true,
		},
		{
			name:        "traversal attempt outside root",
			allowedRoot: "/plugins",
			candidate:   "/etc/plugins",
			wantErr:     true,
		},
		{
			name:        "prefix but not child (sibling directory)",
			allowedRoot: "/plugins",
			candidate:   "/plugins-evil/malicious",
			wantErr:     true,
		},
		{
			name:        "empty allowedRoot",
			allowedRoot: "",
			candidate:   "/plugins/my-plugin",
			wantErr:     true,
		},
		{
			name:        "empty candidate",
			allowedRoot: "/plugins",
			candidate:   "",
			wantErr:     true,
		},
		{
			name:        "both empty",
			allowedRoot: "",
			candidate:   "",
			wantErr:     true,
		},
		{
			name:        "absolute traversal",
			allowedRoot: "/home/user/plugins",
			candidate:   "/home/user/plugins/../../other",
			wantErr:     true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := web.ValidatePluginPath(tc.allowedRoot, tc.candidate)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for allowedRoot=%q candidate=%q, got nil", tc.allowedRoot, tc.candidate)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error for allowedRoot=%q candidate=%q, got %v", tc.allowedRoot, tc.candidate, err)
			}
			if tc.wantErr && err != nil && !errors.Is(err, web.ErrPluginPathNotAllowed) {
				t.Errorf("expected ErrPluginPathNotAllowed, got %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewWebPluginAdminHandler construction tests
// ---------------------------------------------------------------------------

func TestNewWebPluginAdminHandler_MissingConfig(t *testing.T) {
	t.Parallel()

	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{}

	tests := []struct {
		name string
		cfg  web.PluginAdminHandlerConfig
	}{
		{
			name: "nil Plugins",
			cfg: web.PluginAdminHandlerConfig{
				Plugins:          nil,
				Bus:              bus,
				Sessions:         store,
				CSRF:             csrf,
				AllowedPluginDir: "/plugins",
			},
		},
		{
			name: "nil Bus",
			cfg: web.PluginAdminHandlerConfig{
				Plugins:          mgr,
				Bus:              nil,
				Sessions:         store,
				CSRF:             csrf,
				AllowedPluginDir: "/plugins",
			},
		},
		{
			name: "nil Sessions",
			cfg: web.PluginAdminHandlerConfig{
				Plugins:          mgr,
				Bus:              bus,
				Sessions:         nil,
				CSRF:             csrf,
				AllowedPluginDir: "/plugins",
			},
		},
		{
			name: "nil CSRF",
			cfg: web.PluginAdminHandlerConfig{
				Plugins:          mgr,
				Bus:              bus,
				Sessions:         store,
				CSRF:             nil,
				AllowedPluginDir: "/plugins",
			},
		},
		{
			name: "empty AllowedPluginDir",
			cfg: web.PluginAdminHandlerConfig{
				Plugins:          mgr,
				Bus:              bus,
				Sessions:         store,
				CSRF:             csrf,
				AllowedPluginDir: "",
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := web.NewWebPluginAdminHandler(tc.cfg)
			if err == nil {
				t.Error("expected error for missing config field, got nil")
			}
		})
	}
}

func TestNewWebPluginAdminHandler_ValidConfig(t *testing.T) {
	t.Parallel()
	h, err := newPluginAdminHandler(t)
	if err != nil {
		t.Fatalf("NewWebPluginAdminHandler: %v", err)
	}
	if h == nil {
		t.Fatal("handler is nil")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newPluginAdminHandler(t *testing.T) (web.WebPluginAdminHandler, error) {
	t.Helper()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{
		plugins: []loader.PluginInfo{
			{Name: "tasks-plugin", Version: "1.0.0", Status: loader.PluginStatusActive, Dir: "/plugins/tasks-plugin"},
		},
		manifest: loader.Manifest{
			Name:        "tasks-plugin",
			Version:     "1.0.0",
			Description: "Task management plugin",
			Author:      "test",
			License:     "MIT",
			Events: loader.EventsDeclaration{
				Emits:      []string{"task.created"},
				Subscribes: []string{},
			},
		},
		mcpMeta: loader.PluginMCPMeta{
			PluginName: "tasks-plugin",
			Tools:      []loader.MCPTool{{Name: "create_task", Description: "Create a task"}},
			Resources:  []loader.MCPResource{},
		},
	}
	return web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
}

func newPluginAdminRequest(t *testing.T, method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
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
// HandlePluginList tests
// ---------------------------------------------------------------------------

func TestHandlePluginList_OK(t *testing.T) {
	t.Parallel()
	h, err := newPluginAdminHandler(t)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins", "")
	h.HandlePluginList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "tasks-plugin") {
		t.Error("plugin list should contain plugin name 'tasks-plugin'")
	}
}

func TestHandlePluginList_EmptyList(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{plugins: []loader.PluginInfo{}}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins", "")
	h.HandlePluginList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandlePluginList_ErrorStatusPlugin(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{
		plugins: []loader.PluginInfo{
			{Name: "broken-plugin", Version: "1.0.0", Status: loader.PluginStatusError, Dir: "/plugins/broken-plugin"},
		},
	}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins", "")
	h.HandlePluginList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "encountered an error") {
		t.Errorf("expected body to contain 'encountered an error', got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// HandlePluginDetail tests
// ---------------------------------------------------------------------------

func TestHandlePluginDetail_OK(t *testing.T) {
	t.Parallel()
	h, err := newPluginAdminHandler(t)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins/tasks-plugin", "")
	req.SetPathValue("name", "tasks-plugin")
	h.HandlePluginDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "tasks-plugin") {
		t.Error("plugin detail page should contain plugin name")
	}
}

func TestHandlePluginDetail_NotFound(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{
		getErr:      loader.ErrPluginNotFound,
		manifestErr: loader.ErrPluginNotFound,
	}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins/nonexistent", "")
	req.SetPathValue("name", "nonexistent")
	h.HandlePluginDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandlePluginLoad tests
// ---------------------------------------------------------------------------

func TestHandlePluginLoad_ValidPath(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{
		plugins: []loader.PluginInfo{
			{Name: "new-plugin", Version: "1.0.0", Status: loader.PluginStatusActive},
		},
	}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}, "dir": {"/plugins/new-plugin"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/load", form.Encode())
	// Inject session into context as CSRF middleware would.
	req = injectSession(req, store.entry)
	h.HandlePluginLoad(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Header().Get("HX-Redirect") != "/admin/plugins" {
		t.Errorf("HX-Redirect = %q, want /admin/plugins", w.Header().Get("HX-Redirect"))
	}
	// Verify event was emitted
	found := false
	for _, e := range bus.events {
		if e.Name == eventbus.EventWebPluginLoadRequested {
			found = true
		}
	}
	if !found {
		t.Error("EventWebPluginLoadRequested should have been emitted")
	}
}

func TestHandlePluginLoad_TraversalPath(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}, "dir": {"/etc/passwd"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/load", form.Encode())
	req = injectSession(req, store.entry)
	h.HandlePluginLoad(w, req)

	// Should render form fragment with error (not redirect)
	if w.Header().Get("HX-Redirect") == "/admin/plugins" {
		t.Error("traversal path should NOT result in HX-Redirect")
	}
	body := w.Body.String()
	if body == "" {
		t.Error("response body should contain error form")
	}
}

func TestHandlePluginLoad_AlreadyLoaded(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{loadErr: loader.ErrPluginAlreadyLoaded}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}, "dir": {"/plugins/tasks-plugin"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/load", form.Encode())
	req = injectSession(req, store.entry)
	h.HandlePluginLoad(w, req)

	if w.Header().Get("HX-Redirect") == "/admin/plugins" {
		t.Error("should not redirect on error")
	}
	body := w.Body.String()
	if !strings.Contains(body, "already loaded") {
		t.Errorf("error message should mention 'already loaded', got: %s", body)
	}
}

func TestHandlePluginLoad_ManifestInvalid(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{loadErr: loader.ErrManifestInvalid}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}, "dir": {"/plugins/bad-plugin"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/load", form.Encode())
	req = injectSession(req, store.entry)
	h.HandlePluginLoad(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "Invalid plugin manifest") {
		t.Errorf("error message should say 'Invalid plugin manifest', got: %s", body)
	}
}

func TestHandlePluginLoad_WASMInitError(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{loadErr: loader.ErrWASMInit}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}, "dir": {"/plugins/wasm-plugin"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/load", form.Encode())
	req = injectSession(req, store.entry)
	h.HandlePluginLoad(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "Failed to initialize plugin") {
		t.Errorf("error message should say 'Failed to initialize plugin', got: %s", body)
	}
}

func TestHandlePluginLoad_MigrationError(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{loadErr: loader.ErrMigration}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}, "dir": {"/plugins/migrate-plugin"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/load", form.Encode())
	req = injectSession(req, store.entry)
	h.HandlePluginLoad(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "Database migration failed") {
		t.Errorf("error message should say 'Database migration failed', got: %s", body)
	}
}

func TestHandlePluginLoad_GenericError(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{loadErr: errors.New("some unknown error")}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}, "dir": {"/plugins/some-plugin"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/load", form.Encode())
	req = injectSession(req, store.entry)
	h.HandlePluginLoad(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "Failed to load plugin") {
		t.Errorf("error message should say 'Failed to load plugin', got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// HandlePluginUnload tests
// ---------------------------------------------------------------------------

func TestHandlePluginUnload_OK(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{unloadErr: nil}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/tasks-plugin/unload", form.Encode())
	req.SetPathValue("name", "tasks-plugin")
	req = injectSession(req, store.entry)
	h.HandlePluginUnload(w, req)

	if w.Code == http.StatusInternalServerError {
		t.Errorf("unexpected 500: %s", w.Body.String())
	}
	// Verify event was emitted
	found := false
	for _, e := range bus.events {
		if e.Name == eventbus.EventWebPluginUnloadRequested {
			found = true
		}
	}
	if !found {
		t.Error("EventWebPluginUnloadRequested should have been emitted")
	}
}

func TestHandlePluginUnload_Error(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{unloadErr: errors.New("unload failed")}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/tasks-plugin/unload", form.Encode())
	req.SetPathValue("name", "tasks-plugin")
	req = injectSession(req, store.entry)
	h.HandlePluginUnload(w, req)

	// Should not be 2xx success redirect
	body := w.Body.String()
	if body == "" {
		t.Error("error response should have body")
	}
}

func TestHandlePluginUnload_NotFound(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{unloadErr: loader.ErrPluginNotFound}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/tasks-plugin/unload", form.Encode())
	req.SetPathValue("name", "tasks-plugin")
	req = injectSession(req, store.entry)
	h.HandlePluginUnload(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Plugin not found") {
		t.Errorf("expected body to contain 'Plugin not found', got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// HandlePluginReload tests
// ---------------------------------------------------------------------------

func TestHandlePluginReload_OK(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{
		reloadErr: nil,
		reloadInfo: loader.PluginInfo{
			Name:    "tasks-plugin",
			Version: "1.1.0",
			Status:  loader.PluginStatusActive,
			Dir:     "/plugins/tasks-plugin",
		},
	}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/tasks-plugin/reload", form.Encode())
	req.SetPathValue("name", "tasks-plugin")
	req = injectSession(req, store.entry)
	h.HandlePluginReload(w, req)

	if w.Code == http.StatusInternalServerError {
		t.Errorf("unexpected 500: %s", w.Body.String())
	}
	body := w.Body.String()
	if body == "" {
		t.Error("reload response should render row")
	}
	// Verify event was emitted
	found := false
	for _, e := range bus.events {
		if e.Name == eventbus.EventWebPluginReloadRequested {
			found = true
		}
	}
	if !found {
		t.Error("EventWebPluginReloadRequested should have been emitted")
	}
}

func TestHandlePluginReload_Error(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{reloadErr: errors.New("reload failed")}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/tasks-plugin/reload", form.Encode())
	req.SetPathValue("name", "tasks-plugin")
	req = injectSession(req, store.entry)
	h.HandlePluginReload(w, req)

	body := w.Body.String()
	if body == "" {
		t.Error("error response should have body")
	}
}

func TestHandlePluginReload_NotFound(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{reloadErr: loader.ErrPluginNotFound}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/tasks-plugin/reload", form.Encode())
	req.SetPathValue("name", "tasks-plugin")
	req = injectSession(req, store.entry)
	h.HandlePluginReload(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Plugin not found") {
		t.Errorf("expected body to contain 'Plugin not found', got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// csrfTokenFromRequest security paths
// ---------------------------------------------------------------------------

// TestCsrfTokenFromRequest_MissingCookie verifies that csrfTokenFromRequest
// returns an empty string (no panic) when no mc_session cookie is present.
// This is exercised indirectly: with no cookie, HandlePluginList still renders.
func TestCsrfTokenFromRequest_MissingCookie(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Request with no mc_session cookie: csrfTokenFromRequest must not panic.
	req := httptest.NewRequest(http.MethodGet, "/plugins", nil)
	// No cookie added
	w := httptest.NewRecorder()
	h.HandlePluginList(w, req)

	// Should still render 200 (with empty CSRF token).
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// TestCsrfTokenFromRequest_SessionGetError verifies that csrfTokenFromRequest
// returns empty string (no panic) when Sessions.Get returns an error.
func TestCsrfTokenFromRequest_SessionGetError(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{err: web.ErrWebSessionNotFound}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Cookie present but Sessions.Get returns error: must not panic.
	req := httptest.NewRequest(http.MethodGet, "/plugins", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "some-session"})
	w := httptest.NewRecorder()
	h.HandlePluginList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandlePluginDetail: non-ErrPluginNotFound error from GetManifest
// ---------------------------------------------------------------------------

// TestHandlePluginDetail_ManifestInternalError verifies that when GetManifest
// returns a non-ErrPluginNotFound error, the handler returns 500 with a safe
// message (no raw error text) instead of panicking or leaking internal details.
func TestHandlePluginDetail_ManifestInternalError(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	bus := &recordingBus{}
	internalErr := errors.New("db connection refused: /var/run/db.sock")
	mgr := &stubPluginAdminManager{manifestErr: internalErr}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins/some-plugin", "")
	req.SetPathValue("name", "some-plugin")
	h.HandlePluginDetail(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 for non-NotFound manifest error", w.Code)
	}
	// Raw internal error must not appear in the response body.
	if strings.Contains(w.Body.String(), "db connection refused") {
		t.Error("raw internal error must not appear in HTTP response")
	}
}

// TestHandlePluginDetail_MCPMetaInternalError verifies that when GetMCPMeta
// returns a non-ErrPluginNotFound error, the handler returns 500.
func TestHandlePluginDetail_MCPMetaInternalError(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	bus := &recordingBus{}
	internalErr := errors.New("mcp meta unavailable: internal error")
	mgr := &stubPluginAdminManager{
		manifest:   loader.Manifest{Name: "tasks-plugin", Version: "1.0.0"},
		mcpMetaErr: internalErr,
	}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins/tasks-plugin", "")
	req.SetPathValue("name", "tasks-plugin")
	h.HandlePluginDetail(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when GetMCPMeta returns non-NotFound error", w.Code)
	}
	if strings.Contains(w.Body.String(), "mcp meta unavailable") {
		t.Error("raw internal MCP meta error must not appear in HTTP response")
	}
}

// ---------------------------------------------------------------------------
// HandlePluginLoad: ParseForm failure → 400
// ---------------------------------------------------------------------------

// TestHandlePluginLoad_ParseFormError verifies that when ParseForm fails
// (e.g., Content-Length mismatch), the handler returns 400 Bad Request.
// We trigger this by sending a body with a declared length that is too large.
func TestHandlePluginLoad_ParseFormError(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	bus := &recordingBus{}
	mgr := &stubPluginAdminManager{}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Craft a request whose body read will fail: use an errReader that
	// returns an error on Read so that ParseForm fails.
	req := httptest.NewRequest(http.MethodPost, "/plugins/load", &errReader{})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	h.HandlePluginLoad(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when ParseForm fails", w.Code)
	}
}

// errReader is an io.Reader that always returns an error.
type errReader struct{}

func (e *errReader) Read(_ []byte) (int, error) {
	return 0, errors.New("simulated read error for ParseForm")
}

// ---------------------------------------------------------------------------
// Security: raw error strings must not appear in responses
// ---------------------------------------------------------------------------

func TestHandlePluginLoad_NoRawErrorInResponse(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"}}
	csrf := &stubCSRFProvider{token: "valid-csrf", err: nil}
	bus := &recordingBus{}
	rawErrMsg := "secret internal path /var/run/something.sock"
	mgr := &stubPluginAdminManager{loadErr: errors.New(rawErrMsg)}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	form := url.Values{"csrf_token": {"valid-csrf"}, "dir": {"/plugins/some-plugin"}}
	req, w := newPluginAdminRequest(t, http.MethodPost, "/plugins/load", form.Encode())
	req = injectSession(req, store.entry)
	h.HandlePluginLoad(w, req)

	body := w.Body.String()
	if strings.Contains(body, rawErrMsg) {
		t.Error("raw error string must not appear in HTML response")
	}
}

// ---------------------------------------------------------------------------
// HandlePluginList with discovered plugins tests
// ---------------------------------------------------------------------------

func TestHandlePluginList_ShowsDiscoveredPlugins(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	bus := &recordingBus{}

	mgr := &stubPluginAdminManager{
		plugins: []loader.PluginInfo{
			{Name: "loaded-plugin", Version: "1.0.0", Dir: "/plugins/loaded-plugin", Status: loader.PluginStatusActive},
		},
		scanResult: []loader.DiscoveredPlugin{
			{
				Name:          "loaded-plugin",
				Dir:           "/plugins/loaded-plugin",
				Version:       "1.0.0",
				ManifestValid: true,
			},
			{
				Name:          "unloaded-plugin",
				Dir:           "/plugins/unloaded-plugin",
				Version:       "2.0.0",
				Description:   "An unloaded plugin",
				ManifestValid: true,
			},
		},
	}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins", "")
	req = injectSession(req, store.entry)
	h.HandlePluginList(w, req)

	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	// The unloaded plugin should appear in discovered section.
	if !strings.Contains(body, "unloaded-plugin") {
		t.Error("expected unloaded-plugin in discovered section")
	}
}

func TestHandlePluginList_HidesLoadedPluginFromDiscovered(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	bus := &recordingBus{}

	mgr := &stubPluginAdminManager{
		plugins: []loader.PluginInfo{
			{Name: "my-plugin", Version: "1.0.0", Dir: "/plugins/my-plugin", Status: loader.PluginStatusActive},
		},
		// ScanDir returns only the already-loaded plugin.
		scanResult: []loader.DiscoveredPlugin{
			{
				Name:          "my-plugin",
				Dir:           "/plugins/my-plugin",
				Version:       "1.0.0",
				ManifestValid: true,
			},
		},
	}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins", "")
	req = injectSession(req, store.entry)
	h.HandlePluginList(w, req)

	body := w.Body.String()
	// The "All discovered plugins are loaded" text should appear.
	if !strings.Contains(body, "All discovered plugins are loaded") {
		t.Error("expected 'All discovered plugins are loaded' message when all are loaded")
	}
}

func TestHandlePluginList_ScanDirError_RendersPage(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	bus := &recordingBus{}

	mgr := &stubPluginAdminManager{
		scanErr: errors.New("disk error"),
	}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins", "")
	req = injectSession(req, store.entry)
	h.HandlePluginList(w, req)

	// Page should still render even when ScanDir fails.
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 even on ScanDir error", w.Code)
	}
}

func TestHandlePluginList_InvalidManifest_ShowsBadge(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	bus := &recordingBus{}

	mgr := &stubPluginAdminManager{
		scanResult: []loader.DiscoveredPlugin{
			{
				Name:          "",
				Dir:           "/plugins/bad-plugin",
				ManifestValid: false,
			},
		},
	}

	h, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          mgr,
		Bus:              bus,
		Sessions:         store,
		CSRF:             csrf,
		AllowedPluginDir: "/plugins",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req, w := newPluginAdminRequest(t, http.MethodGet, "/plugins", "")
	req = injectSession(req, store.entry)
	h.HandlePluginList(w, req)

	body := w.Body.String()
	// Invalid manifest plugin should show warning badge instead of Load button.
	if !strings.Contains(body, "invalid manifest") {
		t.Error("expected 'invalid manifest' badge for invalid manifest plugin")
	}
}
