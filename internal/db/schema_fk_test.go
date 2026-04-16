package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestParsePluginSchema_ValidReference exercises the parser on a schema that
// declares a CASCADE foreign key in the YAML `references:` block.
func TestParsePluginSchema_ValidReference(t *testing.T) {
	yamlData := []byte(`
version: "1"
tables:
  - name: parents
    columns:
      - name: id
        type: INTEGER
        primary_key: true
        auto_increment: true
        not_null: true
  - name: children
    columns:
      - name: id
        type: INTEGER
        primary_key: true
        auto_increment: true
        not_null: true
      - name: parent_id
        type: INTEGER
        not_null: true
        references:
          table: parents
          column: id
          on_delete: CASCADE
          on_update: CASCADE
`)
	s, err := ParsePluginSchema(yamlData)
	if err != nil {
		t.Fatalf("ParsePluginSchema: %v", err)
	}
	if len(s.Tables) != 2 {
		t.Fatalf("want 2 tables, got %d", len(s.Tables))
	}
	parentIDCol := s.Tables[1].Columns[1]
	if parentIDCol.References == nil {
		t.Fatal("references not parsed")
	}
	if parentIDCol.References.Table != "parents" || parentIDCol.References.Column != "id" {
		t.Errorf("wrong parent: %+v", parentIDCol.References)
	}
	if parentIDCol.References.OnDelete != "CASCADE" || parentIDCol.References.OnUpdate != "CASCADE" {
		t.Errorf("wrong actions: %+v", parentIDCol.References)
	}
}

// TestParsePluginSchema_ReferenceActions covers case-insensitivity and the
// three actions beyond CASCADE, plus lowercase input.
func TestParsePluginSchema_ReferenceActions(t *testing.T) {
	cases := []struct {
		name     string
		action   string
		wantOK   bool
		wantEmit bool // true when the DDL should include an ON DELETE clause
	}{
		{"cascade upper", "CASCADE", true, true},
		{"cascade lower", "cascade", true, true},
		{"set null", "SET NULL", true, true},
		{"restrict", "RESTRICT", true, true},
		{"set default", "SET DEFAULT", true, true},
		{"no action explicit", "NO ACTION", true, false},
		{"no action lower", "no action", true, false},
		{"empty omitted", "", true, false},
		{"garbage", "FLAMINGO", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := PluginSchema{
				Version: "1",
				Tables: []TableSchema{
					{Name: "parents", Columns: []ColumnSchema{{Name: "id", Type: "INTEGER", PrimaryKey: true, NotNull: true}}},
					{Name: "children", Columns: []ColumnSchema{
						{Name: "id", Type: "INTEGER", PrimaryKey: true, NotNull: true},
						{Name: "parent_id", Type: "INTEGER", NotNull: true, References: &ColumnReference{
							Table: "parents", Column: "id", OnDelete: tc.action,
						}},
					}},
				},
			}
			err := ValidatePluginSchema(s)
			if tc.wantOK && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatalf("expected validation error for %q", tc.action)
			}
			if !tc.wantOK {
				return
			}
			ddl := buildCreateTableSQL(s.Tables[1])
			if !strings.Contains(ddl, "FOREIGN KEY (parent_id) REFERENCES parents(id)") {
				t.Errorf("FK clause missing: %s", ddl)
			}
			hasOnDelete := strings.Contains(ddl, "ON DELETE ")
			if tc.wantEmit && !hasOnDelete {
				t.Errorf("expected ON DELETE clause in DDL: %s", ddl)
			}
			if !tc.wantEmit && hasOnDelete {
				t.Errorf("unexpected ON DELETE clause in DDL: %s", ddl)
			}
		})
	}
}

func TestValidatePluginSchema_ReferenceBadTable(t *testing.T) {
	s := PluginSchema{
		Version: "1",
		Tables: []TableSchema{
			{Name: "t", Columns: []ColumnSchema{
				{Name: "id", Type: "INTEGER", PrimaryKey: true, NotNull: true},
				{Name: "fk", Type: "INTEGER", References: &ColumnReference{Table: "Bad-Name", Column: "id"}},
			}},
		},
	}
	err := ValidatePluginSchema(s)
	if err == nil || !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("expected ErrSchemaInvalid, got %v", err)
	}
}

// TestMigrate_ForeignKeyCascadeEnforced exercises the end-to-end path: the
// migration runner emits a FK with ON DELETE CASCADE, we open the DB with
// foreign_keys ON, insert a child row, delete the parent, and verify the
// child row was cascaded. If the DDL silently dropped the FK clause or if
// the pragma was off, the child row would remain.
func TestMigrate_ForeignKeyCascadeEnforced(t *testing.T) {
	ctx := context.Background()
	// withForeignKeysPragma is defined in internal/loader/factories.go: here
	// we replicate its effect inline so this test does not depend on loader.
	db, err := sql.Open("sqlite", "file:fk_cascade?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Confirm the pragma is actually on: otherwise the rest of the test is
	// meaningless and we want a loud failure.
	var fkOn int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fkOn); err != nil {
		t.Fatalf("read pragma: %v", err)
	}
	if fkOn != 1 {
		t.Fatalf("foreign_keys pragma is off (%d)", fkOn)
	}

	schema := PluginSchema{
		Version: "1",
		Tables: []TableSchema{
			{Name: "parents", Columns: []ColumnSchema{
				{Name: "id", Type: "INTEGER", PrimaryKey: true, AutoIncrement: true, NotNull: true},
				{Name: "name", Type: "TEXT", NotNull: true},
			}},
			{Name: "children", Columns: []ColumnSchema{
				{Name: "id", Type: "INTEGER", PrimaryKey: true, AutoIncrement: true, NotNull: true},
				{Name: "parent_id", Type: "INTEGER", NotNull: true, References: &ColumnReference{
					Table: "parents", Column: "id", OnDelete: "CASCADE",
				}},
			}},
		},
	}
	runner := NewMigrationRunner("fk-test")
	if err := runner.Migrate(ctx, db, schema); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Insert a parent and a child.
	res, err := db.ExecContext(ctx, "INSERT INTO parents (name) VALUES (?)", "alice")
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	pid, _ := res.LastInsertId()

	if _, err := db.ExecContext(ctx, "INSERT INTO children (parent_id) VALUES (?)", pid); err != nil {
		t.Fatalf("insert child: %v", err)
	}

	// Inserting a child with a nonexistent parent must fail thanks to the FK.
	if _, err := db.ExecContext(ctx, "INSERT INTO children (parent_id) VALUES (?)", 99999); err == nil {
		t.Fatal("expected FK violation on orphan insert, got nil")
	}

	// Delete the parent; cascade should remove the child row.
	if _, err := db.ExecContext(ctx, "DELETE FROM parents WHERE id = ?", pid); err != nil {
		t.Fatalf("delete parent: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM children WHERE parent_id = ?", pid).Scan(&n); err != nil {
		t.Fatalf("count children: %v", err)
	}
	if n != 0 {
		t.Errorf("cascade did not delete child rows: %d remain", n)
	}
}
