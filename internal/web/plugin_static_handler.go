package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// allowedStaticExtensions is the whitelist of file extensions that may be served
// from plugin static directories. Requests for other extensions are rejected
// with 403 Forbidden to prevent serving sensitive files (.html, .php, etc.).
var allowedStaticExtensions = map[string]bool{
	".css":   true,
	".js":    true,
	".png":   true,
	".jpg":   true,
	".jpeg":  true,
	".gif":   true,
	".ico":   true,
	".woff":  true,
	".woff2": true,
	".ttf":   true,
	".eot":   true,
	".svg":   true,
	".map":   true,
}

// PluginStaticHandler serves static files from a plugin's frontend/ directory.
// Route: GET /p/{plugin}/static/{path...}
// No authentication required: same as core /static/ route.
type PluginStaticHandler struct {
	PluginsDir string
}

func (h *PluginStaticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pluginName := r.PathValue("plugin")
	requestedPath := r.PathValue("path")

	// Reject plugin names containing path separators
	if strings.ContainsAny(pluginName, "/\\.") {
		http.NotFound(w, r)
		return
	}

	if pluginName == "" || requestedPath == "" {
		http.NotFound(w, r)
		return
	}

	base := filepath.Join(h.PluginsDir, pluginName, "frontend")
	full := filepath.Clean(filepath.Join(base, requestedPath))

	// Path traversal protection: resolved path must stay inside frontend/
	if !strings.HasPrefix(full, filepath.Clean(base)+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}

	// VULN-02: Only serve files with whitelisted extensions.
	ext := strings.ToLower(filepath.Ext(requestedPath))
	if !allowedStaticExtensions[ext] {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, full)
}
