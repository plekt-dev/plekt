package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
)

func TestResolveAssetURLs(t *testing.T) {
	t.Parallel()

	// Build a temp directory that mimics ~/.plekt/plugins/myplugin/frontend/
	tmpDir := t.TempDir()
	pluginName := "myplugin"
	frontendDir := filepath.Join(tmpDir, pluginName, "frontend")
	if err := os.MkdirAll(frontendDir, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Write a JS file and a CSS file that the handler should detect.
	if err := os.WriteFile(filepath.Join(frontendDir, "app.js"), []byte("// js"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(frontendDir, "app.css"), []byte("/* css */"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tests := []struct {
		name       string
		pluginsDir string
		pluginName string
		assets     *loader.FrontendAssets
		wantScript string
		wantStyle  string
	}{
		{
			name:       "empty PluginsDir returns empty strings",
			pluginsDir: "",
			pluginName: pluginName,
			assets:     &loader.FrontendAssets{JSFile: "app.js", CSSFile: "app.css"},
			wantScript: "",
			wantStyle:  "",
		},
		{
			name:       "nil assets returns empty strings",
			pluginsDir: tmpDir,
			pluginName: pluginName,
			assets:     nil,
			wantScript: "",
			wantStyle:  "",
		},
		{
			name:       "existing JS and CSS files return correct URLs",
			pluginsDir: tmpDir,
			pluginName: pluginName,
			assets:     &loader.FrontendAssets{JSFile: "app.js", CSSFile: "app.css"},
			wantScript: "/p/myplugin/static/app.js",
			wantStyle:  "/p/myplugin/static/app.css",
		},
		{
			name:       "absent JS file returns empty scriptURL",
			pluginsDir: tmpDir,
			pluginName: pluginName,
			assets:     &loader.FrontendAssets{JSFile: "missing.js"},
			wantScript: "",
			wantStyle:  "",
		},
		{
			name:       "absent CSS file returns empty styleURL",
			pluginsDir: tmpDir,
			pluginName: pluginName,
			assets:     &loader.FrontendAssets{CSSFile: "missing.css"},
			wantScript: "",
			wantStyle:  "",
		},
		{
			name:       "JSFile with path separator (traversal) returns empty scriptURL",
			pluginsDir: tmpDir,
			pluginName: pluginName,
			assets:     &loader.FrontendAssets{JSFile: "../other.js"},
			wantScript: "",
			wantStyle:  "",
		},
		{
			name:       "CSSFile with path separator (traversal) returns empty styleURL",
			pluginsDir: tmpDir,
			pluginName: pluginName,
			assets:     &loader.FrontendAssets{CSSFile: "../other.css"},
			wantScript: "",
			wantStyle:  "",
		},
		{
			name:       "JSFile with double-dot prefix returns empty scriptURL",
			pluginsDir: tmpDir,
			pluginName: pluginName,
			assets:     &loader.FrontendAssets{JSFile: "../../etc/passwd"},
			wantScript: "",
			wantStyle:  "",
		},
		{
			name:       "empty JSFile and CSSFile returns empty strings",
			pluginsDir: tmpDir,
			pluginName: pluginName,
			assets:     &loader.FrontendAssets{},
			wantScript: "",
			wantStyle:  "",
		},
		{
			name:       "only JS file present, CSS absent returns script URL only",
			pluginsDir: tmpDir,
			pluginName: pluginName,
			assets:     &loader.FrontendAssets{JSFile: "app.js", CSSFile: "missing.css"},
			wantScript: "/p/myplugin/static/app.js",
			wantStyle:  "",
		},
		{
			name:       "only CSS file present, JS absent returns style URL only",
			pluginsDir: tmpDir,
			pluginName: pluginName,
			assets:     &loader.FrontendAssets{JSFile: "missing.js", CSSFile: "app.css"},
			wantScript: "",
			wantStyle:  "/p/myplugin/static/app.css",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script, style := ResolveAssetURLs(tc.pluginsDir, tc.pluginName, tc.assets)
			if script != tc.wantScript {
				t.Errorf("scriptURL = %q, want %q", script, tc.wantScript)
			}
			if style != tc.wantStyle {
				t.Errorf("styleURL = %q, want %q", style, tc.wantStyle)
			}
		})
	}
}
