package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	_ "modernc.org/sqlite"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/config"
	"github.com/plekt-dev/plekt/internal/eventbus"
	mcI18n "github.com/plekt-dev/plekt/internal/i18n"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/mcp"
	"github.com/plekt-dev/plekt/internal/updater"
	"github.com/plekt-dev/plekt/internal/version"
)

func main() {
	// Check for --stdio mode (MCP stdio transport for Claude Desktop).
	for _, arg := range os.Args[1:] {
		if arg == "--stdio" {
			if err := runStdio(os.Args, os.Getenv); err != nil {
				slog.Error("stdio server failed", "error", err)
				os.Exit(1)
			}
			return
		}
	}
	if err := runServer(os.Args, os.Getenv); err != nil {
		os.Exit(1)
	}
}

// bannerLine pads content to exactly width visible characters inside a "  *  ...  *" frame.
func bannerLine(content string, width int) string {
	visible := len(content)
	pad := width - visible
	if pad < 0 {
		pad = 0
	}
	return "  *  " + content + strings.Repeat(" ", pad) + "  *"
}

// printBanner prints the Plekt ASCII art banner with version info.
func printBanner() {
	const w = 64 // inner visible width between "  *  " and "  *"
	border := "  " + strings.Repeat("*", w+6)
	empty := bannerLine("", w)

	fmt.Println()
	fmt.Println(border)
	fmt.Println(empty)
	fmt.Println(bannerLine(`  ____  _      _    _`, w))
	fmt.Println(bannerLine(` |  _ \| | ___| | _| |_`, w))
	fmt.Println(bannerLine(` | |_) | |/ _ \ |/ / __|`, w))
	fmt.Println(bannerLine(` |  __/| |  __/   <| |_`, w))
	fmt.Println(bannerLine(` |_|   |_|\___|_|\_\\__|`, w))
	fmt.Println(empty)
	fmt.Println(bannerLine(" v"+version.Version, w))
	fmt.Println(empty)
	fmt.Println(border)
	fmt.Println()
}

// runServer contains the testable startup logic for main.
// It returns an error if startup fails; errors are already logged before returning.
func runServer(args []string, getenv func(string) string) error {
	printBanner()

	// 1. Determine config path: first CLI arg, then MC_CONFIG env var.
	cfgPath := resolveConfigPath(args, getenv)

	// 2. Load configuration (uses built-in defaults when no file is found).
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return err
	}

	// Clean up stale .old binary from a previous update cycle.
	if err := updater.CleanupOldBinary(); err != nil {
		slog.Warn("updater: cleanup old binary", "error", err)
	}

	// 3–9. Assemble server components.
	srv, manager, bus, cleanup, err := buildApplication(cfg)
	if err != nil {
		slog.Error("failed to build application", "error", err)
		return err
	}
	defer func() {
		cleanup()
		if err := bus.Close(); err != nil {
			slog.Error("event bus close error", "error", err)
		}
	}()

	// 10. Start server in goroutine.
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("starting Plekt", "addr", cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// 11. Wait for shutdown signal or server error.
	waitForShutdown(srv, serverErr)

	// 12. Shutdown plugins.
	shutdownPlugins(manager)
	return nil
}

// runStdio starts the MCP server in stdio mode (for Claude Desktop integration).
// It initializes plugins and agents, then reads JSON-RPC from stdin / writes to stdout.
func runStdio(args []string, getenv func(string) string) error {
	// Filter out --stdio from args so resolveConfigPath works correctly.
	filteredArgs := make([]string, 0, len(args))
	for _, a := range args {
		if a != "--stdio" {
			filteredArgs = append(filteredArgs, a)
		}
	}

	cfgPath := resolveConfigPath(filteredArgs, getenv)

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return err
	}

	// Redirect slog to stderr so stdout stays clean for JSON-RPC.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	mcI18n.Init()
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg.ApplyDefaults()
	normalizeConfigPaths(&cfg)

	infra, err := buildInfrastructure(cfg)
	if err != nil {
		return err
	}
	defer infra.stack.closeAll()
	defer func() { _ = infra.bus.Close() }()

	loader.BootstrapPlugins(context.Background(), infra.manager, infra.registryStore, loader.BootstrapConfig{
		PluginDir:      cfg.PluginDir,
		DefaultPlugins: cfg.Registry.DefaultPlugins,
		RegistryURL:    cfg.Registry.URL,
		AutoLoad:       cfg.Loader.AutoLoadOnStartup,
	})

	systemHandler := loader.NewPluginMCPHandler(infra.manager)

	agentStorePath := filepath.Join(cfg.DataDir, "agents.db")
	agentStore, err := agents.NewSQLiteAgentStore(agentStorePath)
	if err != nil {
		return fmt.Errorf("open agent store: %w", err)
	}
	defer agentStore.Close()
	agentSvc := agents.NewAgentService(agentStore, infra.bus)

	// Token from env (set by Claude Desktop config).
	token := getenv("MCP_TOKEN")

	slog.Info("starting MCP stdio server", "plugins", len(infra.manager.List()))

	return mcp.RunStdio(mcp.StdioServerConfig{
		Manager:       infra.manager,
		SystemHandler: systemHandler,
		AgentService:  agentSvc,
		Bus:           infra.bus,
		Token:         token,
	})
}

// resolveConfigPath determines the configuration file path from CLI args, environment,
// or falls back to config.yaml in the current working directory.
func resolveConfigPath(args []string, getenv func(string) string) string {
	if len(args) > 1 {
		return args[1]
	}
	if v := getenv("MC_CONFIG"); v != "" {
		return v
	}
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}
	return ""
}

// normalizeConfigPaths resolves relative plugin/data dirs to absolute paths
// and applies a default DataDir if empty.
func normalizeConfigPaths(cfg *config.Config) {
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	if cfg.PluginDir != "" && !filepath.IsAbs(cfg.PluginDir) {
		if abs, err := filepath.Abs(cfg.PluginDir); err == nil {
			cfg.PluginDir = abs
		}
	}
	if !filepath.IsAbs(cfg.DataDir) {
		if abs, err := filepath.Abs(cfg.DataDir); err == nil {
			cfg.DataDir = abs
		}
	}
}

// buildApplication assembles all server components from the given config.
// Returns the HTTP server, plugin manager, event bus (caller must Close),
// a cleanup function (caller must call after server shutdown), and any error.
//
// Construction is split into helpers in app.go:
//   - buildInfrastructure: bus, DBs, plugin manager
//   - buildUserSystem: settings, users, sessions
//   - buildWebLayer: all HTTP handlers and services
//   - buildHTTPHandler: mux assembly and middleware chain
func buildApplication(cfg config.Config) (*http.Server, loader.PluginManager, eventbus.EventBus, func(), error) {
	mcI18n.Init()

	if err := cfg.Validate(); err != nil {
		return nil, nil, nil, nil, err
	}
	cfg.ApplyDefaults()
	normalizeConfigPaths(&cfg)

	// 1. Infrastructure (bus, DBs, plugin manager).
	infra, err := buildInfrastructure(cfg)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// 2. Bootstrap plugins: seed → trust → restore → auto-load.
	ctx := context.Background()
	bootstrapResult := loader.BootstrapPlugins(ctx, infra.manager, infra.registryStore, loader.BootstrapConfig{
		PluginDir:      cfg.PluginDir,
		DefaultPlugins: cfg.Registry.DefaultPlugins,
		RegistryURL:    cfg.Registry.URL,
		AutoLoad:       cfg.Loader.AutoLoadOnStartup,
	})
	infra.bus.Emit(ctx, eventbus.Event{
		Name:    eventbus.EventPluginRegistryRestored,
		Payload: bootstrapResult.RestorePayload,
	})

	// 3. User system (settings, users, sessions).
	us, err := buildUserSystem(cfg, infra.bus)
	if err != nil {
		infra.stack.closeAll()
		return nil, nil, nil, nil, err
	}

	// 4. First-run setup (defaults + setup token).
	handleFirstRun(ctx, us)

	// 5. Web layer (all HTTP handlers and services).
	wl, err := buildWebLayer(cfg, infra, us)
	if err != nil {
		us.stack.closeAll()
		infra.stack.closeAll()
		return nil, nil, nil, nil, err
	}

	// 6. HTTP handler (mux + middleware chain).
	handler := buildHTTPHandler(cfg, infra, us, wl)
	srv := buildServer(cfg.Server, handler)

	cleanup := func() {
		wl.cleanup()
		us.stack.closeAll()
		infra.stack.closeAll()
	}

	return srv, infra.manager, infra.bus, cleanup, nil
}

// openSettingsDB opens (or creates) the SQLite database file for global settings.
func openSettingsDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "settings.db")
	return sql.Open("sqlite", dbPath)
}

// openAuditDB opens (or creates) the SQLite database file for audit logs.
func openAuditDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "audit.db")
	return sql.Open("sqlite", dbPath)
}

// loadConfig reads and parses the YAML config file at path.
// If path is empty, built-in defaults are returned so the application
// can start without a config file on disk.
func loadConfig(path string) (config.Config, error) {
	if path == "" {
		slog.Info("no config file found, using built-in defaults")
		return config.DefaultConfig(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return config.Config{}, err
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// buildServer constructs an http.Server from the given ServerConfig.
func buildServer(cfg config.ServerConfig, handler http.Handler) *http.Server {
	readTimeout := cfg.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 30 * time.Second
	}
	writeTimeout := cfg.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 30 * time.Second
	}
	return &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
}

// waitForShutdown blocks until SIGINT/SIGTERM is received or the server returns an error.
func waitForShutdown(srv *http.Server, serverErr <-chan error) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		slog.Info("received signal, shutting down", "signal", sig.String())
	case err := <-serverErr:
		slog.Error("server error", "error", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
}

// shutdownPlugins gracefully closes all loaded plugins without removing them
// from the registry. Using Shutdown (not Unload) preserves registry entries so
// that plugins are automatically restored on the next startup via
// RestoreFromRegistry, ensuring that data (e.g. projects-plugin SQLite rows)
// remains accessible after a server restart.
func shutdownPlugins(manager loader.PluginManager) {
	if err := manager.Shutdown(context.Background()); err != nil {
		slog.Error("plugin shutdown error", "error", err)
	}
}
