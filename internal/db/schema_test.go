package db

import (
	"errors"
	"testing"
)

func TestParsePluginSchema_Valid(t *testing.T) {
	yaml := []byte(`
version: "1"
tables:
  - name: tasks
    columns:
      - name: id
        type: INTEGER
        primary_key: true
        auto_increment: true
      - name: title
        type: TEXT
        not_null: true
    indexes:
      - name: idx_tasks_title
        columns: [title]
`)
	schema, err := ParsePluginSchema(yaml)
	if err != nil {
		t.Fatalf("ParsePluginSchema: %v", err)
	}
	if schema.Version != "1" {
		t.Errorf("expected version 1, got %q", schema.Version)
	}
	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}
	if schema.Tables[0].Name != "tasks" {
		t.Errorf("expected table name tasks, got %q", schema.Tables[0].Name)
	}
	if len(schema.Tables[0].Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(schema.Tables[0].Columns))
	}
}

func TestParsePluginSchema_MissingVersion(t *testing.T) {
	yaml := []byte(`
tables:
  - name: tasks
    columns:
      - name: id
        type: INTEGER
        primary_key: true
`)
	_, err := ParsePluginSchema(yaml)
	if err == nil {
		t.Fatal("expected error for missing version, got nil")
	}
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestParsePluginSchema_UnknownColumnType(t *testing.T) {
	yaml := []byte(`
version: "1"
tables:
  - name: tasks
    columns:
      - name: id
        type: BADTYPE
        primary_key: true
`)
	_, err := ParsePluginSchema(yaml)
	if err == nil {
		t.Fatal("expected error for unknown column type, got nil")
	}
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestParsePluginSchema_DuplicatePrimaryKey(t *testing.T) {
	yaml := []byte(`
version: "1"
tables:
  - name: tasks
    columns:
      - name: id
        type: INTEGER
        primary_key: true
      - name: other_id
        type: INTEGER
        primary_key: true
`)
	_, err := ParsePluginSchema(yaml)
	if err == nil {
		t.Fatal("expected error for duplicate primary key, got nil")
	}
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestParsePluginSchema_IndexReferencesNonexistentColumn(t *testing.T) {
	yaml := []byte(`
version: "1"
tables:
  - name: tasks
    columns:
      - name: id
        type: INTEGER
        primary_key: true
    indexes:
      - name: idx_missing
        columns: [nonexistent_col]
`)
	_, err := ParsePluginSchema(yaml)
	if err == nil {
		t.Fatal("expected error for index referencing nonexistent column, got nil")
	}
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestValidatePluginSchema_InvalidVersion(t *testing.T) {
	s := PluginSchema{
		Version: "2",
		Tables: []TableSchema{
			{
				Name: "t",
				Columns: []ColumnSchema{
					{Name: "id", Type: "INTEGER", PrimaryKey: true},
				},
			},
		},
	}
	err := ValidatePluginSchema(s)
	if err == nil {
		t.Fatal("expected error for invalid version, got nil")
	}
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestValidatePluginSchema_InvalidTableName(t *testing.T) {
	s := PluginSchema{
		Version: "1",
		Tables: []TableSchema{
			{
				Name: "BadTableName",
				Columns: []ColumnSchema{
					{Name: "id", Type: "INTEGER", PrimaryKey: true},
				},
			},
		},
	}
	err := ValidatePluginSchema(s)
	if err == nil {
		t.Fatal("expected error for invalid table name, got nil")
	}
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestValidatePluginSchema_TableNameStartsWithDigit(t *testing.T) {
	s := PluginSchema{
		Version: "1",
		Tables: []TableSchema{
			{
				Name: "1tasks",
				Columns: []ColumnSchema{
					{Name: "id", Type: "INTEGER", PrimaryKey: true},
				},
			},
		},
	}
	err := ValidatePluginSchema(s)
	if err == nil {
		t.Fatal("expected error for table name starting with digit, got nil")
	}
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestValidatePluginSchema_InvalidColumnName(t *testing.T) {
	s := PluginSchema{
		Version: "1",
		Tables: []TableSchema{
			{
				Name: "tasks",
				Columns: []ColumnSchema{
					{Name: "BadCol", Type: "INTEGER", PrimaryKey: true},
				},
			},
		},
	}
	err := ValidatePluginSchema(s)
	if err == nil {
		t.Fatal("expected error for invalid column name, got nil")
	}
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestValidatePluginSchema_NoPrimaryKey(t *testing.T) {
	s := PluginSchema{
		Version: "1",
		Tables: []TableSchema{
			{
				Name: "tasks",
				Columns: []ColumnSchema{
					{Name: "id", Type: "INTEGER"},
					{Name: "title", Type: "TEXT"},
				},
			},
		},
	}
	err := ValidatePluginSchema(s)
	if err == nil {
		t.Fatal("expected error for table with no primary key, got nil")
	}
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestValidatePluginSchema_MultipleColumnTypes(t *testing.T) {
	cases := []struct {
		colType string
		wantErr bool
	}{
		{"TEXT", false},
		{"INTEGER", false},
		{"REAL", false},
		{"BLOB", false},
		{"NUMERIC", false},
		{"VARCHAR", true},
		{"INT", true},
		{"FLOAT", true},
		{"", true},
	}
	for _, tc := range cases {
		t.Run(tc.colType, func(t *testing.T) {
			s := PluginSchema{
				Version: "1",
				Tables: []TableSchema{
					{
						Name: "t",
						Columns: []ColumnSchema{
							{Name: "id", Type: tc.colType, PrimaryKey: true},
						},
					},
				},
			}
			err := ValidatePluginSchema(s)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for type %q, got nil", tc.colType)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error for type %q, got %v", tc.colType, err)
			}
		})
	}
}

func TestValidatePluginSchema_EmptyTableName(t *testing.T) {
	s := PluginSchema{
		Version: "1",
		Tables: []TableSchema{
			{
				Name: "",
				Columns: []ColumnSchema{
					{Name: "id", Type: "INTEGER", PrimaryKey: true},
				},
			},
		},
	}
	err := ValidatePluginSchema(s)
	if err == nil {
		t.Fatal("expected error for empty table name, got nil")
	}
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("expected ErrSchemaInvalid, got %v", err)
	}
}

func TestParsePluginSchema_InvalidYAML(t *testing.T) {
	_, err := ParsePluginSchema([]byte("\x01\x02\x03 bad yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestValidatePluginSchema_IndexNameValidation(t *testing.T) {
	cases := []struct {
		name      string
		indexName string
		wantErr   bool
	}{
		{"valid lowercase", "idx_tasks_title", false},
		{"valid simple", "myindex", false},
		{"uppercase letters", "IDX_Tasks", true},
		{"contains spaces", "idx tasks", true},
		{"contains semicolon", "idx;drop", true},
		{"starts with digit", "1idx", true},
		{"contains hyphen", "idx-name", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := PluginSchema{
				Version: "1",
				Tables: []TableSchema{
					{
						Name: "tasks",
						Columns: []ColumnSchema{
							{Name: "id", Type: "INTEGER", PrimaryKey: true},
							{Name: "title", Type: "TEXT"},
						},
						Indexes: []IndexSchema{
							{Name: tc.indexName, Columns: []string{"title"}},
						},
					},
				},
			}
			err := ValidatePluginSchema(s)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for index name %q, got nil", tc.indexName)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrSchemaInvalid) {
				t.Errorf("expected ErrSchemaInvalid for index name %q, got %v", tc.indexName, err)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error for index name %q, got %v", tc.indexName, err)
			}
		})
	}
}

func TestValidatePluginSchema_DefaultValueValidation(t *testing.T) {
	cases := []struct {
		name         string
		defaultValue string
		wantErr      bool
	}{
		{"zero integer", "0", false},
		{"positive integer", "42", false},
		{"negative integer", "-5", false},
		{"decimal", "3.14", false},
		{"NULL", "NULL", false},
		{"null lowercase", "null", false},
		{"CURRENT_TIMESTAMP", "CURRENT_TIMESTAMP", false},
		{"CURRENT_DATE", "CURRENT_DATE", false},
		{"CURRENT_TIME", "CURRENT_TIME", false},
		{"simple quoted string", "'hello'", false},
		{"empty quoted string", "''", false},
		{"sql injection drop", "0); DROP TABLE tasks; --", true},
		{"sql injection delete", "'; DELETE FROM users; --", true},
		{"unquoted string", "foobar", true},
		{"function call", "datetime('now')", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := PluginSchema{
				Version: "1",
				Tables: []TableSchema{
					{
						Name: "tasks",
						Columns: []ColumnSchema{
							{Name: "id", Type: "INTEGER", PrimaryKey: true},
							{Name: "status", Type: "TEXT", Default: tc.defaultValue},
						},
					},
				},
			}
			err := ValidatePluginSchema(s)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for default %q, got nil", tc.defaultValue)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrSchemaInvalid) {
				t.Errorf("expected ErrSchemaInvalid for default %q, got %v", tc.defaultValue, err)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error for default %q, got %v", tc.defaultValue, err)
			}
		})
	}
}
