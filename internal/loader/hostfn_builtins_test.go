package loader

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/plekt-dev/plekt/internal/db"
)

// openHostFnTestDB opens a uniquely-named in-memory SQLite database.
// The connection is closed automatically when the test finishes.
func openHostFnTestDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("openHostFnTestDB %q: %v", name, err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

// ---------------------------------------------------------------------------
// DBQueryHostFn
// ---------------------------------------------------------------------------

func TestDBQueryHostFn(t *testing.T) {
	type testCase struct {
		name            string
		buildPCC        func(t *testing.T) PluginCallContext
		params          DBQueryParams
		wantErr         error
		wantErrContains string
		wantCols        []string
		wantRows        int
	}

	cases := []testCase{
		{
			name: "nil DB returns error",
			buildPCC: func(t *testing.T) PluginCallContext {
				return PluginCallContext{PluginName: "test"}
			},
			params:          DBQueryParams{SQL: "SELECT 1"},
			wantErrContains: "no database",
		},
		{
			name: "DDL rejected",
			buildPCC: func(t *testing.T) PluginCallContext {
				return PluginCallContext{
					PluginName: "test",
					DB:         openHostFnTestDB(t, "hostfn_query_ddl_reject"),
				}
			},
			params:  DBQueryParams{SQL: "CREATE TABLE x (id INTEGER)"},
			wantErr: db.ErrDDLNotPermitted,
		},
		{
			name: "unparameterized rejected",
			buildPCC: func(t *testing.T) PluginCallContext {
				return PluginCallContext{
					PluginName: "test",
					DB:         openHostFnTestDB(t, "hostfn_query_unparam"),
				}
			},
			params:  DBQueryParams{SQL: "SELECT * FROM t WHERE name='alice'"},
			wantErr: db.ErrUnparameterizedQuery,
		},
		{
			name: "empty SQL rejected",
			buildPCC: func(t *testing.T) PluginCallContext {
				return PluginCallContext{
					PluginName: "test",
					DB:         openHostFnTestDB(t, "hostfn_query_empty"),
				}
			},
			params:  DBQueryParams{SQL: ""},
			wantErr: db.ErrEmptyQuery,
		},
		{
			name: "success returns columns and rows",
			buildPCC: func(t *testing.T) PluginCallContext {
				sqlDB := openHostFnTestDB(t, "hostfn_query_success")
				if _, err := sqlDB.Exec("CREATE TABLE items (id INTEGER, name TEXT)"); err != nil {
					t.Fatalf("create table: %v", err)
				}
				if _, err := sqlDB.Exec("INSERT INTO items VALUES (1, 'foo')"); err != nil {
					t.Fatalf("insert: %v", err)
				}
				return PluginCallContext{PluginName: "test", DB: sqlDB}
			},
			params:   DBQueryParams{SQL: "SELECT id, name FROM items", Args: nil},
			wantCols: []string{"id", "name"},
			wantRows: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pcc := tc.buildPCC(t)
			result, err := DBQueryHostFn(context.Background(), pcc, tc.params)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("DBQueryHostFn error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if tc.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrContains)
				}
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tc.wantCols) > 0 {
				if len(result.Columns) != len(tc.wantCols) {
					t.Errorf("columns = %v, want %v", result.Columns, tc.wantCols)
				} else {
					for i, col := range tc.wantCols {
						if result.Columns[i] != col {
							t.Errorf("column[%d] = %q, want %q", i, result.Columns[i], col)
						}
					}
				}
			}
			if tc.wantRows > 0 && len(result.Rows) != tc.wantRows {
				t.Errorf("rows = %d, want %d", len(result.Rows), tc.wantRows)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DBExecHostFn
// ---------------------------------------------------------------------------

func TestDBExecHostFn(t *testing.T) {
	type testCase struct {
		name             string
		buildPCC         func(t *testing.T) PluginCallContext
		params           DBExecParams
		wantErr          error
		wantErrContains  string
		wantRowsAffected int64
	}

	cases := []testCase{
		{
			name: "nil DB returns error",
			buildPCC: func(t *testing.T) PluginCallContext {
				return PluginCallContext{PluginName: "test"}
			},
			params:          DBExecParams{SQL: "INSERT INTO t VALUES (1)"},
			wantErrContains: "no database",
		},
		{
			name: "DDL rejected",
			buildPCC: func(t *testing.T) PluginCallContext {
				return PluginCallContext{
					PluginName: "test",
					DB:         openHostFnTestDB(t, "hostfn_exec_ddl_reject"),
				}
			},
			params:  DBExecParams{SQL: "DROP TABLE items"},
			wantErr: db.ErrDDLNotPermitted,
		},
		{
			name: "unparameterized rejected",
			buildPCC: func(t *testing.T) PluginCallContext {
				return PluginCallContext{
					PluginName: "test",
					DB:         openHostFnTestDB(t, "hostfn_exec_unparam"),
				}
			},
			params:  DBExecParams{SQL: "INSERT INTO t VALUES ('hardcoded')"},
			wantErr: db.ErrUnparameterizedQuery,
		},
		{
			name: "empty SQL rejected",
			buildPCC: func(t *testing.T) PluginCallContext {
				return PluginCallContext{
					PluginName: "test",
					DB:         openHostFnTestDB(t, "hostfn_exec_empty"),
				}
			},
			params:  DBExecParams{SQL: ""},
			wantErr: db.ErrEmptyQuery,
		},
		{
			name: "success insert with args",
			buildPCC: func(t *testing.T) PluginCallContext {
				sqlDB := openHostFnTestDB(t, "hostfn_exec_success")
				if _, err := sqlDB.Exec("CREATE TABLE things (id INTEGER PRIMARY KEY, label TEXT)"); err != nil {
					t.Fatalf("create table: %v", err)
				}
				return PluginCallContext{PluginName: "test", DB: sqlDB}
			},
			params:           DBExecParams{SQL: "INSERT INTO things (id, label) VALUES (?, ?)", Args: []any{99, "bar"}},
			wantRowsAffected: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pcc := tc.buildPCC(t)
			result, err := DBExecHostFn(context.Background(), pcc, tc.params)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("DBExecHostFn error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if tc.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrContains)
				}
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantRowsAffected != 0 && result.RowsAffected != tc.wantRowsAffected {
				t.Errorf("RowsAffected = %d, want %d", result.RowsAffected, tc.wantRowsAffected)
			}
		})
	}
}
