package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// WebPluginPageHandler handles plugin UI page requests.
type WebPluginPageHandler interface {
	HandlePluginPage(w http.ResponseWriter, r *http.Request)
	HandlePluginAction(w http.ResponseWriter, r *http.Request)
}

// PluginPageHandlerConfig holds dependencies for the plugin page handler.
type PluginPageHandlerConfig struct {
	Plugins    loader.PluginManager
	Extensions *loader.ExtensionRegistry
	Sessions   WebSessionStore
	CSRF       CSRFProvider
	Users      users.UserStore
	PluginsDir string
}

type defaultPluginPageHandler struct {
	cfg PluginPageHandlerConfig
}

// NewPluginPageHandler constructs a WebPluginPageHandler.
func NewPluginPageHandler(cfg PluginPageHandlerConfig) (WebPluginPageHandler, error) {
	if cfg.Plugins == nil {
		return nil, errors.New("PluginPageHandlerConfig.Plugins must not be nil")
	}
	if cfg.Sessions == nil {
		return nil, errors.New("PluginPageHandlerConfig.Sessions must not be nil")
	}
	if cfg.CSRF == nil {
		return nil, errors.New("PluginPageHandlerConfig.CSRF must not be nil")
	}
	return &defaultPluginPageHandler{cfg: cfg}, nil
}

// HandlePluginPage renders a plugin page (GET /p/{plugin}/{page}).
func (h *defaultPluginPageHandler) HandlePluginPage(w http.ResponseWriter, r *http.Request) {
	pluginName := r.PathValue("plugin")
	pageID := r.PathValue("page")

	csrfToken := h.csrfTokenFromRequest(r)

	manifest, err := h.cfg.Plugins.GetManifest(pluginName)
	if err != nil {
		if errors.Is(err, loader.ErrPluginNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Find the page descriptor.
	var page *loader.PageDescriptor
	for i := range manifest.UI.Pages {
		if manifest.UI.Pages[i].ID == pageID {
			page = &manifest.UI.Pages[i]
			break
		}
	}
	if page == nil {
		http.NotFound(w, r)
		return
	}

	// Serialize MCP tools as JSON for the JS to build forms dynamically.
	toolsJSON, _ := json.Marshal(manifest.MCP.Tools)

	data := templates.PluginPageData{
		CSRFToken:  csrfToken,
		PluginName: pluginName,
		PageID:     pageID,
		PageTitle:  page.Title,
		PageType:   page.PageType,
		ActivePage: "plugin:" + pluginName + ":" + pageID,
		ToolsJSON:  string(toolsJSON),
	}

	// Resolve username from session for comment attribution.
	if entry, err := h.sessionFromRequest(r); err == nil && entry.UserID > 0 {
		data.UserID = entry.UserID
		if h.cfg.Users != nil {
			if u, uErr := h.cfg.Users.GetByID(r.Context(), entry.UserID); uErr == nil {
				data.Username = u.Username
			}
		}
	}

	// Check if this page has sub-items registered under it. If so, table rows
	// should be clickable links navigating into the sub-item context.
	parentRef := pluginName + ":" + pageID
	subItems := BuildSubItems(h.cfg.Plugins, parentRef)
	if len(subItems) > 0 {
		first := subItems[0]
		// URL pattern: /p/{plugin}/project/{id}/{subItemPageID}
		// {id} is the placeholder that JS replaces with the row's id field.
		data.RowLinkTemplate = fmt.Sprintf("/p/%s/project/{id}/%s", pluginName, first.PageID)
	}

	result, callErr := h.cfg.Plugins.CallPlugin(r.Context(), pluginName, page.DataFunction, nil)
	if callErr != nil {
		slog.Error("plugin page data call failed",
			"plugin", pluginName,
			"function", page.DataFunction,
			"error", callErr,
		)
		data.Error = fmt.Sprintf("Failed to load page data: %v", callErr)
	} else {
		data.DataJSON = string(result)
	}

	// Resolve extensions for this page's extension points.
	if h.cfg.Extensions != nil && len(page.ExtensionPoints) > 0 {
		extData := ResolveExtensions(r.Context(), h.cfg.Plugins, h.cfg.Extensions, pluginName, page.ExtensionPoints, result)
		if len(extData) > 0 {
			extJSON, _ := json.Marshal(extData)
			data.ExtensionsJSON = string(extJSON)
		}
	}

	if page.Frontend != nil {
		data.ScriptURL, data.StyleURL = ResolveAssetURLs(h.cfg.PluginsDir, pluginName, page.Frontend)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.PluginPage(data).Render(r.Context(), w)
}

// HandlePluginAction calls an MCP tool function via WASM and returns JSON.
// POST /p/{plugin}/action/{tool}
// Only tools declared in manifest.mcp.tools are callable.
func (h *defaultPluginPageHandler) HandlePluginAction(w http.ResponseWriter, r *http.Request) {
	pluginName := r.PathValue("plugin")
	toolName := r.PathValue("tool")

	manifest, err := h.cfg.Plugins.GetManifest(pluginName)
	if err != nil {
		if errors.Is(err, loader.ErrPluginNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Validate that the tool exists in manifest: no arbitrary function calls.
	toolAllowed := false
	for _, tool := range manifest.MCP.Tools {
		if tool.Name == toolName {
			toolAllowed = true
			break
		}
	}
	if !toolAllowed {
		http.Error(w, "tool not found", http.StatusNotFound)
		return
	}

	// Determine the input for CallPlugin.
	// WebCSRFMiddleware (which runs before this handler) calls r.ParseForm() for
	// POST requests, consuming r.Body. After that, r.Form is populated with all
	// form values. For form submissions (application/x-www-form-urlencoded) we
	// convert r.Form to JSON; for JSON submissions we fall back to reading r.Body.
	var body []byte
	ct := r.Header.Get("Content-Type")
	if ct == "application/x-www-form-urlencoded" {
		// Form was already parsed by WebCSRFMiddleware: read from r.Form.
		// Ensure r.ParseForm has been called (no-op if already done).
		_ = r.ParseForm()
		if len(r.Form) > 0 {
			m := make(map[string]string, len(r.Form))
			for k, v := range r.Form {
				if k == "csrf_token" {
					continue // never forward CSRF token into plugin input
				}
				if len(v) > 0 {
					m[k] = v[0]
				}
			}
			if jsonBytes, jsonErr := json.Marshal(m); jsonErr == nil {
				body = jsonBytes
			}
		}
	}
	if len(body) == 0 {
		// JSON or unknown content type: read directly from body.
		raw, err := io.ReadAll(io.LimitReader(r.Body, 200*1024*1024))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
		if len(raw) > 0 {
			body = raw
		} else {
			body = []byte("{}")
		}
	}

	result, callErr := h.cfg.Plugins.CallPlugin(r.Context(), pluginName, toolName, body)
	if callErr != nil {
		slog.Error("plugin action call failed",
			"plugin", pluginName,
			"tool", toolName,
			"error", callErr,
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		resp, _ := json.Marshal(map[string]string{"error": callErr.Error()})
		w.Write(resp)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(result)
}

func (h *defaultPluginPageHandler) sessionFromRequest(r *http.Request) (WebSessionEntry, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return WebSessionEntry{}, err
	}
	return h.cfg.Sessions.Get(cookie.Value)
}

func (h *defaultPluginPageHandler) csrfTokenFromRequest(r *http.Request) string {
	return CSRFTokenFromRequest(r, h.cfg.Sessions, h.cfg.CSRF)
}
