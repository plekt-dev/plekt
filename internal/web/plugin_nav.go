package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// isLayoutFreePath returns true for request paths that never render the
// shared layout (and therefore don't need plugin nav / global scripts /
// locale resolution). Hot polling endpoints belong here so they don't pile
// up work on every heartbeat.
func isLayoutFreePath(p string) bool {
	if p == "/api/events" || p == "/health" || p == "/favicon.ico" {
		return true
	}
	if strings.HasPrefix(p, "/static/") {
		return true
	}
	return false
}

// PluginNavCache holds an immutable snapshot of plugin nav items so the
// nav middleware doesn't acquire RLocks on the plugin manager for every
// HTTP request. The snapshot is rebuilt on Invalidate(): wire that to
// plugin.loaded / plugin.unloaded EventBus events at startup.
//
// Without this cache, every page render takes ~7 RLocks (List + GetManifest
// per plugin), which contends with the manager's write locks during plugin
// load and produces visible TTFB spikes when many requests stack up.
type PluginNavCache struct {
	pm    loader.PluginManager
	value atomic.Value // []templates.PluginNavItem (immutable, never mutated)
}

// NewPluginNavCache builds the initial snapshot eagerly so the first HTTP
// request after startup doesn't pay the build cost.
func NewPluginNavCache(pm loader.PluginManager) *PluginNavCache {
	c := &PluginNavCache{pm: pm}
	c.value.Store(buildPluginNavItems(pm))
	return c
}

// Get returns the current snapshot. Safe for concurrent readers: atomic load.
func (c *PluginNavCache) Get() []templates.PluginNavItem {
	v := c.value.Load()
	if v == nil {
		return nil
	}
	return v.([]templates.PluginNavItem)
}

// Invalidate rebuilds the snapshot from the manager. Call this after a plugin
// is loaded or unloaded. Safe for concurrent invalidations: last writer wins
// and the value is immutable.
func (c *PluginNavCache) Invalidate() {
	c.value.Store(buildPluginNavItems(c.pm))
}

// GlobalScriptsMiddleware injects the registered plugin-global frontend
// scripts into the request context. Templates read them via
// templates.GlobalScriptsFromContext without any handler changes.
//
// A nil registry is tolerated and produces an empty list, leaving the layout
// loop a no-op. This keeps the middleware safe to install unconditionally.
func GlobalScriptsMiddleware(reg GlobalScriptRegistry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip for streaming / API / heartbeat endpoints: they don't
			// render layouts and have no need for plugin nav or scripts.
			// Avoids stacking work on hot polling paths.
			if isLayoutFreePath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			var scripts []templates.GlobalScript
			if reg != nil {
				for _, e := range reg.List() {
					scripts = append(scripts, templates.GlobalScript{
						PluginName: e.PluginName,
						URL:        e.URL,
						CSSURL:     e.CSSURL,
					})
				}
			}
			ctx := templates.WithGlobalScripts(r.Context(), scripts)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// PluginNavMiddleware injects plugin navigation items into the request context.
// Templates read them via templates.PluginNavFromContext without handler changes.
//
// Backwards compatible: pass either a *PluginNavCache (preferred: atomic
// snapshot, no lock contention) or a loader.PluginManager (legacy: rebuilds
// on every request). Tests still using the manager-only signature continue
// to compile via PluginNavMiddlewareFromManager.
func PluginNavMiddleware(cache *PluginNavCache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip for streaming / API / heartbeat endpoints: they don't
			// render layouts and have no need for plugin nav or scripts.
			// Avoids stacking work on hot polling paths.
			if isLayoutFreePath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			var items []templates.PluginNavItem
			if cache != nil {
				items = cache.Get() // atomic load: zero contention
			}
			ctx := templates.WithPluginNav(r.Context(), items)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// PluginNavMiddlewareFromManager is the legacy entry point for code/tests that
// hold only a PluginManager. It wraps the manager in a one-shot cache. Prefer
// PluginNavMiddleware(cache) at production wiring sites so invalidation can be
// hooked to plugin.loaded / plugin.unloaded events.
func PluginNavMiddlewareFromManager(pm loader.PluginManager) func(http.Handler) http.Handler {
	return PluginNavMiddleware(NewPluginNavCache(pm))
}

// buildPluginNavItems returns top-level plugin nav items (pages without nav_parent).
// Sub-items are excluded from the global sidebar.
func buildPluginNavItems(pm loader.PluginManager) []templates.PluginNavItem {
	var items []templates.PluginNavItem
	for _, info := range pm.List() {
		if info.Status != loader.PluginStatusActive {
			continue
		}
		manifest, err := pm.GetManifest(info.Name)
		if err != nil {
			continue
		}
		for _, page := range manifest.UI.Pages {
			if page.NavParent != "" {
				continue
			}
			items = append(items, templates.PluginNavItem{
				PluginName: info.Name,
				PageID:     page.ID,
				Title:      page.Title,
				Icon:       page.Icon,
				URL:        fmt.Sprintf("/p/%s/%s", info.Name, page.ID),
			})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Title < items[j].Title
	})
	return items
}

// BuildSubItems returns sub-item nav entries for a given parent page.
// parentRef is the nav_parent value to match, formatted as "{plugin}:{page_id}".
// Results are sorted by NavOrder, then alphabetically by Title.
func BuildSubItems(pm loader.PluginManager, parentRef string) []templates.PluginNavItem {
	var items []templates.PluginNavItem
	for _, info := range pm.List() {
		if info.Status != loader.PluginStatusActive {
			continue
		}
		manifest, err := pm.GetManifest(info.Name)
		if err != nil {
			continue
		}
		for _, page := range manifest.UI.Pages {
			if page.NavParent != parentRef {
				continue
			}
			items = append(items, templates.PluginNavItem{
				PluginName: info.Name,
				PageID:     page.ID,
				Title:      page.Title,
				Icon:       page.Icon,
				URL:        fmt.Sprintf("/p/%s/%s", info.Name, page.ID),
				NavOrder:   page.NavOrder,
			})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].NavOrder != items[j].NavOrder {
			return items[i].NavOrder < items[j].NavOrder
		}
		return items[i].Title < items[j].Title
	})
	return items
}
