package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, no CGO
)

// AgentStore persists and retrieves agents and their permissions.
type AgentStore interface {
	CreateAgent(ctx context.Context, name, token string) (Agent, error)
	GetAgentByID(ctx context.Context, id int64) (Agent, error)
	GetAgentByName(ctx context.Context, name string) (Agent, error)
	GetAgentByToken(ctx context.Context, token string) (Agent, error)
	ListAgents(ctx context.Context) ([]Agent, error)
	UpdateAgentToken(ctx context.Context, id int64, newToken string) error
	UpdateWebhookConfig(ctx context.Context, id int64, url, mode string) error
	UpdateWebhookSecret(ctx context.Context, id int64, secret string) error
	DeleteAgent(ctx context.Context, id int64) error
	SetPermissions(ctx context.Context, agentID int64, perms []AgentPermission) error
	ListPermissions(ctx context.Context, agentID int64) ([]AgentPermission, error)
	Close() error
}

// SQLiteAgentStore is an AgentStore backed by a SQLite database.
type SQLiteAgentStore struct {
	db *sql.DB
}

// NewSQLiteAgentStore opens (or creates) the SQLite database at dbPath,
// enables WAL journal mode, configures the connection pool, enables foreign
// keys, and ensures the schema exists.
func NewSQLiteAgentStore(dbPath string) (*SQLiteAgentStore, error) {
	dsn := dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open agent store db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping agent store db: %w", err)
	}
	if err := initAgentSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create agent store schema: %w", err)
	}
	return &SQLiteAgentStore{db: db}, nil
}

// initAgentSchema creates required tables and enables foreign keys.
func initAgentSchema(db *sql.DB) error {
	// Enable foreign key enforcement for this connection.
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}

	const q = `
CREATE TABLE IF NOT EXISTS agents (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL UNIQUE,
    token          TEXT NOT NULL UNIQUE,
    webhook_url    TEXT NOT NULL DEFAULT '',
    webhook_secret TEXT NOT NULL DEFAULT '',
    webhook_mode   TEXT NOT NULL DEFAULT 'async',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_permissions (
    agent_id    INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    plugin_name TEXT NOT NULL,
    tool_name   TEXT NOT NULL,
    PRIMARY KEY (agent_id, plugin_name, tool_name)
);`
	if _, err := db.Exec(q); err != nil {
		return err
	}

	// Idempotent migration: add webhook columns to pre-existing agents tables.
	// PRAGMA table_info returns each column on a row; we add anything missing.
	if err := ensureAgentColumns(db); err != nil {
		return fmt.Errorf("migrate agents columns: %w", err)
	}
	return nil
}

// ensureAgentColumns adds the webhook_* columns to the agents table if a
// pre-existing database created the table without them. Each ALTER is wrapped
// in its own check so a partial migration converges on a fresh run.
func ensureAgentColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(agents)`)
	if err != nil {
		return fmt.Errorf("table_info agents: %w", err)
	}
	defer rows.Close()

	have := make(map[string]bool)
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan table_info: %w", err)
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("table_info rows: %w", err)
	}

	type col struct{ name, ddl string }
	wanted := []col{
		{"webhook_url", `ALTER TABLE agents ADD COLUMN webhook_url TEXT NOT NULL DEFAULT ''`},
		{"webhook_secret", `ALTER TABLE agents ADD COLUMN webhook_secret TEXT NOT NULL DEFAULT ''`},
		{"webhook_mode", `ALTER TABLE agents ADD COLUMN webhook_mode TEXT NOT NULL DEFAULT 'async'`},
	}
	for _, c := range wanted {
		if have[c.name] {
			continue
		}
		if _, err := db.Exec(c.ddl); err != nil {
			return fmt.Errorf("add column %s: %w", c.name, err)
		}
	}
	return nil
}

// Close releases the underlying database connection.
func (s *SQLiteAgentStore) Close() error {
	return s.db.Close()
}

// CreateAgent inserts a new agent record and returns the created Agent.
// Returns ErrAgentAlreadyExists if name or token is already taken.
func (s *SQLiteAgentStore) CreateAgent(ctx context.Context, name, token string) (Agent, error) {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

	const q = `INSERT INTO agents (name, token, created_at, updated_at) VALUES (?, ?, ?, ?)`
	res, err := s.db.ExecContext(ctx, q, name, token, nowStr, nowStr)
	if err != nil {
		if isUniqueConstraintError(err) {
			return Agent{}, ErrAgentAlreadyExists
		}
		return Agent{}, fmt.Errorf("create agent: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Agent{}, fmt.Errorf("get last insert id: %w", err)
	}

	return Agent{
		ID:          id,
		Name:        name,
		Token:       token,
		WebhookMode: WebhookModeAsync,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// GetAgentByID retrieves an agent by its primary key.
// Returns ErrAgentNotFound if no agent with that ID exists.
func (s *SQLiteAgentStore) GetAgentByID(ctx context.Context, id int64) (Agent, error) {
	const q = `SELECT id, name, token, webhook_url, webhook_secret, webhook_mode, created_at, updated_at FROM agents WHERE id = ?`
	row := s.db.QueryRowContext(ctx, q, id)
	return scanAgent(row)
}

// GetAgentByName retrieves an agent by its unique name.
// Returns ErrAgentNotFound if no agent with that name exists.
func (s *SQLiteAgentStore) GetAgentByName(ctx context.Context, name string) (Agent, error) {
	const q = `SELECT id, name, token, webhook_url, webhook_secret, webhook_mode, created_at, updated_at FROM agents WHERE name = ?`
	row := s.db.QueryRowContext(ctx, q, name)
	return scanAgent(row)
}

// GetAgentByToken retrieves an agent by its token.
// Returns ErrAgentNotFound if no agent with that token exists.
// Constant-time comparison must be done at the middleware layer, not here.
func (s *SQLiteAgentStore) GetAgentByToken(ctx context.Context, token string) (Agent, error) {
	const q = `SELECT id, name, token, webhook_url, webhook_secret, webhook_mode, created_at, updated_at FROM agents WHERE token = ?`
	row := s.db.QueryRowContext(ctx, q, token)
	return scanAgent(row)
}

// ListAgents returns all agents ordered alphabetically by name.
func (s *SQLiteAgentStore) ListAgents(ctx context.Context) ([]Agent, error) {
	const q = `SELECT id, name, token, webhook_url, webhook_secret, webhook_mode, created_at, updated_at FROM agents ORDER BY name COLLATE NOCASE ASC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var result []Agent
	for rows.Next() {
		a, err := scanAgentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent row: %w", err)
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list agents rows: %w", err)
	}
	if result == nil {
		result = []Agent{}
	}
	return result, nil
}

// UpdateAgentToken sets a new token for the agent and bumps updated_at.
// Returns ErrAgentNotFound if no agent with that ID exists.
func (s *SQLiteAgentStore) UpdateAgentToken(ctx context.Context, id int64, newToken string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const q = `UPDATE agents SET token = ?, updated_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, newToken, now, id)
	if err != nil {
		return fmt.Errorf("update agent token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentNotFound
	}
	return nil
}

// UpdateWebhookConfig updates the webhook URL and mode for an agent. The
// secret is updated separately via UpdateWebhookSecret to keep blank-form
// submits from accidentally erasing it.
// Returns ErrAgentNotFound if no agent with that ID exists.
func (s *SQLiteAgentStore) UpdateWebhookConfig(ctx context.Context, id int64, webhookURL, mode string) error {
	if mode == "" {
		mode = WebhookModeAsync
	}
	if mode != WebhookModeAsync && mode != WebhookModeSync {
		return fmt.Errorf("invalid webhook mode %q", mode)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const q = `UPDATE agents SET webhook_url = ?, webhook_mode = ?, updated_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, webhookURL, mode, now, id)
	if err != nil {
		return fmt.Errorf("update webhook config: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentNotFound
	}
	return nil
}

// UpdateWebhookSecret writes a new HMAC secret. Caller must validate that
// the new value is non-empty (a blank submit should not erase the secret).
// Returns ErrAgentNotFound if no agent with that ID exists.
func (s *SQLiteAgentStore) UpdateWebhookSecret(ctx context.Context, id int64, secret string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	const q = `UPDATE agents SET webhook_secret = ?, updated_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, secret, now, id)
	if err != nil {
		return fmt.Errorf("update webhook secret: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentNotFound
	}
	return nil
}

// DeleteAgent removes an agent by ID. Permissions are deleted via CASCADE.
// Returns ErrAgentNotFound if no agent with that ID exists.
func (s *SQLiteAgentStore) DeleteAgent(ctx context.Context, id int64) error {
	// Ensure foreign keys are on for this connection before delete.
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	const q = `DELETE FROM agents WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentNotFound
	}
	return nil
}

// SetPermissions atomically replaces all permissions for agentID.
// An empty slice clears all permissions for the agent.
func (s *SQLiteAgentStore) SetPermissions(ctx context.Context, agentID int64, perms []AgentPermission) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// Enable FK within the transaction connection.
	if _, err := tx.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		return fmt.Errorf("enable foreign keys in tx: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_permissions WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("delete old permissions: %w", err)
	}

	const ins = `INSERT INTO agent_permissions (agent_id, plugin_name, tool_name) VALUES (?, ?, ?)`
	for _, p := range perms {
		if _, err := tx.ExecContext(ctx, ins, agentID, p.PluginName, p.ToolName); err != nil {
			return fmt.Errorf("insert permission (%s/%s): %w", p.PluginName, p.ToolName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit permissions: %w", err)
	}
	tx = nil // prevent deferred rollback after successful commit
	return nil
}

// ListPermissions returns all permissions for the given agentID.
func (s *SQLiteAgentStore) ListPermissions(ctx context.Context, agentID int64) ([]AgentPermission, error) {
	const q = `SELECT agent_id, plugin_name, tool_name FROM agent_permissions WHERE agent_id = ?`
	rows, err := s.db.QueryContext(ctx, q, agentID)
	if err != nil {
		return nil, fmt.Errorf("list permissions: %w", err)
	}
	defer rows.Close()

	var result []AgentPermission
	for rows.Next() {
		var p AgentPermission
		if err := rows.Scan(&p.AgentID, &p.PluginName, &p.ToolName); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list permissions rows: %w", err)
	}
	if result == nil {
		result = []AgentPermission{}
	}
	return result, nil
}

// scanAgent scans a single agent row from a QueryRowContext result.
func scanAgent(row *sql.Row) (Agent, error) {
	var a Agent
	var createdStr, updatedStr string
	if err := row.Scan(&a.ID, &a.Name, &a.Token, &a.WebhookURL, &a.WebhookSecret, &a.WebhookMode, &createdStr, &updatedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Agent{}, ErrAgentNotFound
		}
		return Agent{}, fmt.Errorf("scan agent: %w", err)
	}
	var parseErr error
	a.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, createdStr)
	if parseErr != nil {
		return Agent{}, fmt.Errorf("parse created_at: %w", parseErr)
	}
	a.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updatedStr)
	if parseErr != nil {
		return Agent{}, fmt.Errorf("parse updated_at: %w", parseErr)
	}
	return a, nil
}

// scanAgentRow scans a single agent from an open sql.Rows cursor.
func scanAgentRow(rows *sql.Rows) (Agent, error) {
	var a Agent
	var createdStr, updatedStr string
	if err := rows.Scan(&a.ID, &a.Name, &a.Token, &a.WebhookURL, &a.WebhookSecret, &a.WebhookMode, &createdStr, &updatedStr); err != nil {
		return Agent{}, fmt.Errorf("scan agent row: %w", err)
	}
	var parseErr error
	a.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, createdStr)
	if parseErr != nil {
		return Agent{}, fmt.Errorf("parse created_at: %w", parseErr)
	}
	a.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updatedStr)
	if parseErr != nil {
		return Agent{}, fmt.Errorf("parse updated_at: %w", parseErr)
	}
	return a, nil
}

// isUniqueConstraintError reports whether err is a SQLite UNIQUE constraint violation.
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
