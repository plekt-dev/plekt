package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func setupTestTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
	if err != nil {
		t.Fatalf("create test table: %v", err)
	}
}

// Table-driven tests for DBManager.

func TestDBManager_Query_Success(t *testing.T) {
	db := openTestDB(t, "mgr_query_success")
	setupTestTable(t, db)
	_, err := db.Exec("INSERT INTO items (id, name) VALUES (?, ?)", 1, "apple")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	result, err := mgr.Query(context.Background(), "SELECT id, name FROM items WHERE id = ?", []any{1})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(result.Columns))
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0][1] != "apple" {
		t.Errorf("expected name=apple, got %v", result.Rows[0][1])
	}
}

func TestDBManager_Query_DDL_Rejected(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"CREATE TABLE", "CREATE TABLE foo (id INTEGER PRIMARY KEY)"},
		{"DROP TABLE", "DROP TABLE items"},
		{"ALTER TABLE", "ALTER TABLE items ADD COLUMN x TEXT"},
		{"TRUNCATE", "TRUNCATE TABLE items"},
		{"PRAGMA", "PRAGMA table_info(items)"},
		{"ATTACH", "ATTACH DATABASE ':memory:' AS other"},
		{"DETACH", "DETACH DATABASE other"},
		{"VACUUM", "VACUUM"},
		{"lowercase create", "create table foo (id integer)"},
		{"mixed case DROP", "Drop Table items"},
	}

	db := openTestDB(t, "mgr_ddl_reject")
	setupTestTable(t, db)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewDBManager(db, "testplugin")
			_, err := mgr.Query(context.Background(), tc.sql, nil)
			if !errors.Is(err, ErrDDLNotPermitted) {
				t.Errorf("expected ErrDDLNotPermitted for %q, got %v", tc.sql, err)
			}
		})
	}
}

func TestDBManager_Query_Unparameterized_Heuristic(t *testing.T) {
	db := openTestDB(t, "mgr_unparam")
	setupTestTable(t, db)
	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	// SQL with single quote and no args triggers heuristic.
	_, err := mgr.Query(context.Background(), "SELECT * FROM items WHERE name='alice'", nil)
	if !errors.Is(err, ErrUnparameterizedQuery) {
		t.Errorf("expected ErrUnparameterizedQuery, got %v", err)
	}
}

func TestDBManager_Query_ArgsWithSingleQuote_Allowed(t *testing.T) {
	// When args are provided, the single-quote heuristic does not apply.
	db := openTestDB(t, "mgr_args_quote")
	setupTestTable(t, db)
	_, err := db.Exec("INSERT INTO items (id, name) VALUES (?, ?)", 1, "alice's item")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	// Query uses a placeholder, args path: allowed even though name has a quote.
	result, err := mgr.Query(context.Background(), "SELECT name FROM items WHERE name = ?", []any{"alice's item"})
	if err != nil {
		t.Fatalf("Query with args containing single quote: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(result.Rows))
	}
}

func TestDBManager_Exec_Success(t *testing.T) {
	db := openTestDB(t, "mgr_exec_success")
	setupTestTable(t, db)
	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	result, err := mgr.Exec(context.Background(), "INSERT INTO items (id, name) VALUES (?, ?)", []any{42, "banana"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", result.RowsAffected)
	}
	if result.LastInsertID != 42 {
		t.Errorf("expected LastInsertID=42, got %d", result.LastInsertID)
	}
}

func TestDBManager_Exec_DDL_Rejected(t *testing.T) {
	db := openTestDB(t, "mgr_exec_ddl_reject")
	setupTestTable(t, db)
	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	_, err := mgr.Exec(context.Background(), "DROP TABLE items", nil)
	if !errors.Is(err, ErrDDLNotPermitted) {
		t.Errorf("expected ErrDDLNotPermitted for DROP TABLE, got %v", err)
	}
}

func TestDBManager_Exec_WithArgsContainingSingleQuote_Allowed(t *testing.T) {
	// Exec with args that contain a single quote must be allowed (parameterized path).
	db := openTestDB(t, "mgr_exec_quote_args")
	setupTestTable(t, db)
	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	result, err := mgr.Exec(context.Background(), "INSERT INTO items (id, name) VALUES (?, ?)", []any{1, "bob's item"})
	if err != nil {
		t.Fatalf("Exec with args containing single quote: %v", err)
	}
	if result.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", result.RowsAffected)
	}
}

func TestDBManager_Query_MultipleRows(t *testing.T) {
	db := openTestDB(t, "mgr_multi_rows")
	setupTestTable(t, db)
	for i := 1; i <= 3; i++ {
		if _, err := db.Exec("INSERT INTO items (id, name) VALUES (?, ?)", i, "item"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	result, err := mgr.Query(context.Background(), "SELECT id, name FROM items", nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(result.Rows))
	}
}

func TestDBManager_Close(t *testing.T) {
	db, err := sql.Open("sqlite", "file:mgr_close?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	mgr := NewDBManager(db, "testplugin")
	if err := mgr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestDBManager_Exec_Unparameterized_NoArgs_WithQuote(t *testing.T) {
	db := openTestDB(t, "mgr_exec_unparam")
	setupTestTable(t, db)
	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	// Zero args + SQL with single-quote = heuristic fires.
	_, err := mgr.Exec(context.Background(), "INSERT INTO items (id, name) VALUES (1, 'hardcoded')", nil)
	if !errors.Is(err, ErrUnparameterizedQuery) {
		t.Errorf("expected ErrUnparameterizedQuery, got %v", err)
	}
}

func TestDBManager_Query_EmptySQL(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
		{"tab and newline", "\t\n"},
	}

	db := openTestDB(t, "mgr_query_empty")
	setupTestTable(t, db)
	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mgr.Query(context.Background(), tc.sql, nil)
			if !errors.Is(err, ErrEmptyQuery) {
				t.Errorf("expected ErrEmptyQuery for %q, got %v", tc.sql, err)
			}
		})
	}
}

func TestDBManager_Exec_EmptySQL(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
		{"tab and newline", "\t\n"},
	}

	db := openTestDB(t, "mgr_exec_empty")
	setupTestTable(t, db)
	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mgr.Exec(context.Background(), tc.sql, nil)
			if !errors.Is(err, ErrEmptyQuery) {
				t.Errorf("expected ErrEmptyQuery for %q, got %v", tc.sql, err)
			}
		})
	}
}
