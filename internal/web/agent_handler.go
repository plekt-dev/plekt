package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// WebAgentHandler handles /agents/* routes for managing AI agent tokens and permissions.
type WebAgentHandler interface {
	HandleAgentList(w http.ResponseWriter, r *http.Request)
	HandleAgentCreate(w http.ResponseWriter, r *http.Request)
	HandleAgentDetail(w http.ResponseWriter, r *http.Request)
	HandleAgentPermissions(w http.ResponseWriter, r *http.Request)
	HandleAgentWebhook(w http.ResponseWriter, r *http.Request)
	HandleAgentRotateToken(w http.ResponseWriter, r *http.Request)
	HandleAgentDelete(w http.ResponseWriter, r *http.Request)
}

type defaultWebAgentHandler struct {
	agents   agents.AgentService
	plugins  loader.PluginManager
	sessions WebSessionStore
	csrf     CSRFProvider
}

// NewWebAgentHandler constructs a WebAgentHandler.
func NewWebAgentHandler(
	agentSvc agents.AgentService,
	plugins loader.PluginManager,
	sessions WebSessionStore,
	csrf CSRFProvider,
) WebAgentHandler {
	return &defaultWebAgentHandler{
		agents:   agentSvc,
		plugins:  plugins,
		sessions: sessions,
		csrf:     csrf,
	}
}

// wantsJSON reports whether the client prefers a JSON response based on its
// Accept header. Used by handlers that content-negotiate between HTML pages
// (default for browser navigation) and JSON (used by plugin frontends and
// scripted clients).
func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json")
}

// HandleAgentList renders GET /agents: lists all agents with masked tokens.
// Content-negotiates: returns JSON when the client sends Accept: application/json.
// JSON variant exposes only id + name (no tokens) so plugin frontends can populate
// agent comboboxes without admin-only token data leaking.
func (h *defaultWebAgentHandler) HandleAgentList(w http.ResponseWriter, r *http.Request) {
	list, err := h.agents.List(r.Context())
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		type agentJSON struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		out := make([]agentJSON, 0, len(list))
		for _, a := range list {
			out = append(out, agentJSON{ID: a.ID, Name: a.Name})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"agents": out, "total": len(out)})
		return
	}

	csrfToken := h.resolveCSRF(r)

	// Check for flash token (shown once after creation).
	newToken := r.URL.Query().Get("new_token")
	newAgentName := r.URL.Query().Get("new_agent")

	rows := make([]templates.AgentRowData, 0, len(list))
	for _, a := range list {
		rows = append(rows, templates.AgentRowData{
			ID:        a.ID,
			Name:      a.Name,
			Masked:    templates.MaskToken(a.Token),
			CreatedAt: a.CreatedAt,
			UpdatedAt: a.UpdatedAt,
		})
	}

	data := templates.AgentListPageData{
		Agents:       rows,
		CSRFToken:    csrfToken,
		NewToken:     newToken,
		NewAgentName: newAgentName,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.AgentListPage(data).Render(r.Context(), w)
}

// HandleAgentCreate handles POST /agents: creates a new agent.
func (h *defaultWebAgentHandler) HandleAgentCreate(w http.ResponseWriter, r *http.Request) {
	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := h.csrf.Validate(session, r.FormValue("csrf_token")); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
		return
	}

	agent, err := h.agents.Create(r.Context(), name)
	if err != nil {
		http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
		return
	}

	// Redirect with flash token (shown once). URL-encode both values.
	http.Redirect(w, r, fmt.Sprintf("/admin/agents?new_token=%s&new_agent=%s",
		url.QueryEscape(agent.Token), url.QueryEscape(agent.Name)), http.StatusSeeOther)
}

// HandleAgentDetail renders GET /agents/{id}: shows agent detail with permission checkboxes.
func (h *defaultWebAgentHandler) HandleAgentDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	agent, err := h.agents.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	perms, err := h.agents.ListPermissions(r.Context(), id)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Build permission lookup set for quick checking in template.
	permSet := make(map[string]bool, len(perms))
	for _, p := range perms {
		permSet[p.PluginName+"/"+p.ToolName] = true
	}

	// Enumerate all plugins and their tools.
	pluginInfos := h.plugins.List()
	pluginSections := make([]templates.PluginPermissionSection, 0, len(pluginInfos)+1)

	// Built-in system tools section.
	builtinTools := []string{
		"list_plugins", "install_plugin", "unload_plugin",
		"reload_plugin", "get_plugin",
	}
	builtinSection := templates.PluginPermissionSection{
		PluginName:  agents.BuiltinPluginName,
		DisplayName: "System (built-in)",
		Tools:       make([]templates.ToolPermission, 0, len(builtinTools)),
	}
	allBuiltin := permSet[agents.BuiltinPluginName+"/"+agents.WildcardTool]
	builtinSection.AllSelected = allBuiltin
	for _, t := range builtinTools {
		builtinSection.Tools = append(builtinSection.Tools, templates.ToolPermission{
			Name:     t,
			Selected: allBuiltin || permSet[agents.BuiltinPluginName+"/"+t],
		})
	}
	pluginSections = append(pluginSections, builtinSection)

	// Plugin tools sections.
	for _, pi := range pluginInfos {
		meta, metaErr := h.plugins.GetMCPMeta(pi.Name)
		if metaErr != nil {
			continue
		}

		section := templates.PluginPermissionSection{
			PluginName:  pi.Name,
			DisplayName: pi.Name,
			Tools:       make([]templates.ToolPermission, 0, len(meta.Tools)),
		}
		allPlugin := permSet[pi.Name+"/"+agents.WildcardTool]
		section.AllSelected = allPlugin
		for _, t := range meta.Tools {
			section.Tools = append(section.Tools, templates.ToolPermission{
				Name:     t.Name,
				Selected: allPlugin || permSet[pi.Name+"/"+t.Name],
			})
		}
		pluginSections = append(pluginSections, section)
	}

	csrfToken := h.resolveCSRF(r)

	data := templates.AgentDetailPageData{
		Agent: templates.AgentRowData{
			ID:        agent.ID,
			Name:      agent.Name,
			Masked:    templates.MaskToken(agent.Token),
			CreatedAt: agent.CreatedAt,
			UpdatedAt: agent.UpdatedAt,
		},
		Sections:         pluginSections,
		CSRFToken:        csrfToken,
		NewToken:         r.URL.Query().Get("new_token"),
		WebhookURL:       agent.WebhookURL,
		WebhookMode:      agent.WebhookMode,
		WebhookSecretSet: agent.WebhookSecret != "",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.AgentDetailPage(data).Render(r.Context(), w)
}

// HandleAgentPermissions handles POST /agents/{id}/permissions: saves checkbox form.
func (h *defaultWebAgentHandler) HandleAgentPermissions(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := h.csrf.Validate(session, r.FormValue("csrf_token")); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Parse permissions from form.
	// Form fields are named: perm_{pluginName} with value = toolName
	// "Select All" sends value = "*"
	// Individual tools send value = toolName
	var perms []agents.AgentPermission

	// Collect all permission values.
	// The form uses checkboxes with name="perm_{pluginName}" and value="{toolName}".
	for key, values := range r.Form {
		if !strings.HasPrefix(key, "perm_") {
			continue
		}
		pluginName := strings.TrimPrefix(key, "perm_")
		if pluginName == "" {
			continue
		}

		hasWildcard := false
		for _, v := range values {
			if v == agents.WildcardTool {
				hasWildcard = true
				break
			}
		}

		if hasWildcard {
			perms = append(perms, agents.AgentPermission{
				AgentID:    id,
				PluginName: pluginName,
				ToolName:   agents.WildcardTool,
			})
		} else {
			for _, toolName := range values {
				if toolName == "" {
					continue
				}
				perms = append(perms, agents.AgentPermission{
					AgentID:    id,
					PluginName: pluginName,
					ToolName:   toolName,
				})
			}
		}
	}

	if err := h.agents.SetPermissions(r.Context(), id, perms); err != nil {
		http.Error(w, "failed to save permissions", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/agents/%d", id), http.StatusSeeOther)
}

// HandleAgentWebhook handles POST /agents/{id}/webhook: saves the webhook
// URL, mode, and (optionally) secret. An empty webhook_secret field is
// treated as "keep current" so an admin re-saving the form does not wipe an
// existing secret by accident.
func (h *defaultWebAgentHandler) HandleAgentWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := h.csrf.Validate(session, r.FormValue("csrf_token")); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	webhookURL := strings.TrimSpace(r.FormValue("webhook_url"))
	mode := strings.TrimSpace(r.FormValue("webhook_mode"))
	secret := r.FormValue("webhook_secret") // do NOT TrimSpace: secret may be hex which won't have spaces, but trimming is risky for edge cases

	if mode == "" {
		mode = agents.WebhookModeAsync
	}

	if err := h.agents.UpdateWebhook(r.Context(), id, webhookURL, mode); err != nil {
		http.Error(w, "failed to save webhook config", http.StatusInternalServerError)
		return
	}
	if secret != "" {
		if err := h.agents.SetWebhookSecret(r.Context(), id, secret); err != nil {
			http.Error(w, "failed to save webhook secret", http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/agents/%d", id), http.StatusSeeOther)
}

// HandleAgentRotateToken handles POST /agents/{id}/rotate: rotates the token.
func (h *defaultWebAgentHandler) HandleAgentRotateToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := h.csrf.Validate(session, r.FormValue("csrf_token")); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	newToken, err := h.agents.RotateToken(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to rotate token", http.StatusInternalServerError)
		return
	}
	// Show the new token once via query param so the user can copy it.
	http.Redirect(w, r, fmt.Sprintf("/admin/agents/%d?new_token=%s", id, url.QueryEscape(newToken)), http.StatusSeeOther)
}

// HandleAgentDelete handles POST /agents/{id}/delete: deletes the agent.
func (h *defaultWebAgentHandler) HandleAgentDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := h.csrf.Validate(session, r.FormValue("csrf_token")); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.agents.Delete(r.Context(), id); err != nil {
		http.Error(w, "failed to delete agent", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
}

// resolveCSRF extracts the CSRF token from the current session.
func (h *defaultWebAgentHandler) resolveCSRF(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	entry, err := h.sessions.Get(cookie.Value)
	if err != nil {
		return ""
	}
	return h.csrf.TokenForSession(entry)
}
