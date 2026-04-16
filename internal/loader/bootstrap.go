package loader

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/registry"
)

// loadLevel is a set of plugins that can be loaded in parallel because all
// of their dependencies have already been satisfied by a prior level.
type loadLevel []PluginRegistryEntry

// levelResult holds the outcome of loading a single plugin within a level.
type levelResult struct {
	entry   PluginRegistryEntry
	loadErr error
}

// buildLoadLevels performs a topological sort (Kahn's algorithm) over the
// given entries using the dependency information in manifests.
//
// Rules:
//   - Optional dependencies that are present in entries count for ordering
//     exactly like hard dependencies (same in-degree contribution).
//   - Optional dependencies absent from entries are silently ignored.
//   - Hard dependencies absent from entries return ErrBootstrapMissingHardDep.
//   - A cycle among entries returns ErrBootstrapCycle.
//   - Within each level, entries preserve their original registry order.
func buildLoadLevels(entries []PluginRegistryEntry, manifests map[string]manifestDeps) ([]loadLevel, error) {
	// Index entries by name so we can look them up quickly.
	entryIndex := make(map[string]int, len(entries))
	for i, e := range entries {
		entryIndex[e.Name] = i
	}

	// Validate hard dependencies and compute in-degrees.
	inDegree := make(map[string]int, len(entries))
	for _, e := range entries {
		inDegree[e.Name] = 0
	}

	// dependents[dep] = list of plugin names that depend on dep.
	dependents := make(map[string][]string, len(entries))

	for _, e := range entries {
		md, ok := manifests[e.Name]
		if !ok {
			// No manifest entry means no dependencies.
			continue
		}

		// Deduplicate dep names per plugin. A manifest listing the same dep twice
		// would otherwise double-increment inDegree and append a duplicate entry
		// to dependents[dep], corrupting level boundaries.
		seenDep := make(map[string]bool, len(md.Dependencies)+len(md.OptionalDependencies))

		// Hard dependencies: must exist.
		for dep := range md.Dependencies {
			if _, present := entryIndex[dep]; !present {
				return nil, fmt.Errorf("%w: plugin %q requires %q", ErrBootstrapMissingHardDep, e.Name, dep)
			}
			if seenDep[dep] {
				continue
			}
			seenDep[dep] = true
			inDegree[e.Name]++
			dependents[dep] = append(dependents[dep], e.Name)
		}

		// Optional dependencies: contribute to ordering only when present.
		for dep := range md.OptionalDependencies {
			if _, present := entryIndex[dep]; !present {
				// Silently ignored per spec.
				continue
			}
			if seenDep[dep] {
				continue
			}
			seenDep[dep] = true
			inDegree[e.Name]++
			dependents[dep] = append(dependents[dep], e.Name)
		}
	}

	// Initialize queue with all zero-in-degree entries, preserving registry order.
	var queue []PluginRegistryEntry
	for _, e := range entries {
		if inDegree[e.Name] == 0 {
			queue = append(queue, e)
		}
	}

	var levels []loadLevel
	visited := 0

	for len(queue) > 0 {
		// The current queue IS the current level.
		currentLevel := make(loadLevel, len(queue))
		copy(currentLevel, queue)
		levels = append(levels, currentLevel)
		visited += len(currentLevel)

		// Collect next level candidates while preserving registry order.
		// Use a set to avoid duplicates, then emit in registry order.
		nextSet := make(map[string]bool)
		for _, e := range currentLevel {
			for _, dep := range dependents[e.Name] {
				inDegree[dep]--
				if inDegree[dep] == 0 {
					nextSet[dep] = true
				}
			}
		}

		queue = queue[:0]
		for _, e := range entries {
			if nextSet[e.Name] {
				queue = append(queue, e)
			}
		}
	}

	if visited < len(entries) {
		return nil, ErrBootstrapCycle
	}

	return levels, nil
}

// loadLevelParallel loads all plugins in a level concurrently using an errgroup
// bounded by maxConcurrency. Individual plugin load failures are captured in the
// result slice and do NOT cancel sibling goroutines. Context cancellation aborts
// unstarted work and is propagated as the group error.
//
// The returned slice is indexed identically to level (order preserved).
func loadLevelParallel(ctx context.Context, manager PluginManager, level loadLevel, maxConcurrency int) ([]levelResult, error) {
	results := make([]levelResult, len(level))

	g := new(errgroup.Group)
	g.SetLimit(maxConcurrency)

	for i, entry := range level {
		i, entry := i, entry // capture loop vars

		g.Go(func() error {
			// Abort unstarted work if the caller's context is already done.
			if err := ctx.Err(); err != nil {
				results[i] = levelResult{entry: entry, loadErr: err}
				// Return the ctx error so errgroup propagates it through g.Wait().
				return err
			}

			_, loadErr := manager.Load(ctx, entry.Dir)
			results[i] = levelResult{entry: entry, loadErr: loadErr}

			// Always return nil so that sibling goroutines are not cancelled
			// by individual plugin load failures.
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		// err here is only from ctx cancellation (the only non-nil returns above).
		return results, err
	}

	// Also surface any ctx error that occurred while goroutines were running
	// (e.g. ctx cancelled while Load was blocking).
	return results, ctx.Err()
}

// logLevelTiming records timing information for a single load level.
func logLevelTiming(levelIdx, entries int, duration time.Duration, restored, failed int) {
	slog.Info("bootstrap level complete",
		"level", levelIdx,
		"entries", entries,
		"restored", restored,
		"failed", failed,
		"duration_ms", duration.Milliseconds(),
	)
}

// logBootstrapTiming records the overall bootstrap timing summary.
func logBootstrapTiming(total time.Duration, levels int, restored, failed int) {
	slog.Info("bootstrap complete",
		"levels", levels,
		"restored", restored,
		"failed", failed,
		"total_ms", total.Milliseconds(),
	)
}

// RestoreFromRegistry loads plugins that are stored in the registry back into the manager.
//
// Phase 1 – directory validation:
//   - For each registry entry, stat the directory. If missing, delete from registry,
//     increment Failed, and skip.
//
// Phase 2 – manifest pre-read:
//   - For each surviving entry, read its manifest.json (name + deps only).
//     If reading fails, increment Failed and skip.
//
// Phase 3 – topological sort:
//   - Build load levels via Kahn's algorithm.
//   - If a cycle or missing hard-dep is detected, return that error immediately
//     along with a payload reflecting all surviving entries as Failed.
//
// Phase 4 – parallel level execution:
//   - Load each level in parallel (bounded by runtime.NumCPU()).
//   - Context cancellation propagates and halts further levels.
//   - Individual load failures increment Failed without stopping siblings.
//
// Returns ErrRegistryListFailed (wrapping the underlying error) if List fails.
func RestoreFromRegistry(
	ctx context.Context,
	manager PluginManager,
	store PluginRegistryStore,
) (eventbus.PluginRegistryRestoredPayload, error) {
	bootstrapStart := time.Now()

	entries, err := store.List(ctx)
	if err != nil {
		return eventbus.PluginRegistryRestoredPayload{}, err
	}

	var payload eventbus.PluginRegistryRestoredPayload

	// Phase 1: directory validation.
	surviving := make([]PluginRegistryEntry, 0, len(entries))
	for _, entry := range entries {
		if _, statErr := os.Stat(entry.Dir); errors.Is(statErr, fs.ErrNotExist) {
			slog.Warn("plugin directory missing, removing from registry",
				"plugin", entry.Name, "dir", entry.Dir)
			if delErr := store.Delete(ctx, entry.Name); delErr != nil {
				slog.Error("failed to delete missing plugin from registry",
					"plugin", entry.Name, "error", delErr)
			}
			payload.Failed++
			continue
		}
		surviving = append(surviving, entry)
	}

	// Phase 2: manifest pre-read.
	manifestsMap := make(map[string]manifestDeps, len(surviving))
	validEntries := make([]PluginRegistryEntry, 0, len(surviving))
	for _, entry := range surviving {
		md, mdErr := readManifestDeps(entry.Dir)
		if mdErr != nil {
			slog.Warn("failed to read manifest deps during bootstrap",
				"plugin", entry.Name, "dir", entry.Dir, "error", mdErr)
			payload.Failed++
			continue
		}
		manifestsMap[entry.Name] = md
		validEntries = append(validEntries, entry)
	}

	// Phase 3: topological sort.
	levels, topoErr := buildLoadLevels(validEntries, manifestsMap)
	if topoErr != nil {
		// Cycle or missing hard dep: treat all valid entries as failed.
		payload.Failed += len(validEntries)
		return payload, topoErr
	}

	// Phase 4: parallel level execution.
	maxConcurrency := runtime.NumCPU()
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}

	for levelIdx, level := range levels {
		levelStart := time.Now()

		results, levelErr := loadLevelParallel(ctx, manager, level, maxConcurrency)

		var restoredInLevel, failedInLevel int
		for _, r := range results {
			if r.loadErr != nil {
				slog.Warn("failed to restore plugin from registry",
					"plugin", r.entry.Name, "dir", r.entry.Dir, "error", r.loadErr)
				payload.Failed++
				failedInLevel++
			} else {
				payload.Restored++
				restoredInLevel++
			}
		}

		logLevelTiming(levelIdx, len(level), time.Since(levelStart), restoredInLevel, failedInLevel)

		if levelErr != nil {
			// Context was cancelled; propagate after recording what we have.
			return payload, levelErr
		}
	}

	logBootstrapTiming(time.Since(bootstrapStart), len(levels), payload.Restored, payload.Failed)
	return payload, nil
}

// IsFirstRun returns true if the plugin directory is empty or does not exist,
// indicating that default plugins should be seeded.
func IsFirstRun(pluginDir string) bool {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return true // directory doesn't exist or unreadable → first run
	}
	// Check if any entry is a directory (plugin directories)
	for _, e := range entries {
		if e.IsDir() {
			return false
		}
	}
	return true
}

// SeedDefaultPlugins downloads and installs default plugins from the registry
// on first run. It fetches registry.json, finds each requested plugin, downloads
// its .mcpkg archive, and unpacks it into pluginDir/{name}/.
//
// Individual plugin failures are logged as warnings but do not stop the seeding
// of remaining plugins. Returns the count of successfully installed plugins.
func SeedDefaultPlugins(
	ctx context.Context,
	pluginDir string,
	client registry.RegistryClient,
	defaultPlugins []string,
) int {
	if len(defaultPlugins) == 0 {
		return 0
	}

	slog.Info("first run detected, seeding default plugins",
		"count", len(defaultPlugins),
		"plugin_dir", pluginDir,
	)

	// Ensure plugin directory exists.
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		slog.Error("failed to create plugin directory", "dir", pluginDir, "error", err)
		return 0
	}

	// Pre-check: try to reach the registry before iterating all plugins.
	// If the registry is unreachable, log once and skip seeding instead of
	// spamming a warning per plugin.
	if _, probeErr := client.FetchRegistry(ctx); probeErr != nil {
		slog.Warn("plugin registry unreachable, skipping default plugin seeding",
			"error", probeErr)
		return 0
	}

	installed := 0
	for _, name := range defaultPlugins {
		if ctx.Err() != nil {
			slog.Warn("context cancelled, stopping default plugin seeding")
			break
		}

		_, pv, err := client.FindCompatibleVersion(ctx, name)
		if err != nil {
			slog.Warn("default plugin not found or incompatible, skipping",
				"plugin", name, "error", err)
			continue
		}

		slog.Info("installing default plugin",
			"plugin", name,
			"version", pv.Version,
		)

		// Download .mcpkg to temp file.
		tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("plekt-seed-%s-%d.mcpkg", name, time.Now().UnixNano()))
		if dlErr := client.DownloadPlugin(ctx, pv, tmpPath); dlErr != nil {
			slog.Warn("failed to download default plugin, skipping",
				"plugin", name, "error", dlErr)
			continue
		}

		// Unpack into plugin directory.
		if _, unpackErr := UnpackPlugin(tmpPath, pluginDir); unpackErr != nil {
			slog.Warn("failed to unpack default plugin, skipping",
				"plugin", name, "error", unpackErr)
			_ = os.Remove(tmpPath)
			continue
		}

		// Clean up temp file.
		_ = os.Remove(tmpPath)

		installed++
		slog.Info("default plugin installed successfully",
			"plugin", name,
			"version", pv.Version,
		)
	}

	slog.Info("default plugin seeding complete",
		"requested", len(defaultPlugins),
		"installed", installed,
	)

	return installed
}

// BootstrapConfig controls the unified plugin bootstrap pipeline.
type BootstrapConfig struct {
	PluginDir      string
	DefaultPlugins []string
	RegistryURL    string
	AutoLoad       bool
}

// BootstrapResult summarises what the bootstrap pipeline did.
type BootstrapResult struct {
	Seeded         int
	RestorePayload eventbus.PluginRegistryRestoredPayload
	AutoLoaded     int
	Failed         int
}

// BootstrapPlugins installs the registry trust snapshot, seeds default
// plugins on first run, restores previously-loaded plugins, and optionally
// auto-loads everything it finds on disk.
func BootstrapPlugins(
	ctx context.Context,
	manager PluginManager,
	store PluginRegistryStore,
	cfg BootstrapConfig,
) BootstrapResult {
	var result BootstrapResult

	if cfg.RegistryURL != "" {
		regClient := registry.NewHTTPRegistryClient(cfg.RegistryURL)
		installTrustSnapshot(ctx, manager, regClient)
		if IsFirstRun(cfg.PluginDir) && len(cfg.DefaultPlugins) > 0 {
			result.Seeded = SeedDefaultPlugins(ctx, cfg.PluginDir, regClient, cfg.DefaultPlugins)
		}
	}

	restorePayload, restoreErr := RestoreFromRegistry(ctx, manager, store)
	if restoreErr != nil {
		slog.Warn("registry restore failed", "error", restoreErr)
	} else {
		slog.Info("registry restore complete",
			"restored", restorePayload.Restored, "failed", restorePayload.Failed)
	}
	result.RestorePayload = restorePayload

	if !cfg.AutoLoad {
		return result
	}
	discovered, scanErr := manager.ScanDir(ctx)
	if scanErr != nil {
		slog.Warn("auto-load scan failed", "error", scanErr)
		return result
	}

	loaded := make(map[string]struct{})
	for _, info := range manager.List() {
		loaded[filepath.Clean(info.Dir)] = struct{}{}
	}

	var pending []DiscoveredPlugin
	for _, dp := range discovered {
		if _, ok := loaded[filepath.Clean(dp.Dir)]; !ok {
			pending = append(pending, dp)
		}
	}

	for len(pending) > 0 {
		var retry []DiscoveredPlugin
		progress := false
		for _, dp := range pending {
			if _, loadErr := manager.Load(ctx, dp.Dir); loadErr != nil {
				if errors.Is(loadErr, ErrDependencyNotLoaded) {
					retry = append(retry, dp)
				} else {
					slog.Warn("auto-load failed", "plugin", dp.Name, "dir", dp.Dir, "error", loadErr)
					result.Failed++
				}
			} else {
				slog.Info("auto-loaded plugin", "plugin", dp.Name, "version", dp.Version)
				result.AutoLoaded++
				progress = true
			}
		}
		pending = retry
		if !progress {
			for _, dp := range pending {
				slog.Warn("auto-load failed", "plugin", dp.Name, "error", "unresolved dependencies")
				result.Failed++
			}
			break
		}
	}

	return result
}

// installTrustSnapshot fetches registry.json + revoked-keys.json and
// wires both into the manager. On failure the snapshot stays empty and
// subsequent Load calls fail closed.
func installTrustSnapshot(ctx context.Context, manager PluginManager, client registry.RegistryClient) {
	reg, regErr := client.FetchRegistry(ctx)
	if regErr != nil {
		slog.Warn("registry fetch failed; trust snapshot stays empty", "error", regErr)
		return
	}
	snap := make(map[string]RegistryEntrySnapshot, len(reg.Plugins))
	for _, p := range reg.Plugins {
		snap[p.Name] = RegistryEntrySnapshot{
			PublicKey: p.PublicKey,
			Official:  p.Official,
		}
	}
	WireRegistrySnapshot(manager, snap)
	slog.Info("registry trust snapshot installed", "plugins", len(snap))

	rk, rkErr := client.FetchRevokedKeys(ctx)
	if rkErr != nil {
		slog.Warn("revoked-keys fetch failed; revocation list stays empty", "error", rkErr)
		return
	}
	revoked := make(map[string]bool, len(rk.Revoked))
	for _, e := range rk.Revoked {
		if e.PublicKey != "" {
			revoked[e.PublicKey] = true
		}
	}
	WireRevokedKeys(manager, revoked)
	if len(revoked) > 0 {
		slog.Warn("revoked-keys snapshot installed", "count", len(revoked))
	}
}
