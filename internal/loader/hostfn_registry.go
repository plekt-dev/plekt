package loader

import (
	"fmt"
	"sync"
)

// Ensure hostFunctionRegistry implements HostFunctionRegistry at compile time.
var _ HostFunctionRegistry = (*hostFunctionRegistry)(nil)

// hostFunctionRegistry is the concrete HostFunctionRegistry.
type hostFunctionRegistry struct {
	mu     sync.RWMutex
	fns    []HostFunction
	sealed bool
}

// NewHostFunctionRegistry returns a ready-to-use HostFunctionRegistry.
func NewHostFunctionRegistry() HostFunctionRegistry {
	return &hostFunctionRegistry{}
}

// Register adds fn to the registry. Returns ErrPermissionDenied when sealed.
func (r *hostFunctionRegistry) Register(fn HostFunction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return fmt.Errorf("%w: host function registry is sealed", ErrPermissionDenied)
	}
	r.fns = append(r.fns, fn)
	return nil
}

// All returns a snapshot of all registered host functions.
// Returns ErrRegistryNotSealed if the registry has not yet been sealed.
// Callers must seal the registry before handing it to the plugin loader.
func (r *hostFunctionRegistry) All() ([]HostFunction, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.sealed {
		return nil, fmt.Errorf("%w: call Seal() before retrieving host functions", ErrRegistryNotSealed)
	}
	out := make([]HostFunction, len(r.fns))
	copy(out, r.fns)
	return out, nil
}

// Seal prevents further registrations.
func (r *hostFunctionRegistry) Seal() {
	r.mu.Lock()
	r.sealed = true
	r.mu.Unlock()
}

// Sealed reports whether the registry has been sealed.
func (r *hostFunctionRegistry) Sealed() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sealed
}
