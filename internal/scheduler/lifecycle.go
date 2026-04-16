package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// PluginName is the canonical name of the scheduler plugin in plugin.loaded /
// plugin.unloaded events. Exported so main.go and tests can reference it
// without duplicating the string literal.
const PluginName = "scheduler-plugin"

// EngineFactory constructs an Engine from a plugin database. Extracted as a
// type so tests can inject a fake that records the call without touching
// SQLite or the real bridge. Returns an error if the engine cannot be built.
type EngineFactory func(db *sql.DB) (Engine, error)

// DefaultEngineFactory is the production EngineFactory: wraps the DB in a
// SQLite-backed PluginBridge and constructs an engine with a fresh cron
// validator and default Config.
func DefaultEngineFactory(bus eventbus.EventBus, logger *slog.Logger) EngineFactory {
	return func(db *sql.DB) (Engine, error) {
		if db == nil {
			return nil, fmt.Errorf("scheduler lifecycle: nil plugin db")
		}
		bridge := NewSQLiteBridge(db)
		return NewEngine(Config{PluginName: PluginName}, bridge, bus, NewCronValidator(), logger), nil
	}
}

// DBResolver returns the per-plugin *sql.DB for the named plugin. Matches the
// shape of loader.PluginManager.PluginDB without importing the loader (keeps
// this package free of a cycle).
type DBResolver func(pluginName string) (*sql.DB, error)

// LifecycleManager owns the optional scheduler engine instance. It handles
// three scenarios:
//
//  1. Plugin is loaded at server start → engine is created and started
//     immediately during TryStartNow.
//  2. Plugin is loaded later via the web admin UI or MCP → the plugin.loaded
//     subscription fires HandlePluginLoaded which starts the engine.
//  3. Plugin is unloaded at runtime → plugin.unloaded fires
//     HandlePluginUnloaded which stops the engine and nils out the handle.
//
// All state is guarded by a mutex so subscriptions, the initial start call,
// and Shutdown can safely run on different goroutines.
type LifecycleManager struct {
	mu      sync.Mutex
	engine  Engine
	factory EngineFactory
	resolve DBResolver
	bus     eventbus.EventBus
	logger  *slog.Logger

	subLoaded   eventbus.Subscription
	subUnloaded eventbus.Subscription
	subscribed  bool
}

// NewLifecycleManager constructs a LifecycleManager. bus, factory, and resolve
// must all be non-nil. logger may be nil (slog.Default is used).
func NewLifecycleManager(bus eventbus.EventBus, factory EngineFactory, resolve DBResolver, logger *slog.Logger) *LifecycleManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &LifecycleManager{
		factory: factory,
		resolve: resolve,
		bus:     bus,
		logger:  logger,
	}
}

// Subscribe registers the plugin.loaded / plugin.unloaded handlers on the bus.
// Call once during application start. Safe to call before or after TryStartNow.
func (m *LifecycleManager) Subscribe() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subscribed {
		return
	}
	m.subLoaded = m.bus.Subscribe(eventbus.EventPluginLoaded, m.onPluginLoaded)
	m.subUnloaded = m.bus.Subscribe(eventbus.EventPluginUnloaded, m.onPluginUnloaded)
	m.subscribed = true
}

// TryStartNow attempts to start the engine immediately for the case where the
// scheduler plugin was already loaded by the time the lifecycle manager was
// wired up. It is a no-op (with an info log) when the plugin is not loaded.
// Returns nil in both cases: failing to find the plugin is expected.
func (m *LifecycleManager) TryStartNow(ctx context.Context) error {
	db, err := m.resolve(PluginName)
	if err != nil {
		m.logger.Info("scheduler lifecycle: plugin not loaded at startup: waiting for plugin.loaded",
			"reason", err)
		return nil
	}
	return m.startEngine(ctx, db)
}

// Shutdown stops the engine (if running) and unsubscribes from the bus.
// Idempotent: safe to call multiple times.
func (m *LifecycleManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil {
		m.engine.Stop()
		m.engine = nil
	}
	if m.subscribed {
		m.bus.Unsubscribe(m.subLoaded)
		m.bus.Unsubscribe(m.subUnloaded)
		m.subscribed = false
	}
}

// IsRunning reports whether the engine is currently active. Primarily for tests.
func (m *LifecycleManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.engine != nil
}

// onPluginLoaded is the plugin.loaded bus handler. It ignores every plugin
// except scheduler-plugin and serialises start/stop through m.mu.
func (m *LifecycleManager) onPluginLoaded(ctx context.Context, ev eventbus.Event) {
	payload, ok := ev.Payload.(eventbus.PluginLoadedPayload)
	if !ok || payload.Name != PluginName {
		return
	}
	db, err := m.resolve(PluginName)
	if err != nil {
		m.logger.Error("scheduler lifecycle: plugin loaded but PluginDB lookup failed",
			"err", err)
		return
	}
	if err := m.startEngine(ctx, db); err != nil {
		m.logger.Error("scheduler lifecycle: failed to start engine on plugin.loaded", "err", err)
	}
}

// onPluginUnloaded is the plugin.unloaded bus handler. Stops the engine and
// clears the field so a subsequent load can re-create it.
func (m *LifecycleManager) onPluginUnloaded(_ context.Context, ev eventbus.Event) {
	payload, ok := ev.Payload.(eventbus.PluginUnloadedPayload)
	if !ok || payload.Name != PluginName {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine == nil {
		return
	}
	m.engine.Stop()
	m.engine = nil
	m.logger.Info("scheduler lifecycle: engine stopped on plugin.unloaded")
}

// startEngine is the shared path for TryStartNow and onPluginLoaded. It only
// builds a new engine when one is not already running, so a duplicate
// plugin.loaded event is a no-op rather than a leak.
func (m *LifecycleManager) startEngine(ctx context.Context, db *sql.DB) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil {
		return nil
	}
	eng, err := m.factory(db)
	if err != nil {
		return fmt.Errorf("build engine: %w", err)
	}
	eng.Start(ctx)
	m.engine = eng
	m.logger.Info("scheduler lifecycle: engine started")
	return nil
}
