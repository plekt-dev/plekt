package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/plekt-dev/plekt/internal/editor"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// WebProjectDetailHandler handles project detail page requests.
type WebProjectDetailHandler interface {
	HandleProjectDetailPage(w http.ResponseWriter, r *http.Request)
}

// ProjectDetailHandlerConfig holds dependencies for the project detail handler.
type ProjectDetailHandlerConfig struct {
	Plugins    loader.PluginManager
	Extensions *loader.ExtensionRegistry
	Sessions   WebSessionStore
	CSRF       CSRFProvider
	Users      users.UserStore
	PluginsDir string
	Renderer   editor.Renderer
}

type defaultProjectDetailHandler struct {
	cfg ProjectDetailHandlerConfig
}

// NewProjectDetailHandler constructs a WebProjectDetailHandler.
func NewProjectDetailHandler(cfg ProjectDetailHandlerConfig) (WebProjectDetailHandler, error) {
	if cfg.Plugins == nil {
		return nil, errors.New("ProjectDetailHandlerConfig.Plugins must not be nil")
	}
	if cfg.Sessions == nil {
		return nil, errors.New("ProjectDetailHandlerConfig.Sessions must not be nil")
	}
	if cfg.CSRF == nil {
		return nil, errors.New("ProjectDetailHandlerConfig.CSRF must not be nil")
	}
	return &defaultProjectDetailHandler{cfg: cfg}, nil
}

// HandleProjectDetailPage renders a plugin page within the project context.
// Route: GET /p/projects-plugin/project/{id}/{tab}
// If {tab} is empty (route without tab), redirects to the first available sub-item.
func (h *defaultProjectDetailHandler) HandleProjectDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	tab := r.PathValue("tab")

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, fmt.Sprintf("invalid project id: %q", idStr), http.StatusBadRequest)
		return
	}

	// Get sub-items registered under the projects page.
	subItems := BuildSubItems(h.cfg.Plugins, "projects-plugin:projects")

	// No tab → redirect to first available sub-item.
	if tab == "" {
		if len(subItems) == 0 {
			http.Error(w, "no project tabs available", http.StatusNotFound)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/p/projects-plugin/project/%d/%s", id, subItems[0].PageID), http.StatusFound)
		return
	}

	// Find the matching sub-item.
	var targetItem *templates.PluginNavItem
	for i := range subItems {
		if subItems[i].PageID == tab {
			targetItem = &subItems[i]
			break
		}
	}
	if targetItem == nil {
		http.NotFound(w, r)
		return
	}

	// Get the manifest for the target plugin to find the page descriptor.
	manifest, err := h.cfg.Plugins.GetManifest(targetItem.PluginName)
	if err != nil {
		slog.Error("project detail: get manifest failed",
			"plugin", targetItem.PluginName,
			"error", err,
		)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	var page *loader.PageDescriptor
	for i := range manifest.UI.Pages {
		if manifest.UI.Pages[i].ID == tab {
			page = &manifest.UI.Pages[i]
			break
		}
	}
	if page == nil {
		http.NotFound(w, r)
		return
	}

	csrfToken := h.csrfTokenFromRequest(r)

	// Get project info for the sidebar.
	inputBytes, _ := json.Marshal(map[string]int64{"id": id})
	resultBytes, err := h.cfg.Plugins.CallPlugin(r.Context(), "projects-plugin", "get_project", inputBytes)
	if err != nil {
		slog.Error("project detail: get_project failed",
			"project_id", id,
			"error", err,
		)
		http.Error(w, fmt.Sprintf("failed to load project: %v", err), http.StatusInternalServerError)
		return
	}

	var getResult struct {
		Project struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Color       string `json:"color"`
			Image       string `json:"image"`
		} `json:"project"`
	}
	if err := json.Unmarshal(resultBytes, &getResult); err != nil {
		slog.Error("project detail: unmarshal get_project result",
			"project_id", id,
			"error", err,
		)
		http.Error(w, "failed to parse project data", http.StatusInternalServerError)
		return
	}

	// Call data_function with project_id so the plugin filters by project.
	dataInput, _ := json.Marshal(map[string]int64{"project_id": id})
	result, callErr := h.cfg.Plugins.CallPlugin(r.Context(), targetItem.PluginName, page.DataFunction, dataInput)

	// Serialize MCP tools as JSON for the JS to build forms dynamically.
	toolsJSON, _ := json.Marshal(manifest.MCP.Tools)

	pageData := templates.ProjectPluginPageData{
		CSRFToken:  csrfToken,
		PluginName: targetItem.PluginName,
		PageID:     tab,
		PageTitle:  page.Title,
		PageType:   page.PageType,
		ToolsJSON:  string(toolsJSON),
		Sidebar:    h.buildSidebarData(csrfToken, id, getResult.Project, tab, subItems),
	}

	if callErr != nil {
		slog.Error("project detail: data function call failed",
			"plugin", targetItem.PluginName,
			"function", page.DataFunction,
			"project_id", id,
			"error", callErr,
		)
		pageData.Error = fmt.Sprintf("Failed to load page data: %v", callErr)
	} else {
		pageData.DataJSON = string(result)
	}

	// Resolve extensions for this page's extension points.
	if len(page.ExtensionPoints) > 0 {
		extData := ResolveExtensions(r.Context(), h.cfg.Plugins, h.cfg.Extensions, targetItem.PluginName, page.ExtensionPoints, result)
		if len(extData) > 0 {
			extJSON, _ := json.Marshal(extData)
			pageData.ExtensionsJSON = string(extJSON)
		}
	}

	// Resolve frontend assets.
	if page.Frontend != nil {
		pageData.ScriptURL, pageData.StyleURL = ResolveAssetURLs(h.cfg.PluginsDir, targetItem.PluginName, page.Frontend)
	}

	// Resolve username from session for comment attribution.
	if entry, err := h.sessionEntry(r); err == nil && entry.UserID > 0 {
		pageData.UserID = entry.UserID
		if h.cfg.Users != nil {
			if u, uErr := h.cfg.Users.GetByID(r.Context(), entry.UserID); uErr == nil {
				pageData.Username = u.Username
			}
		}
	}

	// Dual render: htmx → partial + OOB sidebar, otherwise full page.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") == "true" {
		_ = templates.ProjectPluginPagePartial(pageData).Render(r.Context(), w)
	} else {
		_ = templates.ProjectPluginPageFull(pageData).Render(r.Context(), w)
	}
}

func (h *defaultProjectDetailHandler) sessionEntry(r *http.Request) (WebSessionEntry, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return WebSessionEntry{}, err
	}
	return h.cfg.Sessions.Get(cookie.Value)
}

func (h *defaultProjectDetailHandler) csrfTokenFromRequest(r *http.Request) string {
	return CSRFTokenFromRequest(r, h.cfg.Sessions, h.cfg.CSRF)
}

// buildSidebarData constructs ProjectSidebarData, rendering markdown description if a Renderer is available.
func (h *defaultProjectDetailHandler) buildSidebarData(
	csrfToken string, projectID int64,
	project struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Color       string `json:"color"`
		Image       string `json:"image"`
	},
	activeTab string, subItems []templates.PluginNavItem,
) templates.ProjectSidebarData {
	sd := templates.ProjectSidebarData{
		CSRFToken:          csrfToken,
		ProjectID:          projectID,
		ProjectName:        project.Name,
		ProjectDescription: project.Description,
		ProjectColor:       project.Color,
		ProjectImage:       project.Image,
		ActiveTab:          activeTab,
		SubItems:           subItems,
	}
	if project.Description != "" && h.cfg.Renderer != nil {
		rendered, err := h.cfg.Renderer.Render([]byte(project.Description), editor.DefaultRenderOptions(""))
		if err == nil {
			sd.RenderedDescription = rendered
		}
	}
	return sd
}
