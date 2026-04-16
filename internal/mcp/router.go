package mcp

import (
	"net/http"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
)

// RouterConfig holds all dependencies for constructing the MCPRouter.
type RouterConfig struct {
	Manager       loader.PluginManager
	SystemHandler *loader.PluginMCPHandler
	Sessions      SessionStore
	AgentService  agents.AgentService // replaces AdminToken
	ServerVersion string
	// Bus is optional. When non-nil, tool-call events are emitted after each
	// tools/call request. When nil, no events are emitted.
	Bus eventbus.EventBus
}

// MCPRouter builds the HTTP routing table for MCP endpoints.
type MCPRouter struct {
	cfg RouterConfig
}

// NewMCPRouter constructs an MCPRouter from the given configuration.
func NewMCPRouter(cfg RouterConfig) MCPRouter {
	return MCPRouter{cfg: cfg}
}

// Build registers MCP routes on mux and returns it.
// Registers:
//   - POST /mcp: federated endpoint, protected by AgentAuthMiddleware (no plugin scope)
//   - POST /plugins/{name}/mcp: per-plugin endpoint, protected by AgentAuthMiddleware (plugin-scoped)
func (router MCPRouter) Build(mux *http.ServeMux) *http.ServeMux {
	federatedDisp := NewFederatedDispatcher(router.cfg.Manager, router.cfg.SystemHandler)
	federatedEP := NewFederatedMCPEndpoint(federatedDisp, router.cfg.Sessions, router.cfg.Bus)
	mux.Handle("POST /mcp", AgentAuthMiddleware("", router.cfg.AgentService, router.cfg.Bus, federatedEP))

	mux.HandleFunc("POST /plugins/{name}/mcp", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := validateURLPluginName(name); err != nil {
			writeError(w, nil, &invalidParamsErr{})
			return
		}

		meta, err := router.cfg.Manager.GetMCPMeta(name)
		if err != nil {
			writeError(w, nil, loader.ErrPluginNotFound)
			return
		}

		disp := NewPluginDispatcher(router.cfg.Manager, meta)
		ep := NewPluginMCPEndpoint(name, disp, router.cfg.Sessions, router.cfg.Bus)

		AgentAuthMiddleware(name, router.cfg.AgentService, router.cfg.Bus, ep).ServeHTTP(w, r)
	})

	return mux
}

// validateURLPluginName delegates to validatePluginName so there is a single
// validation path for plugin names extracted from URL paths.
func validateURLPluginName(name string) error {
	return validatePluginName(name)
}
