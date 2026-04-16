package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// MigrationRunner applies a PluginSchema to a SQLite database using pure Go DDL.
// It is idempotent: tables and indexes that already exist are not recreated.
type MigrationRunner interface {
	Migrate(ctx context.Context, db *sql.DB, schema PluginSchema) error
}

type migrationRunner struct {
	pluginName string
}

// NewMigrationRunner returns a MigrationRunner for the named plugin.
func NewMigrationRunner(pluginName string) MigrationRunner {
	return &migrationRunner{pluginName: pluginName}
}

// Migrate applies the PluginSchema to db.
// It creates missing tables and adds missing columns to existing tables.
// Index creation is idempotent via CREATE INDEX IF NOT EXISTS.
// All DDL is executed inside a single transaction; errors cause a rollback.
func (r *migrationRunner) Migrate(ctx context.Context, db *sql.DB, schema PluginSchema) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: begin transaction: %v", ErrMigrationFailed, err)
	}
	// Always rollback on exit; if we Commit() first the rollback is a no-op.
	defer tx.Rollback() //nolint:errcheck

	// Read existing tables.
	existingTables, err := queryExistingTables(ctx, tx)
	if err != nil {
		return fmt.Errorf("%w: query existing tables: %v", ErrMigrationFailed, err)
	}

	for _, tbl := range schema.Tables {
		if _, exists := existingTables[tbl.Name]; !exists {
			// Table does not exist: create it.
			createSQL := buildCreateTableSQL(tbl)
			if _, err := tx.ExecContext(ctx, createSQL); err != nil {
				return fmt.Errorf("%w: create table %q: %v", ErrMigrationFailed, tbl.Name, err)
			}
		} else {
			// Table exists: add any missing columns.
			existingCols, err := queryExistingColumns(ctx, tx, tbl.Name)
			if err != nil {
				return fmt.Errorf("%w: query columns for table %q: %v", ErrMigrationFailed, tbl.Name, err)
			}
			for _, col := range tbl.Columns {
				if !existingCols[col.Name] {
					addSQL := buildAddColumnSQL(tbl.Name, col)
					if _, err := tx.ExecContext(ctx, addSQL); err != nil {
						return fmt.Errorf("%w: add column %q to table %q: %v",
							ErrMigrationFailed, col.Name, tbl.Name, err)
					}
				}
			}
		}

		// Drop stale indexes that are no longer in the schema.
		// This prevents legacy unique indexes from blocking new composite ones.
		desiredIndexes := make(map[string]struct{}, len(tbl.Indexes))
		for _, idx := range tbl.Indexes {
			desiredIndexes[idx.Name] = struct{}{}
		}
		existingIndexes, _ := queryExistingIndexes(ctx, tx, tbl.Name)
		for _, eidx := range existingIndexes {
			if _, wanted := desiredIndexes[eidx]; !wanted {
				dropSQL := "DROP INDEX IF EXISTS " + eidx
				if _, err := tx.ExecContext(ctx, dropSQL); err != nil {
					return fmt.Errorf("%w: drop stale index %q on table %q: %v",
						ErrMigrationFailed, eidx, tbl.Name, err)
				}
			}
		}

		// Create any missing indexes.
		for _, idx := range tbl.Indexes {
			createIdxSQL := buildCreateIndexSQL(tbl.Name, idx)
			if _, err := tx.ExecContext(ctx, createIdxSQL); err != nil {
				return fmt.Errorf("%w: create index %q on table %q: %v",
					ErrMigrationFailed, idx.Name, tbl.Name, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: commit: %v", ErrMigrationFailed, err)
	}
	return nil
}

// queryExistingTables returns the set of table names currently in the database.
// Uses parameterized query; table name filtering via LIKE is safe here.
func queryExistingTables(ctx context.Context, tx *sql.Tx) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables[name] = struct{}{}
	}
	return tables, rows.Err()
}

// queryExistingColumns returns the set of column names for the given table.
// Table name comes from ValidatePluginSchema and matches [a-z][a-z0-9_]*; safe to interpolate.
func queryExistingColumns(ctx context.Context, tx *sql.Tx, tableName string) (map[string]bool, error) {
	// PRAGMA table_info cannot use bind parameters for the table name.
	// tableName is validated to match [a-z][a-z0-9_]* before reaching this point.
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+tableName+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// queryExistingIndexes returns the names of user-created indexes on the given table.
// Excludes SQLite auto-indexes (sqlite_autoindex_*).
func queryExistingIndexes(ctx context.Context, tx *sql.Tx, tableName string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='index' AND tbl_name=? AND name NOT LIKE 'sqlite_%' AND sql IS NOT NULL",
		tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// buildCreateTableSQL generates a CREATE TABLE IF NOT EXISTS statement.
// tableName and column names are validated via [a-z][a-z0-9_]* before reaching this function.
// Foreign-key constraints declared via ColumnSchema.References are emitted as
// table-level FOREIGN KEY clauses at the end of the column list.
func buildCreateTableSQL(t TableSchema) string {
	var sb strings.Builder
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(t.Name)
	sb.WriteString(" (")
	for i, col := range t.Columns {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(col.Name)
		sb.WriteString(" ")
		sb.WriteString(col.Type)
		if col.PrimaryKey {
			sb.WriteString(" PRIMARY KEY")
			if col.AutoIncrement {
				sb.WriteString(" AUTOINCREMENT")
			}
		}
		if col.NotNull && !col.PrimaryKey {
			sb.WriteString(" NOT NULL")
		}
		if col.Default != "" {
			sb.WriteString(" DEFAULT ")
			sb.WriteString(col.Default)
		}
	}
	// Emit FK clauses after all columns. Table-level FOREIGN KEY syntax is used
	// (rather than inline REFERENCES) so actions and identifiers are clearly
	// separated from column type info and easier to read in the generated DDL.
	for _, col := range t.Columns {
		if col.References == nil {
			continue
		}
		sb.WriteString(", FOREIGN KEY (")
		sb.WriteString(col.Name)
		sb.WriteString(") REFERENCES ")
		sb.WriteString(col.References.Table)
		sb.WriteString("(")
		sb.WriteString(col.References.Column)
		sb.WriteString(")")
		if act := normalizeFKAction(col.References.OnDelete); act != "" && act != "NO ACTION" {
			sb.WriteString(" ON DELETE ")
			sb.WriteString(act)
		}
		if act := normalizeFKAction(col.References.OnUpdate); act != "" && act != "NO ACTION" {
			sb.WriteString(" ON UPDATE ")
			sb.WriteString(act)
		}
	}
	sb.WriteString(")")
	return sb.String()
}

// buildAddColumnSQL generates an ALTER TABLE ... ADD COLUMN statement.
// tableName and col.Name are validated via [a-z][a-z0-9_]* before reaching this function.
//
// LIMITATION: SQLite does not allow ADD COLUMN to introduce a new FOREIGN KEY
// constraint on an existing table (the constraint can only be declared at
// CREATE TABLE time, or the table must be rebuilt). For now, FK clauses on
// columns added via ALTER TABLE are silently dropped. This is acceptable
// because plugins are expected to declare their foreign keys on first
// installation; adding a new FK to an existing table requires a manual data
// migration anyway.
func buildAddColumnSQL(tableName string, col ColumnSchema) string {
	var sb strings.Builder
	sb.WriteString("ALTER TABLE ")
	sb.WriteString(tableName)
	sb.WriteString(" ADD COLUMN ")
	sb.WriteString(col.Name)
	sb.WriteString(" ")
	sb.WriteString(col.Type)
	if col.NotNull {
		sb.WriteString(" NOT NULL")
	}
	if col.Default != "" {
		sb.WriteString(" DEFAULT ")
		sb.WriteString(col.Default)
	}
	return sb.String()
}

// buildCreateIndexSQL generates a CREATE INDEX IF NOT EXISTS statement.
// tableName, idx.Name, and column names are validated via [a-z][a-z0-9_]* before reaching here.
func buildCreateIndexSQL(tableName string, idx IndexSchema) string {
	var sb strings.Builder
	if idx.Unique {
		sb.WriteString("CREATE UNIQUE INDEX IF NOT EXISTS ")
	} else {
		sb.WriteString("CREATE INDEX IF NOT EXISTS ")
	}
	sb.WriteString(idx.Name)
	sb.WriteString(" ON ")
	sb.WriteString(tableName)
	sb.WriteString(" (")
	sb.WriteString(strings.Join(idx.Columns, ", "))
	sb.WriteString(")")
	return sb.String()
}
