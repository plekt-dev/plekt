package web

import (
	"log/slog"
	"net/http"

	"github.com/plekt-dev/plekt/internal/updater"
	"github.com/plekt-dev/plekt/internal/version"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// WebUpdateHandler handles core update check and apply requests.
type WebUpdateHandler interface {
	HandleCheckUpdate(w http.ResponseWriter, r *http.Request)
	HandleApplyUpdate(w http.ResponseWriter, r *http.Request)
}

// WebUpdateHandlerConfig holds dependencies for the update handler.
type WebUpdateHandlerConfig struct {
	Updater  *updater.Updater
	Sessions WebSessionStore
	CSRF     CSRFProvider
}

type defaultWebUpdateHandler struct {
	updater  *updater.Updater
	sessions WebSessionStore
	csrf     CSRFProvider
}

// NewWebUpdateHandler creates a new WebUpdateHandler.
func NewWebUpdateHandler(cfg WebUpdateHandlerConfig) WebUpdateHandler {
	return &defaultWebUpdateHandler{
		updater:  cfg.Updater,
		sessions: cfg.Sessions,
		csrf:     cfg.CSRF,
	}
}

func (h *defaultWebUpdateHandler) HandleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	h.runAndRespond(w, r, func() {
		if _, err := h.updater.CheckNow(r.Context()); err != nil {
			slog.Warn("update check failed", "error", err)
		}
	})
}

func (h *defaultWebUpdateHandler) HandleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	h.runAndRespond(w, r, func() {
		if err := h.updater.Apply(r.Context()); err != nil {
			slog.WarnContext(r.Context(), "update: apply failed", "error", err)
		}
	})
}

// runAndRespond requires a web session, runs the updater action, and either
// returns the CoreUpdateSection fragment (htmx) or redirects to
// /admin/settings (native form submit) so the user never sees a bare fragment.
func (h *defaultWebUpdateHandler) runAndRespond(w http.ResponseWriter, r *http.Request, action func()) {
	session, err := SessionFromRequest(r)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	action()

	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
		return
	}

	st := h.updater.State()
	data := templates.CoreUpdateData{
		CurrentVersion: version.Version,
		LatestVersion:  st.LatestVersion,
		ReleaseNotes:   st.ReleaseNotes,
		ReleasedAt:     templates.FormatDateTime(st.ReleasedAt),
		Status:         st.Status.String(),
		Error:          st.Error,
		IsDocker:       st.IsDocker,
		CSRFToken:      h.csrf.TokenForSession(session),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.CoreUpdateSection(data).Render(r.Context(), w); err != nil {
		slog.WarnContext(r.Context(), "update: render error", "error", err)
	}
}
