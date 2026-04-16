package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/web"
)

// --- stubs ---

type stubSettingsStore struct {
	loaded  settings.Settings
	saved   *settings.Settings
	loadErr error
	saveErr error
}

func (s *stubSettingsStore) Load(_ context.Context) (settings.Settings, error) {
	return s.loaded, s.loadErr
}

func (s *stubSettingsStore) Save(_ context.Context, st settings.Settings) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = &st
	return nil
}

func (s *stubSettingsStore) GetRaw(_ context.Context, key string) (string, error) {
	return "", settings.ErrSettingNotFound
}
func (s *stubSettingsStore) SetRaw(_ context.Context, key, value string) error { return nil }
func (s *stubSettingsStore) DeleteRaw(_ context.Context, key string) error     { return nil }
func (s *stubSettingsStore) Close() error                                      { return nil }

// newSettingsSession creates a real session in store and returns its cookie value.
func newSettingsSession(t *testing.T, sessions web.WebSessionStore) string {
	t.Helper()
	entry, err := sessions.Create("127.0.0.1:0", 0, "", "", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return entry.ID
}

func buildSettingsHandler(t *testing.T, store settings.SettingsStore) (web.WebSettingsHandler, web.WebSessionStore) {
	t.Helper()
	sessions, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	csrf := web.NewCSRFProvider()
	bus := eventbus.NewInMemoryBus()
	h, err := web.NewWebSettingsHandler(web.WebSettingsHandlerConfig{
		Store:    store,
		Sessions: sessions,
		CSRF:     csrf,
		Bus:      bus,
	})
	if err != nil {
		t.Fatalf("NewWebSettingsHandler: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })
	return h, sessions
}

// --- constructor tests ---

func TestNewWebSettingsHandler_NilStore(t *testing.T) {
	sessions, _ := web.NewInMemoryWebSessionStore()
	defer sessions.Close()
	_, err := web.NewWebSettingsHandler(web.WebSettingsHandlerConfig{
		Store:    nil,
		Sessions: sessions,
		CSRF:     web.NewCSRFProvider(),
		Bus:      eventbus.NewInMemoryBus(),
	})
	if err == nil {
		t.Fatal("expected error for nil Store, got nil")
	}
}

func TestNewWebSettingsHandler_NilSessions(t *testing.T) {
	_, err := web.NewWebSettingsHandler(web.WebSettingsHandlerConfig{
		Store:    &stubSettingsStore{},
		Sessions: nil,
		CSRF:     web.NewCSRFProvider(),
		Bus:      eventbus.NewInMemoryBus(),
	})
	if err == nil {
		t.Fatal("expected error for nil Sessions, got nil")
	}
}

func TestNewWebSettingsHandler_NilCSRF(t *testing.T) {
	sessions, _ := web.NewInMemoryWebSessionStore()
	defer sessions.Close()
	_, err := web.NewWebSettingsHandler(web.WebSettingsHandlerConfig{
		Store:    &stubSettingsStore{},
		Sessions: sessions,
		CSRF:     nil,
		Bus:      eventbus.NewInMemoryBus(),
	})
	if err == nil {
		t.Fatal("expected error for nil CSRF, got nil")
	}
}

func TestNewWebSettingsHandler_NilBusAllowed(t *testing.T) {
	sessions, _ := web.NewInMemoryWebSessionStore()
	defer sessions.Close()
	_, err := web.NewWebSettingsHandler(web.WebSettingsHandlerConfig{
		Store:    &stubSettingsStore{},
		Sessions: sessions,
		CSRF:     web.NewCSRFProvider(),
		Bus:      nil,
	})
	if err != nil {
		t.Fatalf("expected nil error for nil Bus, got %v", err)
	}
}

// --- HandleSettingsPage tests ---

func TestHandleSettingsPage_RedirectsWhenNoSession(t *testing.T) {
	store := &stubSettingsStore{}
	h, _ := buildSettingsHandler(t, store)

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rr := httptest.NewRecorder()
	h.HandleSettingsPage(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got %d, want %d", rr.Code, http.StatusSeeOther)
	}
}

func TestHandleSettingsPage_RendersForm(t *testing.T) {
	store := &stubSettingsStore{
		loaded: settings.Settings{

			PasswordMinLength: 12,
		},
	}
	h, sessions := buildSettingsHandler(t, store)

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Settings") {
		t.Errorf("body missing Settings heading")
	}
}

func TestHandleSettingsPage_FlashSuccess(t *testing.T) {
	store := &stubSettingsStore{
		loaded: settings.Settings{PasswordMinLength: 12},
	}
	h, sessions := buildSettingsHandler(t, store)

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	req := httptest.NewRequest(http.MethodGet, "/admin/settings?saved=1", nil)
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "saved") && !strings.Contains(body, "success") && !strings.Contains(body, "Settings saved") {
		t.Log("body snippet:", body[:minLen(500, len(body))])
		t.Errorf("body should contain flash success indicator when ?saved=1")
	}
}

func TestHandleSettingsPage_StoreLoadError(t *testing.T) {
	store := &stubSettingsStore{loadErr: errors.New("db offline")}
	h, sessions := buildSettingsHandler(t, store)

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsPage(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on store load error, got %d", rr.Code)
	}
}

// --- HandleSettingsSave tests ---

func TestHandleSettingsSave_RedirectsWhenNoSession(t *testing.T) {
	store := &stubSettingsStore{}
	h, _ := buildSettingsHandler(t, store)

	form := url.Values{"admin_email": {"test@example.com"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.HandleSettingsSave(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got %d, want 303", rr.Code)
	}
}

func TestHandleSettingsSave_ValidationError_ReRendersForm(t *testing.T) {
	store := &stubSettingsStore{
		loaded: settings.Settings{PasswordMinLength: 12},
	}
	h, sessions := buildSettingsHandler(t, store)

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	form := url.Values{
		"csrf_token":          {entry.CSRFToken},
		"admin_email":         {"notanemail"},
		"session_ttl_minutes": {"30"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsSave(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 re-render on validation error, got %d", rr.Code)
	}
	if store.saved != nil {
		t.Error("Save must not be called when validation fails")
	}
}

func TestHandleSettingsSave_Success_Redirects(t *testing.T) {
	store := &stubSettingsStore{
		loaded: settings.Settings{PasswordMinLength: 12},
	}
	h, sessions := buildSettingsHandler(t, store)

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	form := url.Values{
		"csrf_token":           {entry.CSRFToken},
		"admin_email":          {"admin@example.com"},
		"allow_plugin_install": {"on"},
		"session_ttl_minutes":  {"30"},
		"password_min_length":  {"10"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsSave(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect on success, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "/admin/settings") || !strings.Contains(loc, "saved=1") {
		t.Errorf("redirect location %q must be /admin/settings?saved=1", loc)
	}
	if store.saved == nil {
		t.Fatal("Save must be called on success")
	}
	if store.saved.AdminEmail != "admin@example.com" {
		t.Errorf("saved AdminEmail = %q, want admin@example.com", store.saved.AdminEmail)
	}
}

func TestHandleSettingsSave_StoreError_Returns500(t *testing.T) {
	store := &stubSettingsStore{
		loaded:  settings.Settings{PasswordMinLength: 12},
		saveErr: errors.New("disk full"),
	}
	h, sessions := buildSettingsHandler(t, store)

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	form := url.Values{
		"csrf_token": {entry.CSRFToken},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsSave(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on store error, got %d", rr.Code)
	}
}

func TestHandleSettingsSave_EmitsEvent(t *testing.T) {
	store := &stubSettingsStore{
		loaded: settings.Settings{PasswordMinLength: 12},
	}
	sessions, _ := web.NewInMemoryWebSessionStore()
	defer sessions.Close()
	csrf := web.NewCSRFProvider()
	bus := eventbus.NewInMemoryBus()

	received := make(chan eventbus.Event, 1)
	bus.Subscribe(eventbus.EventAdminSettingsSaved, func(_ context.Context, e eventbus.Event) {
		received <- e
	})

	h, err := web.NewWebSettingsHandler(web.WebSettingsHandlerConfig{
		Store:    store,
		Sessions: sessions,
		CSRF:     csrf,
		Bus:      bus,
	})
	if err != nil {
		t.Fatalf("NewWebSettingsHandler: %v", err)
	}

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	form := url.Values{
		"csrf_token": {entry.CSRFToken},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsSave(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", rr.Code)
	}

	select {
	case evt := <-received:
		payload, ok := evt.Payload.(eventbus.AdminSettingsSavedPayload)
		if !ok {
			t.Fatalf("wrong payload type: %T", evt.Payload)
		}
		if payload.ActorSessionID != sessionID {
			t.Errorf("ActorSessionID = %q, want %q", payload.ActorSessionID, sessionID)
		}
		if payload.OccurredAt.IsZero() {
			t.Error("OccurredAt must not be zero")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestHandleSettingsSave_InvalidAdminEmail_ReRendersForm(t *testing.T) {
	store := &stubSettingsStore{
		loaded: settings.Settings{PasswordMinLength: 12},
	}
	h, sessions := buildSettingsHandler(t, store)

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	form := url.Values{
		"csrf_token":  {entry.CSRFToken},
		"admin_email": {"notanemail"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsSave(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 re-render for invalid admin_email, got %d", rr.Code)
	}
	if store.saved != nil {
		t.Error("Save must not be called when admin_email is invalid")
	}
}

func TestHandleSettingsSave_NegativeSessionTTL_ReRendersForm(t *testing.T) {
	store := &stubSettingsStore{
		loaded: settings.Settings{PasswordMinLength: 12},
	}
	h, sessions := buildSettingsHandler(t, store)

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	form := url.Values{
		"csrf_token":          {entry.CSRFToken},
		"session_ttl_minutes": {"-10"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsSave(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 re-render for negative session_ttl_minutes, got %d", rr.Code)
	}
	if store.saved != nil {
		t.Error("Save must not be called when session_ttl_minutes is negative")
	}
}

func TestHandleSettingsSave_NilBus_SucceedsWithoutPanic(t *testing.T) {
	store := &stubSettingsStore{
		loaded: settings.Settings{PasswordMinLength: 12},
	}
	sessions, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	defer sessions.Close()
	csrf := web.NewCSRFProvider()

	h, err := web.NewWebSettingsHandler(web.WebSettingsHandlerConfig{
		Store:    store,
		Sessions: sessions,
		CSRF:     csrf,
		Bus:      nil,
	})
	if err != nil {
		t.Fatalf("NewWebSettingsHandler: %v", err)
	}

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	form := url.Values{
		"csrf_token": {entry.CSRFToken},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsSave(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect with nil bus, got %d", rr.Code)
	}
}

func TestHandleSettingsSave_NonNumericSessionTTL_ReRendersForm(t *testing.T) {
	store := &stubSettingsStore{
		loaded: settings.Settings{PasswordMinLength: 12},
	}
	h, sessions := buildSettingsHandler(t, store)

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	form := url.Values{
		"csrf_token":          {entry.CSRFToken},
		"session_ttl_minutes": {"xyz"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsSave(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 re-render for non-numeric session_ttl_minutes, got %d", rr.Code)
	}
	if store.saved != nil {
		t.Error("Save must not be called when session_ttl_minutes cannot be parsed")
	}
}

func TestHandleSettingsSave_NonNumericPasswordMinLength_ReRendersForm(t *testing.T) {
	store := &stubSettingsStore{
		loaded: settings.Settings{PasswordMinLength: 12},
	}
	h, sessions := buildSettingsHandler(t, store)

	sessionID := newSettingsSession(t, sessions)
	entry, _ := sessions.Get(sessionID)

	form := url.Values{
		"csrf_token":          {entry.CSRFToken},
		"password_min_length": {"abc"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = injectSession(req, entry)

	rr := httptest.NewRecorder()
	h.HandleSettingsSave(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 re-render for non-numeric password_min_length, got %d", rr.Code)
	}
	if store.saved != nil {
		t.Error("Save must not be called when password_min_length cannot be parsed")
	}
}

// minLen returns the smaller of a and b.
func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Plugin extension tests ---

// fakePluginManager is a stubPluginManager that lets tests control CallPlugin output.
type fakePluginManager struct {
	stubPluginManager
	callResponses map[string][]byte // key: pluginName+":"+function
	callErrors    map[string]error
}

func (f *fakePluginManager) CallPlugin(_ context.Context, name, function string, _ []byte) ([]byte, error) {
	key := name + ":" + function
	if err, ok := f.callErrors[key]; ok {
		return nil, err
	}
	if data, ok := f.callResponses[key]; ok {
		return data, nil
	}
	return nil, nil
}

func buildSettingsHandlerWithExtensions(
	t *testing.T,
	store settings.SettingsStore,
	pm loader.PluginManager,
	reg *loader.ExtensionRegistry,
) (web.WebSettingsHandler, web.WebSessionStore) {
	t.Helper()
	sessions, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	csrf := web.NewCSRFProvider()
	bus := eventbus.NewInMemoryBus()
	h, err := web.NewWebSettingsHandler(web.WebSettingsHandlerConfig{
		Store:      store,
		Sessions:   sessions,
		CSRF:       csrf,
		Bus:        bus,
		Plugins:    pm,
		Extensions: reg,
	})
	if err != nil {
		t.Fatalf("NewWebSettingsHandler: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })
	return h, sessions
}

func TestSettingsHandler_RendersPluginExtensions(t *testing.T) {
	validSectionJSON := `{
		"title": "Voice Transcription",
		"description": "Configure voice backend.",
		"submit_action": "save_settings_section",
		"fields": [
			{"name": "backend_mode", "type": "select", "label": "Backend mode", "value": "local",
			 "options": [{"value": "local", "label": "Local"}, {"value": "api", "label": "API"}]},
			{"name": "openai_api_key", "type": "password", "label": "API Key", "value": "secret123", "write_only": true}
		]
	}`

	tests := []struct {
		name        string
		setupReg    func(reg *loader.ExtensionRegistry)
		setupPM     func(pm *fakePluginManager)
		wantInBody  []string
		notWantBody []string
		checkOrder  []string // if set, assert these strings appear in order
	}{
		{
			name:       "no extensions: page renders without plugin sections",
			setupReg:   func(reg *loader.ExtensionRegistry) {},
			setupPM:    func(pm *fakePluginManager) {},
			wantInBody: []string{"Settings"},
		},
		{
			name: "valid section: rendered with title and fields",
			setupReg: func(reg *loader.ExtensionRegistry) {
				reg.Register("voice-plugin", []loader.ExtensionDescriptor{
					{
						Point:        loader.ExtPointAdminSettingsIntegrations,
						TargetPlugin: "core",
						DataFunction: "get_settings_section",
						Type:         "section",
					},
				})
			},
			setupPM: func(pm *fakePluginManager) {
				pm.callResponses = map[string][]byte{
					"voice-plugin:get_settings_section": []byte(validSectionJSON),
				}
			},
			wantInBody: []string{"Voice Transcription", "voice-plugin", "backend_mode"},
		},
		{
			name: "call fails: section skipped, page still renders",
			setupReg: func(reg *loader.ExtensionRegistry) {
				reg.Register("voice-plugin", []loader.ExtensionDescriptor{
					{
						Point:        loader.ExtPointAdminSettingsIntegrations,
						TargetPlugin: "core",
						DataFunction: "get_settings_section",
						Type:         "section",
					},
				})
			},
			setupPM: func(pm *fakePluginManager) {
				pm.callErrors = map[string]error{
					"voice-plugin:get_settings_section": errors.New("plugin unavailable"),
				}
			},
			wantInBody: []string{"Settings"},
		},
		{
			name: "malformed JSON: section skipped, page still renders",
			setupReg: func(reg *loader.ExtensionRegistry) {
				reg.Register("voice-plugin", []loader.ExtensionDescriptor{
					{
						Point:        loader.ExtPointAdminSettingsIntegrations,
						TargetPlugin: "core",
						DataFunction: "get_settings_section",
						Type:         "section",
					},
				})
			},
			setupPM: func(pm *fakePluginManager) {
				pm.callResponses = map[string][]byte{
					"voice-plugin:get_settings_section": []byte(`{broken json`),
				}
			},
			wantInBody: []string{"Settings"},
		},
		{
			name: "write_only field: secret value not in HTML",
			setupReg: func(reg *loader.ExtensionRegistry) {
				reg.Register("voice-plugin", []loader.ExtensionDescriptor{
					{
						Point:        loader.ExtPointAdminSettingsIntegrations,
						TargetPlugin: "core",
						DataFunction: "get_settings_section",
						Type:         "section",
					},
				})
			},
			setupPM: func(pm *fakePluginManager) {
				pm.callResponses = map[string][]byte{
					"voice-plugin:get_settings_section": []byte(validSectionJSON),
				}
			},
			wantInBody:  []string{"Voice Transcription"},
			notWantBody: []string{"secret123"},
		},
		{
			name: "two sections: both rendered in order",
			setupReg: func(reg *loader.ExtensionRegistry) {
				reg.Register("plugin-a", []loader.ExtensionDescriptor{
					{Point: loader.ExtPointAdminSettingsGeneral, TargetPlugin: "core", DataFunction: "fn", Type: "section"},
				})
				reg.Register("plugin-b", []loader.ExtensionDescriptor{
					{Point: loader.ExtPointAdminSettingsIntegrations, TargetPlugin: "core", DataFunction: "fn", Type: "section"},
				})
			},
			setupPM: func(pm *fakePluginManager) {
				pm.callResponses = map[string][]byte{
					"plugin-a:fn": []byte(`{"title": "Section A", "submit_action": "save_a", "fields": []}`),
					"plugin-b:fn": []byte(`{"title": "Section B", "submit_action": "save_b", "fields": []}`),
				}
			},
			wantInBody: []string{"Section A", "Section B"},
			checkOrder: []string{"Section A", "Section B"},
		},
		{
			name: "non-object data: unmarshal into SettingsSection fails, section skipped",
			setupReg: func(reg *loader.ExtensionRegistry) {
				reg.Register("bad-plugin", []loader.ExtensionDescriptor{
					{Point: loader.ExtPointAdminSettingsIntegrations, TargetPlugin: "core", DataFunction: "fn", Type: "section"},
				})
			},
			setupPM: func(pm *fakePluginManager) {
				// result.Data will be a JSON string, not an object: unmarshal into SettingsSection fails.
				pm.callResponses = map[string][]byte{
					"bad-plugin:fn": []byte(`"not an object"`),
				}
			},
			wantInBody: []string{"Settings"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubSettingsStore{
				loaded: settings.Settings{PasswordMinLength: 12},
			}
			reg := loader.NewExtensionRegistry()
			pm := &fakePluginManager{}
			tc.setupReg(reg)
			tc.setupPM(pm)

			h, sessions := buildSettingsHandlerWithExtensions(t, store, pm, reg)
			sessionID := newSettingsSession(t, sessions)
			entry, _ := sessions.Get(sessionID)
			req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
			req = injectSession(req, entry)
			rr := httptest.NewRecorder()
			h.HandleSettingsPage(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			for _, want := range tc.wantInBody {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q", want)
				}
			}
			for _, notWant := range tc.notWantBody {
				if strings.Contains(body, notWant) {
					t.Errorf("body must not contain %q (secret leak)", notWant)
				}
			}
			// Verify ordering: each element must appear after the previous one.
			if len(tc.checkOrder) > 1 {
				prev := strings.Index(body, tc.checkOrder[0])
				for i := 1; i < len(tc.checkOrder); i++ {
					idx := strings.Index(body, tc.checkOrder[i])
					if idx <= prev {
						t.Errorf("ordering: %q (pos %d) should appear after %q (pos %d) in body",
							tc.checkOrder[i], idx, tc.checkOrder[i-1], prev)
					}
					prev = idx
				}
			}
		})
	}
}
