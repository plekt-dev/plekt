package templates

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	mcI18n "github.com/plekt-dev/plekt/internal/i18n"
)

// PluginNavItem represents a plugin page link in the sidebar.
type PluginNavItem struct {
	PluginName string
	PageID     string
	Title      string
	Icon       string
	URL        string
	NavOrder   int
}

type userRoleCtxKey struct{}

// WithUserRole returns a new context carrying the authenticated user's role.
func WithUserRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, userRoleCtxKey{}, role)
}

// UserRoleFromContext extracts the user role from context.
// Returns "" if not set.
func UserRoleFromContext(ctx context.Context) string {
	if role, ok := ctx.Value(userRoleCtxKey{}).(string); ok {
		return role
	}
	return ""
}

// IsAdmin returns true if the context carries an "admin" role.
func IsAdmin(ctx context.Context) bool {
	return UserRoleFromContext(ctx) == "admin"
}

type usernameCtxKey struct{}

// WithUsername returns a new context carrying the authenticated user's username.
func WithUsername(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, usernameCtxKey{}, username)
}

// UsernameFromContext extracts the username from context.
// Returns "" if not set.
func UsernameFromContext(ctx context.Context) string {
	if name, ok := ctx.Value(usernameCtxKey{}).(string); ok {
		return name
	}
	return ""
}

// initials returns the first two uppercase letters of a username, or "?" if empty.
func initials(s string) string {
	runes := []rune(strings.ToUpper(s))
	if len(runes) >= 2 {
		return string(runes[:2])
	}
	if len(runes) == 1 {
		return string(runes)
	}
	return "?"
}

// roleLabel returns a localized role label for the sidebar.
func roleLabel(ctx context.Context) string {
	role := UserRoleFromContext(ctx)
	switch role {
	case "admin":
		return T(ctx, "common.admin")
	default:
		return T(ctx, "users.user")
	}
}

// CoreUpdateBanner carries state for the global update notification banner.
type CoreUpdateBanner struct {
	Available     bool
	LatestVersion string
	IsDocker      bool
	Status        string
}

type coreUpdateBannerCtxKey struct{}

// WithCoreUpdateBanner returns a new context carrying the given update banner state.
func WithCoreUpdateBanner(ctx context.Context, b CoreUpdateBanner) context.Context {
	return context.WithValue(ctx, coreUpdateBannerCtxKey{}, b)
}

// CoreUpdateBannerFromContext extracts the core update banner state from context.
// Returns a zero-value CoreUpdateBanner if not set.
func CoreUpdateBannerFromContext(ctx context.Context) CoreUpdateBanner {
	if b, ok := ctx.Value(coreUpdateBannerCtxKey{}).(CoreUpdateBanner); ok {
		return b
	}
	return CoreUpdateBanner{}
}

// AppName is the hardcoded application name.
const AppName = "Plekt"

// SiteNameFromContext returns the application name.
// The name is hardcoded and cannot be changed at runtime.
func SiteNameFromContext(ctx context.Context) string {
	return AppName
}

type pluginNavCtxKey struct{}

// WithPluginNav returns a new context carrying the given plugin nav items.
func WithPluginNav(ctx context.Context, items []PluginNavItem) context.Context {
	return context.WithValue(ctx, pluginNavCtxKey{}, items)
}

// PluginNavFromContext extracts plugin nav items injected by middleware.
func PluginNavFromContext(ctx context.Context) []PluginNavItem {
	items, _ := ctx.Value(pluginNavCtxKey{}).([]PluginNavItem)
	return items
}

// GlobalScript is a single plugin-global frontend asset entry, mirroring
// internal/web.GlobalScriptEntry. Defined locally to keep the templates
// package free of an internal/web import.
type GlobalScript struct {
	PluginName string
	URL        string
	CSSURL     string
}

type globalScriptsCtxKey struct{}

// WithGlobalScripts returns a new context carrying the given global scripts.
func WithGlobalScripts(ctx context.Context, scripts []GlobalScript) context.Context {
	return context.WithValue(ctx, globalScriptsCtxKey{}, scripts)
}

// GlobalScriptsFromContext extracts the plugin-global scripts injected by
// middleware. Returns nil when no middleware ran (e.g. tests).
func GlobalScriptsFromContext(ctx context.Context) []GlobalScript {
	scripts, _ := ctx.Value(globalScriptsCtxKey{}).([]GlobalScript)
	return scripts
}

// itoa converts an int to its decimal string representation.
// Used in Templ templates where strconv is not directly importable.
func itoa(n int) string {
	return strconv.Itoa(n)
}

// intToString converts an int to its decimal string representation.
func intToString(n int) string {
	return strconv.Itoa(n)
}

// int64ToString converts an int64 to its decimal string representation.
func int64ToString(n int64) string {
	return strconv.FormatInt(n, 10)
}

// firstLetter returns the first UTF-8 character of s uppercased, or "?" if empty.
func firstLetter(s string) string {
	for _, r := range s {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// dateTimeFormat is the display format used throughout template rendering.
const dateTimeFormat = "2006-01-02 15:04:05"

// FormatDateTime formats t into the standard display format used in templates.
// It returns the formatted string in UTC.
func FormatDateTime(t time.Time) string {
	return t.UTC().Format(dateTimeFormat)
}

// MaskToken returns a display-safe masked representation of a bearer token.
// It shows a bullet-prefix followed by the last 4 characters of the token.
// Tokens shorter than 4 characters are fully replaced with bullet characters.
func MaskToken(token string) string {
	const bullets = "••••••••"
	if len(token) <= 4 {
		return bullets
	}
	return bullets + token[len(token)-4:]
}

// TruncatePluginName truncates a plugin name to at most maxLen runes,
// appending "…" if truncation occurred. Returns the original name when
// maxLen is zero or the name is already within the limit.
func TruncatePluginName(name string, maxLen int) string {
	if maxLen <= 0 {
		return name
	}
	runes := []rune(name)
	if len(runes) <= maxLen {
		return name
	}
	return string(runes[:maxLen]) + "…"
}

// LogoutURL returns the logout endpoint path.
// Provided as a package-level constant function so templates can reference it
// symbolically rather than with a hard-coded string literal.
func LogoutURL() string {
	return "/logout"
}

// LoginURL returns the login endpoint path.
func LoginURL() string {
	return "/login"
}

// JSONCSRFToken returns a JSON object string {"csrf_token":"<token>"} safe for
// use in hx-vals attributes. It uses encoding/json to properly escape any
// special characters in the token value, preventing JSON injection.
func JSONCSRFToken(token string) string {
	b, err := json.Marshal(map[string]string{"csrf_token": token})
	if err != nil {
		// json.Marshal on a map[string]string never errors in practice,
		// but fall back to a safe empty object rather than panic.
		return `{"csrf_token":""}`
	}
	return string(b)
}

// T translates a message ID using the localizer stored in the request context.
// Falls back to returning the message ID if no translation is found.
func T(ctx context.Context, msgID string) string {
	return mcI18n.T(ctx, msgID)
}

// Lang returns the current language code from context (e.g. "en", "de", "ru").
func Lang(ctx context.Context) string {
	return mcI18n.LangFromContext(ctx)
}

// TNav translates a plugin nav item title. Tries key "{plugin}.nav.{pageID}"
// first, falls back to the original title from manifest.json.
func TNav(ctx context.Context, pluginName, pageID, fallbackTitle string) string {
	key := pluginName + ".nav." + pageID
	translated := mcI18n.T(ctx, key)
	if translated == key {
		return fallbackTitle
	}
	return translated
}

// I18nJSON returns all translations for the current language as a JSON string.
// Used to inject translations into the JS layer via a <script> tag in the layout.
func I18nJSON(ctx context.Context) string {
	lang := mcI18n.LangFromContext(ctx)
	translations := mcI18n.TranslateAll(lang)
	if translations == nil {
		return "{}"
	}
	data, err := json.Marshal(translations)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// langLabel returns the display label for a language code (e.g. "en" → "English").
func langLabel(code string) string {
	switch code {
	case "de":
		return "Deutsch"
	case "ru":
		return "Русский"
	default:
		return "English"
	}
}

// boolStr returns "true" or "false" as a string for use in HTML attributes.
func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// maxInt returns the larger of a or b.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// auditBadgeClass returns the CSS badge class for a given audit event name.
func auditBadgeClass(eventName string) string {
	switch {
	case strings.HasPrefix(eventName, "web.auth.login_failed"),
		strings.HasPrefix(eventName, "auth.token.validation_failed"),
		strings.HasPrefix(eventName, "plugin.error"),
		strings.HasPrefix(eventName, "plugin.migration.failed"),
		strings.HasPrefix(eventName, "plugin.scan.failed"),
		strings.HasPrefix(eventName, "dashboard.widget.fetch_error"):
		return "badge-error"
	case strings.HasPrefix(eventName, "web.auth."):
		return "badge-auth"
	case strings.HasPrefix(eventName, "auth.token."):
		return "badge-token"
	case strings.HasPrefix(eventName, "web.admin."):
		return "badge-admin"
	case strings.HasPrefix(eventName, "web.user."):
		return "badge-user"
	case strings.HasPrefix(eventName, "web.plugin."), strings.HasPrefix(eventName, "plugin."):
		return "badge-plugin"
	case strings.HasPrefix(eventName, "mcp."):
		return "badge-mcp"
	case strings.HasPrefix(eventName, "task."), strings.HasPrefix(eventName, "comment."):
		return "badge-task"
	case strings.HasPrefix(eventName, "notes."):
		return "badge-notes"
	case strings.HasPrefix(eventName, "project."):
		return "badge-project"
	case strings.HasPrefix(eventName, "pomodoro."):
		return "badge-pomodoro"
	case strings.HasPrefix(eventName, "dashboard."):
		return "badge-dashboard"
	default:
		return "badge-default"
	}
}

// SanitizePluginName replaces characters that are unsafe in URL path segments
// with a hyphen. Only lowercase ASCII letters, digits, and hyphens are kept.
func SanitizePluginName(name string) string {
	var sb strings.Builder
	sb.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			sb.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	result := sb.String()
	// Collapse consecutive hyphens.
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	// Trim leading and trailing hyphens.
	result = strings.Trim(result, "-")
	return result
}
