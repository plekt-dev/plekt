package web

import (
	"net/http"

	"github.com/plekt-dev/plekt/internal/i18n"
	"github.com/plekt-dev/plekt/internal/settings"
)

// LocaleMiddleware detects the user's preferred language and injects
// an i18n.Localizer into the request context. Detection order:
// 1. mc_lang cookie (explicit user choice via language switcher)
// 2. Fallback: "en"
// Browser Accept-Language is intentionally ignored so a fresh session
// always lands on English until the user picks otherwise.
func LocaleMiddleware(store settings.SettingsStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lang := i18n.DetectLanguage(r, "en")
			loc := i18n.NewLocalizer(lang)
			ctx := i18n.WithLocalizer(r.Context(), loc, lang)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// HandleLanguageSwitch handles POST /api/language: sets mc_lang cookie and
// returns HX-Refresh header so htmx reloads the page in the new language.
func HandleLanguageSwitch(w http.ResponseWriter, r *http.Request) {
	lang := r.FormValue("language")
	if !i18n.IsSupported(lang) {
		lang = i18n.DefaultLanguage
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "mc_lang",
		Value:    lang,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
