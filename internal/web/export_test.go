// export_test.go exposes internal symbols for white-box testing.
// This file is only compiled during tests.
package web

// SweepSessions triggers a manual sweep of expired sessions on the store.
// Panics if store is not an *inMemoryWebSessionStore.
func SweepSessions(store WebSessionStore) {
	s := store.(*inMemoryWebSessionStore)
	s.sweep()
}

// ParseUserIDForTest exposes parseUserID for white-box testing.
func ParseUserIDForTest(s string) int64 {
	return parseUserID(s)
}
