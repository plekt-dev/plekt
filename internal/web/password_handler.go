package web

import (
	"log/slog"
	"net/http"

	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// WebPasswordHandler handles password change operations.
type WebPasswordHandler interface {
	HandleChangePasswordPage(w http.ResponseWriter, r *http.Request)
	HandleChangePasswordSubmit(w http.ResponseWriter, r *http.Request)
}

// defaultWebPasswordHandler is the production implementation.
type defaultWebPasswordHandler struct {
	users    users.UserService
	sessions WebSessionStore
	csrf     CSRFProvider
	settings settings.SettingsStore
}

// NewWebPasswordHandler constructs a WebPasswordHandler.
func NewWebPasswordHandler(
	userSvc users.UserService,
	sessions WebSessionStore,
	csrf CSRFProvider,
	settingsStore settings.SettingsStore,
) WebPasswordHandler {
	return &defaultWebPasswordHandler{
		users:    userSvc,
		sessions: sessions,
		csrf:     csrf,
		settings: settingsStore,
	}
}

// HandleChangePasswordPage renders the change password form (GET /change-password).
func (h *defaultWebPasswordHandler) HandleChangePasswordPage(w http.ResponseWriter, r *http.Request) {
	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	csrfToken := h.csrf.TokenForSession(session)
	data := templates.ChangePasswordPageData{
		CSRFToken: csrfToken,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.ChangePasswordPage(data).Render(r.Context(), w)
}

// HandleChangePasswordSubmit processes the change password form (POST /change-password).
func (h *defaultWebPasswordHandler) HandleChangePasswordSubmit(w http.ResponseWriter, r *http.Request) {
	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	currentPw := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirmPw := r.FormValue("confirm_password")

	if newPw != confirmPw {
		h.renderWithError(w, r, session, "Passwords do not match.")
		return
	}

	minLen := h.minPasswordLength(r)
	if err := h.users.ChangePassword(r.Context(), session.UserID, currentPw, newPw, minLen); err != nil {
		h.renderWithError(w, r, session, "Failed to change password: "+err.Error())
		return
	}

	// Clear MustChangePassword in session.
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		if sessErr := h.sessions.SetMustChangePassword(cookie.Value, false); sessErr != nil {
			slog.Warn("password change: failed to clear must_change_password flag", "error", sessErr)
		}
	}

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// minPasswordLength returns the minimum password length from settings.
func (h *defaultWebPasswordHandler) minPasswordLength(r *http.Request) int {
	if h.settings != nil {
		s, err := h.settings.Load(r.Context())
		if err == nil && s.PasswordMinLength > 0 {
			return s.PasswordMinLength
		}
	}
	return 12
}

// renderWithError re-renders the change password page with an error.
func (h *defaultWebPasswordHandler) renderWithError(w http.ResponseWriter, r *http.Request, session WebSessionEntry, errMsg string) {
	csrfToken := h.csrf.TokenForSession(session)
	data := templates.ChangePasswordPageData{
		CSRFToken: csrfToken,
		Error:     errMsg,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = templates.ChangePasswordPage(data).Render(r.Context(), w)
}
