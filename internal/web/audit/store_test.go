package audit_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/web/audit"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNewSQLiteAuditLogStore_CreatesTable(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store, err := audit.NewSQLiteAuditLogStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteAuditLogStore: %v", err)
	}
	defer store.Close()

	// If table was created, ListRecent should succeed (not error with "no such table")
	entries, err := store.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent after construction: %v", err)
	}
	if entries == nil {
		t.Error("ListRecent should return empty slice, not nil")
	}
}

func TestAuditLogStore_Append_ListRecent(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store, err := audit.NewSQLiteAuditLogStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteAuditLogStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	base := time.Now().UTC()

	// Append 5 entries with distinct timestamps
	for i := 0; i < 5; i++ {
		e := audit.AuditLogEntry{
			EventName:  "test.event",
			RemoteAddr: "127.0.0.1",
			SessionID:  "sess-" + string(rune('a'+i)),
			PluginName: "plugin-x",
			Detail:     "detail text",
			OccurredAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := store.Append(ctx, e); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}

	// ListRecent(3) should return 3 newest entries
	recent, err := store.ListRecent(ctx, 3)
	if err != nil {
		t.Fatalf("ListRecent(3): %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("ListRecent(3) returned %d entries, want 3", len(recent))
	}
	// Newest first
	if recent[0].OccurredAt.Before(recent[1].OccurredAt) {
		t.Error("ListRecent should return newest entries first")
	}
}

func TestAuditLogStore_ListRecent_Empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store, err := audit.NewSQLiteAuditLogStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteAuditLogStore: %v", err)
	}
	defer store.Close()

	entries, err := store.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent on empty store: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ListRecent on empty store returned %d entries, want 0", len(entries))
	}
}

func TestAuditLogStore_ListRecent_NParamIsLimit(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store, err := audit.NewSQLiteAuditLogStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteAuditLogStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	base := time.Now().UTC()

	// Insert 5 entries
	for i := 0; i < 5; i++ {
		e := audit.AuditLogEntry{
			EventName:  "x",
			OccurredAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := store.Append(ctx, e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// n=1 must return exactly 1
	one, err := store.ListRecent(ctx, 1)
	if err != nil {
		t.Fatalf("ListRecent(1): %v", err)
	}
	if len(one) != 1 {
		t.Errorf("ListRecent(1) returned %d entries, want 1", len(one))
	}
}

func TestAuditLogStore_Append_SetsRecordedAt(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store, err := audit.NewSQLiteAuditLogStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteAuditLogStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	before := time.Now().UTC().Add(-time.Second)
	entry := audit.AuditLogEntry{
		EventName:  "test.event",
		OccurredAt: time.Now().UTC(),
		RecordedAt: time.Time{}, // caller provides zero: store must overwrite
	}

	if err := store.Append(ctx, entry); err != nil {
		t.Fatalf("Append: %v", err)
	}

	after := time.Now().UTC().Add(time.Second)
	entries, err := store.ListRecent(ctx, 1)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(entries) != 1 {
		t.Fatal("expected 1 entry")
	}
	got := entries[0].RecordedAt
	if got.Before(before) || got.After(after) {
		t.Errorf("RecordedAt = %v, expected between %v and %v", got, before, after)
	}
}

func TestAuditLogStore_Close(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store, err := audit.NewSQLiteAuditLogStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteAuditLogStore: %v", err)
	}
	// Close should not error
	if err := store.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestAuditLogStore_Append_AllFields(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store, err := audit.NewSQLiteAuditLogStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteAuditLogStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	entry := audit.AuditLogEntry{
		EventName:  "web.auth.login_success",
		RemoteAddr: "10.0.0.1:1234",
		SessionID:  "sessionabc123",
		PluginName: "tasks-plugin",
		Detail:     "some detail",
		OccurredAt: now,
	}
	if err := store.Append(ctx, entry); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries, err := store.ListRecent(ctx, 1)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(entries) != 1 {
		t.Fatal("expected 1 entry")
	}
	got := entries[0]
	if got.EventName != entry.EventName {
		t.Errorf("EventName = %q, want %q", got.EventName, entry.EventName)
	}
	if got.RemoteAddr != entry.RemoteAddr {
		t.Errorf("RemoteAddr = %q, want %q", got.RemoteAddr, entry.RemoteAddr)
	}
	if got.SessionID != entry.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, entry.SessionID)
	}
	if got.PluginName != entry.PluginName {
		t.Errorf("PluginName = %q, want %q", got.PluginName, entry.PluginName)
	}
	if got.Detail != entry.Detail {
		t.Errorf("Detail = %q, want %q", got.Detail, entry.Detail)
	}
}
