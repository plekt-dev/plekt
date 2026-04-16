package settings_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/plekt-dev/plekt/internal/settings"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNewSQLiteSettingsStore_CreatesTable(t *testing.T) {
	db := openTestDB(t)
	store, err := settings.NewSQLiteSettingsStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteSettingsStore: %v", err)
	}
	defer store.Close()
}

func TestNewSQLiteSettingsStore_NilDB(t *testing.T) {
	_, err := settings.NewSQLiteSettingsStore(nil)
	if err == nil {
		t.Fatal("expected error for nil db, got nil")
	}
}

func TestSQLiteSettingsStore_LoadDefaults(t *testing.T) {
	db := openTestDB(t)
	store, err := settings.NewSQLiteSettingsStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteSettingsStore: %v", err)
	}
	defer store.Close()

	s, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// All zero values expected when no rows exist (except PasswordMinLength defaults to 12).
	if s.AllowPluginInstall != false {
		t.Error("AllowPluginInstall: got true, want false")
	}
	if s.SessionTTLMinutes != 0 {
		t.Errorf("SessionTTLMinutes: got %d, want 0", s.SessionTTLMinutes)
	}
	if s.PasswordMinLength != 12 {
		t.Errorf("PasswordMinLength: got %d, want 12 (default)", s.PasswordMinLength)
	}
}

func TestSQLiteSettingsStore_SaveAndLoad(t *testing.T) {
	db := openTestDB(t)
	store, err := settings.NewSQLiteSettingsStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteSettingsStore: %v", err)
	}
	defer store.Close()

	input := settings.Settings{
		AdminEmail:          "admin@example.com",
		AllowPluginInstall:  true,
		SessionTTLMinutes:   60,
		RegistrationEnabled: true,
		PasswordMinLength:   8,
	}
	if err := store.Save(context.Background(), input); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != input {
		t.Errorf("Load after Save:\n  got  %+v\n  want %+v", got, input)
	}
}

func TestSQLiteSettingsStore_SaveOverwrites(t *testing.T) {
	db := openTestDB(t)
	store, err := settings.NewSQLiteSettingsStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteSettingsStore: %v", err)
	}
	defer store.Close()

	first := settings.Settings{AdminEmail: "first@example.com", PasswordMinLength: 10}
	if err := store.Save(context.Background(), first); err != nil {
		t.Fatalf("Save first: %v", err)
	}

	second := settings.Settings{AdminEmail: "second@example.com", PasswordMinLength: 14}
	if err := store.Save(context.Background(), second); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != second {
		t.Errorf("expected second write to win: got %+v, want %+v", got, second)
	}
}

func TestSQLiteSettingsStore_SaveBoolFalse(t *testing.T) {
	db := openTestDB(t)
	store, err := settings.NewSQLiteSettingsStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteSettingsStore: %v", err)
	}
	defer store.Close()

	// Save with AllowPluginInstall=true first.
	if err := store.Save(context.Background(), settings.Settings{
		AllowPluginInstall: true,
	}); err != nil {
		t.Fatalf("Save true: %v", err)
	}
	// Then save with AllowPluginInstall=false.
	if err := store.Save(context.Background(), settings.Settings{
		AllowPluginInstall: false,
	}); err != nil {
		t.Fatalf("Save false: %v", err)
	}

	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AllowPluginInstall {
		t.Error("AllowPluginInstall: expected false after second save")
	}
}

func TestSQLiteSettingsStore_ContextCancellation(t *testing.T) {
	db := openTestDB(t)
	store, err := settings.NewSQLiteSettingsStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteSettingsStore: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Both Load and Save should handle cancelled context gracefully (return error, no panic).
	_, loadErr := store.Load(ctx)
	if loadErr == nil {
		t.Error("Load with cancelled context: expected non-nil error, got nil")
	}
	saveErr := store.Save(ctx, settings.Settings{AdminEmail: "x@example.com"})
	if saveErr == nil {
		t.Error("Save with cancelled context: expected non-nil error, got nil")
	}
}

func TestSQLiteSettingsStore_Close(t *testing.T) {
	db := openTestDB(t)
	store, err := settings.NewSQLiteSettingsStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteSettingsStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestSQLiteSettingsStore_RawKeyValue(t *testing.T) {
	db := openTestDB(t)
	store, err := settings.NewSQLiteSettingsStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteSettingsStore: %v", err)
	}

	ctx := context.Background()

	// GetRaw on missing key returns ErrSettingNotFound.
	_, err = store.GetRaw(ctx, "missing_key")
	if err != settings.ErrSettingNotFound {
		t.Errorf("GetRaw missing key: err = %v, want ErrSettingNotFound", err)
	}

	// SetRaw + GetRaw.
	if err := store.SetRaw(ctx, "test_key", "test_value"); err != nil {
		t.Fatalf("SetRaw: %v", err)
	}
	val, err := store.GetRaw(ctx, "test_key")
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if val != "test_value" {
		t.Errorf("GetRaw = %q, want %q", val, "test_value")
	}

	// SetRaw overwrite.
	if err := store.SetRaw(ctx, "test_key", "updated_value"); err != nil {
		t.Fatalf("SetRaw overwrite: %v", err)
	}
	val, err = store.GetRaw(ctx, "test_key")
	if err != nil {
		t.Fatalf("GetRaw after overwrite: %v", err)
	}
	if val != "updated_value" {
		t.Errorf("GetRaw = %q, want %q", val, "updated_value")
	}

	// DeleteRaw.
	if err := store.DeleteRaw(ctx, "test_key"); err != nil {
		t.Fatalf("DeleteRaw: %v", err)
	}
	_, err = store.GetRaw(ctx, "test_key")
	if err != settings.ErrSettingNotFound {
		t.Errorf("GetRaw after delete: err = %v, want ErrSettingNotFound", err)
	}

	// DeleteRaw on missing key is a no-op (no error).
	if err := store.DeleteRaw(ctx, "missing_key"); err != nil {
		t.Errorf("DeleteRaw missing key: %v", err)
	}
}
