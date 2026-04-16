package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// SessionScope identifies whether a session belongs to a single plugin or the federated endpoint.
type SessionScope int

const (
	SessionScopePlugin    SessionScope = iota
	SessionScopeFederated SessionScope = iota
)

// SessionEntry holds the data for a single MCP session.
type SessionEntry struct {
	ID         SessionID
	PluginName string
	Scope      SessionScope
}

// SessionStore manages MCP session lifecycle.
type SessionStore interface {
	// Create registers a new session, assigns a UUID-like ID, and returns it.
	Create(entry SessionEntry) SessionID
	// Get retrieves a session by ID. Returns false if not found.
	Get(id SessionID) (SessionEntry, bool)
	// Delete removes the session with the given ID. No-op if not found.
	Delete(id SessionID)
}

// inMemorySessionStore is a thread-safe in-memory SessionStore.
type inMemorySessionStore struct {
	mu       sync.RWMutex
	sessions map[SessionID]SessionEntry
}

// NewSessionStore constructs a ready-to-use in-memory SessionStore.
func NewSessionStore() SessionStore {
	return &inMemorySessionStore{
		sessions: make(map[SessionID]SessionEntry),
	}
}

// Create generates a cryptographically random session ID, stores the entry, and returns the ID.
func (s *inMemorySessionStore) Create(entry SessionEntry) SessionID {
	id := generateSessionID()
	entry.ID = id
	s.mu.Lock()
	s.sessions[id] = entry
	s.mu.Unlock()
	return id
}

// Get retrieves a session by ID.
func (s *inMemorySessionStore) Get(id SessionID) (SessionEntry, bool) {
	s.mu.RLock()
	e, ok := s.sessions[id]
	s.mu.RUnlock()
	return e, ok
}

// Delete removes a session. Safe to call on a non-existent ID.
func (s *inMemorySessionStore) Delete(id SessionID) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// generateSessionID generates a 16-byte random session ID formatted as a hex string.
// Uses crypto/rand for cryptographic quality randomness.
func generateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is catastrophic; panic is acceptable here.
		panic("mcp: crypto/rand failure: " + err.Error())
	}
	return hex.EncodeToString(b)
}
