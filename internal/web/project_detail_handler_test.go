package web_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/i18n"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/web"
)

// ---------------------------------------------------------------------------
// Stub: controllable plugin manager for project detail tests
// ---------------------------------------------------------------------------

// projectDetailPluginManager is a minimal stub that controls CallPlugin, List, and GetManifest.
type projectDetailPluginManager struct {
	plugins     []loader.PluginInfo
	manifests   map[string]loader.Manifest // key: plugin name
	callResults map[string][]byte          // key: "pluginName/function"
	callErrors  map[string]error
}

func (m *projectDetailPluginManager) callKey(plugin, fn string) string {
	return plugin + "/" + fn
}

func (m *projectDetailPluginManager) Load(_ context.Context, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (m *projectDetailPluginManager) Unload(_ context.Context, _ string) error { return nil }
func (m *projectDetailPluginManager) Reload(_ context.Context, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (m *projectDetailPluginManager) Get(_ string) (loader.Plugin, error) { return nil, nil }
func (m *projectDetailPluginManager) List() []loader.PluginInfo           { return m.plugins }
func (m *projectDetailPluginManager) GetMCPMeta(_ string) (loader.PluginMCPMeta, error) {
	return loader.PluginMCPMeta{}, nil
}
func (m *projectDetailPluginManager) CallPlugin(_ context.Context, plugin, fn string, _ []byte) ([]byte, error) {
	key := m.callKey(plugin, fn)
	if err, ok := m.callErrors[key]; ok {
		return nil, err
	}
	if result, ok := m.callResults[key]; ok {
		return result, nil
	}
	return []byte("{}"), nil
}
func (m *projectDetailPluginManager) GetManifest(name string) (loader.Manifest, error) {
	if m.manifests != nil {
		if man, ok := m.manifests[name]; ok {
			return man, nil
		}
	}
	return loader.Manifest{}, nil
}
func (m *projectDetailPluginManager) ScanDir(_ context.Context) ([]loader.DiscoveredPlugin, error) {
	return nil, nil
}
func (m *projectDetailPluginManager) Shutdown(_ context.Context) error { return nil }
func (m *projectDetailPluginManager) PluginDB(_ string) (*sql.DB, error) {
	return nil, loader.ErrPluginNotFound
}

func (m *projectDetailPluginManager) InstallFromURL(_ context.Context, _, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (m *projectDetailPluginManager) DownloadAndUnpack(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func projectJSON(id int64, name string) []byte {
	return projectJSONWithImage(id, name, "")
}

func projectJSONWithImage(id int64, name, image string) []byte {
	type proj struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Color  string `json:"color"`
		Image  string `json:"image"`
		Status string `json:"status"`
	}
	b, _ := json.Marshal(map[string]proj{
		"project": {ID: id, Name: name, Color: "#6366f1", Image: image, Status: "active"},
	})
	return b
}

// tasksPluginManifest returns a manifest with a "project-tasks" sub-item page.
func tasksPluginManifest() loader.Manifest {
	return loader.Manifest{
		Name: "tasks-plugin",
		UI: loader.UIDeclaration{
			Pages: []loader.PageDescriptor{
				{ID: "board", Title: "Tasks", Icon: "kanban", DataFunction: "get_tasks_board", PageType: "kanban"},
				{ID: "project-tasks", Title: "Tasks", Icon: "kanban", DataFunction: "get_tasks_board", PageType: "kanban", NavParent: "projects-plugin:projects", NavOrder: 1},
			},
		},
	}
}

// notesPluginManifest returns a manifest with a "project-notes" sub-item page.
func notesPluginManifest() loader.Manifest {
	return loader.Manifest{
		Name: "notes-plugin",
		UI: loader.UIDeclaration{
			Pages: []loader.PageDescriptor{
				{ID: "notes", Title: "Notes", Icon: "file-text", DataFunction: "list_notes"},
				{ID: "project-notes", Title: "Notes", Icon: "file-text", DataFunction: "list_notes", NavParent: "projects-plugin:projects", NavOrder: 2},
			},
		},
	}
}

func buildProjectDetailMux(t *testing.T, mgr *projectDetailPluginManager) *http.ServeMux {
	t.Helper()
	store := &stubSessionStore{
		entry: web.WebSessionEntry{ID: "valid-session", CSRFToken: "valid-csrf"},
	}
	csrf := &stubCSRFProvider{token: "valid-csrf"}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	detailHandler, err := web.NewProjectDetailHandler(web.ProjectDetailHandlerConfig{
		Plugins:  mgr,
		Sessions: store,
		CSRF:     csrf,
	})
	if err != nil {
		t.Fatalf("NewProjectDetailHandler: %v", err)
	}

	cfg := web.WebRouterConfig{
		Auth:          auth,
		Sessions:      store,
		CSRF:          csrf,
		ProjectDetail: detailHandler,
	}
	mux := web.NewWebRouter(cfg).Build(nil)

	// Wrap with i18n context so SidebarFooter's T()/Lang() don't panic.
	i18n.Init()
	wrapper := http.NewServeMux()
	wrapper.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loc := i18n.NewLocalizer("en")
		ctx := i18n.WithLocalizer(r.Context(), loc, "en")
		mux.ServeHTTP(w, r.WithContext(ctx))
	}))
	return wrapper
}

// ---------------------------------------------------------------------------
// NewProjectDetailHandler: nil dependency validation
// ---------------------------------------------------------------------------

func TestNewProjectDetailHandler_NilPlugins(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{}
	csrf := &stubCSRFProvider{}
	_, err := web.NewProjectDetailHandler(web.ProjectDetailHandlerConfig{
		Plugins:  nil,
		Sessions: store,
		CSRF:     csrf,
	})
	if err == nil {
		t.Error("expected error when Plugins is nil")
	}
}

func TestNewProjectDetailHandler_NilSessions(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{}
	csrf := &stubCSRFProvider{}
	_, err := web.NewProjectDetailHandler(web.ProjectDetailHandlerConfig{
		Plugins:  mgr,
		Sessions: nil,
		CSRF:     csrf,
	})
	if err == nil {
		t.Error("expected error when Sessions is nil")
	}
}

func TestNewProjectDetailHandler_NilCSRF(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{}
	store := &stubSessionStore{}
	_, err := web.NewProjectDetailHandler(web.ProjectDetailHandlerConfig{
		Plugins:  mgr,
		Sessions: store,
		CSRF:     nil,
	})
	if err == nil {
		t.Error("expected error when CSRF is nil")
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: /project/{id} redirects to first tab
// ---------------------------------------------------------------------------

func TestProjectDetailPage_RedirectToFirstTab(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
			{Name: "tasks-plugin", Status: loader.PluginStatusActive},
		},
		manifests: map[string]loader.Manifest{
			"tasks-plugin": tasksPluginManifest(),
		},
		callResults: map[string][]byte{
			"projects-plugin/get_project": projectJSON(1, "My Project"),
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/1", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("GET /p/projects-plugin/project/1: status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/p/projects-plugin/project/1/project-tasks") {
		t.Errorf("Location = %q, want redirect to first tab", loc)
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: no sub-items returns 404
// ---------------------------------------------------------------------------

func TestProjectDetailPage_NoSubItems(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/1", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("no sub-items: status = %d, want 404", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: unauthenticated redirects to /login
// ---------------------------------------------------------------------------

func TestProjectDetailPage_Unauthenticated(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/1/project-tasks", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("unauthenticated: status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: invalid ID returns 400
// ---------------------------------------------------------------------------

func TestProjectDetailPage_InvalidID(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/notanumber/project-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid id: status = %d, want 400", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: zero ID treated as invalid
// ---------------------------------------------------------------------------

func TestProjectDetailPage_ZeroID(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/0/project-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("zero id: status = %d, want 400", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: unknown tab returns 404
// ---------------------------------------------------------------------------

func TestProjectDetailPage_UnknownTab(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
			{Name: "tasks-plugin", Status: loader.PluginStatusActive},
		},
		manifests: map[string]loader.Manifest{
			"tasks-plugin": tasksPluginManifest(),
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/1/nonexistent-tab", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("unknown tab: status = %d, want 404", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: success renders plugin page with project sidebar
// ---------------------------------------------------------------------------

func TestProjectDetailPage_Success(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
			{Name: "tasks-plugin", Status: loader.PluginStatusActive},
		},
		manifests: map[string]loader.Manifest{
			"tasks-plugin": tasksPluginManifest(),
		},
		callResults: map[string][]byte{
			"projects-plugin/get_project":  projectJSON(42, "Test Project"),
			"tasks-plugin/get_tasks_board": []byte(`{"columns":[],"tasks":[]}`),
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/42/project-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("success: status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	// Should contain project name in sidebar
	if !strings.Contains(body, "Test Project") {
		t.Errorf("expected project name in body, got: %s", body)
	}
	// Should contain plugin-page-content div
	if !strings.Contains(body, "plugin-page-content") {
		t.Errorf("expected plugin-page-content div in body")
	}
	// Should contain back link (i18n key or translated text)
	if !strings.Contains(body, "back_to_projects") && !strings.Contains(body, "Back to Projects") {
		t.Errorf("expected back link in body")
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: htmx partial render includes OOB sidebar
// ---------------------------------------------------------------------------

func TestProjectDetailPage_HTMXPartial(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
			{Name: "tasks-plugin", Status: loader.PluginStatusActive},
		},
		manifests: map[string]loader.Manifest{
			"tasks-plugin": tasksPluginManifest(),
		},
		callResults: map[string][]byte{
			"projects-plugin/get_project":  projectJSON(42, "Test Project"),
			"tasks-plugin/get_tasks_board": []byte(`{"columns":[],"tasks":[]}`),
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/42/project-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("htmx partial: status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	// Should NOT contain full HTML document structure
	if strings.Contains(body, "<!DOCTYPE") || strings.Contains(body, "<!doctype") {
		t.Error("htmx partial should not contain DOCTYPE")
	}
	// Should contain OOB swap attribute
	if !strings.Contains(body, "hx-swap-oob") {
		t.Error("htmx partial should contain OOB sidebar swap")
	}
	// Should still have project name in sidebar
	if !strings.Contains(body, "Test Project") {
		t.Errorf("expected project name in OOB sidebar")
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: project not found (CallPlugin error)
// ---------------------------------------------------------------------------

func TestProjectDetailPage_ProjectNotFound(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
			{Name: "tasks-plugin", Status: loader.PluginStatusActive},
		},
		manifests: map[string]loader.Manifest{
			"tasks-plugin": tasksPluginManifest(),
		},
		callErrors: map[string]error{
			"projects-plugin/get_project": errors.New("project not found"),
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/999/project-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("project not found: status = %d, want 500", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: malformed JSON from get_project
// ---------------------------------------------------------------------------

func TestProjectDetailPage_MalformedProjectJSON(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
			{Name: "tasks-plugin", Status: loader.PluginStatusActive},
		},
		manifests: map[string]loader.Manifest{
			"tasks-plugin": tasksPluginManifest(),
		},
		callResults: map[string][]byte{
			"projects-plugin/get_project": []byte("not valid json {{{"),
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/5/project-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("malformed JSON: status = %d, want 500", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: data function error renders error in page
// ---------------------------------------------------------------------------

func TestProjectDetailPage_DataFunctionError(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
			{Name: "tasks-plugin", Status: loader.PluginStatusActive},
		},
		manifests: map[string]loader.Manifest{
			"tasks-plugin": tasksPluginManifest(),
		},
		callResults: map[string][]byte{
			"projects-plugin/get_project": projectJSON(3, "Err Project"),
		},
		callErrors: map[string]error{
			"tasks-plugin/get_tasks_board": errors.New("data function failed"),
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/3/project-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should still render 200 with error in body (not a crash)
	if w.Code != http.StatusOK {
		t.Errorf("data function error: status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Failed") {
		t.Errorf("expected error message in body, got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: multiple sub-items, correct tab renders
// ---------------------------------------------------------------------------

func TestProjectDetailPage_MultipleSubItems(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
			{Name: "tasks-plugin", Status: loader.PluginStatusActive},
			{Name: "notes-plugin", Status: loader.PluginStatusActive},
		},
		manifests: map[string]loader.Manifest{
			"tasks-plugin": tasksPluginManifest(),
			"notes-plugin": notesPluginManifest(),
		},
		callResults: map[string][]byte{
			"projects-plugin/get_project": projectJSON(5, "Multi Project"),
			"notes-plugin/list_notes":     []byte(`{"notes":[]}`),
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/5/project-notes", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("notes tab: status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Multi Project") {
		t.Errorf("expected project name in body")
	}
}

// ---------------------------------------------------------------------------
// ProjectDetail route not registered when nil
// ---------------------------------------------------------------------------

func TestProjectDetailPage_NilHandler_RouteNotRegistered(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{entry: web.WebSessionEntry{ID: "x", CSRFToken: "tok"}}
	csrf := &stubCSRFProvider{token: "tok"}
	auth := web.NewWebAuthHandler(newStubUserService(), store, csrf, nil, nil)

	cfg := web.WebRouterConfig{
		Auth:          auth,
		Sessions:      store,
		CSRF:          csrf,
		ProjectDetail: nil, // must not register the route
	}
	mux := web.NewWebRouter(cfg).Build(nil)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/1/project-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "x"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Without ProjectDetail and without PluginPages, route returns 404.
	if w.Code != http.StatusNotFound {
		t.Errorf("nil handler: status = %d, want 404", w.Code)
	}
}

// ---------------------------------------------------------------------------
// HandleProjectDetailPage: Image field parsed from get_project response
// ---------------------------------------------------------------------------

func TestProjectDetailPage_ImageFieldParsed(t *testing.T) {
	t.Parallel()
	imageData := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
			{Name: "tasks-plugin", Status: loader.PluginStatusActive},
		},
		manifests: map[string]loader.Manifest{
			"tasks-plugin": tasksPluginManifest(),
		},
		callResults: map[string][]byte{
			"projects-plugin/get_project":  projectJSONWithImage(42, "Image Project", imageData),
			"tasks-plugin/get_tasks_board": []byte(`{"columns":[],"tasks":[]}`),
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/42/project-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("image field test: status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	// Verify the project name is rendered (basic sanity)
	if !strings.Contains(body, "Image Project") {
		t.Error("expected project name 'Image Project' in body")
	}
}

func TestProjectDetailPage_EmptyImageFieldParsed(t *testing.T) {
	t.Parallel()
	mgr := &projectDetailPluginManager{
		plugins: []loader.PluginInfo{
			{Name: "projects-plugin", Status: loader.PluginStatusActive},
			{Name: "tasks-plugin", Status: loader.PluginStatusActive},
		},
		manifests: map[string]loader.Manifest{
			"tasks-plugin": tasksPluginManifest(),
		},
		callResults: map[string][]byte{
			"projects-plugin/get_project":  projectJSONWithImage(42, "No Image Project", ""),
			"tasks-plugin/get_tasks_board": []byte(`{"columns":[],"tasks":[]}`),
		},
	}
	mux := buildProjectDetailMux(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/p/projects-plugin/project/42/project-tasks", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: "valid-session"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("empty image field test: status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No Image Project") {
		t.Error("expected project name 'No Image Project' in body")
	}
}
