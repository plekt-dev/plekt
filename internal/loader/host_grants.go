// Package loader: host grants store.
//
// plugin_host_grants is the single source of truth for which outbound hosts a
// plugin is allowed to reach via Extism AllowedHosts. The store is written to
// ONLY by the operator (install modal or Permissions settings page). Plugins
// have no host function to write here.
package loader

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HostGrant is one row in plugin_host_grants.
type HostGrant struct {
	PluginName string    `json:"plugin_name"`
	Host       string    `json:"host"`
	GrantedBy  string    `json:"granted_by"`
	GrantedAt  time.Time `json:"granted_at"`
	Source     string    `json:"source"` // "install" | "operator"
}

// HostGrant errors.
var (
	ErrHostGrantNotFound = errors.New("host grant not found")
	ErrInvalidHost       = errors.New("invalid host format")
	ErrHostGrantConflict = errors.New("host grant already exists")
)

// HostGrantStore persists per-plugin host grants.
//
// Grant is an upsert (INSERT OR REPLACE). Revoke is a hard delete and returns
// ErrHostGrantNotFound if the (plugin, host) pair did not exist.
type HostGrantStore interface {
	Grant(ctx context.Context, g HostGrant) error
	Revoke(ctx context.Context, pluginName, host string) error
	List(ctx context.Context, pluginName string) ([]HostGrant, error)
	ListAll(ctx context.Context) (map[string][]HostGrant, error)
	Close() error
}

const hostGrantSchema = `
CREATE TABLE IF NOT EXISTS plugin_host_grants (
    plugin_name TEXT NOT NULL,
    host        TEXT NOT NULL,
    granted_by  TEXT NOT NULL,
    granted_at  TEXT NOT NULL,
    source      TEXT NOT NULL CHECK(source IN ('install','operator')),
    PRIMARY KEY (plugin_name, host)
)`

// sqliteHostGrantStore is the SQLite-backed HostGrantStore.
type sqliteHostGrantStore struct {
	db *sql.DB
	mu sync.RWMutex // serializes writes; reads use the DB connection pool directly
}

// NewSQLiteHostGrantStore creates the plugin_host_grants table (if absent) and
// returns a HostGrantStore backed by the provided *sql.DB. Mirrors the style
// of NewSQLitePluginRegistryStore: callers own the *sql.DB lifecycle.
func NewSQLiteHostGrantStore(db *sql.DB) (HostGrantStore, error) {
	if db == nil {
		return nil, errors.New("db must not be nil")
	}
	if _, err := db.Exec(hostGrantSchema); err != nil {
		return nil, fmt.Errorf("create plugin_host_grants: %w", err)
	}
	return &sqliteHostGrantStore{db: db}, nil
}

// Grant inserts or replaces a host grant. The host is validated before write.
func (s *sqliteHostGrantStore) Grant(ctx context.Context, g HostGrant) error {
	if strings.TrimSpace(g.PluginName) == "" {
		return errors.New("plugin_name must not be empty")
	}
	if err := ValidateHost(g.Host); err != nil {
		return err
	}
	if g.Source != "install" && g.Source != "operator" {
		return fmt.Errorf("invalid source %q", g.Source)
	}
	if g.GrantedAt.IsZero() {
		g.GrantedAt = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	const q = `
		INSERT OR REPLACE INTO plugin_host_grants
		    (plugin_name, host, granted_by, granted_at, source)
		VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, q,
		g.PluginName,
		g.Host,
		g.GrantedBy,
		g.GrantedAt.UTC().Format(time.RFC3339Nano),
		g.Source,
	)
	return err
}

// Revoke removes a single (plugin, host) grant. Returns ErrHostGrantNotFound
// when the row does not exist.
func (s *sqliteHostGrantStore) Revoke(ctx context.Context, pluginName, host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	const q = `DELETE FROM plugin_host_grants WHERE plugin_name = ? AND host = ?`
	res, err := s.db.ExecContext(ctx, q, pluginName, host)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrHostGrantNotFound
	}
	return nil
}

// List returns all grants for a given plugin, ordered by host.
func (s *sqliteHostGrantStore) List(ctx context.Context, pluginName string) ([]HostGrant, error) {
	const q = `
		SELECT plugin_name, host, granted_by, granted_at, source
		FROM plugin_host_grants
		WHERE plugin_name = ?
		ORDER BY host`
	rows, err := s.db.QueryContext(ctx, q, pluginName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HostGrant
	for rows.Next() {
		g, err := scanHostGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListAll returns every grant keyed by plugin name.
func (s *sqliteHostGrantStore) ListAll(ctx context.Context) (map[string][]HostGrant, error) {
	const q = `
		SELECT plugin_name, host, granted_by, granted_at, source
		FROM plugin_host_grants
		ORDER BY plugin_name, host`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]HostGrant)
	for rows.Next() {
		g, err := scanHostGrant(rows)
		if err != nil {
			return nil, err
		}
		out[g.PluginName] = append(out[g.PluginName], g)
	}
	return out, rows.Err()
}

// Close is a no-op; the *sql.DB is owned by the caller.
func (s *sqliteHostGrantStore) Close() error { return nil }

func scanHostGrant(rows *sql.Rows) (HostGrant, error) {
	var g HostGrant
	var grantedAtStr string
	if err := rows.Scan(&g.PluginName, &g.Host, &g.GrantedBy, &grantedAtStr, &g.Source); err != nil {
		return HostGrant{}, err
	}
	t, err := time.Parse(time.RFC3339Nano, grantedAtStr)
	if err != nil {
		return HostGrant{}, err
	}
	g.GrantedAt = t
	return g, nil
}

// ValidateHost checks that host is a well-formed host[:port] reference:
// no scheme, no path, no wildcards, port (when present) in 1..65535, and the
// hostname labels are RFC 1123-ish.
func ValidateHost(host string) error {
	if host == "" {
		return fmt.Errorf("%w: empty", ErrInvalidHost)
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("%w: whitespace", ErrInvalidHost)
	}
	if strings.Contains(host, "://") {
		return fmt.Errorf("%w: scheme not allowed", ErrInvalidHost)
	}
	if strings.ContainsAny(host, "/?#") {
		return fmt.Errorf("%w: path or query not allowed", ErrInvalidHost)
	}
	if strings.Contains(host, "*") {
		return fmt.Errorf("%w: wildcard not allowed", ErrInvalidHost)
	}

	h := host
	if strings.Contains(host, ":") {
		var portStr string
		var err error
		h, portStr, err = net.SplitHostPort(host)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidHost, err)
		}
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("%w: bad port %q", ErrInvalidHost, portStr)
		}
	}
	if h == "" {
		return fmt.Errorf("%w: empty host", ErrInvalidHost)
	}
	if len(h) > 253 {
		return fmt.Errorf("%w: hostname too long", ErrInvalidHost)
	}
	// Allow IPv4 / IPv6 literals or RFC1123 hostnames.
	if ip := net.ParseIP(h); ip != nil {
		return nil
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("%w: bad label %q", ErrInvalidHost, label)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '-'
			if !ok {
				return fmt.Errorf("%w: bad character in label %q", ErrInvalidHost, label)
			}
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%w: label %q starts or ends with hyphen", ErrInvalidHost, label)
		}
	}
	return nil
}
