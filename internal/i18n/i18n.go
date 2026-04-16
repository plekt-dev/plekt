// Package i18n provides internationalization support for Plekt.
// It uses go-i18n with embedded JSON locale files and exposes a T() helper
// that extracts the localizer from context for use in Templ templates.
package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

var initOnce sync.Once

//go:embed locales/*.json
var localeFS embed.FS

// SupportedLanguages is the ordered list of language codes the core supports.
var SupportedLanguages = []string{"en", "de", "ru"}

// DefaultLanguage is the fallback language when no preference is detected.
const DefaultLanguage = "en"

// Bundle is the application-wide message bundle, initialized by Init().
// Reads must happen AFTER initOnce.Do has returned (which provides
// happens-before guarantees), or under msgIDsMu.
var bundle *i18n.Bundle

// msgIDsMu guards both bundle reads/writes after initialization and
// allMessageIDs map mutations. go-i18n's Bundle.ParseMessageFileBytes
// is also not safe to call concurrently with Localize.
var msgIDsMu sync.RWMutex

// allMessageIDs tracks every message ID registered into the bundle.
// go-i18n doesn't expose an accessor, so we maintain our own set.
// Always access under msgIDsMu.
var allMessageIDs = make(map[string]struct{})

// Init loads all embedded locale files into the global bundle.
// Safe to call multiple times and from concurrent goroutines.
func Init() {
	initOnce.Do(func() {
		bundle = i18n.NewBundle(language.English)
		bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

		for _, lang := range SupportedLanguages {
			path := "locales/" + lang + ".json"
			if _, err := bundle.LoadMessageFileFS(localeFS, path); err != nil {
				slog.Warn("i18n: failed to load locale file", "path", path, "error", err)
				continue
			}
			if data, err := localeFS.ReadFile(path); err == nil {
				// No lock needed: Once guarantees no other goroutine
				// can access allMessageIDs until Init returns.
				trackIDsFromJSONLocked(data)
			}
		}
	})
}

// LoadPluginMessages merges plugin-provided messages into the bundle.
// data is the raw JSON content of a plugin's locale file, lang is the
// language code (e.g. "en"). Message IDs should be prefixed with the
// plugin name to avoid collisions (e.g. "notes.task_created").
func LoadPluginMessages(lang string, data []byte) error {
	Init()
	msgIDsMu.Lock()
	defer msgIDsMu.Unlock()
	if bundle == nil {
		return nil
	}
	_, err := bundle.ParseMessageFileBytes(data, lang+".json")
	if err == nil {
		trackIDsFromJSONLocked(data)
	}
	return err
}

// trackIDsFromJSONLocked extracts top-level keys from a flat JSON translation
// file and adds them to the allMessageIDs set. Caller MUST hold msgIDsMu.
func trackIDsFromJSONLocked(data []byte) {
	var flat map[string]json.RawMessage
	if err := json.Unmarshal(data, &flat); err != nil {
		return
	}
	for id := range flat {
		allMessageIDs[id] = struct{}{}
	}
}

// NewLocalizer creates a localizer for the given language preferences.
// Languages are tried in order; the first match wins.
//
// Init() is always called first; sync.Once provides happens-before
// semantics so the read of bundle after it returns is race-free.
func NewLocalizer(langs ...string) *i18n.Localizer {
	Init()
	return i18n.NewLocalizer(bundle, langs...)
}

// localizerCtxKey is the context key for the current request's localizer.
type localizerCtxKey struct{}

// langCtxKey is the context key for the current language code string.
type langCtxKey struct{}

// WithLocalizer stores a localizer in the context.
func WithLocalizer(ctx context.Context, loc *i18n.Localizer, lang string) context.Context {
	ctx = context.WithValue(ctx, localizerCtxKey{}, loc)
	ctx = context.WithValue(ctx, langCtxKey{}, lang)
	return ctx
}

// LocalizerFromContext extracts the localizer from context.
// Returns a default English localizer if none is set.
func LocalizerFromContext(ctx context.Context) *i18n.Localizer {
	if loc, ok := ctx.Value(localizerCtxKey{}).(*i18n.Localizer); ok {
		return loc
	}
	return NewLocalizer(DefaultLanguage)
}

// LangFromContext returns the current language code from context (e.g. "en").
func LangFromContext(ctx context.Context) string {
	if lang, ok := ctx.Value(langCtxKey{}).(string); ok {
		return lang
	}
	return DefaultLanguage
}

// T translates a message ID using the localizer in the context.
// Returns the message ID itself if translation is not found.
func T(ctx context.Context, msgID string) string {
	loc := LocalizerFromContext(ctx)
	// RLock: Localizer.Localize reads bundle's internal message map,
	// which LoadPluginMessages mutates under the write lock.
	msgIDsMu.RLock()
	msg, err := loc.Localize(&i18n.LocalizeConfig{MessageID: msgID})
	msgIDsMu.RUnlock()
	if err != nil {
		return msgID
	}
	return msg
}

// DetectLanguage determines the user's preferred language from:
// 1. mc_lang cookie (explicit user choice)
// 2. Accept-Language header
// 3. defaultLang from settings
// Returns a supported language code.
func DetectLanguage(r *http.Request, defaultLang string) string {
	// 1. Cookie
	if cookie, err := r.Cookie("mc_lang"); err == nil {
		lang := normalizeLang(cookie.Value)
		if isSupported(lang) {
			return lang
		}
	}

	// 2. Accept-Language header
	if accept := r.Header.Get("Accept-Language"); accept != "" {
		tags, _, err := language.ParseAcceptLanguage(accept)
		if err == nil {
			for _, tag := range tags {
				lang := normalizeLang(tag.String())
				if isSupported(lang) {
					return lang
				}
			}
		}
	}

	// 3. Settings default
	if defaultLang != "" && isSupported(defaultLang) {
		return defaultLang
	}

	return DefaultLanguage
}

// isSupported checks if a language code is in the supported list.
func isSupported(lang string) bool {
	for _, s := range SupportedLanguages {
		if s == lang {
			return true
		}
	}
	return false
}

// IsSupported checks if a language code is in the supported list (exported).
func IsSupported(lang string) bool {
	return isSupported(lang)
}

// TranslateAll returns all known translations for the given language as a flat
// map[string]string. This includes both core and plugin messages.
// Used to serialize translations as JSON for the client-side JavaScript layer.
func TranslateAll(lang string) map[string]string {
	Init()
	// Hold RLock for the entire Localize loop: LoadPluginMessages can
	// concurrently mutate the bundle's internal message map via
	// ParseMessageFileBytes, and go-i18n's Localizer.Localize reads that
	// same map. Without the lock we race on bundle internals.
	msgIDsMu.RLock()
	defer msgIDsMu.RUnlock()
	loc := i18n.NewLocalizer(bundle, lang)
	result := make(map[string]string, len(allMessageIDs))
	for id := range allMessageIDs {
		msg, err := loc.Localize(&i18n.LocalizeConfig{MessageID: id})
		if err == nil {
			result[id] = msg
		}
	}
	return result
}

// normalizeLang converts a BCP47 tag to our short code (e.g. "de-DE" → "de").
func normalizeLang(tag string) string {
	code := strings.ToLower(strings.SplitN(tag, "-", 2)[0])
	code = strings.SplitN(code, "_", 2)[0]
	return code
}
