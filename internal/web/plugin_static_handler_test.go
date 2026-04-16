package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginStaticHandler(t *testing.T) {
	// Create a temporary plugin directory with a test file.
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "test-plugin", "frontend")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.js"), []byte("// hello"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	handler := &PluginStaticHandler{PluginsDir: tmpDir}

	tests := []struct {
		name       string
		plugin     string
		path       string
		wantStatus int
	}{
		{
			name:       "existing file returns 200",
			plugin:     "test-plugin",
			path:       "plugin.js",
			wantStatus: http.StatusOK,
		},
		{
			name:       "non-existent file returns 404",
			plugin:     "test-plugin",
			path:       "missing.js",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "path traversal attempt returns 404",
			plugin:     "test-plugin",
			path:       "../../etc/passwd",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "empty plugin name returns 404",
			plugin:     "",
			path:       "plugin.js",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "empty path returns 404",
			plugin:     "test-plugin",
			path:       "",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "Windows-style path traversal in plugin name returns 404",
			plugin:     `..\..\`,
			path:       "plugin.js",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/p/"+tc.plugin+"/static/"+tc.path, nil)
			// httptest does not populate PathValue; set manually.
			req.SetPathValue("plugin", tc.plugin)
			req.SetPathValue("path", tc.path)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("got status %d, want %d", rr.Code, tc.wantStatus)
			}
		})
	}
}

func TestPluginStaticHandler_EmptyPluginsDir(t *testing.T) {
	// A handler with an empty PluginsDir must return 404 for any request
	// because filepath.Join("", ...) would resolve relative to cwd, which
	// is outside any plugin directory.
	handler := &PluginStaticHandler{PluginsDir: ""}

	req := httptest.NewRequest("GET", "/p/test-plugin/static/plugin.js", nil)
	req.SetPathValue("plugin", "test-plugin")
	req.SetPathValue("path", "plugin.js")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// The traversal guard will catch this because the resolved path won't
	// share the expected prefix. 404 is expected.
	if rr.Code != http.StatusNotFound {
		t.Errorf("empty PluginsDir: got status %d, want 404", rr.Code)
	}
}
