package web

import (
	"context"
	"net/http"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

const (
	sessionCookieName = "mc_session"
	cookieMaxAge      = 86400 // 24 hours in seconds
)

// WebAuthHandler handles login and logout for the web UI.
type WebAuthHandler interface {
	HandleLoginPage(w http.ResponseWriter, r *http.Request)
	HandleLoginSubmit(w http.ResponseWriter, r *http.Request)
	HandleLogout(w http.ResponseWriter, r *http.Request)
}

// defaultWebAuthHandler is the production WebAuthHandler implementation.
type defaultWebAuthHandler struct {
	users    users.UserService
	sessions WebSessionStore
	csrf     CSRFProvider
	bus      eventbus.EventBus
	settings settings.SettingsStore
}

// NewWebAuthHandler constructs a WebAuthHandler.
// bus and settingsStore may be nil; events are silently dropped when nil,
// and registration link falls back to count==0 when settingsStore is nil.
func NewWebAuthHandler(
	userSvc users.UserService,
	sessions WebSessionStore,
	csrf CSRFProvider,
	bus eventbus.EventBus,
	settingsStore settings.SettingsStore,
) WebAuthHandler {
	return &defaultWebAuthHandler{
		users:    userSvc,
		sessions: sessions,
		csrf:     csrf,
		bus:      bus,
		settings: settingsStore,
	}
}

// HandleLoginPage renders the login form (GET /login).
func (h *defaultWebAuthHandler) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	// Create a temporary pre-login session to obtain a CSRF token for the login form.
	entry, err := h.sessions.Create(r.RemoteAddr, 0, "", "", false)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, entry.ID)

	data := templates.LoginPageData{
		CSRFToken:        entry.CSRFToken,
		ShowRegisterLink: h.isRegistrationOpen(r.Context()),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.LoginPage(data).Render(r.Context(), w)
}

// HandleLoginSubmit processes the login form POST.
func (h *defaultWebAuthHandler) HandleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	remoteAddr := r.RemoteAddr

	// Require a pre-login session cookie so CSRF can be validated.
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	preEntry, err := h.sessions.Get(cookie.Value)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Validate CSRF token against the pre-login session before doing anything else.
	if err := h.csrf.Validate(preEntry, r.FormValue("csrf_token")); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	h.emit(r.Context(), eventbus.Event{
		Name: eventbus.EventWebLoginAttempt,
		Payload: eventbus.WebLoginAttemptPayload{
			RemoteAddr: remoteAddr,
			OccurredAt: time.Now().UTC(),
		},
	})

	username := r.FormValue("username")
	password := r.FormValue("password")

	user, authErr := h.users.Authenticate(r.Context(), username, password)
	if authErr != nil {
		h.emit(r.Context(), eventbus.Event{
			Name: eventbus.EventWebLoginFailed,
			Payload: eventbus.WebLoginFailedPayload{
				RemoteAddr: remoteAddr,
				Reason:     "invalid_credential",
				OccurredAt: time.Now().UTC(),
			},
		})
		h.renderLoginWithError(w, r, "Invalid username or password.")
		return
	}

	// Credentials match. Destroy the pre-login session (session fixation prevention)
	// and issue a brand-new authenticated session.
	h.sessions.Delete(preEntry.ID)

	entry, err := h.sessions.Create(remoteAddr, user.ID, string(user.Role), user.Username, user.MustChangePassword)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	setSessionCookie(w, entry.ID)

	h.emit(r.Context(), eventbus.Event{
		Name: eventbus.EventWebLoginSuccess,
		Payload: eventbus.WebLoginSuccessPayload{
			RemoteAddr: remoteAddr,
			SessionID:  entry.ID,
			OccurredAt: time.Now().UTC(),
		},
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// HandleLogout terminates the current session (POST /logout).
func (h *defaultWebAuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	// Look up the current session.
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		clearSessionCookie(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	entry, err := h.sessions.Get(cookie.Value)
	if err != nil {
		clearSessionCookie(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Validate CSRF before proceeding.
	submitted := r.FormValue("csrf_token")
	if err := h.csrf.Validate(entry, submitted); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	h.sessions.Delete(entry.ID)
	clearSessionCookie(w)

	h.emit(r.Context(), eventbus.Event{
		Name: eventbus.EventWebLogout,
		Payload: eventbus.WebLogoutPayload{
			RemoteAddr: r.RemoteAddr,
			SessionID:  entry.ID,
			OccurredAt: time.Now().UTC(),
		},
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// renderLoginWithError re-renders the login page with an error message.
func (h *defaultWebAuthHandler) renderLoginWithError(w http.ResponseWriter, r *http.Request, errMsg string) {
	var csrfToken string
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if entry, err := h.sessions.Get(cookie.Value); err == nil {
			csrfToken = entry.CSRFToken
		}
	}
	if csrfToken == "" {
		entry, err := h.sessions.Create(r.RemoteAddr, 0, "", "", false)
		if err == nil {
			setSessionCookie(w, entry.ID)
			csrfToken = entry.CSRFToken
		}
	}

	data := templates.LoginPageData{
		CSRFToken:        csrfToken,
		Error:            errMsg,
		ShowRegisterLink: h.isRegistrationOpen(r.Context()),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = templates.LoginPage(data).Render(r.Context(), w)
}

// isRegistrationOpen returns true when registration should be available:
// either no users exist yet (initial setup) or RegistrationEnabled is true in settings.
func (h *defaultWebAuthHandler) isRegistrationOpen(ctx context.Context) bool {
	if h.users != nil {
		if count, err := h.users.CountUsers(ctx); err == nil && count == 0 {
			return true
		}
	}
	if h.settings != nil {
		if s, err := h.settings.Load(ctx); err == nil && s.RegistrationEnabled {
			return true
		}
	}
	return false
}

// emit publishes an event if a bus is configured.
func (h *defaultWebAuthHandler) emit(ctx context.Context, e eventbus.Event) {
	if h.bus != nil {
		h.bus.Emit(ctx, e)
	}
}

// setSessionCookie writes the mc_session cookie.
// Secure flag is intentionally omitted so the app works over plain HTTP (dev/local).
// In production behind TLS the browser will still send the cookie; add Secure:true
// via a reverse-proxy "Secure" flag or a future config option if needed.
func setSessionCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie overwrites the mc_session cookie with MaxAge=-1 to delete it.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
