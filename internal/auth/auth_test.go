package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/auth"
	"github.com/plekt-dev/plekt/internal/eventbus"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// ValidateTokenFormat
// ---------------------------------------------------------------------------

func TestValidateTokenFormat(t *testing.T) {
	cases := []struct {
		name    string
		token   string
		wantErr error
	}{
		{
			name:    "valid 64 char lower hex",
			token:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			wantErr: nil,
		},
		{
			name:    "all zeros",
			token:   "0000000000000000000000000000000000000000000000000000000000000000",
			wantErr: nil,
		},
		{
			name:    "all f",
			token:   "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			wantErr: nil,
		},
		{
			name:    "too short",
			token:   "abc123",
			wantErr: auth.ErrTokenFormatInvalid,
		},
		{
			name:    "too long",
			token:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2extra",
			wantErr: auth.ErrTokenFormatInvalid,
		},
		{
			name:    "uppercase hex",
			token:   "A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2",
			wantErr: auth.ErrTokenFormatInvalid,
		},
		{
			name:    "mixed case",
			token:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6A1B2",
			wantErr: auth.ErrTokenFormatInvalid,
		},
		{
			name:    "non-hex characters",
			token:   "g1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			wantErr: auth.ErrTokenFormatInvalid,
		},
		{
			name:    "empty string",
			token:   "",
			wantErr: auth.ErrTokenFormatInvalid,
		},
		{
			name:    "spaces",
			token:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6 1b2",
			wantErr: auth.ErrTokenFormatInvalid,
		},
		{
			name:    "sql injection attempt",
			token:   "'; DROP TABLE tokens; --                                       ",
			wantErr: auth.ErrTokenFormatInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.ValidateTokenFormat(tc.token)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("ValidateTokenFormat(%q) error = %v, want %v", tc.token, err, tc.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateTokenFormat(%q) unexpected error: %v", tc.token, err)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SQLiteTokenStore
// ---------------------------------------------------------------------------

func newTempStore(t *testing.T) auth.TokenStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tokens.db")
	store, err := auth.NewSQLiteTokenStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteTokenStore: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestSQLiteTokenStore_SaveAndLoad(t *testing.T) {
	store := newTempStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	rec := auth.TokenRecord{
		PluginName: "my-plugin",
		Token:      "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx, "my-plugin")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.PluginName != rec.PluginName {
		t.Errorf("PluginName = %q, want %q", loaded.PluginName, rec.PluginName)
	}
	if loaded.Token != rec.Token {
		t.Errorf("Token = %q, want %q", loaded.Token, rec.Token)
	}
}

func TestSQLiteTokenStore_Load_NotFound(t *testing.T) {
	store := newTempStore(t)
	ctx := context.Background()

	_, err := store.Load(ctx, "nonexistent")
	if !errors.Is(err, auth.ErrTokenNotFound) {
		t.Errorf("Load missing plugin: error = %v, want ErrTokenNotFound", err)
	}
}

func TestSQLiteTokenStore_Save_Upsert(t *testing.T) {
	store := newTempStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	rec1 := auth.TokenRecord{
		PluginName: "myplugin",
		Token:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.Save(ctx, rec1); err != nil {
		t.Fatalf("Save first: %v", err)
	}

	// Save again with a different token (upsert/rotate).
	later := now.Add(time.Minute)
	rec2 := auth.TokenRecord{
		PluginName: "myplugin",
		Token:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CreatedAt:  now,
		UpdatedAt:  later,
	}
	if err := store.Save(ctx, rec2); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	loaded, err := store.Load(ctx, "myplugin")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Token != rec2.Token {
		t.Errorf("Token after upsert = %q, want %q", loaded.Token, rec2.Token)
	}
}

func TestSQLiteTokenStore_Delete(t *testing.T) {
	store := newTempStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	rec := auth.TokenRecord{
		PluginName: "deleteme",
		Token:      "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete(ctx, "deleteme"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.Load(ctx, "deleteme")
	if !errors.Is(err, auth.ErrTokenNotFound) {
		t.Errorf("Load after delete: error = %v, want ErrTokenNotFound", err)
	}
}

func TestSQLiteTokenStore_Delete_NotFound(t *testing.T) {
	store := newTempStore(t)
	ctx := context.Background()

	// Deleting a non-existent record should not return an error.
	err := store.Delete(ctx, "ghost")
	if err != nil {
		t.Errorf("Delete nonexistent: unexpected error: %v", err)
	}
}

func TestNewSQLiteTokenStore_BadPath(t *testing.T) {
	// Provide a path whose parent directory does not exist.
	_, err := auth.NewSQLiteTokenStore("/nonexistent/deeply/nested/path/tokens.db")
	if err == nil {
		t.Error("expected error for non-existent parent dir, got nil")
	}
}

func TestNewSQLiteTokenStore_PingFails_DirectoryPath(t *testing.T) {
	// Passing a directory as the DB path causes db.Ping() to fail,
	// exercising the ping-error branch in NewSQLiteTokenStore.
	dir := t.TempDir()
	// dir itself is a directory: SQLite cannot use a directory as a database file.
	_, err := auth.NewSQLiteTokenStore(dir)
	if err == nil {
		t.Error("expected error when DB path is a directory (Ping should fail), got nil")
	}
}

func TestNewSQLiteTokenStore_SchemaFails_ReadOnlyFile(t *testing.T) {
	// Create a read-only empty file so that Ping succeeds but CREATE TABLE fails.
	// This exercises the createSchema error branch in NewSQLiteTokenStore.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "readonly.db")

	// Create the file then make it read-only.
	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	f.Close()
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	// Restore permissions so t.TempDir cleanup can delete the file.
	t.Cleanup(func() { _ = os.Chmod(dbPath, 0644) })

	_, err = auth.NewSQLiteTokenStore(dbPath)
	if err == nil {
		t.Error("expected error for read-only DB file (createSchema should fail), got nil")
	}
}

// ---------------------------------------------------------------------------
// DefaultTokenService
// ---------------------------------------------------------------------------

// fakeStore is an in-memory TokenStore for unit-testing DefaultTokenService.
type fakeStore struct {
	mu      sync.Mutex
	records map[string]auth.TokenRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{records: make(map[string]auth.TokenRecord)}
}

func (f *fakeStore) Save(_ context.Context, rec auth.TokenRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[rec.PluginName] = rec
	return nil
}

func (f *fakeStore) Load(_ context.Context, pluginName string) (auth.TokenRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.records[pluginName]
	if !ok {
		return auth.TokenRecord{}, auth.ErrTokenNotFound
	}
	return rec, nil
}

func (f *fakeStore) Delete(_ context.Context, pluginName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.records, pluginName)
	return nil
}

// fakeEventBus collects emitted events for assertions.
type fakeEventBus struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func (b *fakeEventBus) Emit(_ context.Context, e eventbus.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}

func (b *fakeEventBus) Subscribe(name string, _ eventbus.Handler) eventbus.Subscription {
	return eventbus.Subscription{}
}
func (b *fakeEventBus) Unsubscribe(_ eventbus.Subscription) {}
func (b *fakeEventBus) Close() error                        { return nil }

func (b *fakeEventBus) lastEvent() (eventbus.Event, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) == 0 {
		return eventbus.Event{}, false
	}
	return b.events[len(b.events)-1], true
}

func TestDefaultTokenService_GenerateAndSave(t *testing.T) {
	store := newFakeStore()
	bus := &fakeEventBus{}
	svc := auth.NewTokenService(store, bus)
	ctx := context.Background()

	token, err := svc.GenerateAndSave(ctx, "myplugin")
	if err != nil {
		t.Fatalf("GenerateAndSave: %v", err)
	}
	// Token must pass format validation.
	if err := auth.ValidateTokenFormat(token); err != nil {
		t.Errorf("generated token fails format validation: %v", err)
	}
	// Must be persisted.
	rec, err := store.Load(ctx, "myplugin")
	if err != nil {
		t.Fatalf("Load after GenerateAndSave: %v", err)
	}
	if rec.Token != token {
		t.Errorf("stored token = %q, want %q", rec.Token, token)
	}
	// EventTokenCreated must be emitted.
	ev, ok := bus.lastEvent()
	if !ok {
		t.Fatal("expected event to be emitted")
	}
	if ev.Name != eventbus.EventTokenCreated {
		t.Errorf("event name = %q, want %q", ev.Name, eventbus.EventTokenCreated)
	}
	payload, ok := ev.Payload.(eventbus.TokenCreatedPayload)
	if !ok {
		t.Fatalf("event payload type = %T, want TokenCreatedPayload", ev.Payload)
	}
	if payload.PluginName != "myplugin" {
		t.Errorf("payload.PluginName = %q, want %q", payload.PluginName, "myplugin")
	}
}

func TestDefaultTokenService_GenerateAndSave_NilBus(t *testing.T) {
	store := newFakeStore()
	// nil bus must not panic.
	svc := auth.NewTokenService(store, nil)
	ctx := context.Background()

	token, err := svc.GenerateAndSave(ctx, "plugin-nilbus")
	if err != nil {
		t.Fatalf("GenerateAndSave with nil bus: %v", err)
	}
	if err := auth.ValidateTokenFormat(token); err != nil {
		t.Errorf("token format invalid: %v", err)
	}
}

func TestDefaultTokenService_Retrieve(t *testing.T) {
	store := newFakeStore()
	svc := auth.NewTokenService(store, nil)
	ctx := context.Background()

	// First generate.
	generated, err := svc.GenerateAndSave(ctx, "plugin-retrieve")
	if err != nil {
		t.Fatalf("GenerateAndSave: %v", err)
	}

	retrieved, err := svc.Retrieve(ctx, "plugin-retrieve")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if retrieved != generated {
		t.Errorf("Retrieve = %q, want %q", retrieved, generated)
	}
}

func TestDefaultTokenService_Retrieve_NotFound(t *testing.T) {
	store := newFakeStore()
	svc := auth.NewTokenService(store, nil)
	ctx := context.Background()

	_, err := svc.Retrieve(ctx, "ghost")
	if !errors.Is(err, auth.ErrTokenNotFound) {
		t.Errorf("Retrieve nonexistent: error = %v, want ErrTokenNotFound", err)
	}
}

func TestDefaultTokenService_Rotate(t *testing.T) {
	store := newFakeStore()
	bus := &fakeEventBus{}
	svc := auth.NewTokenService(store, bus)
	ctx := context.Background()

	original, err := svc.GenerateAndSave(ctx, "rotateme")
	if err != nil {
		t.Fatalf("GenerateAndSave: %v", err)
	}

	newToken, err := svc.Rotate(ctx, "rotateme")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if newToken == original {
		t.Error("Rotate must produce a different token")
	}
	if err := auth.ValidateTokenFormat(newToken); err != nil {
		t.Errorf("rotated token fails format validation: %v", err)
	}
	// Persisted token must be the new one.
	rec, err := store.Load(ctx, "rotateme")
	if err != nil {
		t.Fatalf("Load after Rotate: %v", err)
	}
	if rec.Token != newToken {
		t.Errorf("stored token after rotate = %q, want %q", rec.Token, newToken)
	}
	// EventTokenRotated must be emitted.
	ev, ok := bus.lastEvent()
	if !ok {
		t.Fatal("expected event to be emitted")
	}
	if ev.Name != eventbus.EventTokenRotated {
		t.Errorf("event name = %q, want %q", ev.Name, eventbus.EventTokenRotated)
	}
	payload, ok := ev.Payload.(eventbus.TokenRotatedPayload)
	if !ok {
		t.Fatalf("event payload type = %T, want TokenRotatedPayload", ev.Payload)
	}
	if payload.PluginName != "rotateme" {
		t.Errorf("payload.PluginName = %q, want rotateme", payload.PluginName)
	}
}

func TestDefaultTokenService_Rotate_PluginNotFound(t *testing.T) {
	store := newFakeStore()
	svc := auth.NewTokenService(store, nil)
	ctx := context.Background()

	_, err := svc.Rotate(ctx, "ghost")
	if !errors.Is(err, auth.ErrTokenNotFound) {
		t.Errorf("Rotate nonexistent: error = %v, want ErrTokenNotFound", err)
	}
}

func TestDefaultTokenService_Delete(t *testing.T) {
	store := newFakeStore()
	svc := auth.NewTokenService(store, nil)
	ctx := context.Background()

	if _, err := svc.GenerateAndSave(ctx, "killme"); err != nil {
		t.Fatalf("GenerateAndSave: %v", err)
	}
	if err := svc.Delete(ctx, "killme"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := svc.Retrieve(ctx, "killme")
	if !errors.Is(err, auth.ErrTokenNotFound) {
		t.Errorf("Retrieve after Delete: error = %v, want ErrTokenNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Concurrency: multiple GenerateAndSave calls must not race.
// ---------------------------------------------------------------------------

func TestDefaultTokenService_ConcurrentGenerateAndSave(t *testing.T) {
	store := newFakeStore()
	svc := auth.NewTokenService(store, nil)
	ctx := context.Background()

	const n = 20
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := svc.GenerateAndSave(ctx, "concurrent-plugin")
			errCh <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent GenerateAndSave error: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// SQLiteTokenStore: parameterized query safety (SQL injection attempt)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// SQLiteTokenStore: closed-DB error paths
// ---------------------------------------------------------------------------

// newRawStore returns the concrete *SQLiteTokenStore so tests can call Close.
func newRawStore(t *testing.T) *auth.SQLiteTokenStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tokens.db")
	store, err := auth.NewSQLiteTokenStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteTokenStore: %v", err)
	}
	return store
}

func TestSQLiteTokenStore_Save_AfterClose(t *testing.T) {
	store := newRawStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	rec := auth.TokenRecord{
		PluginName: "myplugin",
		Token:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	err := store.Save(ctx, rec)
	if err == nil {
		t.Error("expected error when saving to closed DB, got nil")
	}
}

func TestSQLiteTokenStore_Delete_AfterClose(t *testing.T) {
	store := newRawStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	rec := auth.TokenRecord{
		PluginName: "deleteme",
		Token:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	err := store.Delete(ctx, "deleteme")
	if err == nil {
		t.Error("expected error when deleting from closed DB, got nil")
	}
}

func TestSQLiteTokenStore_Load_AfterClose(t *testing.T) {
	store := newRawStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	rec := auth.TokenRecord{
		PluginName: "loadme",
		Token:      "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := store.Load(ctx, "loadme")
	if err == nil {
		t.Error("expected error when loading from closed DB, got nil")
	}
}

// TestSQLiteTokenStore_Load_CorruptUpdatedAt triggers the parse updated_at error path
// by inserting a record with an invalid updated_at timestamp value directly into the DB.
func TestSQLiteTokenStore_Load_CorruptUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tokens.db")

	// First create the store so the schema is established.
	store, err := auth.NewSQLiteTokenStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteTokenStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Open a second raw connection to insert a corrupt record directly.
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("raw sql.Open: %v", err)
	}
	defer func() { _ = rawDB.Close() }()

	// Insert a record with valid created_at but an invalid updated_at value.
	_, err = rawDB.Exec(
		`INSERT INTO tokens (plugin_name, token, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"corrupt-plugin",
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"2024-01-01T00:00:00Z",
		"not-a-valid-datetime",
	)
	if err != nil {
		t.Fatalf("insert corrupt record: %v", err)
	}

	ctx := context.Background()
	_, loadErr := store.Load(ctx, "corrupt-plugin")
	if loadErr == nil {
		t.Error("expected error when loading record with corrupt updated_at, got nil")
	}
}

// TestSQLiteTokenStore_Load_CorruptCreatedAt triggers the parse created_at error path.
func TestSQLiteTokenStore_Load_CorruptCreatedAt(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tokens.db")

	store, err := auth.NewSQLiteTokenStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteTokenStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("raw sql.Open: %v", err)
	}
	defer func() { _ = rawDB.Close() }()

	// Insert a record with an invalid created_at value.
	_, err = rawDB.Exec(
		`INSERT INTO tokens (plugin_name, token, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"corrupt-created",
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"not-a-valid-datetime",
		"2024-01-01T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("insert corrupt record: %v", err)
	}

	ctx := context.Background()
	_, loadErr := store.Load(ctx, "corrupt-created")
	if loadErr == nil {
		t.Error("expected error when loading record with corrupt created_at, got nil")
	}
}

// ---------------------------------------------------------------------------
// DefaultTokenService: store error propagation
// ---------------------------------------------------------------------------

// errStore always returns an error from Save.
type errStore struct {
	saveErr   error
	loadErr   error
	deleteErr error
	fallback  *fakeStore
}

func newErrStore(saveErr, loadErr, deleteErr error) *errStore {
	return &errStore{
		saveErr:   saveErr,
		loadErr:   loadErr,
		deleteErr: deleteErr,
		fallback:  newFakeStore(),
	}
}

func (e *errStore) Save(ctx context.Context, rec auth.TokenRecord) error {
	if e.saveErr != nil {
		return e.saveErr
	}
	return e.fallback.Save(ctx, rec)
}

func (e *errStore) Load(ctx context.Context, pluginName string) (auth.TokenRecord, error) {
	if e.loadErr != nil {
		return auth.TokenRecord{}, e.loadErr
	}
	return e.fallback.Load(ctx, pluginName)
}

func (e *errStore) Delete(ctx context.Context, pluginName string) error {
	if e.deleteErr != nil {
		return e.deleteErr
	}
	return e.fallback.Delete(ctx, pluginName)
}

func TestDefaultTokenService_GenerateAndSave_SaveError(t *testing.T) {
	saveErr := errors.New("storage unavailable")
	store := newErrStore(saveErr, nil, nil)
	svc := auth.NewTokenService(store, nil)
	ctx := context.Background()

	_, err := svc.GenerateAndSave(ctx, "myplugin")
	if err == nil {
		t.Fatal("expected error when store.Save fails, got nil")
	}
}

func TestDefaultTokenService_Rotate_SaveError(t *testing.T) {
	// Must have an existing token for Rotate to proceed past the Load check.
	now := time.Now().UTC()
	existing := auth.TokenRecord{
		PluginName: "myplugin",
		Token:      "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// A store that Load succeeds but Save returns an error.
	saveErr := errors.New("disk full")
	store := &errStore{
		saveErr: saveErr,
		fallback: &fakeStore{
			records: map[string]auth.TokenRecord{
				"myplugin": existing,
			},
		},
	}

	svc := auth.NewTokenService(store, nil)
	ctx := context.Background()

	_, err := svc.Rotate(ctx, "myplugin")
	if err == nil {
		t.Fatal("expected error when Rotate save fails, got nil")
	}
}

// ---------------------------------------------------------------------------
// Concurrent Rotate safety
// ---------------------------------------------------------------------------

func TestDefaultTokenService_ConcurrentRotate(t *testing.T) {
	store := newFakeStore()
	svc := auth.NewTokenService(store, nil)
	ctx := context.Background()

	// Set up initial token.
	if _, err := svc.GenerateAndSave(ctx, "concurrent-rotate"); err != nil {
		t.Fatalf("GenerateAndSave: %v", err)
	}

	const n = 10
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := svc.Rotate(ctx, "concurrent-rotate")
			errCh <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent Rotate error: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Token format uniqueness (generateToken output validity)
// ---------------------------------------------------------------------------

func TestDefaultTokenService_GeneratedTokensAreUnique(t *testing.T) {
	store := newFakeStore()
	svc := auth.NewTokenService(store, nil)
	ctx := context.Background()

	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		pluginName := "plugin-unique"
		token, err := svc.GenerateAndSave(ctx, pluginName)
		if err != nil {
			t.Fatalf("GenerateAndSave: %v", err)
		}
		if seen[token] {
			t.Errorf("duplicate token generated: %s", token)
		}
		seen[token] = true
	}
}

func TestSQLiteTokenStore_SQLInjection(t *testing.T) {
	store := newTempStore(t)
	ctx := context.Background()

	// Plugin name that looks like SQL. Should be stored/retrieved as a literal string.
	maliciousName := "'; DROP TABLE tokens; --"
	now := time.Now().UTC().Truncate(time.Second)
	rec := auth.TokenRecord{
		PluginName: maliciousName,
		Token:      "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save with injection name: %v", err)
	}

	loaded, err := store.Load(ctx, maliciousName)
	if err != nil {
		t.Fatalf("Load with injection name: %v", err)
	}
	if loaded.PluginName != maliciousName {
		t.Errorf("PluginName = %q, want %q", loaded.PluginName, maliciousName)
	}

	// Normal plugin must still exist (table not dropped).
	normalRec := auth.TokenRecord{
		PluginName: "normal-plugin",
		Token:      "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.Save(ctx, normalRec); err != nil {
		t.Fatalf("Save normal plugin after injection attempt: %v", err)
	}
}
