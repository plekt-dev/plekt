package db

// SQLite DSN helpers for system and plugin databases.
//
// modernc.org/sqlite reads `_pragma=name(value)` query parameters at
// connection time. Applying them via DSN guarantees every connection the
// *sql.DB pool hands out is configured identically: the alternative
// (PRAGMA via Exec on the first connection) leaks defaults onto fresh
// pool connections under concurrency, which is exactly how SQLITE_BUSY
// reappears after a restart.
//
// All system DBs need WAL + busy_timeout. Plugin DBs additionally need
// foreign_keys ON, since plugin schemas declare REFERENCES clauses we
// expect to enforce.

// WithSystemPragmas appends WAL journal mode and a 5s busy_timeout to a
// SQLite DSN. Use for system databases that do not declare foreign keys
// (audit.db, settings.db, plugins.db, host_grants.db, tokens.db,
// users.db).
func WithSystemPragmas(dsn string) string {
	return appendPragmas(dsn,
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
	)
}

// WithPluginPragmas appends foreign_keys ON, WAL, and busy_timeout. Use
// for per-plugin databases that may declare REFERENCES clauses.
func WithPluginPragmas(dsn string) string {
	return appendPragmas(dsn,
		"_pragma=foreign_keys(1)",
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
	)
}

// appendPragmas joins pragmas onto a DSN, picking '?' or '&' depending
// on whether the DSN already contains a query string.
func appendPragmas(dsn string, pragmas ...string) string {
	sep := "?"
	for _, c := range dsn {
		if c == '?' {
			sep = "&"
			break
		}
	}
	out := dsn
	for _, p := range pragmas {
		out += sep + p
		sep = "&"
	}
	return out
}
