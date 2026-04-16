package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, no CGO
)

// Sentinel errors.
var (
	ErrTokenNotFound      = errors.New("token not found")
	ErrTokenFormatInvalid = errors.New("token format invalid: must be 64 lowercase hex characters")
)

// tokenFormatRE matches exactly 64 lowercase hexadecimal characters.
var tokenFormatRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidateTokenFormat returns ErrTokenFormatInvalid if token is not exactly
// 64 lowercase hexadecimal characters.
func ValidateTokenFormat(token string) error {
	if !tokenFormatRE.MatchString(token) {
		return ErrTokenFormatInvalid
	}
	return nil
}

// TokenRecord holds a bearer token for a plugin.
type TokenRecord struct {
	PluginName string
	Token      string    // 64-char lowercase hex, never logged
	CreatedAt  time.Time // UTC
	UpdatedAt  time.Time // UTC
}

// TokenStore persists and retrieves TokenRecords.
type TokenStore interface {
	Save(ctx context.Context, rec TokenRecord) error
	Load(ctx context.Context, pluginName string) (TokenRecord, error)
	Delete(ctx context.Context, pluginName string) error
}

// TokenService provides high-level token management for plugins.
type TokenService interface {
	GenerateAndSave(ctx context.Context, pluginName string) (token string, err error)
	Retrieve(ctx context.Context, pluginName string) (token string, err error)
	Rotate(ctx context.Context, pluginName string) (newToken string, err error)
	Delete(ctx context.Context, pluginName string) error
}

// ---------------------------------------------------------------------------
// SQLiteTokenStore
// ---------------------------------------------------------------------------

// SQLiteTokenStore is a TokenStore backed by a per-plugin SQLite database.
// The database file path is provided at construction time.
type SQLiteTokenStore struct {
	db *sql.DB
}

// NewSQLiteTokenStore opens (or creates) the SQLite database at dbPath and
// ensures the tokens table exists. The caller is responsible for calling
// Close() on the returned store.
func NewSQLiteTokenStore(dbPath string) (*SQLiteTokenStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open token store db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping token store db: %w", err)
	}
	if err := createSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create token store schema: %w", err)
	}
	return &SQLiteTokenStore{db: db}, nil
}

// createSchema ensures the tokens table exists.
func createSchema(db *sql.DB) error {
	const q = `CREATE TABLE IF NOT EXISTS tokens (
		plugin_name TEXT PRIMARY KEY,
		token       TEXT NOT NULL,
		created_at  DATETIME NOT NULL,
		updated_at  DATETIME NOT NULL
	)`
	_, err := db.Exec(q)
	return err
}

// Close releases the underlying database connection.
func (s *SQLiteTokenStore) Close() error {
	return s.db.Close()
}

// Save inserts or replaces the token record for the given plugin.
// All SQL uses ? placeholders: no string interpolation.
func (s *SQLiteTokenStore) Save(ctx context.Context, rec TokenRecord) error {
	const q = `INSERT INTO tokens (plugin_name, token, created_at, updated_at)
	           VALUES (?, ?, ?, ?)
	           ON CONFLICT(plugin_name) DO UPDATE SET
	               token      = excluded.token,
	               updated_at = excluded.updated_at`
	_, err := s.db.ExecContext(ctx, q,
		rec.PluginName,
		rec.Token,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save token record: %w", err)
	}
	return nil
}

// Load retrieves the token record for pluginName.
// Returns ErrTokenNotFound if no record exists.
func (s *SQLiteTokenStore) Load(ctx context.Context, pluginName string) (TokenRecord, error) {
	const q = `SELECT plugin_name, token, created_at, updated_at
	           FROM tokens WHERE plugin_name = ?`
	row := s.db.QueryRowContext(ctx, q, pluginName)
	var rec TokenRecord
	var createdStr, updatedStr string
	if err := row.Scan(&rec.PluginName, &rec.Token, &createdStr, &updatedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TokenRecord{}, ErrTokenNotFound
		}
		return TokenRecord{}, fmt.Errorf("load token record: %w", err)
	}
	var parseErr error
	rec.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, createdStr)
	if parseErr != nil {
		return TokenRecord{}, fmt.Errorf("parse created_at: %w", parseErr)
	}
	rec.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updatedStr)
	if parseErr != nil {
		return TokenRecord{}, fmt.Errorf("parse updated_at: %w", parseErr)
	}
	return rec, nil
}

// Delete removes the token record for pluginName.
// Returns nil if the record does not exist.
func (s *SQLiteTokenStore) Delete(ctx context.Context, pluginName string) error {
	const q = `DELETE FROM tokens WHERE plugin_name = ?`
	_, err := s.db.ExecContext(ctx, q, pluginName)
	if err != nil {
		return fmt.Errorf("delete token record: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// DefaultTokenService
// ---------------------------------------------------------------------------

// DefaultTokenService implements TokenService using a TokenStore and optional EventBus.
type DefaultTokenService struct {
	store TokenStore
	bus   eventbus.EventBus // may be nil
}

// NewTokenService constructs a DefaultTokenService.
// bus may be nil; when nil, events are not emitted.
func NewTokenService(store TokenStore, bus eventbus.EventBus) *DefaultTokenService {
	return &DefaultTokenService{store: store, bus: bus}
}

// GenerateAndSave generates a new 32-byte random token encoded as 64 lowercase
// hex characters, persists it, and emits EventTokenCreated.
func (svc *DefaultTokenService) GenerateAndSave(ctx context.Context, pluginName string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	// Validate before saving: defense in depth.
	if err := ValidateTokenFormat(token); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	rec := TokenRecord{
		PluginName: pluginName,
		Token:      token,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := svc.store.Save(ctx, rec); err != nil {
		return "", fmt.Errorf("save token: %w", err)
	}
	svc.emit(ctx, eventbus.Event{
		Name:         eventbus.EventTokenCreated,
		SourcePlugin: pluginName,
		Payload: eventbus.TokenCreatedPayload{
			PluginName: pluginName,
			CreatedAt:  now,
		},
	})
	return token, nil
}

// Retrieve returns the current token for pluginName.
func (svc *DefaultTokenService) Retrieve(ctx context.Context, pluginName string) (string, error) {
	rec, err := svc.store.Load(ctx, pluginName)
	if err != nil {
		return "", err
	}
	return rec.Token, nil
}

// Rotate generates a new token for pluginName, replacing the existing one.
// Returns ErrTokenNotFound if no token exists for the plugin.
func (svc *DefaultTokenService) Rotate(ctx context.Context, pluginName string) (string, error) {
	// Verify the plugin already has a token before generating a replacement.
	if _, err := svc.store.Load(ctx, pluginName); err != nil {
		return "", err
	}
	newToken, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generate rotation token: %w", err)
	}
	if err := ValidateTokenFormat(newToken); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	rec := TokenRecord{
		PluginName: pluginName,
		Token:      newToken,
		CreatedAt:  now, // updated in upsert path: created_at preserved by ON CONFLICT clause
		UpdatedAt:  now,
	}
	if err := svc.store.Save(ctx, rec); err != nil {
		return "", fmt.Errorf("save rotated token: %w", err)
	}
	svc.emit(ctx, eventbus.Event{
		Name:         eventbus.EventTokenRotated,
		SourcePlugin: pluginName,
		Payload: eventbus.TokenRotatedPayload{
			PluginName: pluginName,
			RotatedAt:  now,
		},
	})
	return newToken, nil
}

// Delete removes the token record for pluginName.
func (svc *DefaultTokenService) Delete(ctx context.Context, pluginName string) error {
	return svc.store.Delete(ctx, pluginName)
}

// emit publishes an event to the bus if one is configured.
// Safe to call with a nil bus.
func (svc *DefaultTokenService) emit(ctx context.Context, e eventbus.Event) {
	if svc.bus != nil {
		svc.bus.Emit(ctx, e)
	}
}

// generateToken produces 32 cryptographically random bytes encoded as 64
// lowercase hexadecimal characters.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
