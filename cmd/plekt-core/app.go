package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/config"
	"github.com/plekt-dev/plekt/internal/editor"
	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/firstrun"
	mcI18n "github.com/plekt-dev/plekt/internal/i18n"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/mcp"
	"github.com/plekt-dev/plekt/internal/realtime"
	"github.com/plekt-dev/plekt/internal/registry"
	"github.com/plekt-dev/plekt/internal/scheduler"
	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/updater"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web"
	"github.com/plekt-dev/plekt/internal/web/audit"
	"github.com/plekt-dev/plekt/internal/web/dashboard"
	"github.com/plekt-dev/plekt/internal/webhooks"
)

// ---------------------------------------------------------------------------
// closerStack tracks io.Closers opened during construction so that on error
// they can all be closed in reverse order with a single call.
// ---------------------------------------------------------------------------

type closerStack struct {
	closers []io.Closer
}

func (s *closerStack) push(c io.Closer) { s.closers = append(s.closers, c) }

// closeAll closes every tracked resource in reverse (LIFO) order.
func (s *closerStack) closeAll() {
	for i := len(s.closers) - 1; i >= 0; i-- {
		if err := s.closers[i].Close(); err != nil {
			slog.Error("cleanup close error", "error", err)
		}
	}
}

// ---------------------------------------------------------------------------
// infrastructure: core components shared by runServer and runStdio.
// ---------------------------------------------------------------------------

type infrastructure struct {
	bus            eventbus.EventBus
	registryDB     *sql.DB
	registryStore  loader.PluginRegistryStore
	hostGrantsDB   *sql.DB
	hostGrantStore loader.HostGrantStore
	manager        loader.PluginManager
	scriptRegistry web.GlobalScriptRegistry
	stack          closerStack
}

func buildInfrastructure(cfg config.Config) (*infrastructure, error) {
	if cfg.PluginDir == "" {
		return nil, fmt.Errorf("config.PluginDir must not be empty")
	}
	if err := os.MkdirAll(cfg.PluginDir, 0o755); err != nil {
		return nil, fmt.Errorf("create plugin dir: %w", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	infra := &infrastructure{}

	bus := eventbus.NewInMemoryBus()
	infra.bus = bus

	registryDB, err := sql.Open("sqlite", filepath.Join(cfg.DataDir, "plugins.db"))
	if err != nil {
		return nil, fmt.Errorf("open registry DB: %w", err)
	}
	infra.registryDB = registryDB
	infra.stack.push(registryDB)

	registryStore, err := loader.NewSQLitePluginRegistryStore(registryDB)
	if err != nil {
		infra.stack.closeAll()
		return nil, fmt.Errorf("create registry store: %w", err)
	}
	infra.registryStore = registryStore
	infra.stack.push(registryStore)

	manager := loader.NewManager(cfg, bus, registryStore)
	infra.manager = manager

	hostGrantsDB, err := sql.Open("sqlite", filepath.Join(cfg.DataDir, "host_grants.db"))
	if err != nil {
		infra.stack.closeAll()
		return nil, fmt.Errorf("open host grants DB: %w", err)
	}
	infra.hostGrantsDB = hostGrantsDB
	infra.stack.push(hostGrantsDB)

	hostGrantStore, err := loader.NewSQLiteHostGrantStore(hostGrantsDB)
	if err != nil {
		infra.stack.closeAll()
		return nil, fmt.Errorf("create host grants store: %w", err)
	}
	infra.hostGrantStore = hostGrantStore
	infra.stack.push(hostGrantStore)

	loader.WireHostGrantStore(manager, hostGrantStore)

	scriptRegistry := web.NewGlobalScriptRegistry()
	loader.WireGlobalScriptRegistry(manager, scriptRegistry)
	infra.scriptRegistry = scriptRegistry

	return infra, nil
}

// ---------------------------------------------------------------------------
// userSystem: authentication, settings, sessions.
// ---------------------------------------------------------------------------

type userSystem struct {
	settingsStore settings.SettingsStore
	userStore     users.UserStore
	userSvc       users.UserService
	webSessions   web.WebSessionStore
	csrfProvider  web.CSRFProvider
	stack         closerStack
}

func buildUserSystem(cfg config.Config, bus eventbus.EventBus) (*userSystem, error) {
	us := &userSystem{}

	settingsDB, err := openSettingsDB(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	us.stack.push(settingsDB)

	settingsStore, err := settings.NewSQLiteSettingsStore(settingsDB)
	if err != nil {
		us.stack.closeAll()
		return nil, err
	}
	us.settingsStore = settingsStore
	us.stack.push(settingsStore)

	coreDBPath := filepath.Join(cfg.DataDir, "core.db")
	userStore, err := users.NewSQLiteUserStore(context.Background(), coreDBPath+"?_journal=WAL")
	if err != nil {
		us.stack.closeAll()
		return nil, err
	}
	us.userStore = userStore
	us.stack.push(userStore)

	currentSettings, settingsErr := settingsStore.Load(context.Background())
	if settingsErr != nil {
		currentSettings.PasswordMinLength = 12
	}
	if currentSettings.PasswordMinLength <= 0 {
		currentSettings.PasswordMinLength = 12
	}
	us.userSvc = users.NewUserService(userStore, currentSettings.PasswordMinLength)

	webSessions, err := web.NewInMemoryWebSessionStore(func() int {
		if s, err := settingsStore.Load(context.Background()); err == nil {
			return s.SessionTTLMinutes
		}
		return 0
	})
	if err != nil {
		us.stack.closeAll()
		return nil, err
	}
	us.webSessions = webSessions
	us.stack.push(webSessions)

	us.csrfProvider = web.NewCSRFProvider()
	return us, nil
}

// handleFirstRun enables defaults and generates a setup token on first run.
func handleFirstRun(ctx context.Context, us *userSystem) {
	if !firstrun.Detect(ctx, us.userSvc) {
		return
	}

	currentSettings, err := us.settingsStore.Load(ctx)
	if err != nil {
		currentSettings.PasswordMinLength = 12
	}
	changed := false
	if !currentSettings.AllowPluginInstall {
		currentSettings.AllowPluginInstall = true
		changed = true
	}
	if currentSettings.SessionTTLMinutes <= 0 {
		currentSettings.SessionTTLMinutes = 480 // 8 hours
		changed = true
	}
	if changed {
		if err := us.settingsStore.Save(ctx, currentSettings); err != nil {
			slog.Warn("first-run: failed to save default settings", "error", err)
		}
	}

	token, tokenErr := firstrun.GenerateSetupToken()
	if tokenErr != nil {
		slog.Error("failed to generate setup token", "error", tokenErr)
		return
	}
	if err := firstrun.StoreTokenHash(ctx, us.settingsStore, token.Hash); err != nil {
		slog.Error("failed to store setup token hash", "error", err)
		return
	}
	firstrun.PrintSetupBanner(token.Plain)
}

// ---------------------------------------------------------------------------
// webLayer: all HTTP handlers and services.
// ---------------------------------------------------------------------------

type webLayer struct {
	mcpRouter            mcp.MCPRouter
	agentHandler         web.WebAgentHandler
	authHandler          web.WebAuthHandler
	registerHandler      web.WebRegisterHandler
	passwordHandler      web.WebPasswordHandler
	adminHandler         web.WebAdminHandler
	pluginAdminHandler   web.WebPluginAdminHandler
	pluginPageHandler    web.WebPluginPageHandler
	projectDetailHandler web.WebProjectDetailHandler
	settingsHandler      web.WebSettingsHandler
	dashboardHandler     dashboard.DashboardHandler
	updateHandler        web.WebUpdateHandler
	editorHandler        *web.EditorHandler
	runCallbackHandler   web.RunCallbackHandler
	sseHandler           *realtime.SSEHandler
	extensionRegistry    *loader.ExtensionRegistry
	coreUpdater          *updater.Updater
	layoutStore          dashboard.DashboardLayoutStore
	schedulerLifecycle   *scheduler.LifecycleManager

	// services that need cleanup
	sseHub            *realtime.InMemoryHub
	webhookDispatcher webhooks.Dispatcher
	auditSubscriber   *audit.AuditLogSubscriber
	stack             closerStack
}

func buildWebLayer(
	cfg config.Config,
	infra *infrastructure,
	us *userSystem,
) (*webLayer, error) {
	ctx := context.Background()
	wl := &webLayer{}

	// Agent store + service.
	agentStore, err := agents.NewSQLiteAgentStore(filepath.Join(cfg.DataDir, "agents.db"))
	if err != nil {
		return nil, fmt.Errorf("open agent store: %w", err)
	}
	wl.stack.push(agentStore)
	agentSvc := agents.NewAgentService(agentStore, infra.bus)

	// MCP router.
	systemHandler := loader.NewPluginMCPHandler(infra.manager)
	sessions := mcp.NewSessionStore()
	wl.mcpRouter = mcp.NewMCPRouter(mcp.RouterConfig{
		Manager:       infra.manager,
		SystemHandler: systemHandler,
		Sessions:      sessions,
		AgentService:  agentSvc,
		ServerVersion: "1.0.0",
		Bus:           infra.bus,
	})

	// Agent web handler.
	wl.agentHandler = web.NewWebAgentHandler(agentSvc, infra.manager, us.webSessions, us.csrfProvider)

	// Extension registry + core updater.
	wl.extensionRegistry = loader.NewExtensionRegistry()
	regClient := registry.NewHTTPRegistryClient(cfg.Registry.URL)
	wl.coreUpdater = updater.New(updater.Config{
		RegistryClient: regClient,
		Bus:            infra.bus,
		CheckInterval:  1 * time.Hour,
		DataDir:        cfg.DataDir,
	})
	wl.coreUpdater.Start(ctx)

	// Handlers.
	wl.updateHandler = web.NewWebUpdateHandler(web.WebUpdateHandlerConfig{
		Updater:  wl.coreUpdater,
		Sessions: us.webSessions,
		CSRF:     us.csrfProvider,
	})

	settingsHandler, err := web.NewWebSettingsHandler(web.WebSettingsHandlerConfig{
		Store:      us.settingsStore,
		Sessions:   us.webSessions,
		CSRF:       us.csrfProvider,
		Bus:        infra.bus,
		Plugins:    infra.manager,
		Extensions: wl.extensionRegistry,
		Updater:    wl.coreUpdater,
	})
	if err != nil {
		wl.coreUpdater.Stop()
		wl.stack.closeAll()
		return nil, err
	}
	wl.settingsHandler = settingsHandler

	wl.authHandler = web.NewWebAuthHandler(us.userSvc, us.webSessions, us.csrfProvider, infra.bus, us.settingsStore)
	wl.registerHandler = web.NewWebRegisterHandler(us.userSvc, us.webSessions, us.csrfProvider, us.settingsStore, infra.bus)
	wl.passwordHandler = web.NewWebPasswordHandler(us.userSvc, us.webSessions, us.csrfProvider, us.settingsStore)

	// Audit.
	auditDB, err := openAuditDB(cfg.DataDir)
	if err != nil {
		wl.coreUpdater.Stop()
		wl.stack.closeAll()
		return nil, err
	}
	wl.stack.push(auditDB)

	auditStore, err := audit.NewSQLiteAuditLogStore(auditDB)
	if err != nil {
		wl.coreUpdater.Stop()
		wl.stack.closeAll()
		return nil, err
	}
	wl.stack.push(auditStore)
	wl.auditSubscriber = audit.NewAuditLogSubscriber(auditStore, infra.bus)

	adminHandler, err := web.NewWebAdminHandler(web.WebAdminHandlerConfig{
		Sessions: us.webSessions,
		AuditLog: auditStore,
		Bus:      infra.bus,
		CSRF:     us.csrfProvider,
		Users:    us.userSvc,
	})
	if err != nil {
		wl.auditSubscriber.Close()
		wl.coreUpdater.Stop()
		wl.stack.closeAll()
		return nil, err
	}
	wl.adminHandler = adminHandler

	pluginAdminHandler, err := web.NewWebPluginAdminHandler(web.PluginAdminHandlerConfig{
		Plugins:          infra.manager,
		Bus:              infra.bus,
		Sessions:         us.webSessions,
		CSRF:             us.csrfProvider,
		AllowedPluginDir: cfg.PluginDir,
		Settings:         us.settingsStore,
		HostGrants:       infra.hostGrantStore,
		Registry:         regClient,
	})
	if err != nil {
		wl.auditSubscriber.Close()
		wl.coreUpdater.Stop()
		wl.stack.closeAll()
		return nil, err
	}
	wl.pluginAdminHandler = pluginAdminHandler

	// Dashboard.
	widgetRegistry := dashboard.NewWidgetRegistry(infra.bus)
	wl.layoutStore = dashboard.NewDashboardLayoutStore()
	dataProvider := dashboard.NewDashboardDataProvider(infra.manager, widgetRegistry, infra.bus)
	wl.dashboardHandler = dashboard.NewDashboardHandler(widgetRegistry, dataProvider, wl.layoutStore, infra.bus)

	wireEventSubscriptions(infra.bus, infra.manager, widgetRegistry, wl.extensionRegistry)
	backfillLoadedPlugins(infra.manager, widgetRegistry, wl.extensionRegistry)

	// Plugin page + project detail handlers.
	pluginPageHandler, err := web.NewPluginPageHandler(web.PluginPageHandlerConfig{
		Plugins:    infra.manager,
		Extensions: wl.extensionRegistry,
		Sessions:   us.webSessions,
		CSRF:       us.csrfProvider,
		Users:      us.userStore,
		PluginsDir: cfg.PluginDir,
	})
	if err != nil {
		wl.auditSubscriber.Close()
		wl.coreUpdater.Stop()
		wl.stack.closeAll()
		return nil, err
	}
	wl.pluginPageHandler = pluginPageHandler

	editorRenderer := editor.NewRenderer(nil)
	projectDetailHandler, err := web.NewProjectDetailHandler(web.ProjectDetailHandlerConfig{
		Plugins:    infra.manager,
		Extensions: wl.extensionRegistry,
		Sessions:   us.webSessions,
		CSRF:       us.csrfProvider,
		Users:      us.userStore,
		PluginsDir: cfg.PluginDir,
		Renderer:   editorRenderer,
	})
	if err != nil {
		wl.auditSubscriber.Close()
		wl.coreUpdater.Stop()
		wl.stack.closeAll()
		return nil, err
	}
	wl.projectDetailHandler = projectDetailHandler
	wl.editorHandler = web.NewEditorHandler(editorRenderer, editor.DefaultRenderOptions(""))

	// Webhooks + SSE.
	lazyBridge := newLazyPluginBridge(infra.manager.PluginDB)
	wl.webhookDispatcher = webhooks.New(webhooks.Config{
		Bus:             infra.bus,
		Agents:          agentSvc,
		Bridge:          lazyBridge,
		HTTPClient:      &http.Client{Timeout: 30 * time.Second},
		CallbackBaseURL: webhookCallbackBaseURL(cfg),
		RetryAttempts:   3,
		RetryBackoff:    2 * time.Second,
	})
	if err := wl.webhookDispatcher.Start(ctx); err != nil {
		wl.auditSubscriber.Close()
		wl.coreUpdater.Stop()
		wl.stack.closeAll()
		return nil, fmt.Errorf("webhook dispatcher: %w", err)
	}
	wl.runCallbackHandler = web.NewRunCallbackHandler(lazyBridge, agentSvc)

	sseHub, err := realtime.NewInMemoryHub(realtime.InMemoryHubConfig{Bus: infra.bus})
	if err != nil {
		wl.webhookDispatcher.Stop()
		wl.auditSubscriber.Close()
		wl.coreUpdater.Stop()
		wl.stack.closeAll()
		return nil, fmt.Errorf("realtime hub: %w", err)
	}
	if err := sseHub.Start(ctx); err != nil {
		wl.webhookDispatcher.Stop()
		wl.auditSubscriber.Close()
		wl.coreUpdater.Stop()
		wl.stack.closeAll()
		return nil, fmt.Errorf("realtime hub start: %w", err)
	}
	wl.sseHub = sseHub
	wl.sseHandler = realtime.NewSSEHandler(sseHub)
	wl.sseHandler.SetSessionIDFunc(func(r *http.Request) string {
		entry, err := web.SessionFromRequest(r)
		if err != nil {
			return ""
		}
		return entry.ID
	})

	// Scheduler lifecycle.
	wl.schedulerLifecycle = scheduler.NewLifecycleManager(
		infra.bus,
		scheduler.DefaultEngineFactory(infra.bus, nil),
		infra.manager.PluginDB,
		nil,
	)
	wl.schedulerLifecycle.Subscribe()
	if err := wl.schedulerLifecycle.TryStartNow(ctx); err != nil {
		slog.Error("scheduler lifecycle: TryStartNow failed", "error", err)
	}

	return wl, nil
}

func (wl *webLayer) cleanup() {
	wl.coreUpdater.Stop()
	wl.sseHub.Stop()
	wl.webhookDispatcher.Stop()
	wl.schedulerLifecycle.Shutdown()
	wl.auditSubscriber.Close()
	if wl.layoutStore != nil {
		_ = wl.layoutStore.Close()
	}
	wl.stack.closeAll()
}

// ---------------------------------------------------------------------------
// Event subscriptions and backfill.
// ---------------------------------------------------------------------------

func wireEventSubscriptions(
	bus eventbus.EventBus,
	manager loader.PluginManager,
	widgetRegistry dashboard.WidgetRegistry,
	extensionRegistry *loader.ExtensionRegistry,
) {
	bus.Subscribe(eventbus.EventPluginLoaded, func(ctx context.Context, e eventbus.Event) {
		payload, ok := e.Payload.(eventbus.PluginLoadedPayload)
		if !ok {
			return
		}
		manifest, err := manager.GetManifest(payload.Name)
		if err != nil {
			return
		}
		if len(manifest.Dashboard.Widgets) > 0 {
			if err := widgetRegistry.Register(payload.Name, manifest.Dashboard); err != nil {
				slog.Warn("widget registration failed", "plugin", payload.Name, "error", err)
			}
		}
		if len(manifest.UI.Extensions) > 0 {
			extensionRegistry.Register(payload.Name, manifest.UI.Extensions)
		}
		// Auto-load plugin i18n locales.
		loadPluginLocales(payload.Name, payload.Dir)
	})

	bus.Subscribe(eventbus.EventPluginUnloaded, func(_ context.Context, e eventbus.Event) {
		payload, ok := e.Payload.(eventbus.PluginUnloadedPayload)
		if !ok {
			return
		}
		widgetRegistry.Unregister(payload.Name)
		extensionRegistry.Unregister(payload.Name)
	})
}

func backfillLoadedPlugins(
	manager loader.PluginManager,
	widgetRegistry dashboard.WidgetRegistry,
	extensionRegistry *loader.ExtensionRegistry,
) {
	for _, info := range manager.List() {
		manifest, err := manager.GetManifest(info.Name)
		if err != nil {
			continue
		}
		if len(manifest.Dashboard.Widgets) > 0 {
			_ = widgetRegistry.Register(info.Name, manifest.Dashboard)
		}
		if len(manifest.UI.Extensions) > 0 {
			extensionRegistry.Register(info.Name, manifest.UI.Extensions)
		}
		loadPluginLocales(info.Name, info.Dir)
	}
}

func loadPluginLocales(pluginName, pluginDir string) {
	localesDir := filepath.Join(pluginDir, "locales")
	entries, err := os.ReadDir(localesDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		lang := strings.TrimSuffix(entry.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(localesDir, entry.Name()))
		if err != nil {
			slog.Warn("i18n: failed to read plugin locale", "plugin", pluginName, "file", entry.Name(), "error", err)
			continue
		}
		if err := mcI18n.LoadPluginMessages(lang, data); err != nil {
			slog.Warn("i18n: failed to load plugin locale", "plugin", pluginName, "lang", lang, "error", err)
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP mux assembly and middleware chain.
// ---------------------------------------------------------------------------

func buildHTTPHandler(
	cfg config.Config,
	infra *infrastructure,
	us *userSystem,
	wl *webLayer,
) http.Handler {
	httpMux := wl.mcpRouter.Build(http.NewServeMux())

	webRouter := web.NewWebRouter(web.WebRouterConfig{
		Auth:                      wl.authHandler,
		Agents:                    wl.agentHandler,
		Sessions:                  us.webSessions,
		CSRF:                      us.csrfProvider,
		Settings:                  wl.settingsHandler,
		Admin:                     wl.adminHandler,
		Plugins:                   wl.pluginAdminHandler,
		PluginPages:               wl.pluginPageHandler,
		ProjectDetail:             wl.projectDetailHandler,
		Dashboard:                 wl.dashboardHandler,
		Register:                  wl.registerHandler,
		Password:                  wl.passwordHandler,
		Update:                    wl.updateHandler,
		PluginStatic:              &web.PluginStaticHandler{PluginsDir: cfg.PluginDir},
		Editor:                    wl.editorHandler,
		RunCallback:               wl.runCallbackHandler,
		SSE:                       wl.sseHandler,
		LoginRateLimitMaxAttempts: cfg.Server.LoginRateLimit.MaxAttempts,
		LoginRateLimitWindow:      cfg.Server.LoginRateLimit.Window,
	})
	webRouter.Build(httpMux)

	pluginNavCache := web.NewPluginNavCache(infra.manager)
	infra.bus.Subscribe(eventbus.EventPluginLoaded, func(_ context.Context, _ eventbus.Event) {
		pluginNavCache.Invalidate()
	})
	infra.bus.Subscribe(eventbus.EventPluginUnloaded, func(_ context.Context, _ eventbus.Event) {
		pluginNavCache.Invalidate()
	})

	var handler http.Handler = httpMux
	handler = web.CoreUpdateBannerMiddleware(wl.coreUpdater)(handler)
	handler = web.GlobalScriptsMiddleware(infra.scriptRegistry)(handler)
	handler = web.PluginNavMiddleware(pluginNavCache)(handler)
	handler = web.SlowRequestLogMiddleware()(handler)
	handler = web.LocaleMiddleware(us.settingsStore)(handler)
	handler = web.FirstRunMiddleware(us.userSvc, handler)
	handler = web.SecurityHeadersMiddleware(handler)
	return handler
}
