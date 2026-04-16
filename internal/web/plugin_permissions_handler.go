// Package web: plugin permissions HTTP handlers (Slice 3).
//
// These handlers implement the operator-facing permissions workflow that
// sits in front of plugin install and edits the core plugin_host_grants store:
//
//   - GET  /admin/plugins/inspect?dir=...            : parse manifest, derive
//     permissions, return JSON
//     (no WASM load)
//   - POST /admin/plugins/load                       : legacy-compatible load
//     that additionally records
//     granted_hosts[] form
//     values into the store
//   - GET  /settings/plugins/{name}/permissions      : settings page showing
//     capabilities + live hosts
//   - POST /settings/plugins/{name}/hosts            : add operator host grant
//     and reload plugin
//   - DELETE /settings/plugins/{name}/hosts/{host}   : revoke grant and reload
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// InspectPluginDirResponse is the JSON body returned by HandleInspectPluginDir.
type InspectPluginDirResponse struct {
	Manifest       loader.Manifest          `json:"manifest"`
	Permissions    loader.PluginPermissions `json:"permissions"`
	RequestedHosts []string                 `json:"requested_hosts"`
	ExistingGrants []loader.HostGrant       `json:"existing_grants"`
}

// sessionUsername pulls the current session username, falling back to "operator".
func (h *defaultWebPluginAdminHandler) sessionUsername(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "operator"
	}
	entry, err := h.cfg.Sessions.Get(cookie.Value)
	if err != nil || entry.Username == "" {
		return "operator"
	}
	return entry.Username
}

// capIDStrings flattens Capability IDs to a plain []string for event payloads.
func capIDStrings(perms loader.PluginPermissions) []string {
	out := make([]string, 0, len(perms.Capabilities))
	for _, c := range perms.Capabilities {
		out = append(out, string(c.ID))
	}
	return out
}

// readManifestFromDir parses the manifest.json file in dir into a loader.Manifest.
// Kept local to this package to avoid growing the loader public API.
func readManifestFromDir(dir string) (loader.Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return loader.Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var m loader.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return loader.Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Name == "" {
		return loader.Manifest{}, errors.New("manifest.name is required")
	}
	return m, nil
}

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONError writes a plain {"error": msg} body with the given status.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// resolveDirFromForm normalises the dir form/query value against the allowed
// plugin root in the same way HandlePluginLoad does.
func (h *defaultWebPluginAdminHandler) resolveDirFromForm(dir string) string {
	if filepath.IsAbs(h.cfg.AllowedPluginDir) && !filepath.IsAbs(dir) {
		if absDir, err := filepath.Abs(dir); err == nil {
			return absDir
		}
	}
	return dir
}

// HandleInspectPluginDir implements GET /admin/plugins/inspect.
//
// It parses the plugin manifest at dir, validates all RequestedHosts, derives
// PluginPermissions (combined with any pre-existing grants from a previous
// install) and returns the result as JSON. No WASM load is performed.
func (h *defaultWebPluginAdminHandler) HandleInspectPluginDir(w http.ResponseWriter, r *http.Request) {
	dir := h.resolveDirFromForm(r.URL.Query().Get("dir"))
	if dir == "" {
		writeJSONError(w, http.StatusBadRequest, "dir is required")
		return
	}
	if err := ValidatePluginPath(h.cfg.AllowedPluginDir, dir); err != nil {
		writeJSONError(w, http.StatusBadRequest, "plugin path is not allowed")
		return
	}

	manifest, err := readManifestFromDir(dir)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "failed to parse manifest: "+err.Error())
		return
	}

	// Validate requested hosts; any single invalid host is a 422.
	for _, host := range manifest.Network.RequestedHosts {
		if err := loader.ValidateHost(host); err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid requested host "+host+": "+err.Error())
			return
		}
	}

	var existing []loader.HostGrant
	if h.cfg.HostGrants != nil {
		var listErr error
		existing, listErr = h.cfg.HostGrants.List(r.Context(), manifest.Name)
		if listErr != nil {
			slog.Warn("permissions: failed to load host grants", "plugin", manifest.Name, "error", listErr)
		}
	}
	perms := loader.DeriveWithGrants(manifest, existing)

	// Audit: operator saw these permissions.
	if h.cfg.Bus != nil {
		h.cfg.Bus.Emit(r.Context(), eventbus.Event{
			Name: eventbus.EventPluginPermissionsPresented,
			Payload: eventbus.PluginPermissionsPresentedPayload{
				PluginName:   manifest.Name,
				Dir:          dir,
				Capabilities: capIDStrings(perms),
			},
		})
	}

	writeJSON(w, http.StatusOK, InspectPluginDirResponse{
		Manifest:       manifest,
		Permissions:    perms,
		RequestedHosts: manifest.Network.RequestedHosts,
		ExistingGrants: existing,
	})
}

// HandlePluginPermissionsPage renders GET /admin/plugins/{name}/permissions.
func (h *defaultWebPluginAdminHandler) HandlePluginPermissionsPage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	csrfToken := h.csrfTokenFromRequest(r)

	manifest, err := h.cfg.Plugins.GetManifest(name)
	if err != nil {
		if errors.Is(err, loader.ErrPluginNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	var grants []loader.HostGrant
	if h.cfg.HostGrants != nil {
		var listErr error
		grants, listErr = h.cfg.HostGrants.List(r.Context(), name)
		if listErr != nil {
			slog.Warn("permissions: failed to load host grants", "plugin", name, "error", listErr)
		}
	}
	perms := loader.DeriveWithGrants(manifest, grants)

	// Look up dir/version from the PluginInfo.
	var dir, version string
	for _, info := range h.cfg.Plugins.List() {
		if info.Name == name {
			dir = info.Dir
			version = info.Version
			break
		}
	}

	data := templates.PluginPermissionsPageData{
		Name:         name,
		Version:      version,
		Dir:          dir,
		Capabilities: perms.Capabilities,
		Hosts:        grants,
		CSRFToken:    csrfToken,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.PluginPermissionsPage(data).Render(r.Context(), w)
}

// grantHostRequest is the JSON/form body for POST /admin/plugins/{name}/hosts.
type grantHostRequest struct {
	Host string `json:"host"`
}

// HandleGrantHost implements POST /admin/plugins/{name}/hosts.
func (h *defaultWebPluginAdminHandler) HandleGrantHost(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || h.cfg.HostGrants == nil {
		writeJSONError(w, http.StatusNotFound, "plugin not found")
		return
	}
	// Ensure plugin is loaded.
	if _, err := h.cfg.Plugins.GetManifest(name); err != nil {
		writeJSONError(w, http.StatusNotFound, "plugin not loaded")
		return
	}

	var host string
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var body grantHostRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		host = strings.TrimSpace(body.Host)
	} else {
		if err := r.ParseForm(); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid form")
			return
		}
		host = strings.TrimSpace(r.FormValue("host"))
	}
	if err := loader.ValidateHost(host); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	user := h.sessionUsername(r)
	grant := loader.HostGrant{
		PluginName: name,
		Host:       host,
		GrantedBy:  user,
		GrantedAt:  time.Now().UTC(),
		Source:     "operator",
	}
	if err := h.cfg.HostGrants.Grant(r.Context(), grant); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to record grant: "+err.Error())
		return
	}

	h.cfg.Bus.Emit(r.Context(), eventbus.Event{
		Name: eventbus.EventPluginPermissionsHostGranted,
		Payload: eventbus.PluginPermissionsHostGrantedPayload{
			PluginName: name,
			Host:       host,
			GrantedBy:  user,
			Source:     "operator",
		},
	})

	// Trigger reload so the plugin picks up the new AllowedHosts.
	h.triggerReload(r.Context(), name, r.RemoteAddr)

	writeJSON(w, http.StatusOK, grant)
}

// HandleRevokeHost implements DELETE /admin/plugins/{name}/hosts/{host}.
func (h *defaultWebPluginAdminHandler) HandleRevokeHost(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	rawHost := r.PathValue("host")
	if name == "" || rawHost == "" || h.cfg.HostGrants == nil {
		writeJSONError(w, http.StatusNotFound, "plugin or host not found")
		return
	}
	host, err := url.PathUnescape(rawHost)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid host encoding")
		return
	}

	if err := h.cfg.HostGrants.Revoke(r.Context(), name, host); err != nil {
		if errors.Is(err, loader.ErrHostGrantNotFound) {
			writeJSONError(w, http.StatusNotFound, "grant not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "failed to revoke: "+err.Error())
		return
	}

	user := h.sessionUsername(r)
	h.cfg.Bus.Emit(r.Context(), eventbus.Event{
		Name: eventbus.EventPluginPermissionsHostRevoked,
		Payload: eventbus.PluginPermissionsHostRevokedPayload{
			PluginName: name,
			Host:       host,
			RevokedBy:  user,
		},
	})

	h.triggerReload(r.Context(), name, r.RemoteAddr)

	w.WriteHeader(http.StatusOK)
}

// triggerReload best-effort reloads the named plugin after a grant change so
// the new AllowedHosts list is picked up. Errors are logged via the event bus
// as reload_requested only: the caller already got its 200.
func (h *defaultWebPluginAdminHandler) triggerReload(ctx context.Context, name, remoteAddr string) {
	if h.cfg.Bus != nil {
		h.cfg.Bus.Emit(ctx, eventbus.Event{
			Name: eventbus.EventWebPluginReloadRequested,
			Payload: eventbus.WebPluginReloadRequestedPayload{
				PluginName: name,
				RemoteAddr: remoteAddr,
				OccurredAt: time.Now().UTC(),
			},
		})
	}
	// Best-effort reload; ignore error (plugin may not be loaded yet on first
	// grant during the install flow: that case is handled by HandlePluginLoad).
	_, _ = h.cfg.Plugins.Reload(ctx, name)
}
