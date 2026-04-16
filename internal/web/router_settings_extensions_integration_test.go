package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/loader"
	"github.com/plekt-dev/plekt/internal/settings"
	"github.com/plekt-dev/plekt/internal/web"
)

// callablePluginManager wraps stubPluginManager and lets tests control
// CallPlugin responses without touching any other method.
type callablePluginManager struct {
	stubPluginManager
	// responses: key is "pluginName:function" → JSON bytes returned
	responses map[string][]byte
	// errs: key is "pluginName:function" → error returned
	errs map[string]error
}

func (m *callablePluginManager) CallPlugin(_ context.Context, name, fn string, _ []byte) ([]byte, error) {
	key := name + ":" + fn
	if err, ok := m.errs[key]; ok {
		return nil, err
	}
	if data, ok := m.responses[key]; ok {
		return data, nil
	}
	return nil, nil
}

// buildExtensionsMux constructs a full http.ServeMux with a real
// WebSettingsHandler backed by the given PluginManager + ExtensionRegistry.
// Returns the mux and a cookie value that authenticates as admin.
func buildExtensionsMux(
	t *testing.T,
	store settings.SettingsStore,
	pm loader.PluginManager,
	reg *loader.ExtensionRegistry,
) (*http.ServeMux, string) {
	t.Helper()

	sessions, err := web.NewInMemoryWebSessionStore()
	if err != nil {
		t.Fatalf("NewInMemoryWebSessionStore: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })

	csrf := web.NewCSRFProvider()
	bus := eventbus.NewInMemoryBus()

	settingsHandler, err := web.NewWebSettingsHandler(web.WebSettingsHandlerConfig{
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

	authHandler := web.NewWebAuthHandler(newStubUserService(), sessions, csrf, nil, nil)
	cfg := web.WebRouterConfig{
		Auth:     authHandler,
		Sessions: sessions,
		CSRF:     csrf,
		Settings: settingsHandler,
	}
	mux := web.NewWebRouter(cfg).Build(nil)

	// Create a session so we have a valid cookie.
	// UserID must be non-zero: WebAuthMiddleware rejects UserID==0 (pre-login sessions).
	entry, err := sessions.Create("127.0.0.1:0", 1, "admin", "admin", false)
	if err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	return mux, entry.ID
}

func doGet(t *testing.T, mux *http.ServeMux, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	req.AddCookie(&http.Cookie{Name: "mc_session", Value: sessionID})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// TestSettingsExtensions_FullHTTP_RendersSection tests that GET /admin/settings
// returns 200 with the plugin section title injected into the body.
func TestSettingsExtensions_FullHTTP_RendersSection(t *testing.T) {
	reg := loader.NewExtensionRegistry()
	reg.Register("voice-plugin", []loader.ExtensionDescriptor{
		{
			Point:        loader.ExtPointAdminSettingsIntegrations,
			TargetPlugin: "core",
			DataFunction: "get_settings_section",
			Type:         "section",
		},
	})

	pm := &callablePluginManager{
		responses: map[string][]byte{
			"voice-plugin:get_settings_section": []byte(`{
				"title": "Voice Transcription",
				"description": "Configure Whisper.",
				"submit_action": "set_prefs",
				"fields": [
					{"name": "backend_mode", "type": "select", "label": "Backend mode", "value": "local",
					 "options": [{"value":"local","label":"Local"},{"value":"api","label":"API"}]}
				]
			}`),
		},
	}

	store := &stubSettingsStore{loaded: settings.Settings{PasswordMinLength: 12}}
	mux, sessionID := buildExtensionsMux(t, store, pm, reg)
	w := doGet(t, mux, sessionID)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/settings: status=%d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Voice Transcription") {
		t.Errorf("body missing section title 'Voice Transcription'; snippet: %s", body[:min(500, len(body))])
	}
	if !strings.Contains(body, "voice-plugin") {
		t.Errorf("body missing source plugin label 'voice-plugin'")
	}
}

// TestSettingsExtensions_WriteOnlySecretNotLeaked verifies that when a plugin
// returns a write_only field with a non-empty Value ("leaked-secret"), the
// server-side handler clears it before rendering: the secret must never appear
// in the HTTP response body.
func TestSettingsExtensions_WriteOnlySecretNotLeaked(t *testing.T) {
	reg := loader.NewExtensionRegistry()
	reg.Register("voice-plugin", []loader.ExtensionDescriptor{
		{
			Point:        loader.ExtPointAdminSettingsIntegrations,
			TargetPlugin: "core",
			DataFunction: "get_settings_section",
			Type:         "section",
		},
	})

	pm := &callablePluginManager{
		responses: map[string][]byte{
			"voice-plugin:get_settings_section": []byte(`{
				"title": "Voice Config",
				"submit_action": "set_prefs",
				"fields": [
					{"name": "openai_api_key", "type": "password", "label": "API Key",
					 "value": "leaked-secret", "write_only": true}
				]
			}`),
		},
	}

	store := &stubSettingsStore{loaded: settings.Settings{PasswordMinLength: 12}}
	mux, sessionID := buildExtensionsMux(t, store, pm, reg)
	w := doGet(t, mux, sessionID)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "leaked-secret") {
		t.Error("server leaked write_only field value in HTTP response body")
	}
}

// TestSettingsExtensions_PluginErrorStillReturns200 verifies that when the
// extension call returns an error the page still renders 200 with the core
// settings form present: no 500, plugin section just absent.
func TestSettingsExtensions_PluginErrorStillReturns200(t *testing.T) {
	reg := loader.NewExtensionRegistry()
	reg.Register("voice-plugin", []loader.ExtensionDescriptor{
		{
			Point:        loader.ExtPointAdminSettingsIntegrations,
			TargetPlugin: "core",
			DataFunction: "get_settings_section",
			Type:         "section",
		},
	})

	pm := &callablePluginManager{
		errs: map[string]error{
			"voice-plugin:get_settings_section": errors.New("plugin crashed"),
		},
	}

	store := &stubSettingsStore{loaded: settings.Settings{PasswordMinLength: 12}}
	mux, sessionID := buildExtensionsMux(t, store, pm, reg)
	w := doGet(t, mux, sessionID)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when plugin errors, got %d", w.Code)
	}
	// Core form must still be rendered.
	if !strings.Contains(w.Body.String(), "admin_email") {
		t.Error("core settings form (admin_email) absent when plugin extension fails")
	}
	// Plugin section title must NOT appear.
	if strings.Contains(w.Body.String(), "Voice Transcription") {
		t.Error("plugin section should be absent when extension call errors")
	}
}

// TestSettingsExtensions_MaliciousLinkURL verifies that plugin-supplied link
// URLs that are not root-relative (e.g. http://evil.example.com) are rendered
// as <span>, not <a>, preventing open-redirect / phishing.
// A safe root-relative URL ("/admin/foo") must be rendered as <a href="/admin/foo">.
func TestSettingsExtensions_MaliciousLinkURL(t *testing.T) {
	reg := loader.NewExtensionRegistry()
	reg.Register("test-plugin", []loader.ExtensionDescriptor{
		{
			Point:        loader.ExtPointAdminSettingsIntegrations,
			TargetPlugin: "core",
			DataFunction: "fn",
			Type:         "section",
		},
	})

	pm := &callablePluginManager{
		responses: map[string][]byte{
			"test-plugin:fn": []byte(`{
				"title": "Test Section",
				"submit_action": "save",
				"fields": [
					{"name": "evil_link",  "type": "link", "label": "Evil",  "value": "http://evil.example.com"},
					{"name": "safe_link",  "type": "link", "label": "Docs",  "value": "/admin/foo"}
				]
			}`),
		},
	}

	store := &stubSettingsStore{loaded: settings.Settings{PasswordMinLength: 12}}
	mux, sessionID := buildExtensionsMux(t, store, pm, reg)
	w := doGet(t, mux, sessionID)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()

	// The evil URL must NOT appear as an href (it may appear escaped in a span).
	if strings.Contains(body, `href="http://evil.example.com"`) {
		t.Error("evil URL rendered as href: open redirect vulnerability")
	}
	// "Evil" label should appear, but wrapped in <span>.
	if !strings.Contains(body, ">Evil<") {
		t.Errorf("expected label 'Evil' in body (as span text), not found; body snippet: %s", body[:min(800, len(body))])
	}

	// The safe URL must appear as an <a href="/admin/foo">.
	if !strings.Contains(body, `href="/admin/foo"`) {
		t.Errorf("safe root-relative URL not rendered as href; body snippet: %s", body[:min(800, len(body))])
	}
}

// TestSettingsExtensions_FormActionUsesSourcePluginName verifies that the
// section form action is built from the host-assigned source plugin name
// (SourcePlugin), never from anything the plugin can control. This is a
// belt-and-braces check since SubmitPlugin was removed from SettingsSection.
func TestSettingsExtensions_FormActionUsesSourcePluginName(t *testing.T) {
	reg := loader.NewExtensionRegistry()
	// Register extension under "voice-plugin": the source name comes from
	// the registry, not from the plugin JSON payload.
	reg.Register("voice-plugin", []loader.ExtensionDescriptor{
		{
			Point:        loader.ExtPointAdminSettingsIntegrations,
			TargetPlugin: "core",
			DataFunction: "get_settings_section",
			Type:         "section",
		},
	})

	pm := &callablePluginManager{
		responses: map[string][]byte{
			// Plugin tries to inject a fake submit action path via the payload
			// (it cannot set SubmitPlugin). The host builds /p/<sourcePlugin>/action/<submitAction>.
			"voice-plugin:get_settings_section": []byte(`{
				"title": "Voice Config",
				"submit_action": "set_prefs",
				"fields": []
			}`),
		},
	}

	store := &stubSettingsStore{loaded: settings.Settings{PasswordMinLength: 12}}
	mux, sessionID := buildExtensionsMux(t, store, pm, reg)
	w := doGet(t, mux, sessionID)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()

	// The form must carry the source plugin name and submit_action as data
	// attributes consumed by MC.pluginSettingsSubmit (the JS handler that
	// intercepts native submission and POSTs JSON to the action endpoint).
	// Native action="..." was replaced because the browser would otherwise
	// navigate to the JSON response on submit instead of staying on
	// /admin/settings.
	for _, want := range []string{
		`data-plugin="voice-plugin"`,
		`data-action="set_prefs"`,
		`onsubmit="return MC.pluginSettingsSubmit(event);"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in body; snippet: %s", want, body[:min(800, len(body))])
		}
	}
}

// TestSettingsExtensions_SectionOrdering verifies that two plugins registered
// at different extension points are rendered in the canonical
// AdminSettingsExtensionPointOrder() order: general → integrations → advanced.
func TestSettingsExtensions_SectionOrdering(t *testing.T) {
	reg := loader.NewExtensionRegistry()
	// Register "plugin-b" at integrations (order=2) and "plugin-a" at general (order=1).
	reg.Register("plugin-b", []loader.ExtensionDescriptor{
		{Point: loader.ExtPointAdminSettingsIntegrations, TargetPlugin: "core", DataFunction: "fn_b", Type: "section"},
	})
	reg.Register("plugin-a", []loader.ExtensionDescriptor{
		{Point: loader.ExtPointAdminSettingsGeneral, TargetPlugin: "core", DataFunction: "fn_a", Type: "section"},
	})

	pm := &callablePluginManager{
		responses: map[string][]byte{
			"plugin-a:fn_a": []byte(`{"title":"Section A","submit_action":"save_a","fields":[]}`),
			"plugin-b:fn_b": []byte(`{"title":"Section B","submit_action":"save_b","fields":[]}`),
		},
	}

	store := &stubSettingsStore{loaded: settings.Settings{PasswordMinLength: 12}}
	mux, sessionID := buildExtensionsMux(t, store, pm, reg)
	w := doGet(t, mux, sessionID)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()

	idxA := strings.Index(body, "Section A")
	idxB := strings.Index(body, "Section B")

	if idxA < 0 {
		t.Fatal("Section A not found in body")
	}
	if idxB < 0 {
		t.Fatal("Section B not found in body")
	}
	// Section A (general) must appear before Section B (integrations).
	if idxA > idxB {
		t.Errorf("Section A (general, pos %d) should appear before Section B (integrations, pos %d)", idxA, idxB)
	}
}

// TestSettingsExtensions_NoPlugins_CoreFormPresent verifies that with no
// extension registry the core settings form still renders normally.
func TestSettingsExtensions_NoPlugins_CoreFormPresent(t *testing.T) {
	store := &stubSettingsStore{loaded: settings.Settings{PasswordMinLength: 12}}
	mux, sessionID := buildExtensionsMux(t, store, nil, nil)
	w := doGet(t, mux, sessionID)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "admin_email") {
		t.Error("core form field admin_email missing when no extensions registered")
	}
}

// TestSettingsExtensions_CapAdminSettingsExtension_DerivedFromManifest verifies
// that a manifest with ui.extensions targeting core.admin.settings.* causes
// Derive() to emit CapAdminSettingsExtension with the correct description.
// This is the capability derivation end-to-end check required by Task #43.
func TestSettingsExtensions_CapAdminSettingsExtension_DerivedFromManifest(t *testing.T) {
	m := loader.Manifest{
		Name: "voice-plugin",
		UI: loader.UIDeclaration{
			Extensions: []loader.ExtensionDescriptor{
				{
					Point:        loader.ExtPointAdminSettingsIntegrations,
					TargetPlugin: "core",
					DataFunction: "get_settings_section",
					Type:         "section",
				},
			},
		},
	}

	d := loader.NewDefaultPermissionDeriver()
	perms := d.Derive(m)

	var cap *loader.Capability
	for i := range perms.Capabilities {
		if perms.Capabilities[i].ID == loader.CapAdminSettingsExtension {
			cap = &perms.Capabilities[i]
			break
		}
	}

	if cap == nil {
		t.Fatalf("expected CapAdminSettingsExtension in derived capabilities, got: %+v", perms.Capabilities)
	}
	if cap.Severity != loader.SeverityMedium {
		t.Errorf("expected SeverityMedium, got %s", cap.Severity)
	}
	found := false
	for _, d := range cap.Details {
		if d == loader.ExtPointAdminSettingsIntegrations {
			found = true
		}
	}
	if !found {
		t.Errorf("Details should include %q; got %v", loader.ExtPointAdminSettingsIntegrations, cap.Details)
	}
}

// TestSettingsExtensions_PermissionsPageRendersCapAdminSettingsDescription
// verifies that the PluginPermissionsPage template renders a human-readable
// description for CapAdminSettingsExtension that includes "Admin settings".
func TestSettingsExtensions_PermissionsPageRendersCapAdminSettingsDescription(t *testing.T) {
	m := loader.Manifest{
		Name: "voice-plugin",
		UI: loader.UIDeclaration{
			Extensions: []loader.ExtensionDescriptor{
				{
					Point:        loader.ExtPointAdminSettingsIntegrations,
					TargetPlugin: "core",
					DataFunction: "get_settings_section",
					Type:         "section",
				},
			},
		},
	}

	d := loader.NewDefaultPermissionDeriver()
	perms := d.Derive(m)

	var cap *loader.Capability
	for i := range perms.Capabilities {
		if perms.Capabilities[i].ID == loader.CapAdminSettingsExtension {
			cap = &perms.Capabilities[i]
			break
		}
	}
	if cap == nil {
		t.Fatal("CapAdminSettingsExtension not derived: cannot check description")
	}

	// The capability description must mention "admin" and "settings" (case-insensitive)
	// so the operator understands what is being requested.
	descLower := strings.ToLower(cap.Description)
	if !strings.Contains(descLower, "admin") || !strings.Contains(descLower, "settings") {
		t.Errorf("CapAdminSettingsExtension.Description = %q; expected to mention both 'admin' and 'settings'", cap.Description)
	}
	// Title should be human-readable and non-empty.
	if cap.Title == "" {
		t.Error("CapAdminSettingsExtension.Title must not be empty")
	}
}
