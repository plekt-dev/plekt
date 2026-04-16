package web_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"

	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/web"
)

// stubPluginManager implements loader.PluginManager for web package tests.
type stubPluginManager struct {
	plugins []loader.PluginInfo
}

func (m *stubPluginManager) Load(_ context.Context, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (m *stubPluginManager) Unload(_ context.Context, _ string) error { return nil }
func (m *stubPluginManager) Reload(_ context.Context, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (m *stubPluginManager) Get(_ string) (loader.Plugin, error) { return nil, nil }
func (m *stubPluginManager) List() []loader.PluginInfo           { return m.plugins }
func (m *stubPluginManager) GetMCPMeta(_ string) (loader.PluginMCPMeta, error) {
	return loader.PluginMCPMeta{}, nil
}
func (m *stubPluginManager) CallPlugin(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
	return nil, nil
}
func (m *stubPluginManager) GetManifest(_ string) (loader.Manifest, error) {
	return loader.Manifest{}, loader.ErrPluginNotFound
}
func (m *stubPluginManager) ScanDir(_ context.Context) ([]loader.DiscoveredPlugin, error) {
	return nil, nil
}
func (m *stubPluginManager) Shutdown(_ context.Context) error { return nil }
func (m *stubPluginManager) PluginDB(_ string) (*sql.DB, error) {
	return nil, loader.ErrPluginNotFound
}

func (m *stubPluginManager) InstallFromURL(_ context.Context, _, _ string) (loader.PluginInfo, error) {
	return loader.PluginInfo{}, nil
}
func (m *stubPluginManager) DownloadAndUnpack(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

// injectSession returns a copy of r with the given WebSessionEntry injected
// into its context, as WebAuthMiddleware would do in production.
func injectSession(r *http.Request, entry web.WebSessionEntry) *http.Request {
	store := &stubSessionStore{entry: entry}
	var enriched *http.Request
	capture := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		enriched = req
	})
	mw := web.WebAuthMiddleware(store, capture)
	r.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
	mw.ServeHTTP(httptest.NewRecorder(), r)
	return enriched
}
