package loader

import (
	"context"
	"database/sql"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// ValType represents a WASM value type used in host function signatures.
type ValType int

const (
	ValTypeI32 ValType = iota
	ValTypeI64
	ValTypeF32
	ValTypeF64
)

// HostFunction describes a host-side function that WASM plugins may call.
type HostFunction struct {
	// Namespace groups related host functions (e.g., "mc_db", "mc_event").
	Namespace string
	// Name is the function name visible from WASM.
	Name string
	// Params lists the WASM parameter types.
	Params []ValType
	// Returns lists the WASM return types.
	Returns []ValType
}

// HostFunctionRegistry is the collection of all host functions exposed to WASM plugins.
// Once Seal() is called, no further registrations are accepted.
type HostFunctionRegistry interface {
	// Register adds a host function to the registry.
	// Returns ErrPermissionDenied if the registry has been sealed.
	Register(fn HostFunction) error
	// All returns all registered host functions.
	// Returns ErrRegistryNotSealed if the registry has not yet been sealed,
	// preventing WASM plugins from loading against an incomplete host function set.
	All() ([]HostFunction, error)
	// Seal prevents further registrations.
	Seal()
	// Sealed reports whether the registry has been sealed.
	Sealed() bool
}

// PluginCallContext carries per-call context injected by the host.
// It is NEVER sourced from WASM; the host constructs it before dispatch.
type PluginCallContext struct {
	PluginName        string
	BearerToken       string            // for MCP auth validation only, never forwarded to WASM
	DB                *sql.DB           // per-plugin database, never forwarded to WASM
	Bus               eventbus.EventBus // event bus for mc_event::emit; nil disables emission
	AllowedEmits      []string          // validated copy of Manifest.Events.Emits, set at load time
	AllowedSubscribes []string          // validated copy of Manifest.Events.Subscribes, set at load time
	// LoadedPlugins returns the names of all currently loaded plugins.
	// Called fresh on each mc_config::get("__available_plugins") invocation so
	// the list reflects live load/unload state. May be nil in tests.
	LoadedPlugins func() []string
}

type pluginCallContextKey struct{}

// WithPluginCallContext attaches a PluginCallContext to ctx.
func WithPluginCallContext(ctx context.Context, pcc PluginCallContext) context.Context {
	return context.WithValue(ctx, pluginCallContextKey{}, pcc)
}

// PluginCallContextFrom retrieves the PluginCallContext from ctx.
// The second return value is false if no context was set.
func PluginCallContextFrom(ctx context.Context) (PluginCallContext, bool) {
	v, ok := ctx.Value(pluginCallContextKey{}).(PluginCallContext)
	return v, ok
}
