package web

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/firstrun"
	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// WebRegisterHandler handles user self-registration.
// It is gated: open if settings.RegistrationEnabled OR users.CountUsers()==0.
type WebRegisterHandler interface {
	HandleRegisterPage(w http.ResponseWriter, r *http.Request)
	HandleRegisterSubmit(w http.ResponseWriter, r *http.Request)
}

// defaultWebRegisterHandler is the production implementation.
type defaultWebRegisterHandler struct {
	users    users.UserService
	sessions WebSessionStore
	csrf     CSRFProvider
	settings settings.SettingsStore
	bus      eventbus.EventBus
}

// NewWebRegisterHandler constructs a WebRegisterHandler.
func NewWebRegisterHandler(
	userSvc users.UserService,
	sessions WebSessionStore,
	csrf CSRFProvider,
	settingsStore settings.SettingsStore,
	bus eventbus.EventBus,
) WebRegisterHandler {
	return &defaultWebRegisterHandler{
		users:    userSvc,
		sessions: sessions,
		csrf:     csrf,
		settings: settingsStore,
		bus:      bus,
	}
}

// HandleRegisterPage renders the registration form (GET /register).
func (h *defaultWebRegisterHandler) HandleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if !h.registrationAllowed(r.Context()) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	// Reuse existing pre-login session if the cookie is still valid.
	// This prevents a race where browser sub-requests (e.g. /favicon.ico)
	// are redirected here by FirstRunMiddleware and overwrite the cookie
	// with a new session, invalidating the CSRF token already in the form.
	var entry WebSessionEntry
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if existing, err := h.sessions.Get(cookie.Value); err == nil {
			entry = existing
		}
	}
	if entry.ID == "" {
		var err error
		entry, err = h.sessions.Create(r.RemoteAddr, 0, "", "", false)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, entry.ID)
	}

	minLen := h.minPasswordLength(r.Context())

	// Show "Sign in" link only when there are existing users (i.e. registration
	// is open via settings, not first-run). During first-run there are no
	// accounts to sign into.
	showSignIn := false
	if count, err := h.users.CountUsers(r.Context()); err == nil && count > 0 {
		showSignIn = true
	}

	// Show setup token field when no users exist (first-user registration).
	showSetupToken := false
	if count, err := h.users.CountUsers(r.Context()); err == nil && count == 0 {
		showSetupToken = true
	}

	data := templates.RegisterPageData{
		CSRFToken:         entry.CSRFToken,
		PasswordMinLength: minLen,
		ShowSignInLink:    showSignIn,
		ShowSetupToken:    showSetupToken,
	}

	// Show error from query param (e.g. after CSRF/session redirect).
	switch r.URL.Query().Get("error") {
	case "no_cookie":
		data.Error = "Session cookie missing. Please try again."
	case "session_not_found":
		data.Error = "Session expired. Please try again."
	case "csrf_mismatch":
		data.Error = "Security token mismatch. Please try again."
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Prevent browser from caching this page: stale CSRF tokens cause 403 on submit.
	w.Header().Set("Cache-Control", "no-store")
	_ = templates.RegisterPage(data).Render(r.Context(), w)
}

// HandleRegisterSubmit processes the registration form POST (POST /register).
func (h *defaultWebRegisterHandler) HandleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.registrationAllowed(r.Context()) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Validate CSRF. If the session is missing (e.g. server restart cleared
	// in-memory sessions), redirect back to GET /register so the user gets a
	// fresh session instead of a cryptic 403.
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		log.Printf("register: no session cookie: %v", err)
		http.Redirect(w, r, "/register?error=no_cookie", http.StatusSeeOther)
		return
	}
	preEntry, err := h.sessions.Get(cookie.Value)
	if err != nil {
		log.Printf("register: session not found: %v", err)
		http.Redirect(w, r, "/register?error=session_not_found", http.StatusSeeOther)
		return
	}
	if err := h.csrf.Validate(preEntry, r.FormValue("csrf_token")); err != nil {
		log.Printf("register: CSRF mismatch")
		http.Redirect(w, r, "/register?error=csrf_mismatch", http.StatusSeeOther)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	confirmPassword := r.FormValue("confirm_password")

	minLen := h.minPasswordLength(r.Context())

	if password != confirmPassword {
		h.renderRegisterWithError(w, r, "Passwords do not match.", minLen)
		return
	}

	// First user gets admin role.
	count, err := h.users.CountUsers(r.Context())
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	role := users.RoleUser
	if count == 0 {
		role = users.RoleAdmin

		// VULN-03: Validate setup token for first-user registration.
		if err := firstrun.ValidateToken(r.Context(), h.settings, r.FormValue("setup_token")); err != nil {
			h.renderRegisterWithError(w, r, "Invalid setup token", minLen)
			return
		}
	}

	user, err := h.users.Create(r.Context(), username, password, role, false)
	if err != nil {
		h.renderRegisterWithError(w, r, "Registration failed: "+err.Error(), minLen)
		return
	}

	// Clean up the setup token hash after successful first-user registration.
	if count == 0 {
		firstrun.DeleteToken(r.Context(), h.settings)
	}

	// Session fixation: destroy pre-session, create authenticated session.
	h.sessions.Delete(preEntry.ID)
	entry, err := h.sessions.Create(r.RemoteAddr, user.ID, string(user.Role), user.Username, user.MustChangePassword)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, entry.ID)

	h.emit(r.Context(), eventbus.Event{
		Name: eventbus.EventUserCreated,
		Payload: eventbus.UserCreatedPayload{
			UserID:    user.ID,
			Username:  user.Username,
			Role:      string(user.Role),
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
	})

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// registrationAllowed returns true when registration is open.
func (h *defaultWebRegisterHandler) registrationAllowed(ctx context.Context) bool {
	count, err := h.users.CountUsers(ctx)
	if err == nil && count == 0 {
		return true
	}
	if h.settings != nil {
		s, err := h.settings.Load(ctx)
		if err == nil && s.RegistrationEnabled {
			return true
		}
	}
	return false
}

// minPasswordLength retrieves the configured minimum password length from settings.
func (h *defaultWebRegisterHandler) minPasswordLength(ctx context.Context) int {
	if h.settings != nil {
		s, err := h.settings.Load(ctx)
		if err == nil && s.PasswordMinLength > 0 {
			return s.PasswordMinLength
		}
	}
	return 12
}

// renderRegisterWithError re-renders the registration page with an error message.
func (h *defaultWebRegisterHandler) renderRegisterWithError(w http.ResponseWriter, r *http.Request, errMsg string, minLen int) {
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
	showSignIn := false
	showSetupToken := false
	if count, err := h.users.CountUsers(r.Context()); err == nil {
		if count > 0 {
			showSignIn = true
		} else {
			showSetupToken = true
		}
	}

	data := templates.RegisterPageData{
		CSRFToken:         csrfToken,
		Error:             errMsg,
		PasswordMinLength: minLen,
		ShowSignInLink:    showSignIn,
		ShowSetupToken:    showSetupToken,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = templates.RegisterPage(data).Render(r.Context(), w)
}

// emit publishes an event if a bus is configured.
func (h *defaultWebRegisterHandler) emit(ctx context.Context, e eventbus.Event) {
	if h.bus != nil {
		h.bus.Emit(ctx, e)
	}
}
