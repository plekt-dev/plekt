package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/i18n"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/registry"
	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/version"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// ErrPluginPathNotAllowed is returned when a plugin path is outside the allowed root.
var ErrPluginPathNotAllowed = errors.New("plugin path not within allowed directory")

// ValidatePluginPath checks that candidate is within allowedRoot using filepath.Clean.
// Returns ErrPluginPathNotAllowed if outside root or if either arg is empty.
// Does NOT check filesystem existence.
func ValidatePluginPath(allowedRoot, candidate string) error {
	if allowedRoot == "" || candidate == "" {
		return ErrPluginPathNotAllowed
	}
	cleanRoot := filepath.Clean(allowedRoot)
	cleanCand := filepath.Clean(candidate)
	if cleanCand == cleanRoot || strings.HasPrefix(cleanCand, cleanRoot+string(filepath.Separator)) {
		return nil
	}
	return ErrPluginPathNotAllowed
}

// WebPluginAdminHandler handles plugin management operations for the web UI.
type WebPluginAdminHandler interface {
	HandlePluginList(w http.ResponseWriter, r *http.Request)
	HandlePluginDetail(w http.ResponseWriter, r *http.Request)
	HandlePluginLoad(w http.ResponseWriter, r *http.Request)
	HandlePluginUnload(w http.ResponseWriter, r *http.Request)
	HandlePluginReload(w http.ResponseWriter, r *http.Request)
	HandlePluginInstallRemote(w http.ResponseWriter, r *http.Request)
	HandlePluginDelete(w http.ResponseWriter, r *http.Request)

	// Slice 3: permissions workflow.
	HandleInspectPluginDir(w http.ResponseWriter, r *http.Request)
	HandlePluginPermissionsPage(w http.ResponseWriter, r *http.Request)
	HandleGrantHost(w http.ResponseWriter, r *http.Request)
	HandleRevokeHost(w http.ResponseWriter, r *http.Request)
}

// PluginAdminHandlerConfig holds dependencies for the plugin admin handler.
type PluginAdminHandlerConfig struct {
	Plugins          loader.PluginManager
	Bus              eventbus.EventBus
	Sessions         WebSessionStore
	CSRF             CSRFProvider
	AllowedPluginDir string
	Settings         settings.SettingsStore  // nil allowed; when set, gates plugin install
	HostGrants       loader.HostGrantStore   // nil allowed; when set, persists per-plugin host grants (Slice 3)
	Registry         registry.RegistryClient // nil allowed; when set, enables the Catalog tab
}

// defaultWebPluginAdminHandler is the production WebPluginAdminHandler implementation.
type defaultWebPluginAdminHandler struct {
	cfg PluginAdminHandlerConfig
}

// NewWebPluginAdminHandler constructs a WebPluginAdminHandler.
// Returns an error if Plugins, Bus, Sessions, CSRF are nil or AllowedPluginDir is empty.
func NewWebPluginAdminHandler(cfg PluginAdminHandlerConfig) (WebPluginAdminHandler, error) {
	if cfg.Plugins == nil {
		return nil, errors.New("PluginAdminHandlerConfig.Plugins must not be nil")
	}
	if cfg.Bus == nil {
		return nil, errors.New("PluginAdminHandlerConfig.Bus must not be nil")
	}
	if cfg.Sessions == nil {
		return nil, errors.New("PluginAdminHandlerConfig.Sessions must not be nil")
	}
	if cfg.CSRF == nil {
		return nil, errors.New("PluginAdminHandlerConfig.CSRF must not be nil")
	}
	if cfg.AllowedPluginDir == "" {
		return nil, errors.New("PluginAdminHandlerConfig.AllowedPluginDir must not be empty")
	}
	return &defaultWebPluginAdminHandler{cfg: cfg}, nil
}

func (h *defaultWebPluginAdminHandler) csrfTokenFromRequest(r *http.Request) string {
	return CSRFTokenFromRequest(r, h.cfg.Sessions, h.cfg.CSRF)
}

// HandlePluginList renders the plugin list page (GET /plugins).
func (h *defaultWebPluginAdminHandler) HandlePluginList(w http.ResponseWriter, r *http.Request) {
	csrfToken := h.csrfTokenFromRequest(r)

	infos := h.cfg.Plugins.List()

	// Build a set of loaded plugin dirs for quick lookup.
	loadedDirs := make(map[string]struct{}, len(infos))
	for _, info := range infos {
		loadedDirs[filepath.Clean(info.Dir)] = struct{}{}
	}

	rows := make([]templates.PluginRowData, 0, len(infos))
	for _, info := range infos {
		errMsg := ""
		if info.Status == loader.PluginStatusError {
			errMsg = "Plugin encountered an error. Check logs."
		}
		rows = append(rows, templates.PluginRowData{
			Name:         info.Name,
			Version:      info.Version,
			Status:       info.Status,
			Dir:          info.Dir,
			ErrorMessage: errMsg,
			CSRFToken:    csrfToken,
		})
	}

	// Discover plugins on disk: show unloaded ones separately.
	var unloaded []templates.DiscoveredPluginRowData
	discovered, err := h.cfg.Plugins.ScanDir(r.Context())
	if err == nil {
		for _, dp := range discovered {
			if _, loaded := loadedDirs[filepath.Clean(dp.Dir)]; loaded {
				continue
			}
			unloaded = append(unloaded, templates.DiscoveredPluginRowData{
				Name:          dp.Name,
				Version:       dp.Version,
				Description:   dp.Description,
				Dir:           dp.Dir,
				ManifestValid: dp.ManifestValid,
				CSRFToken:     csrfToken,
			})
		}
	}

	// Fetch catalog from registry when available.
	var catalog []templates.CatalogPluginData
	if h.cfg.Registry != nil {
		reg, regErr := h.cfg.Registry.FetchRegistry(r.Context())
		if regErr == nil {
			// Build lookup of installed plugin names -> versions.
			installedVersions := make(map[string]string, len(infos))
			for _, info := range infos {
				installedVersions[info.Name] = info.Version
			}
			for _, rp := range reg.Plugins {
				if len(rp.Versions) == 0 {
					continue
				}
				cp := templates.CatalogPluginData{
					Name:        rp.Name,
					Description: rp.Description,
					Author:      rp.Author,
					// A plugin is "signed" from the UI's perspective when the
					// registry entry carries a public_key. Without one,
					// runtime Load would refuse it (ErrPluginNotInRegistry)
					//: surface that here so the operator gets an explicit
					// confirmation modal before installing.
					Signed:    rp.PublicKey != "",
					Official:  rp.Official,
					CSRFToken: csrfToken,
				}
				// Build version list with compatibility info.
				for _, v := range rp.Versions {
					compatible := true
					if v.MinCoreVersion != "" {
						if ok, _ := version.AtLeast(version.Version, v.MinCoreVersion); !ok {
							compatible = false
						}
					}
					cp.Versions = append(cp.Versions, templates.CatalogVersionData{
						Version:        v.Version,
						MinCoreVersion: v.MinCoreVersion,
						Compatible:     compatible,
						SizeBytes:      v.SizeBytes,
					})
				}
				// Collect core constraint and deps from the latest compatible version.
				for _, v := range rp.Versions {
					compatible := true
					if v.MinCoreVersion != "" {
						if compat, _ := version.AtLeast(version.Version, v.MinCoreVersion); !compat {
							compatible = false
						}
					}
					if !compatible {
						// Use first version's core constraint for display even if incompatible.
						if cp.CoreConstraint == "" {
							cp.CoreConstraint = v.MinCoreVersion
							cp.CoreCompatible = false
						}
						continue
					}
					cp.CoreConstraint = v.MinCoreVersion
					cp.CoreCompatible = true
					for dep, constraint := range v.Dependencies {
						_, depInstalled := installedVersions[dep]
						cp.Deps = append(cp.Deps, templates.CatalogDepData{
							Name: dep, Constraint: constraint,
							Installed: depInstalled, Required: true,
						})
					}
					for dep, constraint := range v.OptionalDependencies {
						_, depInstalled := installedVersions[dep]
						cp.Deps = append(cp.Deps, templates.CatalogDepData{
							Name: dep, Constraint: constraint,
							Installed: depInstalled, Required: false,
						})
					}
					break // only first compatible version
				}

				if localVer, ok := installedVersions[rp.Name]; ok {
					cp.Installed = true
					cp.InstalledVersion = localVer
					for _, v := range cp.Versions {
						if v.Compatible && v.Version != localVer {
							cp.UpdateAvail = v.Version
							break
						}
					}
				}
				catalog = append(catalog, cp)
			}
		}
	}

	activeTab := r.URL.Query().Get("tab")

	data := templates.PluginListPageData{
		Plugins:           rows,
		DiscoveredPlugins: unloaded,
		CatalogPlugins:    catalog,
		CSRFToken:         csrfToken,
		ActiveTab:         activeTab,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.PluginListPage(data).Render(r.Context(), w)
}

// HandlePluginDetail renders the plugin detail page (GET /plugins/{name}).
func (h *defaultWebPluginAdminHandler) HandlePluginDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
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

	mcpMeta, err := h.cfg.Plugins.GetMCPMeta(name)
	if err != nil && !errors.Is(err, loader.ErrPluginNotFound) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Get status from PluginInfo.
	infos := h.cfg.Plugins.List()
	var status loader.PluginStatus
	var dir string
	for _, info := range infos {
		if info.Name == name {
			status = info.Status
			dir = info.Dir
			break
		}
	}

	// Translate plugin description and MCP tool descriptions via i18n.
	// Convention: "{plugin-name}.description" and "{plugin-name}.mcp.{tool-name}".
	description := tryTranslate(r.Context(), name+".description", manifest.Description)

	translatedTools := make([]loader.MCPTool, len(mcpMeta.Tools))
	for idx, tool := range mcpMeta.Tools {
		translatedTools[idx] = loader.MCPTool{
			Name:        tool.Name,
			Description: tryTranslate(r.Context(), name+".mcp."+tool.Name, tool.Description),
		}
	}

	data := templates.PluginDetailPageData{
		Name:             manifest.Name,
		Version:          manifest.Version,
		Status:           status,
		Dir:              dir,
		Description:      description,
		Author:           manifest.Author,
		License:          manifest.License,
		EmitsEvents:      manifest.Events.Emits,
		SubscribesEvents: manifest.Events.Subscribes,
		MCPTools:         translatedTools,
		MCPResources:     mcpMeta.Resources,
		CSRFToken:        csrfToken,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.PluginDetailPage(data).Render(r.Context(), w)
}

// tryTranslate attempts to translate key via i18n; returns fallback if no translation found.
func tryTranslate(ctx context.Context, key, fallback string) string {
	translated := i18n.T(ctx, key)
	if translated == key {
		return fallback
	}
	return translated
}

// translateLoadError maps known load errors to safe user-facing messages.
func translateLoadError(err error) string {
	switch {
	case errors.Is(err, loader.ErrPluginAlreadyLoaded):
		return "Plugin is already loaded"
	case errors.Is(err, loader.ErrManifestInvalid):
		return "Invalid plugin manifest"
	case errors.Is(err, loader.ErrWASMInit):
		return "Failed to initialize plugin"
	case errors.Is(err, loader.ErrMigration):
		return "Database migration failed"
	default:
		return "Failed to load plugin"
	}
}

// HandlePluginLoad handles plugin loading (POST /plugins/load).
func (h *defaultWebPluginAdminHandler) HandlePluginLoad(w http.ResponseWriter, r *http.Request) {
	csrfToken := h.csrfTokenFromRequest(r)

	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		h.renderLoadFormWithError(w, r, csrfToken, "", "Failed to load plugin")
		return
	}

	dir := r.FormValue("dir")

	// Resolve to absolute path only when AllowedPluginDir is itself absolute.
	// This handles relative form inputs (e.g. "./e2e/test-plugins/plugin") in
	// production while leaving unit-test configs that use Unix-style roots unchanged.
	if filepath.IsAbs(h.cfg.AllowedPluginDir) && !filepath.IsAbs(dir) {
		if absDir, err := filepath.Abs(dir); err == nil {
			dir = absDir
		}
	}

	// Validate path before any Load call: non-negotiable.
	if err := ValidatePluginPath(h.cfg.AllowedPluginDir, dir); err != nil {
		h.renderLoadFormWithError(w, r, csrfToken, dir, "Plugin path is not allowed")
		return
	}

	// Check allow_plugin_install setting.
	if h.cfg.Settings != nil {
		if s, err := h.cfg.Settings.Load(r.Context()); err == nil && !s.AllowPluginInstall {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			http.Error(w, "Plugin installation is disabled. Enable it in Settings.", http.StatusForbidden)
			return
		}
	}

	// Emit event before Load call.
	h.cfg.Bus.Emit(r.Context(), eventbus.Event{
		Name: eventbus.EventWebPluginLoadRequested,
		Payload: eventbus.WebPluginLoadRequestedPayload{
			Dir:        dir,
			RemoteAddr: r.RemoteAddr,
			OccurredAt: time.Now().UTC(),
		},
	})

	// Slice 3: record any granted_hosts[] the operator approved in the
	// install permissions modal BEFORE Load so the plugin starts with the
	// right AllowedHosts list. Requires HostGrants store and a parseable
	// manifest to resolve plugin name.
	grantedHosts := r.Form["granted_hosts"]
	var grantedPluginName string
	if len(grantedHosts) > 0 && h.cfg.HostGrants != nil {
		manifest, mErr := readManifestFromDir(dir)
		if mErr != nil {
			h.renderLoadFormWithError(w, r, csrfToken, dir, "Failed to parse manifest: "+mErr.Error())
			return
		}
		grantedPluginName = manifest.Name
		user := h.sessionUsername(r)
		for _, host := range grantedHosts {
			host = strings.TrimSpace(host)
			if host == "" {
				continue
			}
			if vErr := loader.ValidateHost(host); vErr != nil {
				h.renderLoadFormWithError(w, r, csrfToken, dir, "Invalid host "+host+": "+vErr.Error())
				return
			}
			if gErr := h.cfg.HostGrants.Grant(r.Context(), loader.HostGrant{
				PluginName: manifest.Name,
				Host:       host,
				GrantedBy:  user,
				GrantedAt:  time.Now().UTC(),
				Source:     "install",
			}); gErr != nil {
				h.renderLoadFormWithError(w, r, csrfToken, dir, "Failed to record host grant: "+gErr.Error())
				return
			}
			h.cfg.Bus.Emit(r.Context(), eventbus.Event{
				Name: eventbus.EventPluginPermissionsHostGranted,
				Payload: eventbus.PluginPermissionsHostGrantedPayload{
					PluginName: manifest.Name,
					Host:       host,
					GrantedBy:  user,
					Source:     "install",
				},
			})
		}
	}

	_, err := h.cfg.Plugins.Load(r.Context(), dir)
	if err != nil {
		errMsg := translateLoadError(err)
		h.renderLoadFormWithError(w, r, csrfToken, dir, errMsg)
		return
	}

	// Slice 3: audit successful install-time grants.
	if grantedPluginName != "" {
		if manifest, mErr := h.cfg.Plugins.GetManifest(grantedPluginName); mErr == nil {
			perms := loader.DeriveWithGrants(manifest, nil)
			h.cfg.Bus.Emit(r.Context(), eventbus.Event{
				Name: eventbus.EventPluginPermissionsGranted,
				Payload: eventbus.PluginPermissionsGrantedPayload{
					PluginName:   grantedPluginName,
					Capabilities: capIDStrings(perms),
					GrantedHosts: grantedHosts,
					GrantedBy:    h.sessionUsername(r),
				},
			})
		}
	}

	// On success, tell htmx to redirect to the plugin list.
	w.Header().Set("HX-Redirect", "/admin/plugins")
	w.WriteHeader(http.StatusOK)
}

// renderLoadFormWithError re-renders the load form fragment with an error message.
func (h *defaultWebPluginAdminHandler) renderLoadFormWithError(w http.ResponseWriter, r *http.Request, csrfToken, dirValue, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = templates.PluginLoadForm(templates.PluginLoadFormData{
		CSRFToken: csrfToken,
		Error:     errMsg,
		DirValue:  dirValue,
	}).Render(r.Context(), w)
}

// HandlePluginUnload handles plugin unloading (POST /plugins/{name}/unload).
func (h *defaultWebPluginAdminHandler) HandlePluginUnload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	csrfToken := h.csrfTokenFromRequest(r)

	// Emit event before Unload call.
	h.cfg.Bus.Emit(r.Context(), eventbus.Event{
		Name: eventbus.EventWebPluginUnloadRequested,
		Payload: eventbus.WebPluginUnloadRequestedPayload{
			PluginName: name,
			RemoteAddr: r.RemoteAddr,
			OccurredAt: time.Now().UTC(),
		},
	})

	if err := h.cfg.Plugins.Unload(r.Context(), name); err != nil {
		// Render row fragment showing error state.
		errMsg := "Failed to unload plugin"
		if errors.Is(err, loader.ErrPluginNotFound) {
			errMsg = "Plugin not found"
		}
		row := templates.PluginRowData{
			Name:         name,
			Status:       loader.PluginStatusError,
			ErrorMessage: errMsg,
			CSRFToken:    csrfToken,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = templates.PluginRow(row).Render(r.Context(), w)
		return
	}

	// On success, redirect to plugin list.
	w.Header().Set("HX-Redirect", "/admin/plugins")
	w.WriteHeader(http.StatusOK)
}

// HandlePluginReload handles plugin reloading (POST /plugins/{name}/reload).
func (h *defaultWebPluginAdminHandler) HandlePluginReload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	csrfToken := h.csrfTokenFromRequest(r)

	// Emit event before Reload call.
	h.cfg.Bus.Emit(r.Context(), eventbus.Event{
		Name: eventbus.EventWebPluginReloadRequested,
		Payload: eventbus.WebPluginReloadRequestedPayload{
			PluginName: name,
			RemoteAddr: r.RemoteAddr,
			OccurredAt: time.Now().UTC(),
		},
	})

	info, err := h.cfg.Plugins.Reload(r.Context(), name)
	if err != nil {
		errMsg := "Failed to reload plugin"
		if errors.Is(err, loader.ErrPluginNotFound) {
			errMsg = "Plugin not found"
		}
		row := templates.PluginRowData{
			Name:         name,
			Status:       loader.PluginStatusError,
			ErrorMessage: errMsg,
			CSRFToken:    csrfToken,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = templates.PluginRow(row).Render(r.Context(), w)
		return
	}

	// Render updated row.
	row := templates.PluginRowData{
		Name:      info.Name,
		Version:   info.Version,
		Status:    info.Status,
		Dir:       info.Dir,
		CSRFToken: csrfToken,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.PluginRow(row).Render(r.Context(), w)
}

// HandlePluginInstallRemote handles installing a plugin from the remote registry
// (POST /admin/plugins/install-remote).
func (h *defaultWebPluginAdminHandler) HandlePluginInstallRemote(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Registry == nil {
		http.Error(w, "registry not configured", http.StatusNotImplemented)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "missing plugin name", http.StatusBadRequest)
		return
	}

	// Check allow_plugin_install setting.
	if h.cfg.Settings != nil {
		if s, err := h.cfg.Settings.Load(r.Context()); err == nil && !s.AllowPluginInstall {
			http.Error(w, "plugin installation is disabled in settings", http.StatusForbidden)
			return
		}
	}

	_, pv, err := h.cfg.Registry.FindCompatibleVersion(r.Context(), name)
	if err != nil {
		if errors.Is(err, registry.ErrPluginNotFound) {
			http.Error(w, "plugin not found in registry", http.StatusNotFound)
			return
		}
		if errors.Is(err, registry.ErrNoCompatibleVersion) {
			http.Error(w, "no compatible version found for current core", http.StatusConflict)
			return
		}
		http.Error(w, "failed to query registry", http.StatusBadGateway)
		return
	}

	// Download and unpack only: do NOT load yet.
	// The client will inspect permissions and then POST to /admin/plugins/load.
	pluginDir, err := h.cfg.Plugins.DownloadAndUnpack(r.Context(), pv.DownloadURL, pv.ChecksumSHA256)
	if err != nil {
		http.Error(w, "failed to download plugin: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return the unpacked dir so the client can trigger inspect → permissions → load.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"dir":%q,"name":%q,"version":%q}`, pluginDir, name, pv.Version)
}

// HandlePluginDelete removes an unloaded plugin directory from disk
// (POST /admin/plugins/delete).
func (h *defaultWebPluginAdminHandler) HandlePluginDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	dir := r.FormValue("dir")
	if dir == "" {
		http.Error(w, "missing dir", http.StatusBadRequest)
		return
	}

	// Validate the path is within the allowed plugin root.
	if err := ValidatePluginPath(h.cfg.AllowedPluginDir, dir); err != nil {
		http.Error(w, "path not allowed", http.StatusForbidden)
		return
	}

	// Safety: refuse to delete if the plugin is currently loaded.
	pluginName := filepath.Base(dir)
	for _, p := range h.cfg.Plugins.List() {
		if p.Name == pluginName {
			http.Error(w, "plugin is loaded: unload first", http.StatusConflict)
			return
		}
	}

	if err := os.RemoveAll(dir); err != nil {
		http.Error(w, "failed to delete: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Redirect to refresh page with updated counts.
	w.Header().Set("HX-Redirect", "/admin/plugins?tab=discovered")
	w.WriteHeader(http.StatusOK)
}
