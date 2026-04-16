package db

import (
	"context"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

// TestIsDDL_CommentBypass verifies that SQL comments cannot be used to bypass DDL detection.
func TestIsDDL_CommentBypass(t *testing.T) {
	cases := []struct {
		name  string
		sql   string
		isDDL bool
	}{
		{"plain SELECT", "SELECT * FROM foo", false},
		{"CREATE TABLE", "CREATE TABLE foo (id int)", true},
		{"block comment bypass ATTACH", "/* bypass */ ATTACH DATABASE ':memory:' AS x", true},
		{"line comment bypass ATTACH", "-- bypass\nATTACH DATABASE ':memory:' AS x", true},
		{"nested block comments DROP", "/* outer /* inner */ */ DROP TABLE foo", true},
		{"multiple block comments DROP", "  /* */ /* */ DROP TABLE foo", true},
		{"PRAGMA with leading comment", "/* x */ PRAGMA table_info(t)", true},
		{"REINDEX with comment", "-- go\nREINDEX", true},
		{"DETACH with block comment", "/* */ DETACH DATABASE other", true},
		{"VACUUM with comment", "/* cleanup */ VACUUM", true},
		{"string with comment-like content", "SELECT '/* not a comment */' FROM foo", false},
		{"empty after stripping", "/* just a comment */", false},
		{"whitespace and comments", "  /* a */ -- b\n  ", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isDDL(tc.sql)
			if got != tc.isDDL {
				t.Errorf("isDDL(%q) = %v, want %v", tc.sql, got, tc.isDDL)
			}
		})
	}
}

// TestStripSQLComments verifies comment removal.
func TestStripSQLComments(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no comments", "SELECT 1", "SELECT 1"},
		{"block comment", "SELECT /* hidden */ 1", "SELECT  1"},
		{"line comment", "SELECT 1 -- trailing", "SELECT 1 "},
		{"nested block", "/* outer /* inner */ end */ SELECT 1", " SELECT 1"},
		{"multi-line comment", "-- first\n-- second\nSELECT 1", "\n\nSELECT 1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripSQLComments(tc.in)
			if got != tc.want {
				t.Errorf("stripSQLComments(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestContainsSemicolonOutsideLiteral verifies semicolon detection.
func TestContainsSemicolonOutsideLiteral(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{"no semicolon", "SELECT 1", false},
		{"trailing semicolon", "SELECT 1;", true},
		{"multi-statement", "SELECT 1; DROP TABLE foo", true},
		{"semicolon in string", "SELECT 'a;b' FROM t", false},
		{"semicolon in escaped string", "SELECT 'it''s;here' FROM t", false},
		{"semicolon after string", "SELECT 'ok' FROM t; DROP TABLE t", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := containsSemicolonOutsideLiteral(tc.sql)
			if got != tc.want {
				t.Errorf("containsSemicolonOutsideLiteral(%q) = %v, want %v", tc.sql, got, tc.want)
			}
		})
	}
}

// TestDBManager_Query_CommentBypassBlocked verifies DDL bypass via comments is blocked at the Query level.
func TestDBManager_Query_CommentBypassBlocked(t *testing.T) {
	db := openTestDB(t, "mgr_comment_bypass_q")
	setupTestTable(t, db)
	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	cases := []struct {
		name string
		sql  string
	}{
		{"block comment ATTACH", "/* bypass */ ATTACH DATABASE ':memory:' AS x"},
		{"line comment DROP", "-- bypass\nDROP TABLE items"},
		{"nested comments CREATE", "/* a /* b */ */ CREATE TABLE evil (id int)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mgr.Query(context.Background(), tc.sql, nil)
			if !errors.Is(err, ErrDDLNotPermitted) {
				t.Errorf("expected ErrDDLNotPermitted for %q, got %v", tc.sql, err)
			}
		})
	}
}

// TestDBManager_Query_MultiStatementBlocked verifies semicolons are blocked.
func TestDBManager_Query_MultiStatementBlocked(t *testing.T) {
	db := openTestDB(t, "mgr_multi_stmt_q")
	setupTestTable(t, db)
	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	_, err := mgr.Query(context.Background(), "SELECT 1; DROP TABLE items", nil)
	if !errors.Is(err, ErrMultiStatementNotPermitted) {
		t.Errorf("expected ErrMultiStatementNotPermitted, got %v", err)
	}
}

// TestDBManager_Exec_MultiStatementBlocked verifies semicolons are blocked on Exec.
func TestDBManager_Exec_MultiStatementBlocked(t *testing.T) {
	db := openTestDB(t, "mgr_multi_stmt_e")
	setupTestTable(t, db)
	mgr := NewDBManager(db, "testplugin")
	defer mgr.Close()

	_, err := mgr.Exec(context.Background(), "INSERT INTO items (id, name) VALUES (1, ?); DROP TABLE items", []any{"x"})
	if !errors.Is(err, ErrMultiStatementNotPermitted) {
		t.Errorf("expected ErrMultiStatementNotPermitted, got %v", err)
	}
}
