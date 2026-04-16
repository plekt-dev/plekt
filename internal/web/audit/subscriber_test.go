package audit_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/web/audit"
	_ "modernc.org/sqlite"
)

// capturingStore captures Append calls for assertion.
type capturingStore struct {
	mu      sync.Mutex
	entries []audit.AuditLogEntry
	err     error
}

func (s *capturingStore) Append(_ context.Context, e audit.AuditLogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return s.err
}

func (s *capturingStore) ListRecent(_ context.Context, _ int) ([]audit.AuditLogEntry, error) {
	return nil, nil
}

func (s *capturingStore) ListFiltered(_ context.Context, _ audit.AuditFilter) ([]audit.AuditLogEntry, int, error) {
	return nil, 0, nil
}

func (s *capturingStore) CountByPrefixes(_ context.Context, prefixes [][]string) ([]int, error) {
	return make([]int, len(prefixes)), nil
}

func (s *capturingStore) Close() error { return nil }

func (s *capturingStore) last() (audit.AuditLogEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 {
		return audit.AuditLogEntry{}, false
	}
	return s.entries[len(s.entries)-1], true
}

func (s *capturingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// synchronousBus is a simple EventBus that delivers events synchronously.
type synchronousBus struct {
	mu   sync.Mutex
	subs map[string][]eventbus.Handler
}

func newSynchronousBus() *synchronousBus {
	return &synchronousBus{subs: make(map[string][]eventbus.Handler)}
}

func (b *synchronousBus) Emit(ctx context.Context, e eventbus.Event) {
	b.mu.Lock()
	handlers := make([]eventbus.Handler, 0, len(b.subs[e.Name])+len(b.subs["*"]))
	handlers = append(handlers, b.subs[e.Name]...)
	handlers = append(handlers, b.subs["*"]...)
	b.mu.Unlock()
	for _, h := range handlers {
		h(ctx, e)
	}
}

func (b *synchronousBus) Subscribe(name string, h eventbus.Handler) eventbus.Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[name] = append(b.subs[name], h)
	return eventbus.Subscription{}
}

func (b *synchronousBus) Unsubscribe(_ eventbus.Subscription) {}
func (b *synchronousBus) Close() error                        { return nil }

func openTestDBForSubscriber(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAuditLogSubscriber_WebLoginAttempt(t *testing.T) {
	t.Parallel()
	store := &capturingStore{}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	now := time.Now().UTC()
	bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventWebLoginAttempt,
		Payload: eventbus.WebLoginAttemptPayload{
			RemoteAddr: "1.2.3.4",
			OccurredAt: now,
		},
	})

	entry, ok := store.last()
	if !ok {
		t.Fatal("expected entry to be appended")
	}
	if entry.EventName != eventbus.EventWebLoginAttempt {
		t.Errorf("EventName = %q, want %q", entry.EventName, eventbus.EventWebLoginAttempt)
	}
	if entry.RemoteAddr != "1.2.3.4" {
		t.Errorf("RemoteAddr = %q, want %q", entry.RemoteAddr, "1.2.3.4")
	}
	if !entry.OccurredAt.Equal(now) {
		t.Errorf("OccurredAt = %v, want %v", entry.OccurredAt, now)
	}
}

func TestAuditLogSubscriber_WebLoginSuccess(t *testing.T) {
	t.Parallel()
	store := &capturingStore{}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	now := time.Now().UTC()
	bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventWebLoginSuccess,
		Payload: eventbus.WebLoginSuccessPayload{
			RemoteAddr: "5.6.7.8",
			SessionID:  "session123",
			OccurredAt: now,
		},
	})

	entry, ok := store.last()
	if !ok {
		t.Fatal("expected entry")
	}
	if entry.EventName != eventbus.EventWebLoginSuccess {
		t.Errorf("EventName = %q", entry.EventName)
	}
	if entry.RemoteAddr != "5.6.7.8" {
		t.Errorf("RemoteAddr = %q", entry.RemoteAddr)
	}
	if entry.SessionID != "session123" {
		t.Errorf("SessionID = %q", entry.SessionID)
	}
}

func TestAuditLogSubscriber_WebLoginFailed(t *testing.T) {
	t.Parallel()
	store := &capturingStore{}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	now := time.Now().UTC()
	bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventWebLoginFailed,
		Payload: eventbus.WebLoginFailedPayload{
			RemoteAddr: "9.9.9.9",
			Reason:     "invalid_credential",
			OccurredAt: now,
		},
	})

	entry, ok := store.last()
	if !ok {
		t.Fatal("expected entry")
	}
	if entry.EventName != eventbus.EventWebLoginFailed {
		t.Errorf("EventName = %q", entry.EventName)
	}
	if entry.Detail != "invalid_credential" {
		t.Errorf("Detail = %q, want Reason as Detail", entry.Detail)
	}
}

func TestAuditLogSubscriber_WebLogout(t *testing.T) {
	t.Parallel()
	store := &capturingStore{}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	now := time.Now().UTC()
	bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventWebLogout,
		Payload: eventbus.WebLogoutPayload{
			RemoteAddr: "1.1.1.1",
			SessionID:  "sess-abc",
			OccurredAt: now,
		},
	})

	entry, ok := store.last()
	if !ok {
		t.Fatal("expected entry")
	}
	if entry.EventName != eventbus.EventWebLogout {
		t.Errorf("EventName = %q", entry.EventName)
	}
	if entry.SessionID != "sess-abc" {
		t.Errorf("SessionID = %q", entry.SessionID)
	}
}

func TestAuditLogSubscriber_TokenCreated(t *testing.T) {
	t.Parallel()
	store := &capturingStore{}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	now := time.Now().UTC()
	bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventTokenCreated,
		Payload: eventbus.TokenCreatedPayload{
			PluginName: "my-plugin",
			CreatedAt:  now,
		},
	})

	entry, ok := store.last()
	if !ok {
		t.Fatal("expected entry")
	}
	if entry.EventName != eventbus.EventTokenCreated {
		t.Errorf("EventName = %q", entry.EventName)
	}
	if entry.PluginName != "my-plugin" {
		t.Errorf("PluginName = %q", entry.PluginName)
	}
	if !entry.OccurredAt.Equal(now) {
		t.Errorf("OccurredAt = %v (from CreatedAt)", entry.OccurredAt)
	}
}

func TestAuditLogSubscriber_TokenRotated(t *testing.T) {
	t.Parallel()
	store := &capturingStore{}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	now := time.Now().UTC()
	bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventTokenRotated,
		Payload: eventbus.TokenRotatedPayload{
			PluginName: "rotated-plugin",
			RotatedAt:  now,
		},
	})

	entry, ok := store.last()
	if !ok {
		t.Fatal("expected entry")
	}
	if entry.EventName != eventbus.EventTokenRotated {
		t.Errorf("EventName = %q", entry.EventName)
	}
	if entry.PluginName != "rotated-plugin" {
		t.Errorf("PluginName = %q", entry.PluginName)
	}
	if !entry.OccurredAt.Equal(now) {
		t.Errorf("OccurredAt = %v (from RotatedAt)", entry.OccurredAt)
	}
}

func TestAuditLogSubscriber_TokenValidationFailed(t *testing.T) {
	t.Parallel()
	store := &capturingStore{}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	now := time.Now().UTC()
	bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventTokenValidationFailed,
		Payload: eventbus.TokenValidationFailedPayload{
			PluginName: "my-plugin",
			RemoteAddr: "10.0.0.1",
			OccurredAt: now,
		},
	})

	entry, ok := store.last()
	if !ok {
		t.Fatal("expected entry")
	}
	if entry.EventName != eventbus.EventTokenValidationFailed {
		t.Errorf("EventName = %q", entry.EventName)
	}
	if entry.PluginName != "my-plugin" {
		t.Errorf("PluginName = %q", entry.PluginName)
	}
	if entry.RemoteAddr != "10.0.0.1" {
		t.Errorf("RemoteAddr = %q", entry.RemoteAddr)
	}
}

func TestAuditLogSubscriber_AdminSessionRevoked(t *testing.T) {
	t.Parallel()
	store := &capturingStore{}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	now := time.Now().UTC()
	bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventAdminSessionRevoked,
		Payload: eventbus.AdminSessionRevokedPayload{
			RevokedSessionID: "revoked-sess",
			ActorSessionID:   "actor-sess",
			RemoteAddr:       "192.168.1.1",
			OccurredAt:       now,
		},
	})

	entry, ok := store.last()
	if !ok {
		t.Fatal("expected entry")
	}
	if entry.EventName != eventbus.EventAdminSessionRevoked {
		t.Errorf("EventName = %q", entry.EventName)
	}
	if entry.SessionID != "revoked-sess" {
		t.Errorf("SessionID = %q, want RevokedSessionID", entry.SessionID)
	}
	if entry.RemoteAddr != "192.168.1.1" {
		t.Errorf("RemoteAddr = %q", entry.RemoteAddr)
	}
}

func TestAuditLogSubscriber_UnrecognizedPayload_NoPanic(t *testing.T) {
	t.Parallel()
	store := &capturingStore{}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	// Emit with unrecognized payload type: must not panic
	bus.Emit(context.Background(), eventbus.Event{
		Name:    eventbus.EventWebLoginAttempt,
		Payload: struct{ Unexpected string }{Unexpected: "data"},
	})

	// Entry should still be appended (with EventName set)
	if store.count() != 1 {
		t.Errorf("expected 1 entry appended for unrecognized payload, got %d", store.count())
	}
	entry, _ := store.last()
	if entry.EventName != eventbus.EventWebLoginAttempt {
		t.Errorf("EventName = %q, want %q", entry.EventName, eventbus.EventWebLoginAttempt)
	}
}

func TestAuditLogSubscriber_AppendError_DoesNotPanic(t *testing.T) {
	t.Parallel()
	store := &capturingStore{err: audit.ErrAuditLogUnavailable}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	// Even with Append returning error, should not panic
	bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventWebLoginAttempt,
		Payload: eventbus.WebLoginAttemptPayload{
			RemoteAddr: "1.2.3.4",
			OccurredAt: time.Now().UTC(),
		},
	})
	// No panic = pass
}

func TestAuditLogSubscriber_Close_Unsubscribes(t *testing.T) {
	t.Parallel()
	store := &capturingStore{}
	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)

	// Close before emitting
	sub.Close()

	// After Close, events should still be handled by bus (unsubscribe is no-op in our test bus)
	// The key assertion: no panic on Close
}

func TestNewAuditLogSubscriber_IntegrationWithSQLite(t *testing.T) {
	t.Parallel()
	db := openTestDBForSubscriber(t)
	store, err := audit.NewSQLiteAuditLogStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteAuditLogStore: %v", err)
	}
	defer store.Close()

	bus := newSynchronousBus()
	sub := audit.NewAuditLogSubscriber(store, bus)
	defer sub.Close()

	now := time.Now().UTC()
	bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventWebLoginSuccess,
		Payload: eventbus.WebLoginSuccessPayload{
			RemoteAddr: "10.1.1.1",
			SessionID:  "integration-sess",
			OccurredAt: now,
		},
	})

	entries, err := store.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].EventName != eventbus.EventWebLoginSuccess {
		t.Errorf("EventName = %q", entries[0].EventName)
	}
}
