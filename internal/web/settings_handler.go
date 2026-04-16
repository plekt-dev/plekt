package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/updater"
	"github.com/plekt-dev/plekt/internal/version"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// WebSettingsHandler handles the global settings admin page.
type WebSettingsHandler interface {
	HandleSettingsPage(w http.ResponseWriter, r *http.Request)
	HandleSettingsSave(w http.ResponseWriter, r *http.Request)
}

// WebSettingsHandlerConfig holds all dependencies for the settings handler.
type WebSettingsHandlerConfig struct {
	Store      settings.SettingsStore
	Sessions   WebSessionStore
	CSRF       CSRFProvider
	Bus        eventbus.EventBus         // nil allowed
	Plugins    loader.PluginManager      // nil allowed; enables plugin settings sections
	Extensions *loader.ExtensionRegistry // nil allowed; enables plugin settings sections
	Updater    *updater.Updater          // nil allowed; enables core update section
}

// defaultWebSettingsHandler is the production implementation.
type defaultWebSettingsHandler struct {
	store      settings.SettingsStore
	sessions   WebSessionStore
	csrf       CSRFProvider
	bus        eventbus.EventBus
	plugins    loader.PluginManager
	extensions *loader.ExtensionRegistry
	updater    *updater.Updater
}

// NewWebSettingsHandler constructs a WebSettingsHandler.
// Returns an error if Store, Sessions, or CSRF is nil.
func NewWebSettingsHandler(cfg WebSettingsHandlerConfig) (WebSettingsHandler, error) {
	if cfg.Store == nil {
		return nil, errors.New("settings handler: Store must not be nil")
	}
	if cfg.Sessions == nil {
		return nil, errors.New("settings handler: Sessions must not be nil")
	}
	if cfg.CSRF == nil {
		return nil, errors.New("settings handler: CSRF must not be nil")
	}
	return &defaultWebSettingsHandler{
		store:      cfg.Store,
		sessions:   cfg.Sessions,
		csrf:       cfg.CSRF,
		bus:        cfg.Bus,
		plugins:    cfg.Plugins,
		extensions: cfg.Extensions,
		updater:    cfg.Updater,
	}, nil
}

// HandleSettingsPage renders the settings form (GET /admin/settings).
func (h *defaultWebSettingsHandler) HandleSettingsPage(w http.ResponseWriter, r *http.Request) {
	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	s, err := h.store.Load(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "settings: failed to load settings", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	csrfToken := h.csrf.TokenForSession(session)
	flashSuccess := r.URL.Query().Get("saved") == "1"

	// Resolve plugin settings sections if plugins and extensions are configured.
	var pluginSections []templates.SettingsSection
	if h.plugins != nil && h.extensions != nil {
		extResults := ResolveExtensions(r.Context(), h.plugins, h.extensions, "core", loader.AdminSettingsExtensionPointOrder(), nil)
		for _, result := range extResults {
			if result.Type != "section" {
				continue
			}
			rawJSON, marshalErr := json.Marshal(result.Data)
			if marshalErr != nil {
				slog.WarnContext(r.Context(), "settings: marshal extension data failed",
					"source", result.SourcePlugin, "error", marshalErr)
				continue
			}
			var section templates.SettingsSection
			if unmarshalErr := json.Unmarshal(rawJSON, &section); unmarshalErr != nil {
				slog.WarnContext(r.Context(), "settings: unmarshal extension section failed",
					"source", result.SourcePlugin, "error", unmarshalErr)
				continue
			}
			section.SourcePlugin = result.SourcePlugin
			// SERVER-SIDE ENFORCE: clear values for write_only fields.
			for i := range section.Fields {
				if section.Fields[i].WriteOnly {
					section.Fields[i].Value = ""
				}
			}
			pluginSections = append(pluginSections, section)
		}
	}

	// Build core update section if updater is configured.
	var coreUpdate *templates.CoreUpdateData
	if h.updater != nil {
		st := h.updater.State()
		coreUpdate = &templates.CoreUpdateData{
			CurrentVersion: version.Version,
			LatestVersion:  st.LatestVersion,
			ReleaseNotes:   st.ReleaseNotes,
			ReleasedAt:     templates.FormatDateTime(st.ReleasedAt),
			Status:         st.Status.String(),
			Error:          st.Error,
			IsDocker:       st.IsDocker,
			CSRFToken:      csrfToken,
		}
	}

	data := templates.SettingsPageData{
		Values:       settingsToFormValues(s),
		Errors:       templates.SettingsFieldError{},
		FlashSuccess: flashSuccess,
		CSRFToken:    csrfToken,
		Sections:     pluginSections,
		CoreUpdate:   coreUpdate,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.SettingsPage(data).Render(r.Context(), w); err != nil {
		slog.WarnContext(r.Context(), "settings: render error", "error", err)
	}
}

// HandleSettingsSave processes the settings form POST (POST /admin/settings).
func (h *defaultWebSettingsHandler) HandleSettingsSave(w http.ResponseWriter, r *http.Request) {
	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	csrfToken := h.csrf.TokenForSession(session)

	// Parse form values.
	formValues := parseSettingsForm(r)

	// Convert to Settings for validation.
	s, parseErrs := formValuesToSettings(formValues)
	if len(parseErrs) > 0 {
		h.renderFormWithErrors(w, r, csrfToken, formValues, parseErrs)
		return
	}

	// Validate settings.
	if validErr := settings.Validate(s); validErr != nil {
		fieldErrs := mapValidationError(validErr)
		h.renderFormWithErrors(w, r, csrfToken, formValues, fieldErrs)
		return
	}

	// Load old settings for diff.
	old, err := h.store.Load(r.Context())
	if err != nil {
		slog.WarnContext(r.Context(), "settings: failed to load old settings for diff", "error", err)
		old = settings.Settings{} // proceed with empty diff
	}

	// Save settings.
	if saveErr := h.store.Save(r.Context(), s); saveErr != nil {
		slog.ErrorContext(r.Context(), "settings: failed to save settings", "error", saveErr)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Emit event with changed keys.
	h.emitSaved(r.Context(), old, s, session.ID, r.RemoteAddr)

	http.Redirect(w, r, "/admin/settings?saved=1", http.StatusSeeOther)
}

// renderFormWithErrors re-renders the settings form with field errors.
func (h *defaultWebSettingsHandler) renderFormWithErrors(
	w http.ResponseWriter,
	r *http.Request,
	csrfToken string,
	formValues templates.SettingsFormValues,
	fieldErrs templates.SettingsFieldError,
) {
	data := templates.SettingsPageData{
		Values:       formValues,
		Errors:       fieldErrs,
		FlashSuccess: false,
		CSRFToken:    csrfToken,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := templates.SettingsPage(data).Render(r.Context(), w); err != nil {
		slog.WarnContext(r.Context(), "settings: render error", "error", err)
	}
}

// emitSaved publishes EventAdminSettingsSaved if a bus is configured.
func (h *defaultWebSettingsHandler) emitSaved(ctx context.Context, old, newS settings.Settings, sessionID, remoteAddr string) {
	if h.bus == nil {
		return
	}
	changed := diffSettings(old, newS)
	h.bus.Emit(ctx, eventbus.Event{
		Name: eventbus.EventAdminSettingsSaved,
		Payload: eventbus.AdminSettingsSavedPayload{
			ChangedKeys:    changed,
			ActorSessionID: sessionID,
			RemoteAddr:     remoteAddr,
			OccurredAt:     time.Now().UTC(),
		},
	})
}

// diffSettings returns the field names that changed between old and new.
func diffSettings(old, new settings.Settings) []string {
	var changed []string
	if old.AdminEmail != new.AdminEmail {
		changed = append(changed, "admin_email")
	}
	if old.AllowPluginInstall != new.AllowPluginInstall {
		changed = append(changed, "allow_plugin_install")
	}
	if old.SessionTTLMinutes != new.SessionTTLMinutes {
		changed = append(changed, "session_ttl_minutes")
	}
	if old.RegistrationEnabled != new.RegistrationEnabled {
		changed = append(changed, "registration_enabled")
	}
	if old.PasswordMinLength != new.PasswordMinLength {
		changed = append(changed, "password_min_length")
	}
	return changed
}

// settingsToFormValues converts a Settings struct to SettingsFormValues.
func settingsToFormValues(s settings.Settings) templates.SettingsFormValues {
	return templates.SettingsFormValues{
		AdminEmail:          s.AdminEmail,
		AllowPluginInstall:  s.AllowPluginInstall,
		SessionTTLMinutes:   strconv.Itoa(s.SessionTTLMinutes),
		RegistrationEnabled: s.RegistrationEnabled,
		PasswordMinLength:   strconv.Itoa(s.PasswordMinLength),
	}
}

// parseSettingsForm reads form fields from r into SettingsFormValues.
func parseSettingsForm(r *http.Request) templates.SettingsFormValues {
	allowPI := r.FormValue("allow_plugin_install")
	regEnabled := r.FormValue("registration_enabled")
	return templates.SettingsFormValues{
		AdminEmail:          r.FormValue("admin_email"),
		AllowPluginInstall:  allowPI == "on" || allowPI == "true" || allowPI == "1",
		SessionTTLMinutes:   r.FormValue("session_ttl_minutes"),
		RegistrationEnabled: regEnabled == "on" || regEnabled == "true" || regEnabled == "1",
		PasswordMinLength:   r.FormValue("password_min_length"),
	}
}

// formValuesToSettings parses form string values into a Settings struct.
// Returns field-level errors for any values that cannot be parsed.
func formValuesToSettings(fv templates.SettingsFormValues) (settings.Settings, templates.SettingsFieldError) {
	errs := templates.SettingsFieldError{}

	sessionTTL := 0
	if fv.SessionTTLMinutes != "" {
		n, err := strconv.Atoi(fv.SessionTTLMinutes)
		if err != nil {
			errs["session_ttl_minutes"] = "session_ttl_minutes must be a whole number"
		} else {
			sessionTTL = n
		}
	}

	passwordMinLen := 12
	if fv.PasswordMinLength != "" {
		n, err := strconv.Atoi(fv.PasswordMinLength)
		if err != nil {
			errs["password_min_length"] = "password_min_length must be a whole number"
		} else {
			passwordMinLen = n
		}
	}

	if len(errs) > 0 {
		return settings.Settings{}, errs
	}

	return settings.Settings{
		AdminEmail:          fv.AdminEmail,
		AllowPluginInstall:  fv.AllowPluginInstall,
		SessionTTLMinutes:   sessionTTL,
		RegistrationEnabled: fv.RegistrationEnabled,
		PasswordMinLength:   passwordMinLen,
	}, nil
}

// mapValidationError maps a settings.Validate error to field-level error messages.
func mapValidationError(err error) templates.SettingsFieldError {
	errs := templates.SettingsFieldError{}
	switch {
	case errors.Is(err, settings.ErrAdminEmailInvalid):
		errs["admin_email"] = err.Error()
	case errors.Is(err, settings.ErrSessionTTLNegative):
		errs["session_ttl_minutes"] = err.Error()
	case errors.Is(err, settings.ErrPasswordMinLengthInvalid):
		errs["password_min_length"] = err.Error()
	default:
		errs["_"] = err.Error()
	}
	return errs
}
