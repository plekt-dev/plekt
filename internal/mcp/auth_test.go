package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/eventbus"
)

func TestExtractBearerToken(t *testing.T) {
	cases := []struct {
		name       string
		authHeader string
		wantToken  string
		wantErr    bool
	}{
		{
			name:       "valid bearer token",
			authHeader: "Bearer mysecrettoken",
			wantToken:  "mysecrettoken",
			wantErr:    false,
		},
		{
			name:       "missing authorization header",
			authHeader: "",
			wantToken:  "",
			wantErr:    true,
		},
		{
			name:       "malformed - no bearer prefix",
			authHeader: "Basic dXNlcjpwYXNz",
			wantToken:  "",
			wantErr:    true,
		},
		{
			name:       "malformed - only bearer keyword",
			authHeader: "Bearer",
			wantToken:  "",
			wantErr:    true,
		},
		{
			name:       "bearer with extra spaces",
			authHeader: "Bearer  mytoken",
			wantToken:  "mytoken",
			wantErr:    false,
		},
		{
			name:       "bearer with only whitespace after prefix",
			authHeader: "Bearer    ",
			wantToken:  "",
			wantErr:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.authHeader != "" {
				r.Header.Set("Authorization", tc.authHeader)
			}
			tok, err := extractBearerToken(r)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.wantErr && tok != tc.wantToken {
				t.Errorf("token = %q, want %q", tok, tc.wantToken)
			}
		})
	}
}

// fakeAgentService is a minimal AgentService for auth middleware tests.
type fakeAgentService struct {
	mu     sync.Mutex
	agents map[string]agents.Agent // token -> agent
	perms  map[int64][]agents.AgentPermission
}

func newFakeAgentService() *fakeAgentService {
	return &fakeAgentService{
		agents: make(map[string]agents.Agent),
		perms:  make(map[int64][]agents.AgentPermission),
	}
}

func (f *fakeAgentService) addAgent(a agents.Agent, perms []agents.AgentPermission) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents[a.Token] = a
	f.perms[a.ID] = perms
}

func (f *fakeAgentService) Create(_ context.Context, _ string) (agents.Agent, error) {
	return agents.Agent{}, nil
}
func (f *fakeAgentService) GetByID(_ context.Context, _ int64) (agents.Agent, error) {
	return agents.Agent{}, agents.ErrAgentNotFound
}
func (f *fakeAgentService) GetByName(_ context.Context, _ string) (agents.Agent, error) {
	return agents.Agent{}, agents.ErrAgentNotFound
}
func (f *fakeAgentService) List(_ context.Context) ([]agents.Agent, error) {
	return nil, nil
}
func (f *fakeAgentService) RotateToken(_ context.Context, _ int64) (string, error) {
	return "", nil
}
func (f *fakeAgentService) UpdateWebhook(_ context.Context, _ int64, _, _ string) error {
	return nil
}
func (f *fakeAgentService) SetWebhookSecret(_ context.Context, _ int64, _ string) error {
	return nil
}
func (f *fakeAgentService) HasWebhookSecret(_ context.Context, _ int64) (bool, error) {
	return false, nil
}
func (f *fakeAgentService) Delete(_ context.Context, _ int64) error { return nil }
func (f *fakeAgentService) SetPermissions(_ context.Context, _ int64, _ []agents.AgentPermission) error {
	return nil
}
func (f *fakeAgentService) ListPermissions(_ context.Context, _ int64) ([]agents.AgentPermission, error) {
	return nil, nil
}
func (f *fakeAgentService) ResolveByToken(_ context.Context, token string) (agents.Agent, []agents.AgentPermission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.agents[token]
	if !ok {
		return agents.Agent{}, nil, agents.ErrAgentNotFound
	}
	return a, f.perms[a.ID], nil
}

func TestAgentAuthMiddleware(t *testing.T) {
	correctToken := "valid-token-abc123"
	pluginName := "myplugin"

	svc := newFakeAgentService()
	svc.addAgent(agents.Agent{ID: 1, Name: "bot", Token: correctToken}, []agents.AgentPermission{
		{AgentID: 1, PluginName: pluginName, ToolName: "*"},
	})

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name           string
		authHeader     string
		wantHTTPStatus int
	}{
		{
			name:           "correct token",
			authHeader:     "Bearer " + correctToken,
			wantHTTPStatus: http.StatusOK,
		},
		{
			name:           "wrong token",
			authHeader:     "Bearer wrong-token",
			wantHTTPStatus: http.StatusUnauthorized,
		},
		{
			name:           "missing authorization header",
			authHeader:     "",
			wantHTTPStatus: http.StatusUnauthorized,
		},
		{
			name:           "malformed authorization header",
			authHeader:     "NotBearer something",
			wantHTTPStatus: http.StatusUnauthorized,
		},
		{
			name:           "bearer with only whitespace after prefix",
			authHeader:     "Bearer   ",
			wantHTTPStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := AgentAuthMiddleware(pluginName, svc, nil, okHandler)
			r := httptest.NewRequest(http.MethodPost, "/plugins/myplugin/mcp", nil)
			if tc.authHeader != "" {
				r.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.wantHTTPStatus {
				t.Errorf("HTTP status = %d, want %d", w.Code, tc.wantHTTPStatus)
			}
			// On failure, response body must not contain the token.
			if w.Code != http.StatusOK {
				body := w.Body.String()
				if containsToken(body, correctToken) {
					t.Error("error response body must not contain the token")
				}
			}
		})
	}
}

func TestAgentAuthMiddleware_FederatedScope(t *testing.T) {
	// When pluginScope is "", any valid agent token is accepted (no plugin scope check).
	correctToken := "federated-token-xyz"
	svc := newFakeAgentService()
	svc.addAgent(agents.Agent{ID: 2, Name: "fedbot", Token: correctToken}, []agents.AgentPermission{
		{AgentID: 2, PluginName: "some-plugin", ToolName: "*"},
	})

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name           string
		authHeader     string
		wantHTTPStatus int
	}{
		{
			name:           "correct token federated",
			authHeader:     "Bearer " + correctToken,
			wantHTTPStatus: http.StatusOK,
		},
		{
			name:           "wrong token federated",
			authHeader:     "Bearer badtoken",
			wantHTTPStatus: http.StatusUnauthorized,
		},
		{
			name:           "missing token federated",
			authHeader:     "",
			wantHTTPStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := AgentAuthMiddleware("", svc, nil, okHandler)
			r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.authHeader != "" {
				r.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.wantHTTPStatus {
				t.Errorf("HTTP status = %d, want %d", w.Code, tc.wantHTTPStatus)
			}
		})
	}
}

func TestAgentAuthMiddleware_PluginScopeCheck(t *testing.T) {
	// Agent has permission for "plugin-a" but not "plugin-b".
	correctToken := "scope-token-999"
	svc := newFakeAgentService()
	svc.addAgent(agents.Agent{ID: 3, Name: "scopebot", Token: correctToken}, []agents.AgentPermission{
		{AgentID: 3, PluginName: "plugin-a", ToolName: "*"},
	})

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Access plugin-a: should succeed.
	t.Run("has permission for plugin", func(t *testing.T) {
		handler := AgentAuthMiddleware("plugin-a", svc, nil, okHandler)
		r := httptest.NewRequest(http.MethodPost, "/plugins/plugin-a/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+correctToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("HTTP status = %d, want 200", w.Code)
		}
	})

	// Access plugin-b: should be rejected (no permission entry).
	t.Run("no permission for plugin", func(t *testing.T) {
		handler := AgentAuthMiddleware("plugin-b", svc, nil, okHandler)
		r := httptest.NewRequest(http.MethodPost, "/plugins/plugin-b/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+correctToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("HTTP status = %d, want 401 when agent has no permission for plugin", w.Code)
		}
	})
}

// ---------------------------------------------------------------------------
// Bus event emission tests
// ---------------------------------------------------------------------------

// recordingBus records emitted events for assertions.
type recordingBus struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func (b *recordingBus) Emit(_ context.Context, e eventbus.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}
func (b *recordingBus) Subscribe(_ string, _ eventbus.Handler) eventbus.Subscription {
	return eventbus.Subscription{}
}
func (b *recordingBus) Unsubscribe(_ eventbus.Subscription) {}
func (b *recordingBus) Close() error                        { return nil }

func (b *recordingBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}
func (b *recordingBus) last() (eventbus.Event, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) == 0 {
		return eventbus.Event{}, false
	}
	return b.events[len(b.events)-1], true
}

func TestAgentAuthMiddleware_EmitsValidationFailedEvent(t *testing.T) {
	bus := &recordingBus{}
	pluginName := "evtplugin"
	svc := newFakeAgentService()
	// No agents registered: all tokens will fail.

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := AgentAuthMiddleware(pluginName, svc, bus, okHandler)

	// Send a request with a wrong token: should emit event.
	r := httptest.NewRequest(http.MethodPost, "/plugins/evtplugin/mcp", nil)
	r.Header.Set("Authorization", "Bearer wrongtoken")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("HTTP status = %d, want 401", w.Code)
	}
	if bus.count() != 1 {
		t.Fatalf("expected 1 event, got %d", bus.count())
	}
	ev, _ := bus.last()
	if ev.Name != eventbus.EventTokenValidationFailed {
		t.Errorf("event name = %q, want %q", ev.Name, eventbus.EventTokenValidationFailed)
	}
	// Payload must not contain any token value.
	payload, ok := ev.Payload.(eventbus.TokenValidationFailedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want TokenValidationFailedPayload", ev.Payload)
	}
	if payload.PluginName != pluginName {
		t.Errorf("payload.PluginName = %q, want %q", payload.PluginName, pluginName)
	}
}

func TestAgentAuthMiddleware_FederatedEmitsValidationFailedEvent(t *testing.T) {
	bus := &recordingBus{}
	svc := newFakeAgentService()

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := AgentAuthMiddleware("", svc, bus, okHandler)

	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer wrongtoken")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("HTTP status = %d, want 401", w.Code)
	}
	if bus.count() != 1 {
		t.Fatalf("expected 1 event, got %d", bus.count())
	}
	ev, _ := bus.last()
	if ev.SourcePlugin != "federated" {
		t.Errorf("event source = %q, want %q", ev.SourcePlugin, "federated")
	}
}

func TestAgentAuthMiddleware_WhitespaceOnlyToken(t *testing.T) {
	// "Bearer   " (spaces only after Bearer) must be rejected.
	svc := newFakeAgentService()
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := AgentAuthMiddleware("", svc, nil, okHandler)
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer   ")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("HTTP status = %d, want 401 for whitespace-only token", w.Code)
	}
}

func TestAgentAuthMiddleware_NilBus_NoEvent(t *testing.T) {
	// Passing nil bus must not panic.
	svc := newFakeAgentService()
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := AgentAuthMiddleware("nilbusplugin", svc, nil, okHandler)
	r := httptest.NewRequest(http.MethodPost, "/plugins/nilbusplugin/mcp", nil)
	r.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	// Must not panic.
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("HTTP status = %d, want 401", w.Code)
	}
}

func TestAgentFromContext_NoAgent(t *testing.T) {
	// When no agent is in context, ok should be false.
	_, ok := AgentFromContext(context.Background())
	if ok {
		t.Error("expected ok=false for empty context")
	}
}

// containsToken checks whether a string contains the given token verbatim.
// Used in tests to verify tokens are not leaked in responses.
func containsToken(body, token string) bool {
	return len(token) > 0 && len(body) > 0 && stringContains(body, token)
}

func stringContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
