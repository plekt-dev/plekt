package users

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// UserStore defines persistence operations for users.
type UserStore interface {
	Create(ctx context.Context, username, passwordHash string, role Role, mustChangePassword bool) (User, error)
	GetByID(ctx context.Context, id int64) (User, error)
	GetByUsername(ctx context.Context, username string) (User, error)
	List(ctx context.Context) ([]User, error)
	Count(ctx context.Context) (int, error)
	UpdateRole(ctx context.Context, id int64, role Role) error
	UpdatePasswordHash(ctx context.Context, id int64, hash string, mustChange bool) error
	SetMustChangePassword(ctx context.Context, id int64, mustChange bool) error
	Delete(ctx context.Context, id int64) error
	CountByRole(ctx context.Context, role Role) (int, error)
	Close() error
}

// SQLiteUserStore is the SQLite-backed UserStore implementation.
type SQLiteUserStore struct {
	db *sql.DB
}

// NewSQLiteUserStore opens a SQLite DB at dsn, runs the schema migration, and returns a store.
func NewSQLiteUserStore(ctx context.Context, dsn string) (*SQLiteUserStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &SQLiteUserStore{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// migrate creates the users table and index if they do not exist.
func (s *SQLiteUserStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id                  INTEGER PRIMARY KEY AUTOINCREMENT,
			username            TEXT NOT NULL UNIQUE COLLATE NOCASE,
			password_hash       TEXT NOT NULL,
			role                TEXT NOT NULL DEFAULT 'viewer',
			must_change_password INTEGER NOT NULL DEFAULT 0,
			created_at          TEXT NOT NULL,
			updated_at          TEXT NOT NULL
		)`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_users_username ON users(username COLLATE NOCASE)`)
	return err
}

// Create inserts a new user and returns it with the auto-generated ID.
func (s *SQLiteUserStore) Create(ctx context.Context, username, passwordHash string, role Role, mustChangePassword bool) (User, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	mustChangeInt := 0
	if mustChangePassword {
		mustChangeInt = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, must_change_password, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		username, passwordHash, string(role), mustChangeInt, now, now,
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return User{}, ErrUserAlreadyExists
		}
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return s.GetByID(ctx, id)
}

// GetByID retrieves a user by ID. Returns ErrUserNotFound if absent.
func (s *SQLiteUserStore) GetByID(ctx context.Context, id int64) (User, error) {
	if id == 0 {
		return User{}, ErrUserNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, must_change_password, created_at, updated_at
		 FROM users WHERE id = ?`, id)
	return scanUser(row)
}

// GetByUsername retrieves a user by username (case-insensitive). Returns ErrUserNotFound if absent.
func (s *SQLiteUserStore) GetByUsername(ctx context.Context, username string) (User, error) {
	if username == "" {
		return User{}, ErrUserNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, must_change_password, created_at, updated_at
		 FROM users WHERE username = ? COLLATE NOCASE`, username)
	return scanUser(row)
}

// List returns all users.
func (s *SQLiteUserStore) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, password_hash, role, must_change_password, created_at, updated_at
		 FROM users ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}

// Count returns the total number of users.
func (s *SQLiteUserStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateRole sets the role for the given user ID.
func (s *SQLiteUserStore) UpdateRole(ctx context.Context, id int64, role Role) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET role = ?, updated_at = ? WHERE id = ?`,
		string(role), now, id)
	return requireOneRow(res, err)
}

// UpdatePasswordHash updates the password hash and mustChangePassword flag.
func (s *SQLiteUserStore) UpdatePasswordHash(ctx context.Context, id int64, hash string, mustChange bool) error {
	now := time.Now().UTC().Format(time.RFC3339)
	mustChangeInt := 0
	if mustChange {
		mustChangeInt = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ?`,
		hash, mustChangeInt, now, id)
	return requireOneRow(res, err)
}

// SetMustChangePassword sets the must_change_password flag for a user.
func (s *SQLiteUserStore) SetMustChangePassword(ctx context.Context, id int64, mustChange bool) error {
	now := time.Now().UTC().Format(time.RFC3339)
	mustChangeInt := 0
	if mustChange {
		mustChangeInt = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET must_change_password = ?, updated_at = ? WHERE id = ?`,
		mustChangeInt, now, id)
	return requireOneRow(res, err)
}

// Delete removes a user by ID.
func (s *SQLiteUserStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return requireOneRow(res, err)
}

// CountByRole returns the number of users with the given role.
func (s *SQLiteUserStore) CountByRole(ctx context.Context, role Role) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = ?`, string(role)).Scan(&n)
	return n, err
}

// Close closes the underlying database connection.
func (s *SQLiteUserStore) Close() error {
	return s.db.Close()
}

// scanUser scans a single *sql.Row into a User.
func scanUser(row *sql.Row) (User, error) {
	var u User
	var mustChangeInt int
	var createdAt, updatedAt string
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &mustChangeInt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.MustChangePassword = mustChangeInt != 0
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return u, nil
}

// scanUserRow scans a *sql.Rows row into a User.
func scanUserRow(rows *sql.Rows) (User, error) {
	var u User
	var mustChangeInt int
	var createdAt, updatedAt string
	err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &mustChangeInt, &createdAt, &updatedAt)
	if err != nil {
		return User{}, err
	}
	u.MustChangePassword = mustChangeInt != 0
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return u, nil
}

// isUniqueConstraintErr returns true when err is a SQLite UNIQUE constraint violation.
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "unique constraint")
}

// requireOneRow checks that exactly one row was affected by an UPDATE/DELETE.
// Returns ErrUserNotFound if zero rows were affected.
func requireOneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrUserNotFound
	}
	return nil
}
