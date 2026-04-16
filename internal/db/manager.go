package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// DBQueryResult is the result of a SELECT query executed via DBManager.
type DBQueryResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// DBExecResult is the result of a non-SELECT statement executed via DBManager.
type DBExecResult struct {
	RowsAffected int64 `json:"rows_affected"`
	LastInsertID int64 `json:"last_insert_id"`
}

// DBManager provides a safe, restricted interface to a plugin's SQLite database.
// DDL statements are blocked. Unparameterized queries (heuristic) are blocked.
type DBManager interface {
	// Query executes a SELECT statement and returns all rows.
	Query(ctx context.Context, sql string, args []any) (DBQueryResult, error)
	// Exec executes a non-SELECT statement and returns affected rows / last insert ID.
	Exec(ctx context.Context, sql string, args []any) (DBExecResult, error)
	// Close closes the underlying database connection.
	Close() error
}

type dbManagerImpl struct {
	db         *sql.DB
	pluginName string
}

// NewDBManager returns a DBManager wrapping the given *sql.DB.
func NewDBManager(db *sql.DB, pluginName string) DBManager {
	return &dbManagerImpl{db: db, pluginName: pluginName}
}

// ddlPrefixes lists the first tokens of DDL statements that plugins must not issue.
var ddlPrefixes = []string{
	"create", "drop", "alter", "truncate", "pragma", "attach", "detach", "vacuum", "reindex",
}

// stripSQLComments removes SQL block comments (/* ... */, including nested) and
// line comments (-- to end of line) from s. It does NOT attempt to preserve
// comments inside string literals: this is intentionally conservative because
// the function is used as a security filter: a false positive (blocking a
// legitimate query that contains a comment-like sequence in a string literal)
// is preferable to a false negative (allowing a DDL bypass).
func stripSQLComments(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	i := 0
	depth := 0 // nesting depth for block comments
	for i < len(s) {
		// Block comment open
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			depth++
			i += 2
			continue
		}
		// Block comment close
		if depth > 0 && i+1 < len(s) && s[i] == '*' && s[i+1] == '/' {
			depth--
			i += 2
			continue
		}
		// Inside block comment: skip character
		if depth > 0 {
			i++
			continue
		}
		// Line comment
		if i+1 < len(s) && s[i] == '-' && s[i+1] == '-' {
			// Skip to end of line
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		buf.WriteByte(s[i])
		i++
	}
	return buf.String()
}

// containsSemicolonOutsideLiteral returns true if sqlStr contains a semicolon
// that is not inside a single-quoted string literal. Plugins must only submit
// single statements; semicolons enable multi-statement injection attacks.
func containsSemicolonOutsideLiteral(sqlStr string) bool {
	inString := false
	for i := 0; i < len(sqlStr); i++ {
		ch := sqlStr[i]
		if ch == '\'' {
			if inString && i+1 < len(sqlStr) && sqlStr[i+1] == '\'' {
				i++ // escaped quote ('')
				continue
			}
			inString = !inString
			continue
		}
		if ch == ';' && !inString {
			return true
		}
	}
	return false
}

// isDDL returns true if the (trimmed, lowercased) first token of sqlStr is a DDL keyword.
// SQL comments are stripped before checking to prevent bypass via comment injection.
// Returns false for empty or whitespace-only input (callers must reject those separately).
func isDDL(sqlStr string) bool {
	stripped := stripSQLComments(sqlStr)
	trimmed := strings.TrimSpace(stripped)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return false
	}
	first := strings.ToLower(fields[0])
	for _, prefix := range ddlPrefixes {
		if first == prefix {
			return true
		}
	}
	return false
}

// hasUnparameterizedLiteral returns true when there are no args and the SQL
// contains a single-quoted string literal: a heuristic for unparameterized queries.
func hasUnparameterizedLiteral(sqlStr string, args []any) bool {
	return len(args) == 0 && strings.Contains(sqlStr, "'")
}

func (m *dbManagerImpl) Query(ctx context.Context, sqlStr string, args []any) (DBQueryResult, error) {
	if strings.TrimSpace(sqlStr) == "" {
		return DBQueryResult{}, ErrEmptyQuery
	}
	if isDDL(sqlStr) {
		stripped := stripSQLComments(sqlStr)
		first := strings.Fields(strings.TrimSpace(stripped))[0]
		return DBQueryResult{}, fmt.Errorf("%w: %q", ErrDDLNotPermitted, first)
	}
	if containsSemicolonOutsideLiteral(sqlStr) {
		return DBQueryResult{}, ErrMultiStatementNotPermitted
	}
	if hasUnparameterizedLiteral(sqlStr, args) {
		return DBQueryResult{}, ErrUnparameterizedQuery
	}

	rows, err := m.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return DBQueryResult{}, fmt.Errorf("db query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return DBQueryResult{}, fmt.Errorf("db columns: %w", err)
	}

	var result DBQueryResult
	result.Columns = cols

	for rows.Next() {
		rowVals := make([]any, len(cols))
		rowPtrs := make([]any, len(cols))
		for i := range rowVals {
			rowPtrs[i] = &rowVals[i]
		}
		if err := rows.Scan(rowPtrs...); err != nil {
			return DBQueryResult{}, fmt.Errorf("db scan: %w", err)
		}
		result.Rows = append(result.Rows, rowVals)
	}
	if err := rows.Err(); err != nil {
		return DBQueryResult{}, fmt.Errorf("db rows: %w", err)
	}
	return result, nil
}

func (m *dbManagerImpl) Exec(ctx context.Context, sqlStr string, args []any) (DBExecResult, error) {
	if strings.TrimSpace(sqlStr) == "" {
		return DBExecResult{}, ErrEmptyQuery
	}
	if isDDL(sqlStr) {
		stripped := stripSQLComments(sqlStr)
		first := strings.Fields(strings.TrimSpace(stripped))[0]
		return DBExecResult{}, fmt.Errorf("%w: %q", ErrDDLNotPermitted, first)
	}
	if containsSemicolonOutsideLiteral(sqlStr) {
		return DBExecResult{}, ErrMultiStatementNotPermitted
	}
	if hasUnparameterizedLiteral(sqlStr, args) {
		return DBExecResult{}, ErrUnparameterizedQuery
	}

	res, err := m.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return DBExecResult{}, fmt.Errorf("db exec: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	lastInsertID, _ := res.LastInsertId()
	return DBExecResult{
		RowsAffected: rowsAffected,
		LastInsertID: lastInsertID,
	}, nil
}

func (m *dbManagerImpl) Close() error {
	return m.db.Close()
}
