package loader

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openTestRegistryDB opens an in-memory SQLite DB for testing.
func openTestRegistryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNewSQLitePluginRegistryStore_CreatesTable(t *testing.T) {
	db := openTestRegistryDB(t)
	store, err := NewSQLitePluginRegistryStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePluginRegistryStore: %v", err)
	}
	defer store.Close()

	// Verify we can list without error (table exists).
	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List after creation: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty list, got %d entries", len(entries))
	}
}

func TestNewSQLitePluginRegistryStore_NilDB(t *testing.T) {
	_, err := NewSQLitePluginRegistryStore(nil)
	if err == nil {
		t.Error("expected error for nil DB, got nil")
	}
}

func TestPluginRegistryStore_Upsert_And_Get(t *testing.T) {
	db := openTestRegistryDB(t)
	store, err := NewSQLitePluginRegistryStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePluginRegistryStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	entry := PluginRegistryEntry{
		Name:      "my-plugin",
		Dir:       "/plugins/my-plugin",
		Version:   "1.0.0",
		LoadedAt:  now,
		UpdatedAt: now,
	}

	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.Get(context.Background(), "my-plugin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != entry.Name {
		t.Errorf("Name: want %q, got %q", entry.Name, got.Name)
	}
	if got.Dir != entry.Dir {
		t.Errorf("Dir: want %q, got %q", entry.Dir, got.Dir)
	}
	if got.Version != entry.Version {
		t.Errorf("Version: want %q, got %q", entry.Version, got.Version)
	}
}

func TestPluginRegistryStore_Upsert_Updates(t *testing.T) {
	db := openTestRegistryDB(t)
	store, err := NewSQLitePluginRegistryStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePluginRegistryStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	entry := PluginRegistryEntry{
		Name:      "updatable",
		Dir:       "/plugins/updatable",
		Version:   "0.1.0",
		LoadedAt:  now,
		UpdatedAt: now,
	}
	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	entry.Version = "0.2.0"
	entry.UpdatedAt = now.Add(time.Hour)
	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	got, err := store.Get(context.Background(), "updatable")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Version != "0.2.0" {
		t.Errorf("Version after update: want 0.2.0, got %q", got.Version)
	}
}

func TestPluginRegistryStore_Get_NotFound(t *testing.T) {
	db := openTestRegistryDB(t)
	store, err := NewSQLitePluginRegistryStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePluginRegistryStore: %v", err)
	}
	defer store.Close()

	_, err = store.Get(context.Background(), "nonexistent")
	if !errors.Is(err, ErrRegistryEntryNotFound) {
		t.Errorf("expected ErrRegistryEntryNotFound, got %v", err)
	}
}

func TestPluginRegistryStore_Delete(t *testing.T) {
	db := openTestRegistryDB(t)
	store, err := NewSQLitePluginRegistryStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePluginRegistryStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	entry := PluginRegistryEntry{
		Name:      "deletable",
		Dir:       "/plugins/deletable",
		Version:   "1.0.0",
		LoadedAt:  now,
		UpdatedAt: now,
	}
	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := store.Delete(context.Background(), "deletable"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = store.Get(context.Background(), "deletable")
	if !errors.Is(err, ErrRegistryEntryNotFound) {
		t.Errorf("expected ErrRegistryEntryNotFound after delete, got %v", err)
	}
}

func TestPluginRegistryStore_Delete_NonExistent(t *testing.T) {
	db := openTestRegistryDB(t)
	store, err := NewSQLitePluginRegistryStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePluginRegistryStore: %v", err)
	}
	defer store.Close()

	// Delete non-existent should succeed (idempotent).
	if err := store.Delete(context.Background(), "ghost"); err != nil {
		t.Errorf("Delete non-existent: expected nil, got %v", err)
	}
}

func TestPluginRegistryStore_List(t *testing.T) {
	db := openTestRegistryDB(t)
	store, err := NewSQLitePluginRegistryStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePluginRegistryStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	entries := []PluginRegistryEntry{
		{Name: "alpha", Dir: "/plugins/alpha", Version: "1.0.0", LoadedAt: now, UpdatedAt: now},
		{Name: "beta", Dir: "/plugins/beta", Version: "2.0.0", LoadedAt: now, UpdatedAt: now},
	}
	for _, e := range entries {
		if err := store.Upsert(context.Background(), e); err != nil {
			t.Fatalf("Upsert %q: %v", e.Name, err)
		}
	}

	list, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 entries, got %d", len(list))
	}
}

func TestPluginRegistryStore_SQLInjection(t *testing.T) {
	db := openTestRegistryDB(t)
	store, err := NewSQLitePluginRegistryStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePluginRegistryStore: %v", err)
	}
	defer store.Close()

	// Attempt SQL injection via name field.
	maliciousName := "'; DROP TABLE plugin_registry; --"
	now := time.Now().UTC()
	entry := PluginRegistryEntry{
		Name:      maliciousName,
		Dir:       "/plugins/evil",
		Version:   "1.0.0",
		LoadedAt:  now,
		UpdatedAt: now,
	}
	// This should either succeed (parameterized) or return a harmless error.
	_ = store.Upsert(context.Background(), entry)

	// Table must still be intact: list should work without error.
	_, err = store.List(context.Background())
	if err != nil {
		t.Errorf("table dropped by SQL injection: %v", err)
	}
}

func TestPluginRegistryStore_Close(t *testing.T) {
	db := openTestRegistryDB(t)
	store, err := NewSQLitePluginRegistryStore(db)
	if err != nil {
		t.Fatalf("NewSQLitePluginRegistryStore: %v", err)
	}
	// Close should not error.
	if err := store.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestPluginRegistryStore_ErrRegistryEntryNotFound_IsSentinel(t *testing.T) {
	if ErrRegistryEntryNotFound == nil {
		t.Error("ErrRegistryEntryNotFound must not be nil")
	}
	var target interface{ Error() string }
	if !errors.As(ErrRegistryEntryNotFound, &target) {
		t.Error("ErrRegistryEntryNotFound must implement error interface")
	}
}
