package db

import "errors"

var (
	ErrSchemaInvalid              = errors.New("plugin schema invalid")
	ErrMigrationFailed            = errors.New("migration failed")
	ErrUnparameterizedQuery       = errors.New("query must use parameterized placeholders")
	ErrDDLNotPermitted            = errors.New("DDL statements not permitted from plugins")
	ErrSchemaFileNotFound         = errors.New("schema.yaml not found")
	ErrEmptyQuery                 = errors.New("SQL query must not be empty")
	ErrMultiStatementNotPermitted = errors.New("multi-statement SQL not permitted from plugins")
)
