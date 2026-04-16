package web

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/plekt-dev/plekt/internal/web/dashboard"
	"github.com/plekt-dev/plekt/internal/web/static"
)

// loginRateLimitDisabledEnv is the environment variable name that, when set
// to "1", disables the login rate limiter entirely. Intended for CI/E2E only.
const loginRateLimitDisabledEnv = "PLEKT_LOGIN_RATE_LIMIT_DISABLED"

// WebRouterConfig holds all dependencies for building the web router.
type WebRouterConfig struct {
	Auth          WebAuthHandler
	Agents        WebAgentHandler // nil disables /agents routes
	Sessions      WebSessionStore
	CSRF          CSRFProvider
	StaticDir     string
	Dashboard     dashboard.DashboardHandler // nil disables dashboard routes
	Plugins       WebPluginAdminHandler      // nil disables plugin admin routes
	PluginPages   WebPluginPageHandler       // nil disables /p/{plugin}/{page} routes
	ProjectDetail WebProjectDetailHandler    // nil disables /p/projects-plugin/project/{id}
	Admin         WebAdminHandler            // nil disables admin routes
	Settings      WebSettingsHandler         // nil disables /admin/settings routes
	Register      WebRegisterHandler         // nil disables /register routes
	Password      WebPasswordHandler         // nil disables /change-password routes
	Update        WebUpdateHandler           // nil disables /api/core/* routes
	PluginStatic  *PluginStaticHandler       // nil disables GET /p/{plugin}/static/{path...}
	Editor        *EditorHandler             // nil disables POST /api/preview-markdown
	RunCallback   RunCallbackHandler         // nil disables POST /api/runs/{run_id}/result
	SSE           http.Handler               // nil disables GET /api/events
	// LoginRateLimit controls brute-force protection on POST /login.
	// Zero values apply production defaults (5 attempts per 15min).
	LoginRateLimitMaxAttempts int
	LoginRateLimitWindow      time.Duration
}

// WebRouter wires HTTP routes for the web UI.
type WebRouter struct {
	cfg WebRouterConfig
}

// NewWebRouter constructs a WebRouter from the provided configuration.
func NewWebRouter(cfg WebRouterConfig) WebRouter {
	return WebRouter{cfg: cfg}
}

// buildLoginHandler wires the POST /login handler with an optional rate limiter.
// Resolves configuration from (in priority order):
//  1. PLEKT_LOGIN_RATE_LIMIT_DISABLED=1: disables rate limiting, logs WARN
//  2. cfg.LoginRateLimitMaxAttempts / LoginRateLimitWindow (from config.yaml)
//  3. Production defaults: 5 attempts per 15 minutes.
func (r WebRouter) buildLoginHandler() http.Handler {
	loginSubmit := http.HandlerFunc(r.cfg.Auth.HandleLoginSubmit)

	if os.Getenv(loginRateLimitDisabledEnv) == "1" {
		slog.Warn("login rate limit DISABLED via " + loginRateLimitDisabledEnv +
			"=1: do NOT use in production")
		return loginSubmit
	}

	maxAttempts := r.cfg.LoginRateLimitMaxAttempts
	window := r.cfg.LoginRateLimitWindow
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	rl := NewLoginRateLimiter(maxAttempts, window)
	return LoginRateLimitMiddleware(rl)(loginSubmit)
}

// Build registers all routes on mux and returns it.
// If mux is nil, a new http.ServeMux is created.
func (r WebRouter) Build(mux *http.ServeMux) *http.ServeMux {
	if mux == nil {
		mux = http.NewServeMux()
	}

	// Root redirect: / → /dashboard if authenticated, else /login
	sessions := r.cfg.Sessions
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, req *http.Request) {
		if cookie, err := req.Cookie("mc_session"); err == nil {
			if entry, err := sessions.Get(cookie.Value); err == nil && entry.UserID > 0 {
				http.Redirect(w, req, "/dashboard", http.StatusFound)
				return
			}
		}
		http.Redirect(w, req, "/login", http.StatusFound)
	})

	// pprof debug endpoints: auth via simple env-flag check at handler time.
	// Path matches the stdlib net/http/pprof default. To enable: set
	// MC_PPROF=1 in the env. Without it the routes return 404.
	// Use to dump goroutines: GET /debug/pprof/goroutine?debug=2
	registerPprofRoutes(mux)

	// Health check (unauthenticated, used by frontend heartbeat)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Unauthenticated routes
	mux.HandleFunc("GET /login", r.cfg.Auth.HandleLoginPage)
	mux.Handle("POST /login", r.buildLoginHandler())
	mux.HandleFunc("POST /logout", r.cfg.Auth.HandleLogout)

	// Static file serving: filesystem override for dev, embedded assets by default.
	// MIME types are fixed globally in static.init() via mime.AddExtensionType.
	if r.cfg.StaticDir != "" {
		fs := http.FileServer(http.Dir(r.cfg.StaticDir))
		mux.Handle("GET /static/", http.StripPrefix("/static/", fs))
	} else {
		fs := http.FileServer(http.FS(static.Assets))
		mux.Handle("GET /static/", http.StripPrefix("/static/", fs))
	}

	// Plugin static file serving (no auth: same as core /static/)
	if r.cfg.PluginStatic != nil {
		mux.Handle("GET /p/{plugin}/static/{path...}", r.cfg.PluginStatic)
	}

	// Language switcher endpoint (authenticated + CSRF)
	mux.Handle("POST /api/language",
		WebAuthMiddleware(r.cfg.Sessions,
			WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF,
				http.HandlerFunc(HandleLanguageSwitch))))

	// Markdown preview endpoint (authenticated + CSRF)
	if r.cfg.Editor != nil {
		mustChangePwEd := MustChangePasswordMiddleware(r.cfg.Sessions)
		mux.Handle("POST /api/preview-markdown",
			WebAuthMiddleware(r.cfg.Sessions,
				WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF,
					mustChangePwEd(http.HandlerFunc(r.cfg.Editor.HandlePreview)))))
	}

	// Optional dashboard routes
	if r.cfg.Dashboard != nil {
		r.registerDashboardRoutes(mux)
	}

	// Optional plugin admin routes
	if r.cfg.Plugins != nil {
		r.registerPluginAdminRoutes(mux)
	}

	// Optional project detail routes (must be registered before generic
	// /p/{plugin}/{page} so they take precedence when both are active).
	if r.cfg.ProjectDetail != nil {
		mustChangePwPD := MustChangePasswordMiddleware(r.cfg.Sessions)
		pdHandler := WebAuthMiddleware(r.cfg.Sessions,
			mustChangePwPD(http.HandlerFunc(r.cfg.ProjectDetail.HandleProjectDetailPage)))
		// /project/{id} redirects to /project/{id}/{first-tab}
		mux.Handle("GET /p/projects-plugin/project/{id}", pdHandler)
		// /project/{id}/{tab} renders the tab content with project sidebar
		mux.Handle("GET /p/projects-plugin/project/{id}/{tab}", pdHandler)
	}

	// Optional plugin page routes
	if r.cfg.PluginPages != nil {
		r.registerPluginPageRoutes(mux)
	}

	// Optional admin routes
	if r.cfg.Admin != nil {
		r.registerAdminRoutes(mux)
	}

	// Optional settings routes
	if r.cfg.Settings != nil {
		r.registerSettingsRoutes(mux)
	}

	// Optional agent management routes (admin only)
	if r.cfg.Agents != nil {
		r.registerAgentRoutes(mux)
	}

	// Optional register routes (unauthenticated)
	if r.cfg.Register != nil {
		r.registerRegisterRoutes(mux)
	}

	// Optional change-password routes (authenticated)
	if r.cfg.Password != nil {
		r.registerPasswordRoutes(mux)
	}

	// Optional core update routes (admin only)
	if r.cfg.Update != nil {
		r.registerUpdateRoutes(mux)
	}

	// Optional user management routes (admin only)
	if r.cfg.Admin != nil {
		r.registerUserManagementRoutes(mux)
	}

	// Realtime SSE endpoint: authenticated by session, no CSRF
	// (CSRF on a GET stream adds nothing and breaks EventSource which
	// cannot set custom headers).
	if r.cfg.SSE != nil {
		mux.Handle("GET /api/events",
			WebAuthMiddleware(r.cfg.Sessions, r.cfg.SSE))
	}

	// Webhook callback endpoint: authenticated by HMAC signature inside the
	// handler, NOT by user session. The relay process is a peer service.
	if r.cfg.RunCallback != nil {
		mux.HandleFunc("POST /api/runs/{run_id}/result", r.cfg.RunCallback.Handle)
	}

	return mux
}

// registerPluginAdminRoutes registers all plugin admin routes on mux.
// All routes require authentication; POST routes also require CSRF validation.
func (r WebRouter) registerPluginAdminRoutes(mux *http.ServeMux) {
	requireAdmin := RequireRole("admin", r.cfg.Sessions)
	mustChangePw := MustChangePasswordMiddleware(r.cfg.Sessions)

	authedAdmin := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions, requireAdmin(mustChangePw(h)))
	}
	authedAdminWithCSRF := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions,
			requireAdmin(WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF, mustChangePw(h))))
	}

	mux.Handle("GET /admin/plugins",
		authedAdmin(http.HandlerFunc(r.cfg.Plugins.HandlePluginList)))

	mux.Handle("GET /admin/plugins/{name}",
		authedAdmin(http.HandlerFunc(r.cfg.Plugins.HandlePluginDetail)))

	mux.Handle("POST /admin/plugins/load",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Plugins.HandlePluginLoad)))

	mux.Handle("POST /admin/plugins/install-remote",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Plugins.HandlePluginInstallRemote)))

	mux.Handle("POST /admin/plugins/delete",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Plugins.HandlePluginDelete)))

	mux.Handle("POST /admin/plugins/{name}/unload",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Plugins.HandlePluginUnload)))

	mux.Handle("POST /admin/plugins/{name}/reload",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Plugins.HandlePluginReload)))

	// Slice 3: permissions workflow.
	//
	// /admin/plugins/inspect is a read-only GET used by the install permissions
	// modal to preview capabilities before Load; the page handler renders the
	// per-plugin permissions settings view; the {host} routes mutate the
	// plugin_host_grants store and trigger reload.
	mux.Handle("GET /admin/plugins/inspect",
		authedAdmin(http.HandlerFunc(r.cfg.Plugins.HandleInspectPluginDir)))

	mux.Handle("GET /admin/plugins/{name}/permissions",
		authedAdmin(http.HandlerFunc(r.cfg.Plugins.HandlePluginPermissionsPage)))

	mux.Handle("POST /admin/plugins/{name}/hosts",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Plugins.HandleGrantHost)))

	mux.Handle("DELETE /admin/plugins/{name}/hosts/{host}",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Plugins.HandleRevokeHost)))
}

// registerAdminRoutes registers all admin user management routes on mux.
// GET routes require authentication; the POST revoke route also requires CSRF validation.
// Session revoke and other sensitive routes also require admin role.
func (r WebRouter) registerAdminRoutes(mux *http.ServeMux) {
	mustChangePw := MustChangePasswordMiddleware(r.cfg.Sessions)
	requireAdmin := RequireRole("admin", r.cfg.Sessions)
	authed := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions, mustChangePw(h))
	}
	authedWithCSRF := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions,
			WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF, mustChangePw(h)))
	}
	authedAdmin := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions, requireAdmin(mustChangePw(h)))
	}

	// User profile (available to all authenticated users: shows own profile only)
	mux.Handle("GET /user",
		authed(http.HandlerFunc(r.cfg.Admin.HandleUserProfile)))

	mux.Handle("POST /user/sessions/{id}/revoke",
		authedWithCSRF(http.HandlerFunc(r.cfg.Admin.HandleSessionRevoke)))

	// Admin-only routes: VULN-04: both /admin/sessions and /admin/audit
	// require RequireRole("admin") to prevent non-admin access.
	mux.Handle("GET /admin/sessions",
		authedAdmin(http.HandlerFunc(r.cfg.Admin.HandleUserProfile)))

	mux.Handle("GET /admin/audit",
		authedAdmin(http.HandlerFunc(r.cfg.Admin.HandleAuditLog)))
}

// registerSettingsRoutes registers the global settings admin routes on mux.
// GET requires authentication; POST also requires CSRF validation.
func (r WebRouter) registerSettingsRoutes(mux *http.ServeMux) {
	requireAdmin := RequireRole("admin", r.cfg.Sessions)
	mustChangePw := MustChangePasswordMiddleware(r.cfg.Sessions)

	mux.Handle("GET /admin/settings",
		WebAuthMiddleware(r.cfg.Sessions,
			requireAdmin(mustChangePw(http.HandlerFunc(r.cfg.Settings.HandleSettingsPage)))))

	mux.Handle("POST /admin/settings",
		WebAuthMiddleware(r.cfg.Sessions,
			requireAdmin(WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF,
				mustChangePw(http.HandlerFunc(r.cfg.Settings.HandleSettingsSave))))))
}

// registerRegisterRoutes registers the self-registration routes on mux.
// These routes are unauthenticated but use CSRF via pre-login sessions.
func (r WebRouter) registerRegisterRoutes(mux *http.ServeMux) {
	// GET /register is unauthenticated: creates a pre-login session for CSRF.
	mux.HandleFunc("GET /register", r.cfg.Register.HandleRegisterPage)

	// POST /register: CSRF is validated by the handler itself (not middleware),
	// so that stale-session failures can redirect back to the form gracefully.
	mux.HandleFunc("POST /register", r.cfg.Register.HandleRegisterSubmit)
}

// registerPasswordRoutes registers the change-password routes on mux.
// All routes require authentication.
func (r WebRouter) registerPasswordRoutes(mux *http.ServeMux) {
	mustChangePw := MustChangePasswordMiddleware(r.cfg.Sessions)
	authed := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions, h)
	}
	authedWithCSRF := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions,
			WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF, h))
	}

	mux.Handle("GET /change-password",
		authed(mustChangePw(http.HandlerFunc(r.cfg.Password.HandleChangePasswordPage))))

	mux.Handle("POST /change-password",
		authedWithCSRF(mustChangePw(http.HandlerFunc(r.cfg.Password.HandleChangePasswordSubmit))))
}

// registerUserManagementRoutes registers the /admin/users routes on mux.
// All routes require admin role.
// Middleware order: WebAuthMiddleware (outermost) → RequireRole → mustChangePw → handler.
func (r WebRouter) registerUserManagementRoutes(mux *http.ServeMux) {
	requireAdmin := RequireRole("admin", r.cfg.Sessions)
	mustChangePw := MustChangePasswordMiddleware(r.cfg.Sessions)

	authedAdmin := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions, requireAdmin(mustChangePw(h)))
	}
	authedAdminWithCSRF := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions,
			requireAdmin(WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF, mustChangePw(h))))
	}

	mux.Handle("GET /admin/users",
		authedAdmin(http.HandlerFunc(r.cfg.Admin.HandleUserList)))

	mux.Handle("POST /admin/users",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Admin.HandleUserCreate)))

	mux.Handle("POST /admin/users/{id}/delete",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Admin.HandleUserDelete)))

	mux.Handle("POST /admin/users/{id}/change-role",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Admin.HandleUserChangeRole)))

	mux.Handle("POST /admin/users/{id}/reset-password",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Admin.HandleUserResetPassword)))
}

// registerPluginPageRoutes registers plugin UI page routes on mux.
func (r WebRouter) registerPluginPageRoutes(mux *http.ServeMux) {
	mustChangePw := MustChangePasswordMiddleware(r.cfg.Sessions)
	authed := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions,
			WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF, mustChangePw(h)))
	}

	mux.Handle("GET /p/{plugin}/{page}",
		WebAuthMiddleware(r.cfg.Sessions,
			mustChangePw(http.HandlerFunc(r.cfg.PluginPages.HandlePluginPage))))

	mux.Handle("POST /p/{plugin}/action/{tool}",
		authed(http.HandlerFunc(r.cfg.PluginPages.HandlePluginAction)))
}

// registerUpdateRoutes registers the core update routes on mux.
// Both routes require admin role and CSRF validation.
func (r WebRouter) registerUpdateRoutes(mux *http.ServeMux) {
	requireAdmin := RequireRole("admin", r.cfg.Sessions)
	mustChangePw := MustChangePasswordMiddleware(r.cfg.Sessions)

	authedAdminWithCSRF := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions,
			requireAdmin(WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF, mustChangePw(h))))
	}

	mux.Handle("POST /api/core/check-update",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Update.HandleCheckUpdate)))

	mux.Handle("POST /api/core/update",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Update.HandleApplyUpdate)))
}

// registerDashboardRoutes registers all dashboard-related routes on mux.
// All routes require authentication; the layout save route also requires CSRF.
func (r WebRouter) registerDashboardRoutes(mux *http.ServeMux) {
	mustChangePw := MustChangePasswordMiddleware(r.cfg.Sessions)
	authed := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions,
			WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF, mustChangePw(h)))
	}

	mux.Handle("GET /dashboard",
		WebAuthMiddleware(r.cfg.Sessions,
			mustChangePw(http.HandlerFunc(r.cfg.Dashboard.HandleDashboardPage))))

	mux.Handle("GET /dashboard/widgets/{key}/refresh",
		WebAuthMiddleware(r.cfg.Sessions,
			mustChangePw(http.HandlerFunc(r.cfg.Dashboard.HandleWidgetRefresh))))

	mux.Handle("POST /dashboard/layout",
		authed(http.HandlerFunc(r.cfg.Dashboard.HandleLayoutSave)))
}

// registerAgentRoutes registers all agent management routes on mux.
// All routes require admin role.
func (r WebRouter) registerAgentRoutes(mux *http.ServeMux) {
	requireAdmin := RequireRole("admin", r.cfg.Sessions)
	mustChangePw := MustChangePasswordMiddleware(r.cfg.Sessions)

	authedAdmin := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions, requireAdmin(mustChangePw(h)))
	}
	authedAdminWithCSRF := func(h http.Handler) http.Handler {
		return WebAuthMiddleware(r.cfg.Sessions,
			requireAdmin(WebCSRFMiddleware(r.cfg.Sessions, r.cfg.CSRF, mustChangePw(h))))
	}

	mux.Handle("GET /admin/agents",
		authedAdmin(http.HandlerFunc(r.cfg.Agents.HandleAgentList)))

	mux.Handle("POST /admin/agents",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Agents.HandleAgentCreate)))

	mux.Handle("GET /admin/agents/{id}",
		authedAdmin(http.HandlerFunc(r.cfg.Agents.HandleAgentDetail)))

	mux.Handle("POST /admin/agents/{id}/permissions",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Agents.HandleAgentPermissions)))

	mux.Handle("POST /admin/agents/{id}/webhook",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Agents.HandleAgentWebhook)))

	mux.Handle("POST /admin/agents/{id}/rotate",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Agents.HandleAgentRotateToken)))

	mux.Handle("POST /admin/agents/{id}/delete",
		authedAdminWithCSRF(http.HandlerFunc(r.cfg.Agents.HandleAgentDelete)))
}
