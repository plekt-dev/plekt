package audit

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrAuditLogUnavailable is returned when the audit log store cannot complete an operation.
var ErrAuditLogUnavailable = errors.New("audit log store unavailable")

// AuditLogEntry is a single audit event record.
type AuditLogEntry struct {
	ID         int64
	EventName  string
	RemoteAddr string
	SessionID  string
	PluginName string
	Detail     string
	OccurredAt time.Time
	RecordedAt time.Time
}

// AuditFilter describes how to filter and paginate audit log queries.
type AuditFilter struct {
	EventPrefixes []string // e.g. ["web.auth.", "auth.token.", "web.user."]: OR match
	Search        string   // free-text search across event_name, detail, plugin_name, remote_addr
	Limit         int      // 0 → default 100
	Offset        int
}

// PrefixCount holds the result of a count-by-prefix query.
type PrefixCount struct {
	Prefix string
	Count  int
}

// AuditLogStore persists and retrieves audit log entries.
type AuditLogStore interface {
	Append(ctx context.Context, entry AuditLogEntry) error
	ListRecent(ctx context.Context, n int) ([]AuditLogEntry, error)
	ListFiltered(ctx context.Context, f AuditFilter) (entries []AuditLogEntry, total int, err error)
	CountByPrefixes(ctx context.Context, prefixes [][]string) ([]int, error)
	Close() error
}

// sqliteAuditLogStore is the SQLite-backed AuditLogStore implementation.
type sqliteAuditLogStore struct {
	db *sql.DB
}

// NewSQLiteAuditLogStore creates and initialises a SQLite-backed AuditLogStore.
// The caller is responsible for opening the *sql.DB. The store takes ownership
// of the db handle and closes it on Close().
func NewSQLiteAuditLogStore(db *sql.DB) (AuditLogStore, error) {
	const createTable = `
CREATE TABLE IF NOT EXISTS audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_name  TEXT    NOT NULL DEFAULT '',
    remote_addr TEXT    NOT NULL DEFAULT '',
    session_id  TEXT    NOT NULL DEFAULT '',
    plugin_name TEXT    NOT NULL DEFAULT '',
    detail      TEXT    NOT NULL DEFAULT '',
    occurred_at INTEGER NOT NULL DEFAULT 0,
    recorded_at INTEGER NOT NULL DEFAULT 0
);`
	const createIndex = `
CREATE INDEX IF NOT EXISTS audit_log_occurred_at_desc
    ON audit_log (occurred_at DESC);`

	if _, err := db.Exec(createTable); err != nil {
		return nil, err
	}
	if _, err := db.Exec(createIndex); err != nil {
		return nil, err
	}
	return &sqliteAuditLogStore{db: db}, nil
}

// Append inserts a new audit log entry. RecordedAt is always set to time.Now().UTC().
func (s *sqliteAuditLogStore) Append(ctx context.Context, entry AuditLogEntry) error {
	const q = `
INSERT INTO audit_log (event_name, remote_addr, session_id, plugin_name, detail, occurred_at, recorded_at)
VALUES (?, ?, ?, ?, ?, ?, ?);`
	recordedAt := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, q,
		entry.EventName,
		entry.RemoteAddr,
		entry.SessionID,
		entry.PluginName,
		entry.Detail,
		entry.OccurredAt.UnixNano(),
		recordedAt.UnixNano(),
	)
	return err
}

// ListRecent returns the n most recent audit log entries, newest first.
func (s *sqliteAuditLogStore) ListRecent(ctx context.Context, n int) ([]AuditLogEntry, error) {
	const q = `
SELECT id, event_name, remote_addr, session_id, plugin_name, detail, occurred_at, recorded_at
FROM audit_log
ORDER BY occurred_at DESC
LIMIT ?;`

	rows, err := s.db.QueryContext(ctx, q, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]AuditLogEntry, 0)
	for rows.Next() {
		var e AuditLogEntry
		var occurredNano, recordedNano int64
		if err := rows.Scan(
			&e.ID,
			&e.EventName,
			&e.RemoteAddr,
			&e.SessionID,
			&e.PluginName,
			&e.Detail,
			&occurredNano,
			&recordedNano,
		); err != nil {
			return nil, err
		}
		e.OccurredAt = time.Unix(0, occurredNano).UTC()
		e.RecordedAt = time.Unix(0, recordedNano).UTC()
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// ListFiltered returns audit log entries matching the given filter, newest first,
// along with the total count of matching entries (for pagination).
func (s *sqliteAuditLogStore) ListFiltered(ctx context.Context, f AuditFilter) ([]AuditLogEntry, int, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}

	var where []string
	var args []interface{}

	if len(f.EventPrefixes) > 0 {
		orClauses := make([]string, 0, len(f.EventPrefixes))
		for _, p := range f.EventPrefixes {
			orClauses = append(orClauses, "event_name LIKE ?")
			args = append(args, p+"%")
		}
		where = append(where, "("+orClauses[0])
		for _, c := range orClauses[1:] {
			where[len(where)-1] += " OR " + c
		}
		where[len(where)-1] += ")"
	}
	if f.Search != "" {
		pattern := "%" + f.Search + "%"
		where = append(where, "(event_name LIKE ? OR detail LIKE ? OR plugin_name LIKE ? OR remote_addr LIKE ?)")
		args = append(args, pattern, pattern, pattern, pattern)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + where[0]
		for _, w := range where[1:] {
			whereClause += " AND " + w
		}
	}

	// Count total matching.
	countQ := "SELECT COUNT(*) FROM audit_log " + whereClause
	var total int
	if err := s.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Fetch page.
	dataQ := "SELECT id, event_name, remote_addr, session_id, plugin_name, detail, occurred_at, recorded_at FROM audit_log " +
		whereClause + " ORDER BY occurred_at DESC LIMIT ? OFFSET ?"
	dataArgs := append(args, limit, f.Offset) //nolint:gocritic // append to copy is intentional

	rows, err := s.db.QueryContext(ctx, dataQ, dataArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	entries := make([]AuditLogEntry, 0)
	for rows.Next() {
		var e AuditLogEntry
		var occurredNano, recordedNano int64
		if err := rows.Scan(
			&e.ID, &e.EventName, &e.RemoteAddr, &e.SessionID,
			&e.PluginName, &e.Detail, &occurredNano, &recordedNano,
		); err != nil {
			return nil, 0, err
		}
		e.OccurredAt = time.Unix(0, occurredNano).UTC()
		e.RecordedAt = time.Unix(0, recordedNano).UTC()
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

// CountByPrefixes counts events for each group of prefixes.
// prefixes[i] is a slice of event_name prefixes (OR-ed within group).
// Returns counts[i] for each group.
func (s *sqliteAuditLogStore) CountByPrefixes(ctx context.Context, prefixes [][]string) ([]int, error) {
	counts := make([]int, len(prefixes))
	for i, group := range prefixes {
		if len(group) == 0 {
			// Empty group = count all.
			if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&counts[i]); err != nil {
				return nil, err
			}
			continue
		}
		q := "SELECT COUNT(*) FROM audit_log WHERE "
		args := make([]interface{}, 0, len(group))
		for j, p := range group {
			if j > 0 {
				q += " OR "
			}
			q += "event_name LIKE ?"
			args = append(args, p+"%")
		}
		if err := s.db.QueryRowContext(ctx, q, args...).Scan(&counts[i]); err != nil {
			return nil, err
		}
	}
	return counts, nil
}

// Close releases the underlying database connection.
func (s *sqliteAuditLogStore) Close() error {
	return s.db.Close()
}
