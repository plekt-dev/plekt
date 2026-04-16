package loader

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/plekt-dev/plekt/internal/config"
	"github.com/plekt-dev/plekt/internal/db"
	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/version"
)

// pluginImpl is the runtime representation of a loaded plugin.
type pluginImpl struct {
	info         PluginInfo
	pool         chan pluginRunner // pool of WASM runner instances; size = min(4, runtime.NumCPU())
	db           *sql.DB
	mu           sync.Mutex // guards info.Status
	inflight     sync.WaitGroup
	allowedEmits []string      // validated copy of Manifest.Events.Emits, for PluginCallContext construction
	mcpTools     []MCPTool     // validated copy of Manifest.MCP.Tools, populated at load time
	mcpResources []MCPResource // validated copy of Manifest.MCP.Resources, populated at load time
}

func (p *pluginImpl) Info() PluginInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info
}

func (p *pluginImpl) Call(ctx context.Context, function string, input []byte) ([]byte, error) {
	p.mu.Lock()
	if p.info.Status != PluginStatusActive {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: plugin %q", ErrPluginNotReady, p.info.Name)
	}
	p.inflight.Add(1)
	p.mu.Unlock()
	defer p.inflight.Done()

	select {
	case runner := <-p.pool:
		defer func() { p.pool <- runner }()
		out, err := runner.CallFunc(function, input)
		if err != nil {
			return nil, fmt.Errorf("WASM call %q.%s: %w", p.info.Name, function, err)
		}
		return out, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PluginDB returns the per-plugin *sql.DB handle for the named loaded plugin.
// Never forward this handle to WASM code: it is for core-side Go callers only
// (e.g. scheduler engine bridge).
func (m *managerImpl) PluginDB(name string) (*sql.DB, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	impl, ok := m.plugins[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrPluginNotFound, name)
	}
	if impl.db == nil {
		return nil, fmt.Errorf("plugin %q has no database", name)
	}
	return impl.db, nil
}

// InstallFromURL downloads a .mcpkg archive from downloadURL, verifies its
// SHA256 checksum, unpacks it into the configured plugin directory, and loads it.
func (m *managerImpl) InstallFromURL(ctx context.Context, downloadURL, checksumSHA256 string) (PluginInfo, error) {
	if downloadURL == "" {
		return PluginInfo{}, errors.New("download URL is required")
	}

	// Download to temp file.
	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("plekt-install-%d.mcpkg", time.Now().UnixNano()))
	defer os.Remove(tmpPath)

	// Create a simple HTTP download with checksum verification.
	if err := downloadAndVerify(ctx, downloadURL, checksumSHA256, tmpPath); err != nil {
		return PluginInfo{}, fmt.Errorf("download plugin: %w", err)
	}

	// Validate and unpack.
	info, err := UnpackPlugin(tmpPath, m.cfg.PluginDir)
	if err != nil {
		return PluginInfo{}, fmt.Errorf("unpack plugin: %w", err)
	}

	// Load the unpacked plugin.
	pluginDir := filepath.Join(m.cfg.PluginDir, info.Name)
	loadedInfo, loadErr := m.Load(ctx, pluginDir)
	if loadErr != nil {
		return PluginInfo{}, fmt.Errorf("load plugin after install: %w", loadErr)
	}

	return loadedInfo, nil
}

// DownloadAndUnpack downloads a .mcpkg archive, verifies checksum, unpacks it,
// but does NOT load the plugin. Returns the unpacked plugin directory path.
func (m *managerImpl) DownloadAndUnpack(ctx context.Context, downloadURL, checksumSHA256 string) (string, error) {
	if downloadURL == "" {
		return "", errors.New("download URL is required")
	}

	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("plekt-install-%d.mcpkg", time.Now().UnixNano()))
	defer os.Remove(tmpPath)

	if err := downloadAndVerify(ctx, downloadURL, checksumSHA256, tmpPath); err != nil {
		return "", fmt.Errorf("download plugin: %w", err)
	}

	info, err := UnpackPlugin(tmpPath, m.cfg.PluginDir)
	if err != nil {
		// If the directory already exists (e.g. unloaded but not removed),
		// remove it and retry.
		if errors.Is(err, ErrPackageAlreadyExists) {
			pkgInfo, inspectErr := ValidatePackage(tmpPath)
			if inspectErr != nil {
				return "", fmt.Errorf("unpack plugin: %w", err)
			}
			existingDir := filepath.Join(m.cfg.PluginDir, pkgInfo.Name)
			// Only remove if the plugin is NOT currently loaded.
			m.mu.RLock()
			_, loaded := m.plugins[pkgInfo.Name]
			m.mu.RUnlock()
			if loaded {
				return "", fmt.Errorf("plugin %q is currently loaded, unload first", pkgInfo.Name)
			}
			if rmErr := os.RemoveAll(existingDir); rmErr != nil {
				return "", fmt.Errorf("remove existing plugin dir: %w", rmErr)
			}
			info, err = UnpackPlugin(tmpPath, m.cfg.PluginDir)
			if err != nil {
				return "", fmt.Errorf("unpack plugin (retry): %w", err)
			}
		} else {
			return "", fmt.Errorf("unpack plugin: %w", err)
		}
	}

	return filepath.Join(m.cfg.PluginDir, info.Name), nil
}

// downloadAndVerify downloads a file from url to destPath and optionally verifies
// its SHA256 checksum. If checksumSHA256 is empty, no verification is performed.
func downloadAndVerify(ctx context.Context, url, checksumSHA256, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Plekt/"+version.Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}

	hasher := sha256.New()
	// Limit to 100MB.
	limited := io.LimitReader(resp.Body, 100<<20)
	if _, err := io.Copy(f, io.TeeReader(limited, hasher)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if checksumSHA256 != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if got != checksumSHA256 {
			os.Remove(destPath)
			return fmt.Errorf("checksum mismatch: got %s, want %s", got, checksumSHA256)
		}
	}

	return nil
}

func (p *pluginImpl) Close() error {
	p.mu.Lock()
	p.info.Status = PluginStatusUnloading
	p.mu.Unlock()

	var firstErr error
	// Drain and close all runners in the pool.
	// inflight.Wait() must have completed before Close is called so the pool
	// is fully populated (all borrowed runners have been returned).
	if p.pool != nil {
	drainLoop:
		for {
			select {
			case runner := <-p.pool:
				if err := runner.Close(); err != nil && firstErr == nil {
					firstErr = err
				}
			default:
				break drainLoop
			}
		}
	}
	if p.db != nil {
		if err := p.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// GlobalScriptRegistry is the loader-local view of the web-layer registry that
// stores per-plugin global frontend assets. It mirrors the three methods on
// internal/web.GlobalScriptRegistry and is satisfied structurally: defined
// here to avoid an internal/web → internal/loader import cycle.
type GlobalScriptRegistry interface {
	Register(pluginName string, asset FrontendAssets) error
	Unregister(pluginName string)
}

// RegistryEntrySnapshot is the per-plugin metadata the manager enforces
// at Load time, populated by the bootstrap pipeline from registry.json.
type RegistryEntrySnapshot struct {
	PublicKey string
	Official  bool
}

type managerImpl struct {
	cfg           config.Config
	bus           eventbus.EventBus
	mu            sync.RWMutex
	plugins       map[string]*pluginImpl
	subscriptions map[string][]eventbus.Subscription
	plugFac       pluginFactory
	dbFac         dbFactory
	schemaLd      schemaLoader
	migRun        migrationRunner
	registry      PluginRegistryStore
	hostGrants    HostGrantStore
	globalScripts GlobalScriptRegistry

	// Trust snapshot kept under its own mutex so bootstrap can refresh it
	// without contending with the plugins map.
	signingMu     sync.RWMutex
	registrySnap  map[string]RegistryEntrySnapshot
	revokedKeys   map[string]bool
	allowUnsigned bool
}

// SetHostGrantStore wires the operator-controlled host grants store. Safe to
// call once at startup before any Load. Passing nil disables outbound network
// for all plugins (default deny).
func (m *managerImpl) SetHostGrantStore(s HostGrantStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hostGrants = s
}

// SetGlobalScriptRegistry wires the global frontend script registry. Safe to
// call once at startup before any Load. Passing nil disables global frontend
// injection.
func (m *managerImpl) SetGlobalScriptRegistry(r GlobalScriptRegistry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalScripts = r
}

// WireHostGrantStore attaches an operator-controlled HostGrantStore to a
// PluginManager produced by NewManager. No-op if pm is not the concrete
// managerImpl. Call once at startup before any Load.
func WireHostGrantStore(pm PluginManager, s HostGrantStore) {
	if mi, ok := pm.(*managerImpl); ok {
		mi.SetHostGrantStore(s)
	}
}

// WireGlobalScriptRegistry attaches a GlobalScriptRegistry to a PluginManager
// produced by NewManager. No-op if pm is not the concrete managerImpl. Call
// once at startup before any Load.
func WireGlobalScriptRegistry(pm PluginManager, r GlobalScriptRegistry) {
	if mi, ok := pm.(*managerImpl); ok {
		mi.SetGlobalScriptRegistry(r)
	}
}

// SetRegistrySnapshot replaces the trust snapshot. Passing nil clears it.
func (m *managerImpl) SetRegistrySnapshot(snap map[string]RegistryEntrySnapshot) {
	m.signingMu.Lock()
	defer m.signingMu.Unlock()
	if snap == nil {
		m.registrySnap = nil
		return
	}
	cp := make(map[string]RegistryEntrySnapshot, len(snap))
	for k, v := range snap {
		cp[k] = v
	}
	m.registrySnap = cp
}

// SetRevokedKeys replaces the revoked-keys set. Passing nil clears it.
func (m *managerImpl) SetRevokedKeys(revoked map[string]bool) {
	m.signingMu.Lock()
	defer m.signingMu.Unlock()
	if revoked == nil {
		m.revokedKeys = nil
		return
	}
	cp := make(map[string]bool, len(revoked))
	for k, v := range revoked {
		cp[k] = v
	}
	m.revokedKeys = cp
}

// AllowUnsigned toggles the strict-registry check. Production code leaves
// it false; tests flip it on to load fresh builds without a registry.
func (m *managerImpl) AllowUnsigned(allow bool) {
	m.signingMu.Lock()
	defer m.signingMu.Unlock()
	m.allowUnsigned = allow
}

func (m *managerImpl) trustContext(name string) (expectedKey string, official bool, allowUnsigned bool, revoked map[string]bool) {
	m.signingMu.RLock()
	defer m.signingMu.RUnlock()
	allowUnsigned = m.allowUnsigned
	revoked = m.revokedKeys
	if entry, ok := m.registrySnap[name]; ok {
		return entry.PublicKey, entry.Official, allowUnsigned, revoked
	}
	return "", false, allowUnsigned, revoked
}

// WireRegistrySnapshot lets callers outside the loader package update the
// trust snapshot without a type assertion. No-op if pm is not *managerImpl.
func WireRegistrySnapshot(pm PluginManager, snap map[string]RegistryEntrySnapshot) {
	if mi, ok := pm.(*managerImpl); ok {
		mi.SetRegistrySnapshot(snap)
	}
}

// WireRevokedKeys is the package-public counterpart to SetRevokedKeys.
func WireRevokedKeys(pm PluginManager, revoked map[string]bool) {
	if mi, ok := pm.(*managerImpl); ok {
		mi.SetRevokedKeys(revoked)
	}
}

// AllowUnsignedPlugins is the package-public counterpart to AllowUnsigned.
func AllowUnsignedPlugins(pm PluginManager, allow bool) {
	if mi, ok := pm.(*managerImpl); ok {
		mi.AllowUnsigned(allow)
	}
}

// NewManager constructs a PluginManager using the real Extism + SQLite factories.
// cfg.PluginDir is the allowed root for plugin directories.
// registry may be nil; when non-nil, Load/Unload/Reload persist to the registry.
func NewManager(cfg config.Config, bus eventbus.EventBus, registry ...PluginRegistryStore) PluginManager {
	var reg PluginRegistryStore
	if len(registry) > 0 {
		reg = registry[0]
	}
	return newManagerWithDeps(
		cfg,
		bus,
		newExtismPluginFactory(cfg.Loader.WASMMemoryLimitPages),
		&sqliteDBFactory{},
		db.NewSchemaLoader(),
		nil, // migRun created per plugin inside Load()
		reg,
	)
}

// newManagerWithDeps constructs a PluginManager with injectable dependencies.
// Used in tests to inject fake WASM and DB factories.
// migRun may be nil, in which case db.NewMigrationRunner is called per-plugin in Load().
func newManagerWithDeps(cfg config.Config, bus eventbus.EventBus, pf pluginFactory, df dbFactory, sl schemaLoader, mr migrationRunner, registry PluginRegistryStore) PluginManager {
	return &managerImpl{
		cfg:           cfg,
		bus:           bus,
		plugins:       make(map[string]*pluginImpl),
		subscriptions: make(map[string][]eventbus.Subscription),
		plugFac:       pf,
		dbFac:         df,
		schemaLd:      sl,
		migRun:        mr,
		registry:      registry,
		registrySnap:  make(map[string]RegistryEntrySnapshot),
		revokedKeys:   make(map[string]bool),
	}
}

// Load validates the plugin directory, parses the manifest, verifies the
// Ed25519 signature on mcp.yaml, initialises the WASM plugin, opens the
// SQLite database, generates a Bearer token, and emits EventPluginLoaded.
func (m *managerImpl) Load(ctx context.Context, pluginDir string) (PluginInfo, error) {
	// --- Path traversal protection ---
	cleanDir := filepath.Clean(pluginDir)
	allowedRoot := filepath.Clean(m.cfg.PluginDir)
	if !strings.HasPrefix(cleanDir, allowedRoot+string(filepath.Separator)) && cleanDir != allowedRoot {
		return PluginInfo{}, fmt.Errorf("%w: %q is outside plugin root %q",
			ErrPluginDirTraversal, pluginDir, m.cfg.PluginDir)
	}

	// --- Read and parse manifest.json ---
	manifestPath := filepath.Join(cleanDir, "manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return PluginInfo{}, fmt.Errorf("%w: read manifest: %v", ErrManifestInvalid, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return PluginInfo{}, fmt.Errorf("%w: parse manifest: %v", ErrManifestInvalid, err)
	}
	if manifest.Name == "" {
		return PluginInfo{}, fmt.Errorf("%w: manifest.name is required", ErrManifestInvalid)
	}
	if manifest.Name == "core" {
		return PluginInfo{}, fmt.Errorf("%w: plugin name %q is reserved and cannot be installed", ErrManifestInvalid, manifest.Name)
	}

	// --- Validate event declarations in manifest ---
	if err := ValidateManifestEvents(manifest.Events); err != nil {
		return PluginInfo{}, fmt.Errorf("%w: %v", ErrManifestInvalid, err)
	}

	// --- Check core version compatibility ---
	if manifest.MinCoreVersion != "" {
		ok, verErr := version.AtLeast(version.Version, manifest.MinCoreVersion)
		if verErr != nil {
			return PluginInfo{}, fmt.Errorf("%w: invalid min_core_version %q: %v",
				ErrManifestInvalid, manifest.MinCoreVersion, verErr)
		}
		if !ok {
			return PluginInfo{}, fmt.Errorf("%w: plugin %q requires core %s, running %s",
				ErrCoreVersionIncompatible, manifest.Name, manifest.MinCoreVersion, version.Version)
		}
	}

	// --- Check for duplicate load and validate dependencies ---
	m.mu.RLock()
	_, exists := m.plugins[manifest.Name]
	// Hard dependencies must be loaded with a compatible version.
	var missingDep string
	var versionMismatchDep string
	var versionMismatchDetail string
	for dep, constraint := range manifest.Dependencies {
		loaded, ok := m.plugins[dep]
		if !ok {
			missingDep = dep
			break
		}
		if constraint != "" {
			if compatible, _ := version.AtLeast(loaded.info.Version, constraint); !compatible {
				versionMismatchDep = dep
				versionMismatchDetail = fmt.Sprintf("have %s, need %s", loaded.info.Version, constraint)
				break
			}
		}
	}
	// Optional dependencies: note which are absent or version-mismatched.
	var missingOptional []string
	for dep, constraint := range manifest.OptionalDependencies {
		loaded, ok := m.plugins[dep]
		if !ok {
			missingOptional = append(missingOptional, dep)
			continue
		}
		if constraint != "" {
			if compatible, _ := version.AtLeast(loaded.info.Version, constraint); !compatible {
				slog.Warn("optional dependency version mismatch",
					"plugin", manifest.Name, "dependency", dep,
					"have", loaded.info.Version, "need", constraint)
			}
		}
	}
	m.mu.RUnlock()

	if exists {
		return PluginInfo{}, fmt.Errorf("%w: %q", ErrPluginAlreadyLoaded, manifest.Name)
	}
	if missingDep != "" {
		return PluginInfo{}, fmt.Errorf("%w: plugin %q requires %q to be loaded first",
			ErrDependencyNotLoaded, manifest.Name, missingDep)
	}
	if versionMismatchDep != "" {
		return PluginInfo{}, fmt.Errorf("%w: plugin %q dependency %q: %s",
			ErrDependencyVersionMismatch, manifest.Name, versionMismatchDep, versionMismatchDetail)
	}
	for _, dep := range missingOptional {
		slog.Info("optional dependency not loaded", "plugin", manifest.Name, "dependency", dep)
	}

	// --- Read mcp.yaml and verify Ed25519 signature ---
	mcpYAMLPath := filepath.Join(cleanDir, "mcp.yaml")
	rawMCPYAML, err := os.ReadFile(mcpYAMLPath)
	if err != nil {
		return PluginInfo{}, fmt.Errorf("%w: read mcp.yaml: %v", ErrManifestInvalid, err)
	}

	// Parse signature block from mcp.yaml.
	var mcpDoc struct {
		Signature MCPSignature `yaml:"signature"`
	}
	if err := parseYAML(rawMCPYAML, &mcpDoc); err != nil {
		return PluginInfo{}, fmt.Errorf("%w: parse mcp.yaml: %v", ErrManifestInvalid, err)
	}

	// Verify signature against the trust snapshot before any WASM init.
	expectedPubKey, official, allowUnsigned, revoked := m.trustContext(manifest.Name)
	if expectedPubKey == "" {
		if !allowUnsigned {
			return PluginInfo{}, fmt.Errorf("%w: %q", ErrPluginNotInRegistry, manifest.Name)
		}
		slog.Warn("loading plugin missing from registry snapshot (allowUnsigned=true)",
			"plugin", manifest.Name)
	} else {
		if err := VerifyMCPSignature(
			expectedPubKey,
			mcpDoc.Signature.PublicKey,
			mcpDoc.Signature.Signature,
			rawMCPYAML,
			revoked,
		); err != nil {
			return PluginInfo{}, err
		}
	}

	// --- Open per-plugin SQLite DB (no CGO) ---
	dbDir := filepath.Join(m.cfg.DataDir, "plugins", manifest.Name)
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		return PluginInfo{}, fmt.Errorf("create plugin data dir: %w", err)
	}
	dbPath := filepath.Join(dbDir, "data.db")
	pluginDB, err := m.dbFac.Open(dbPath)
	if err != nil {
		m.emitPluginError(ctx, manifest.Name, "db open failed")
		return PluginInfo{}, fmt.Errorf("open plugin DB: %w", err)
	}
	if err := pluginDB.PingContext(ctx); err != nil {
		_ = pluginDB.Close()
		m.emitPluginError(ctx, manifest.Name, "db open failed")
		return PluginInfo{}, fmt.Errorf("ping plugin DB: %w", err)
	}

	// --- Load schema.yaml and run migrations ---
	schema, schemaErr := m.schemaLd.Load(cleanDir)
	if schemaErr != nil {
		if !errors.Is(schemaErr, db.ErrSchemaFileNotFound) {
			// Schema file present but invalid.
			_ = pluginDB.Close()
			return PluginInfo{}, fmt.Errorf("%w: %w", ErrMigration, schemaErr)
		}
		// Schema file absent: skip migration entirely.
	} else {
		// Schema loaded: determine which migration runner to use.
		mr := m.migRun
		if mr == nil {
			mr = db.NewMigrationRunner(manifest.Name)
		}
		if migErr := mr.Migrate(ctx, pluginDB, schema); migErr != nil {
			m.bus.Emit(ctx, eventbus.Event{
				Name:         eventbus.EventPluginMigrationFailed,
				SourcePlugin: manifest.Name,
				Payload: eventbus.PluginMigrationFailedPayload{
					PluginName: manifest.Name,
					Error:      "migration failed", // sanitized: never raw SQL error
				},
			})
			m.emitPluginError(ctx, manifest.Name, "migration failed")
			_ = pluginDB.Close()
			return PluginInfo{}, ErrMigration
		}
		// Count tables and indexes for the success event.
		tablesApplied := len(schema.Tables)
		indexesApplied := 0
		for _, tbl := range schema.Tables {
			indexesApplied += len(tbl.Indexes)
		}
		m.bus.Emit(ctx, eventbus.Event{
			Name:         eventbus.EventPluginSchemaMigrated,
			SourcePlugin: manifest.Name,
			Payload: eventbus.PluginSchemaMigratedPayload{
				PluginName:     manifest.Name,
				TablesApplied:  tablesApplied,
				IndexesApplied: indexesApplied,
			},
		})
	}

	// --- Initialise WASM plugin via factory (after DB + migration) ---
	// Build per-plugin call context for host functions.
	// AllowedEmits is the validated whitelist from manifest.
	// Resolve granted outbound hosts from the operator-controlled store.
	// Empty slice (or nil store) → default deny via Extism AllowedHosts.
	var grantedHosts []string
	if m.hostGrants != nil {
		grants, grantErr := m.hostGrants.List(ctx, manifest.Name)
		if grantErr != nil {
			slog.Warn("host grants lookup failed",
				"plugin", manifest.Name, "error", grantErr)
		} else {
			grantedHosts = make([]string, 0, len(grants))
			for _, g := range grants {
				grantedHosts = append(grantedHosts, g.Host)
			}
		}
	}

	loadPCC := PluginCallContext{
		PluginName:        manifest.Name,
		DB:                pluginDB,
		Bus:               m.bus,
		AllowedEmits:      append([]string(nil), manifest.Events.Emits...),
		AllowedSubscribes: append([]string(nil), manifest.Events.Subscribes...),
		LoadedPlugins: func() []string {
			m.mu.RLock()
			defer m.mu.RUnlock()
			names := make([]string, 0, len(m.plugins))
			for name := range m.plugins {
				names = append(names, name)
			}
			return names
		},
	}
	wasmPath := filepath.Join(cleanDir, "plugin.wasm")

	// Create a pool of WASM runner instances to allow concurrent calls.
	// Each Extism/wazero instance is NOT goroutine-safe, so we use one instance
	// per slot. Pool size is capped at 4 to avoid excessive memory use.
	poolSize := runtime.NumCPU()
	if poolSize > 4 {
		poolSize = 4
	}
	pool := make(chan pluginRunner, poolSize)
	for i := 0; i < poolSize; i++ {
		r, err := m.plugFac.New(wasmPath, nil, m.cfg.Loader.WASMMemoryLimitPages, loadPCC, grantedHosts)
		if err != nil {
			// Close any already-created runners before returning.
			close(pool)
			for existing := range pool {
				_ = existing.Close()
			}
			_ = pluginDB.Close()
			m.emitPluginError(ctx, manifest.Name, "wasm init failed")
			return PluginInfo{}, fmt.Errorf("%w: %v", ErrWASMInit, err)
		}
		pool <- r
	}

	// Store a validated copy of Emits for later PluginCallContext construction.
	allowedEmits := make([]string, len(manifest.Events.Emits))
	copy(allowedEmits, manifest.Events.Emits)

	// Store a validated copy of MCP tools and resources for later metadata retrieval.
	mcpTools := make([]MCPTool, len(manifest.MCP.Tools))
	copy(mcpTools, manifest.MCP.Tools)
	mcpResources := make([]MCPResource, len(manifest.MCP.Resources))
	copy(mcpResources, manifest.MCP.Resources)

	impl := &pluginImpl{
		info: PluginInfo{
			Name:      manifest.Name,
			Version:   manifest.Version,
			Status:    PluginStatusActive,
			Dir:       cleanDir,
			PublicKey: expectedPubKey,
			Official:  official,
		},
		pool:         pool,
		db:           pluginDB,
		allowedEmits: allowedEmits,
		mcpTools:     mcpTools,
		mcpResources: mcpResources,
	}

	m.mu.Lock()
	// Double-check after acquiring write lock.
	if _, exists := m.plugins[manifest.Name]; exists {
		m.mu.Unlock()
		_ = impl.Close()
		return PluginInfo{}, fmt.Errorf("%w: %q", ErrPluginAlreadyLoaded, manifest.Name)
	}
	m.plugins[manifest.Name] = impl
	m.mu.Unlock()

	// Register the plugin's optional global frontend script. Failure here is
	// non-fatal: the plugin still loads, the operator just won't see the
	// global asset injected.
	if m.globalScripts != nil && manifest.UI.GlobalFrontend != nil &&
		manifest.UI.GlobalFrontend.JSFile != "" {
		if regErr := m.globalScripts.Register(manifest.Name, *manifest.UI.GlobalFrontend); regErr != nil {
			slog.Warn("global frontend register failed",
				"plugin", manifest.Name, "error", regErr)
		} else {
			slog.Info("global frontend registered",
				"plugin", manifest.Name,
				"js", manifest.UI.GlobalFrontend.JSFile,
				"css", manifest.UI.GlobalFrontend.CSSFile)
		}
	} else if manifest.UI.GlobalFrontend != nil {
		slog.Warn("global frontend declared but not registered",
			"plugin", manifest.Name,
			"globalScriptsNil", m.globalScripts == nil,
			"jsFile", manifest.UI.GlobalFrontend.JSFile)
	}

	// Wire host-side bus subscriptions for events the plugin subscribes to.
	m.wirePluginSubscriptions(ctx, manifest.Name, manifest.Events.Subscribes)

	// Persist to registry if configured.
	if m.registry != nil {
		now := time.Now().UTC()
		if regErr := m.registry.Upsert(ctx, PluginRegistryEntry{
			Name:      manifest.Name,
			Dir:       cleanDir,
			Version:   manifest.Version,
			LoadedAt:  now,
			UpdatedAt: now,
		}); regErr != nil {
			slog.Warn("failed to persist plugin to registry",
				"plugin", manifest.Name, "error", regErr)
		}
	}

	// Emit plugin.loaded.
	m.bus.Emit(ctx, eventbus.Event{
		Name:         eventbus.EventPluginLoaded,
		SourcePlugin: manifest.Name,
		Payload: eventbus.PluginLoadedPayload{
			Name:    manifest.Name,
			Version: manifest.Version,
			Dir:     cleanDir,
		},
	})

	return impl.Info(), nil
}

// Unload gracefully shuts down the named plugin.
func (m *managerImpl) Unload(ctx context.Context, name string) error {
	m.mu.Lock()
	impl, ok := m.plugins[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrPluginNotFound, name)
	}
	// Mark as unloading BEFORE removing from map to prevent new calls.
	impl.mu.Lock()
	impl.info.Status = PluginStatusUnloading
	impl.mu.Unlock()
	delete(m.plugins, name)
	m.mu.Unlock()

	// Signal in-flight calls that we're unloading.
	m.bus.Emit(ctx, eventbus.Event{
		Name:         eventbus.EventPluginUnloading,
		SourcePlugin: name,
		Payload:      eventbus.PluginUnloadingPayload{Name: name},
	})

	// Drain in-flight calls within the configured timeout.
	drainTimeout := m.cfg.Loader.ReloadDrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = 5 * time.Second
	}
	done := make(chan struct{})
	go func() {
		impl.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(drainTimeout):
		// Continue with close even if some calls are still running.
	}

	// Remove all bus subscriptions for this plugin before closing.
	m.unwirePluginSubscriptions(name)

	// Drop any registered global frontend script for this plugin.
	if m.globalScripts != nil {
		m.globalScripts.Unregister(name)
	}

	if err := impl.Close(); err != nil {
		return fmt.Errorf("close plugin %q: %w", name, err)
	}

	// Remove from registry if configured.
	if m.registry != nil {
		if regErr := m.registry.Delete(ctx, name); regErr != nil {
			slog.Warn("failed to remove plugin from registry",
				"plugin", name, "error", regErr)
		}
	}

	m.bus.Emit(ctx, eventbus.Event{
		Name:         eventbus.EventPluginUnloaded,
		SourcePlugin: name,
		Payload:      eventbus.PluginUnloadedPayload{Name: name},
	})
	return nil
}

// Reload performs Unload followed by Load from the same directory.
func (m *managerImpl) Reload(ctx context.Context, name string) (PluginInfo, error) {
	m.mu.RLock()
	impl, ok := m.plugins[name]
	m.mu.RUnlock()
	if !ok {
		return PluginInfo{}, fmt.Errorf("%w: %q", ErrPluginNotFound, name)
	}
	dir := impl.Info().Dir

	if err := m.Unload(ctx, name); err != nil {
		return PluginInfo{}, fmt.Errorf("reload unload phase: %w", err)
	}

	info, err := m.Load(ctx, dir)
	if err != nil {
		return PluginInfo{}, fmt.Errorf("reload load phase: %w", err)
	}

	m.bus.Emit(ctx, eventbus.Event{
		Name:         eventbus.EventPluginReloaded,
		SourcePlugin: name,
		Payload: eventbus.PluginReloadedPayload{
			Name:    info.Name,
			Version: info.Version,
			Dir:     info.Dir,
		},
	})

	return info, nil
}

// Get returns the Plugin for the given name.
func (m *managerImpl) Get(name string) (Plugin, error) {
	m.mu.RLock()
	impl, ok := m.plugins[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrPluginNotFound, name)
	}
	return impl, nil
}

// List returns PluginInfo for all loaded plugins.
func (m *managerImpl) List() []PluginInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PluginInfo, 0, len(m.plugins))
	for _, impl := range m.plugins {
		result = append(result, impl.Info())
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

// GetMCPMeta returns a safe copy of the MCP tool/resource metadata for the named plugin.
func (m *managerImpl) GetMCPMeta(name string) (PluginMCPMeta, error) {
	m.mu.RLock()
	impl, ok := m.plugins[name]
	m.mu.RUnlock()
	if !ok {
		return PluginMCPMeta{}, ErrPluginNotFound
	}
	impl.mu.Lock()
	defer impl.mu.Unlock()
	tools := make([]MCPTool, len(impl.mcpTools))
	copy(tools, impl.mcpTools)
	resources := make([]MCPResource, len(impl.mcpResources))
	copy(resources, impl.mcpResources)
	return PluginMCPMeta{
		PluginName: name,
		Tools:      tools,
		Resources:  resources,
	}, nil
}

// CallPlugin dispatches a WASM function call to the named plugin.
func (m *managerImpl) CallPlugin(ctx context.Context, name, function string, input []byte) ([]byte, error) {
	p, err := m.Get(name)
	if err != nil {
		return nil, err
	}
	return p.Call(ctx, function, input)
}

// GetManifest returns the parsed Manifest for the named plugin by reading
// manifest.json from the plugin's directory. Returns ErrPluginNotFound if
// the plugin is not loaded.
func (m *managerImpl) GetManifest(name string) (Manifest, error) {
	m.mu.RLock()
	impl, ok := m.plugins[name]
	m.mu.RUnlock()
	if !ok {
		return Manifest{}, fmt.Errorf("%w: %q", ErrPluginNotFound, name)
	}

	manifestPath := filepath.Join(impl.Info().Dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: read manifest for %q: %v", ErrManifestInvalid, name, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: parse manifest for %q: %v", ErrManifestInvalid, name, err)
	}
	return manifest, nil
}

// emitPluginError emits plugin.error for trusted plugins only.
// Must only be called after VerifyMCPSignature has returned nil for this plugin.
func (m *managerImpl) emitPluginError(ctx context.Context, pluginName string, reason string) {
	m.bus.Emit(ctx, eventbus.Event{
		Name:         eventbus.EventPluginError,
		SourcePlugin: pluginName,
		Payload: eventbus.PluginErrorPayload{
			Name:  pluginName,
			Error: reason,
		},
	})
}

// wirePluginSubscriptions creates host-side bus subscriptions for each event
// name in subscribes (from manifest.Events.Subscribes). Each handler calls
// the plugin's WASM "on_event" export with the serialised event.
// Subscriptions are stored in m.subscriptions for later cleanup.
func (m *managerImpl) wirePluginSubscriptions(ctx context.Context, pluginName string, subscribes []string) {
	if len(subscribes) == 0 {
		return
	}
	subs := make([]eventbus.Subscription, 0, len(subscribes))
	for _, eventName := range subscribes {
		// Block plugins from subscribing to system event prefixes.
		if isSystemEvent(eventName) {
			slog.Warn("plugin tried to subscribe to system event: blocked",
				"plugin", pluginName, "event", eventName)
			continue
		}
		eventName := eventName // capture for closure
		sub := m.bus.Subscribe(eventName, func(ctx context.Context, e eventbus.Event) {
			// Retrieve plugin at handler invocation time (not at wiring time)
			// so we never hold a stale reference to an unloaded plugin.
			info, err := m.Get(pluginName)
			if err != nil || info.Info().Status != PluginStatusActive {
				return // plugin no longer active, skip delivery
			}
			// Call the plugin's on_event WASM export.
			// Errors are swallowed: handler panics are recovered by the bus safeCall.
			_, _ = m.CallPlugin(ctx, pluginName, "on_event", marshalEvent(e))
		})
		subs = append(subs, sub)
	}
	m.mu.Lock()
	m.subscriptions[pluginName] = append(m.subscriptions[pluginName], subs...)
	m.mu.Unlock()
}

// marshalEvent serialises an eventbus.Event to JSON for WASM delivery.
// Returns nil on marshal error (WASM will receive empty input).
func marshalEvent(e eventbus.Event) []byte {
	data, _ := json.Marshal(e)
	return data
}

// unwirePluginSubscriptions removes all bus subscriptions for pluginName.
func (m *managerImpl) unwirePluginSubscriptions(pluginName string) {
	m.mu.Lock()
	subs := m.subscriptions[pluginName]
	for _, sub := range subs {
		m.bus.Unsubscribe(sub)
	}
	delete(m.subscriptions, pluginName)
	m.mu.Unlock()
}

// Shutdown gracefully closes all loaded plugins without removing them from the
// registry. Call this on server shutdown instead of Unload so that plugins are
// automatically restored by RestoreFromRegistry (or auto_load_on_startup) on
// the next startup. The first close error encountered is returned; all plugins
// are attempted regardless.
func (m *managerImpl) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	names := make([]string, 0, len(m.plugins))
	for name := range m.plugins {
		names = append(names, name)
	}
	m.mu.Unlock()

	var firstErr error
	for _, name := range names {
		m.mu.Lock()
		impl, ok := m.plugins[name]
		if !ok {
			m.mu.Unlock()
			continue
		}
		impl.mu.Lock()
		impl.info.Status = PluginStatusUnloading
		impl.mu.Unlock()
		delete(m.plugins, name)
		m.mu.Unlock()

		m.bus.Emit(ctx, eventbus.Event{
			Name:         eventbus.EventPluginUnloading,
			SourcePlugin: name,
			Payload:      eventbus.PluginUnloadingPayload{Name: name},
		})

		// Drain in-flight calls within the configured timeout.
		drainTimeout := m.cfg.Loader.ReloadDrainTimeout
		if drainTimeout <= 0 {
			drainTimeout = 5 * time.Second
		}
		done := make(chan struct{})
		go func() {
			impl.inflight.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(drainTimeout):
		}

		m.unwirePluginSubscriptions(name)
		if m.globalScripts != nil {
			m.globalScripts.Unregister(name)
		}

		if err := impl.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		// Registry entry is intentionally NOT deleted: plugins will be
		// restored on next startup via RestoreFromRegistry.
	}
	return firstErr
}

// ScanDir scans the configured PluginDir for subdirectories and returns a
// DiscoveredPlugin for each one. It does NOT load plugins or mutate state.
// Safe to call concurrently.
func (m *managerImpl) ScanDir(ctx context.Context) ([]DiscoveredPlugin, error) {
	allowedRoot := filepath.Clean(m.cfg.PluginDir)

	entries, err := os.ReadDir(allowedRoot)
	if err != nil {
		return nil, fmt.Errorf("scan plugin dir %q: %w", allowedRoot, err)
	}

	now := time.Now().UTC()
	var results []DiscoveredPlugin

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirPath := filepath.Join(allowedRoot, entry.Name())

		// Path safety: verify the resolved path is within the allowed root.
		cleanDir := filepath.Clean(dirPath)
		if !strings.HasPrefix(cleanDir, allowedRoot+string(filepath.Separator)) && cleanDir != allowedRoot {
			continue
		}

		dp := DiscoveredPlugin{
			Dir:       cleanDir,
			ScannedAt: now,
		}

		manifestPath := filepath.Join(cleanDir, "manifest.json")
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			// manifest.json missing or unreadable: mark invalid, still include.
			dp.ManifestValid = false
			results = append(results, dp)
			continue
		}

		var manifest Manifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			dp.ManifestValid = false
			results = append(results, dp)
			continue
		}
		if manifest.Name == "" {
			dp.ManifestValid = false
			results = append(results, dp)
			continue
		}

		dp.Name = manifest.Name
		dp.Version = manifest.Version
		dp.Description = manifest.Description
		dp.OptionalDependencies = manifest.OptionalDependencies
		dp.ManifestValid = true
		results = append(results, dp)
	}

	return results, nil
}

// parseYAML is a thin wrapper over yaml.v3 to avoid scattering the import.
func parseYAML(data []byte, out interface{}) error {
	// Import is via gopkg.in/yaml.v3; the function avoids a top-level import cycle.
	return unmarshalYAML(data, out)
}
