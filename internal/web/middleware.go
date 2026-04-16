package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/plekt-dev/plekt/internal/updater"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// sessionContextKey is the unexported type used as a context key for web sessions.
type sessionContextKeyType struct{}

var sessionContextKey = sessionContextKeyType{}

// WebAuthMiddleware wraps next, requiring a valid "mc_session" cookie.
// On missing or invalid session it redirects to /login with HTTP 303.
// On success it injects the WebSessionEntry into the request context.
func WebAuthMiddleware(sessions WebSessionStore, next http.Handler) http.Handler {
	redirectToLogin := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/login")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("mc_session")
		if err != nil {
			redirectToLogin(w, r)
			return
		}
		entry, err := sessions.Get(cookie.Value)
		if err != nil {
			redirectToLogin(w, r)
			return
		}
		// Reject pre-login sessions (UserID==0). These are created for CSRF
		// on login/register pages and must not grant access to protected routes.
		if entry.UserID == 0 {
			redirectToLogin(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), sessionContextKey, entry)
		ctx = templates.WithUserRole(ctx, entry.Role)
		ctx = templates.WithUsername(ctx, entry.Username)
		r = r.Clone(ctx)
		r.Header.Set("X-CSRF-Token", entry.CSRFToken)
		next.ServeHTTP(w, r)
	})
}

// WebCSRFMiddleware validates the CSRF token on state-mutating HTTP methods.
// GET, HEAD, OPTIONS, and TRACE requests are passed through without validation.
// For all other methods it reads r.FormValue("csrf_token"), looks up the session
// from the "mc_session" cookie, and calls csrf.Validate(). On failure it
// returns HTTP 403 Forbidden.
func WebCSRFMiddleware(sessions WebSessionStore, csrf CSRFProvider, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie("mc_session")
		if err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		entry, err := sessions.Get(cookie.Value)
		if err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		submitted := r.FormValue("csrf_token")
		if submitted == "" {
			submitted = r.Header.Get("X-CSRF-Token")
		}
		if err := csrf.Validate(entry, submitted); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole returns middleware that checks session.Role == required.
// Must be placed AFTER WebAuthMiddleware (session must exist in context).
// Returns 403 Forbidden (not redirect) for wrong role.
func RequireRole(required string, sessions WebSessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := SessionFromRequest(r)
			if err != nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if session.Role != required {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MustChangePasswordMiddleware redirects to /change-password if session.MustChangePassword is true.
// Skips redirect if path is /change-password or /logout.
// Must be placed after WebAuthMiddleware.
func MustChangePasswordMiddleware(sessions WebSessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/change-password" || path == "/logout" {
				next.ServeHTTP(w, r)
				return
			}
			session, err := SessionFromRequest(r)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if session.MustChangePassword {
				http.Redirect(w, r, "/change-password", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// FirstRunUserCounter is the minimal interface needed by FirstRunMiddleware.
type FirstRunUserCounter interface {
	CountUsers(ctx context.Context) (int, error)
}

// FirstRunMiddleware redirects to /register when no users exist in the system.
// Allows /register and /static/ to pass through unconditionally.
// This enables the first-run setup flow without requiring a pre-configured admin.
func FirstRunMiddleware(users FirstRunUserCounter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// Always allow auth pages, static assets, and API/MCP endpoints through.
		// /favicon.ico is included to prevent browser sub-requests from being
		// redirected to /register and overwriting the session cookie.
		if path == "/register" || path == "/login" || path == "/logout" ||
			path == "/favicon.ico" ||
			strings.HasPrefix(path, "/static/") ||
			strings.HasPrefix(path, "/plugins/") ||
			path == "/mcp" || strings.HasPrefix(path, "/mcp/") {
			next.ServeHTTP(w, r)
			return
		}
		count, err := users.CountUsers(r.Context())
		if err != nil || count > 0 {
			next.ServeHTTP(w, r)
			return
		}
		// No users: redirect to register
		http.Redirect(w, r, "/register", http.StatusFound)
	})
}

// CoreUpdateBannerMiddleware injects the core update banner state into the
// request context so that the layout template can render a global update
// notification when a newer version is available.
func CoreUpdateBannerMiddleware(u *updater.Updater) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			st := u.State()
			banner := templates.CoreUpdateBanner{
				Available:     st.Status == updater.StatusAvailable,
				LatestVersion: st.LatestVersion,
				IsDocker:      st.IsDocker,
				Status:        st.Status.String(),
			}
			ctx := templates.WithCoreUpdateBanner(r.Context(), banner)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SecurityHeadersMiddleware sets standard response headers and should be
// applied as the outermost wrapper.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// SessionFromRequest extracts the WebSessionEntry injected by WebAuthMiddleware
// from the request context. Returns ErrWebSessionNotFound if absent.
func SessionFromRequest(r *http.Request) (WebSessionEntry, error) {
	v := r.Context().Value(sessionContextKey)
	if v == nil {
		return WebSessionEntry{}, ErrWebSessionNotFound
	}
	entry, ok := v.(WebSessionEntry)
	if !ok {
		return WebSessionEntry{}, ErrWebSessionNotFound
	}
	return entry, nil
}
