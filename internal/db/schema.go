package db

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

// safeDefaultRe matches safe SQL default values:
//   - numeric literals: 0, 1, 3.14, -5
//   - NULL
//   - CURRENT_TIMESTAMP, CURRENT_DATE, CURRENT_TIME
//   - simple quoted strings: ” or 'word' (no nested quotes)
var safeDefaultRe = regexp.MustCompile(`(?i)^(NULL|CURRENT_TIMESTAMP|CURRENT_DATE|CURRENT_TIME|-?[0-9]+(\.[0-9]+)?|'[^']*')$`)

// isValidDefault returns true when d is a safe SQL literal that can be used
// verbatim in a DEFAULT clause without risk of DDL injection.
func isValidDefault(d string) bool {
	return safeDefaultRe.MatchString(d)
}

// PluginSchema is the top-level structure of a plugin's schema.yaml.
type PluginSchema struct {
	Version string        `yaml:"version"`
	Tables  []TableSchema `yaml:"tables"`
}

// TableSchema describes a single database table.
type TableSchema struct {
	Name    string         `yaml:"name"`
	Columns []ColumnSchema `yaml:"columns"`
	Indexes []IndexSchema  `yaml:"indexes,omitempty"`
}

// ColumnSchema describes a single column within a table.
type ColumnSchema struct {
	Name          string           `yaml:"name"`
	Type          string           `yaml:"type"` // TEXT, INTEGER, REAL, BLOB, NUMERIC
	NotNull       bool             `yaml:"not_null,omitempty"`
	PrimaryKey    bool             `yaml:"primary_key,omitempty"`
	AutoIncrement bool             `yaml:"auto_increment,omitempty"`
	Default       string           `yaml:"default,omitempty"`
	References    *ColumnReference `yaml:"references,omitempty"`
}

// ColumnReference declares a foreign-key constraint on a column.
//
// Table and Column identify the parent row in the SAME plugin database
// (cross-plugin foreign keys are not supported: plugins own their own data).
// OnDelete and OnUpdate default to NO ACTION when empty and accept
// NO ACTION, RESTRICT, CASCADE, SET NULL, SET DEFAULT.
//
// IMPORTANT: SQLite enforces foreign keys only when the connection has
// `PRAGMA foreign_keys = ON`. The plugin loader's real sqliteDBFactory.Open
// sets this pragma on every per-plugin database (see
// internal/loader/factories.go) so the constraint declared here is actually
// enforced at runtime. Tests that open their own SQLite handles must set the
// pragma themselves if they rely on FK behavior.
type ColumnReference struct {
	Table    string `yaml:"table"`
	Column   string `yaml:"column"`
	OnDelete string `yaml:"on_delete,omitempty"`
	OnUpdate string `yaml:"on_update,omitempty"`
}

// IndexSchema describes a single index on a table.
type IndexSchema struct {
	Name    string   `yaml:"name"`
	Columns []string `yaml:"columns"`
	Unique  bool     `yaml:"unique,omitempty"`
}

var (
	identifierRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	validColTypes = map[string]bool{
		"TEXT":    true,
		"INTEGER": true,
		"REAL":    true,
		"BLOB":    true,
		"NUMERIC": true,
	}
	// validFKActions enumerates the SQL actions accepted in on_delete / on_update.
	// The empty string is allowed (treated as NO ACTION and omitted from DDL).
	validFKActions = map[string]bool{
		"":            true,
		"NO ACTION":   true,
		"RESTRICT":    true,
		"CASCADE":     true,
		"SET NULL":    true,
		"SET DEFAULT": true,
	}
)

// normalizeFKAction uppercases and trims an FK action string for comparison.
func normalizeFKAction(s string) string {
	out := make([]byte, 0, len(s))
	// trim leading/trailing ASCII space/tab
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	for i := start; i < end; i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}

// validateReference checks that a column's FK declaration has the required
// table/column fields and a recognised on_delete / on_update action.
func validateReference(tableName string, col ColumnSchema) error {
	ref := col.References
	if !identifierRe.MatchString(ref.Table) {
		return fmt.Errorf("%w: column %q in table %q references invalid parent table %q",
			ErrSchemaInvalid, col.Name, tableName, ref.Table)
	}
	if !identifierRe.MatchString(ref.Column) {
		return fmt.Errorf("%w: column %q in table %q references invalid parent column %q",
			ErrSchemaInvalid, col.Name, tableName, ref.Column)
	}
	if !validFKActions[normalizeFKAction(ref.OnDelete)] {
		return fmt.Errorf("%w: column %q in table %q has invalid on_delete action %q",
			ErrSchemaInvalid, col.Name, tableName, ref.OnDelete)
	}
	if !validFKActions[normalizeFKAction(ref.OnUpdate)] {
		return fmt.Errorf("%w: column %q in table %q has invalid on_update action %q",
			ErrSchemaInvalid, col.Name, tableName, ref.OnUpdate)
	}
	return nil
}

// ParsePluginSchema unmarshals YAML bytes into a PluginSchema and validates it.
// Returns ErrSchemaInvalid (wrapped) if the schema is structurally invalid.
func ParsePluginSchema(data []byte) (PluginSchema, error) {
	var s PluginSchema
	if err := yaml.Unmarshal(data, &s); err != nil {
		return PluginSchema{}, fmt.Errorf("%w: yaml parse: %v", ErrSchemaInvalid, err)
	}
	if err := ValidatePluginSchema(s); err != nil {
		return PluginSchema{}, err
	}
	return s, nil
}

// ValidatePluginSchema checks structural constraints on a PluginSchema.
// Returns ErrSchemaInvalid (wrapped) on the first detected violation.
func ValidatePluginSchema(s PluginSchema) error {
	if s.Version != "1" {
		return fmt.Errorf("%w: version must be \"1\", got %q", ErrSchemaInvalid, s.Version)
	}
	for _, tbl := range s.Tables {
		if err := validateTable(tbl); err != nil {
			return err
		}
	}
	return nil
}

func validateTable(tbl TableSchema) error {
	if !identifierRe.MatchString(tbl.Name) {
		return fmt.Errorf("%w: table name %q must match [a-z][a-z0-9_]*", ErrSchemaInvalid, tbl.Name)
	}

	// Build column name set and count primary keys.
	colNames := make(map[string]bool, len(tbl.Columns))
	pkCount := 0
	for _, col := range tbl.Columns {
		if !identifierRe.MatchString(col.Name) {
			return fmt.Errorf("%w: column name %q in table %q must match [a-z][a-z0-9_]*",
				ErrSchemaInvalid, col.Name, tbl.Name)
		}
		if !validColTypes[col.Type] {
			return fmt.Errorf("%w: column %q in table %q has invalid type %q; must be one of TEXT, INTEGER, REAL, BLOB, NUMERIC",
				ErrSchemaInvalid, col.Name, tbl.Name, col.Type)
		}
		if col.Default != "" && !isValidDefault(col.Default) {
			return fmt.Errorf("%w: column %q has unsafe default value %q",
				ErrSchemaInvalid, col.Name, col.Default)
		}
		if col.References != nil {
			if err := validateReference(tbl.Name, col); err != nil {
				return err
			}
		}
		if col.PrimaryKey {
			pkCount++
		}
		colNames[col.Name] = true
	}
	if pkCount != 1 {
		return fmt.Errorf("%w: table %q must have exactly one primary key column, found %d",
			ErrSchemaInvalid, tbl.Name, pkCount)
	}

	// Validate indexes: name must be a safe identifier; columns must exist.
	for _, idx := range tbl.Indexes {
		if !identifierRe.MatchString(idx.Name) {
			return fmt.Errorf("%w: index name %q is invalid (must match [a-z][a-z0-9_]*)",
				ErrSchemaInvalid, idx.Name)
		}
		for _, colName := range idx.Columns {
			if !colNames[colName] {
				return fmt.Errorf("%w: index %q on table %q references nonexistent column %q",
					ErrSchemaInvalid, idx.Name, tbl.Name, colName)
			}
		}
	}
	return nil
}
