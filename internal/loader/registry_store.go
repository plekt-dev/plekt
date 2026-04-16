package loader

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrRegistryEntryNotFound is returned when a plugin registry entry is not found.
var ErrRegistryEntryNotFound = errors.New("plugin registry entry not found")

// PluginRegistryEntry represents a persisted plugin record in the registry.
type PluginRegistryEntry struct {
	Name      string
	Dir       string
	Version   string
	LoadedAt  time.Time
	UpdatedAt time.Time
}

// PluginRegistryStore persists plugin registry entries to a database.
type PluginRegistryStore interface {
	Upsert(ctx context.Context, entry PluginRegistryEntry) error
	Delete(ctx context.Context, name string) error
	List(ctx context.Context) ([]PluginRegistryEntry, error)
	Get(ctx context.Context, name string) (PluginRegistryEntry, error)
	Close() error
}

const pluginRegistrySchema = `
CREATE TABLE IF NOT EXISTS plugin_registry (
    name       TEXT PRIMARY KEY,
    dir        TEXT NOT NULL,
    version    TEXT NOT NULL DEFAULT '',
    loaded_at  TEXT NOT NULL,
    updated_at TEXT NOT NULL
)`

// sqlitePluginRegistryStore is the SQLite-backed PluginRegistryStore.
type sqlitePluginRegistryStore struct {
	db *sql.DB
}

// NewSQLitePluginRegistryStore creates the plugin_registry table (if absent) and
// returns a PluginRegistryStore backed by the provided *sql.DB.
// Returns an error if db is nil or table creation fails.
func NewSQLitePluginRegistryStore(db *sql.DB) (PluginRegistryStore, error) {
	if db == nil {
		return nil, errors.New("db must not be nil")
	}
	if _, err := db.Exec(pluginRegistrySchema); err != nil {
		return nil, err
	}
	// Migration: drop legacy bearer_token column if it exists.
	if columnExists(db, "plugin_registry", "bearer_token") {
		_, _ = db.Exec(`ALTER TABLE plugin_registry DROP COLUMN bearer_token`)
	}
	return &sqlitePluginRegistryStore{db: db}, nil
}

// columnExists checks whether a column exists in a SQLite table.
func columnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// Upsert inserts or replaces a plugin registry entry.
func (s *sqlitePluginRegistryStore) Upsert(ctx context.Context, e PluginRegistryEntry) error {
	const q = `
		INSERT INTO plugin_registry (name, dir, version, loaded_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			dir        = excluded.dir,
			version    = excluded.version,
			loaded_at  = loaded_at,
			updated_at = excluded.updated_at`
	_, err := s.db.ExecContext(ctx, q,
		e.Name,
		e.Dir,
		e.Version,
		e.LoadedAt.UTC().Format(time.RFC3339Nano),
		e.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

// Delete removes the registry entry for the given plugin name.
// Deleting a non-existent name is a no-op (idempotent).
func (s *sqlitePluginRegistryStore) Delete(ctx context.Context, name string) error {
	const q = `DELETE FROM plugin_registry WHERE name = ?`
	_, err := s.db.ExecContext(ctx, q, name)
	return err
}

// List returns all registry entries.
func (s *sqlitePluginRegistryStore) List(ctx context.Context) ([]PluginRegistryEntry, error) {
	const q = `SELECT name, dir, version, loaded_at, updated_at FROM plugin_registry ORDER BY name`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []PluginRegistryEntry
	for rows.Next() {
		var e PluginRegistryEntry
		var loadedAtStr, updatedAtStr string
		if err := rows.Scan(&e.Name, &e.Dir, &e.Version, &loadedAtStr, &updatedAtStr); err != nil {
			return nil, err
		}
		e.LoadedAt, err = time.Parse(time.RFC3339Nano, loadedAtStr)
		if err != nil {
			return nil, err
		}
		e.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAtStr)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// Get returns the registry entry for the given plugin name.
// Returns ErrRegistryEntryNotFound if no entry exists.
func (s *sqlitePluginRegistryStore) Get(ctx context.Context, name string) (PluginRegistryEntry, error) {
	const q = `SELECT name, dir, version, loaded_at, updated_at FROM plugin_registry WHERE name = ?`
	row := s.db.QueryRowContext(ctx, q, name)
	var e PluginRegistryEntry
	var loadedAtStr, updatedAtStr string
	if err := row.Scan(&e.Name, &e.Dir, &e.Version, &loadedAtStr, &updatedAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PluginRegistryEntry{}, ErrRegistryEntryNotFound
		}
		return PluginRegistryEntry{}, err
	}
	var err error
	e.LoadedAt, err = time.Parse(time.RFC3339Nano, loadedAtStr)
	if err != nil {
		return PluginRegistryEntry{}, err
	}
	e.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAtStr)
	if err != nil {
		return PluginRegistryEntry{}, err
	}
	return e, nil
}

// Close is a no-op on the store itself; callers manage the *sql.DB lifecycle.
func (s *sqlitePluginRegistryStore) Close() error {
	return nil
}
