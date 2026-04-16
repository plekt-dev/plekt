package settings

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"

	_ "modernc.org/sqlite"
)

// key names for the settings table.
const (
	keyAdminEmail          = "admin_email"
	keyAllowPluginInstall  = "allow_plugin_install"
	keySessionTTLMinutes   = "session_ttl_minutes"
	keyRegistrationEnabled = "registration_enabled"
	keyPasswordMinLength   = "password_min_length"
)

// sqliteSettingsStore is the SQLite-backed SettingsStore implementation.
type sqliteSettingsStore struct {
	db *sql.DB
}

// NewSQLiteSettingsStore creates a new SQLite-backed SettingsStore.
// It creates the settings table if it does not already exist.
// The caller is responsible for opening the *sql.DB. The store takes ownership
// of the db handle and closes it on Close().
func NewSQLiteSettingsStore(db *sql.DB) (SettingsStore, error) {
	if db == nil {
		return nil, errors.New("settings store: db must not be nil")
	}
	const createTable = `
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);`
	if _, err := db.Exec(createTable); err != nil {
		return nil, err
	}
	return &sqliteSettingsStore{db: db}, nil
}

// Load reads all settings rows and populates a Settings struct.
// Missing keys yield zero values for that field.
func (s *sqliteSettingsStore) Load(ctx context.Context) (Settings, error) {
	const q = `SELECT key, value FROM settings;`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return Settings{}, err
	}
	defer rows.Close()

	kv := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Settings{}, err
		}
		kv[k] = v
	}
	if err := rows.Err(); err != nil {
		return Settings{}, err
	}

	out := Settings{}
	out.AdminEmail = kv[keyAdminEmail]
	if v, ok := kv[keyAllowPluginInstall]; ok {
		out.AllowPluginInstall = v == "1"
	}
	if v, ok := kv[keySessionTTLMinutes]; ok {
		if n, err := strconv.Atoi(v); err != nil {
			slog.Warn("settings: invalid integer value in store", "key", keySessionTTLMinutes, "value", v)
		} else {
			out.SessionTTLMinutes = n
		}
	}
	if v, ok := kv[keyRegistrationEnabled]; ok {
		out.RegistrationEnabled = v == "1"
	}
	if v, ok := kv[keyPasswordMinLength]; ok {
		if n, err := strconv.Atoi(v); err != nil {
			slog.Warn("settings: invalid integer value in store", "key", keyPasswordMinLength, "value", v)
		} else {
			out.PasswordMinLength = n
		}
	}
	if out.PasswordMinLength <= 0 {
		out.PasswordMinLength = 12
	}
	return out, nil
}

// Save persists all fields of s to the database in a single transaction
// using INSERT OR REPLACE for each key.
func (s *sqliteSettingsStore) Save(ctx context.Context, st Settings) error {
	allowVal := "0"
	if st.AllowPluginInstall {
		allowVal = "1"
	}
	registrationVal := "0"
	if st.RegistrationEnabled {
		registrationVal = "1"
	}
	minLen := st.PasswordMinLength
	if minLen <= 0 {
		minLen = 12
	}

	pairs := []struct{ k, v string }{
		{keyAdminEmail, st.AdminEmail},
		{keyAllowPluginInstall, allowVal},
		{keySessionTTLMinutes, strconv.Itoa(st.SessionTTLMinutes)},
		{keyRegistrationEnabled, registrationVal},
		{keyPasswordMinLength, strconv.Itoa(minLen)},
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const upsert = `INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?);`
	for _, p := range pairs {
		if _, err := tx.ExecContext(ctx, upsert, p.k, p.v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetRaw retrieves a raw key-value setting by key.
// Returns ErrSettingNotFound if the key does not exist.
func (s *sqliteSettingsStore) GetRaw(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrSettingNotFound
		}
		return "", err
	}
	return value, nil
}

// SetRaw persists a raw key-value setting using INSERT OR REPLACE.
func (s *sqliteSettingsStore) SetRaw(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, key, value)
	return err
}

// DeleteRaw removes a raw key-value setting by key.
func (s *sqliteSettingsStore) DeleteRaw(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return err
}

// Close releases the underlying database connection.
func (s *sqliteSettingsStore) Close() error {
	return s.db.Close()
}
