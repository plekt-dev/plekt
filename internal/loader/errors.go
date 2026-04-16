package loader

import "errors"

// Sentinel errors returned by the loader package.
var (
	ErrPluginNotFound            = errors.New("plugin not found")
	ErrPluginAlreadyLoaded       = errors.New("plugin already loaded")
	ErrPluginNotReady            = errors.New("plugin not ready")
	ErrSignatureInvalid          = errors.New("Ed25519 signature invalid")
	ErrUnsignedPlugin            = errors.New("plugin mcp.yaml has no signature block")
	ErrPluginNotInRegistry       = errors.New("plugin not present in trusted registry snapshot")
	ErrPublicKeyMismatch         = errors.New("plugin signing key does not match registry entry")
	ErrKeyRevoked                = errors.New("plugin signing key has been revoked")
	ErrManifestInvalid           = errors.New("manifest invalid")
	ErrPluginDirTraversal        = errors.New("plugin dir traversal detected")
	ErrPermissionDenied          = errors.New("permission denied")
	ErrWASMInit                  = errors.New("WASM plugin initialization failed")
	ErrRegistryNotSealed         = errors.New("host function registry is not yet sealed")
	ErrMigration                 = errors.New("plugin DB migration failed")
	ErrDependencyNotLoaded       = errors.New("required plugin dependency is not loaded")
	ErrCoreVersionIncompatible   = errors.New("plugin requires newer core version")
	ErrDependencyVersionMismatch = errors.New("dependency version mismatch")
	ErrBootstrapCycle            = errors.New("plugin dependency cycle detected")
	ErrBootstrapMissingHardDep   = errors.New("plugin hard dependency not in registry")
)
