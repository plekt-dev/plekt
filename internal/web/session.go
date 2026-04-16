package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// WebSessionID is a 32-char lowercase hex session identifier.
type WebSessionID = string

// WebSessionEntry holds session state for an authenticated web user.
type WebSessionEntry struct {
	ID                 WebSessionID
	CreatedAt          time.Time
	ExpiresAt          time.Time
	CSRFToken          string
	RemoteAddr         string
	UserID             int64
	Role               string
	Username           string
	MustChangePassword bool
}

// Sentinel errors.
var (
	ErrWebSessionNotFound     = errors.New("web session not found or expired")
	ErrCSRFTokenInvalid       = errors.New("CSRF token invalid")
	ErrInvalidAdminCredential = errors.New("invalid admin credential")
)

// WebSessionStore persists and retrieves web session entries.
type WebSessionStore interface {
	Create(remoteAddr string, userID int64, role string, username string, mustChangePassword bool) (WebSessionEntry, error)
	Get(id WebSessionID) (WebSessionEntry, error)
	Delete(id WebSessionID)
	ListAll() []WebSessionEntry
	SetMustChangePassword(id WebSessionID, mustChange bool) error
	Close() error
}

const (
	sessionTTL    = 24 * time.Hour
	sweepInterval = 5 * time.Minute
)

// SessionTTLProvider returns the configured session TTL in minutes.
// Returns 0 to use the default (24 hours).
type SessionTTLProvider func() int

// inMemoryWebSessionStore is a WebSessionStore backed by an in-memory map.
type inMemoryWebSessionStore struct {
	mu          sync.RWMutex
	sessions    map[WebSessionID]WebSessionEntry
	stopCh      chan struct{}
	doneCh      chan struct{}
	ttlProvider SessionTTLProvider
}

// NewInMemoryWebSessionStore constructs and starts an inMemoryWebSessionStore.
// An optional ttlProvider can supply a configurable TTL; pass nil for the default 24h.
// The caller must call Close() to stop the background sweeper.
func NewInMemoryWebSessionStore(ttlProvider ...SessionTTLProvider) (WebSessionStore, error) {
	var tp SessionTTLProvider
	if len(ttlProvider) > 0 && ttlProvider[0] != nil {
		tp = ttlProvider[0]
	}
	s := &inMemoryWebSessionStore{
		sessions:    make(map[WebSessionID]WebSessionEntry),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		ttlProvider: tp,
	}
	go s.sweeper()
	return s, nil
}

// sessionDuration returns the effective session TTL, consulting the provider first.
func (s *inMemoryWebSessionStore) sessionDuration() time.Duration {
	if s.ttlProvider != nil {
		if minutes := s.ttlProvider(); minutes > 0 {
			return time.Duration(minutes) * time.Minute
		}
	}
	return sessionTTL
}

// Create generates a new session for remoteAddr and stores it.
func (s *inMemoryWebSessionStore) Create(remoteAddr string, userID int64, role string, username string, mustChangePassword bool) (WebSessionEntry, error) {
	id, err := generateHex16()
	if err != nil {
		return WebSessionEntry{}, err
	}
	csrf, err := generateHex16()
	if err != nil {
		return WebSessionEntry{}, err
	}
	now := time.Now().UTC()
	entry := WebSessionEntry{
		ID:                 id,
		CreatedAt:          now,
		ExpiresAt:          now.Add(s.sessionDuration()),
		CSRFToken:          csrf,
		RemoteAddr:         remoteAddr,
		UserID:             userID,
		Role:               role,
		Username:           username,
		MustChangePassword: mustChangePassword,
	}
	s.mu.Lock()
	s.sessions[id] = entry
	s.mu.Unlock()
	return entry, nil
}

// SetMustChangePassword updates the MustChangePassword flag of an existing session.
func (s *inMemoryWebSessionStore) SetMustChangePassword(id WebSessionID, mustChange bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.sessions[id]
	if !ok {
		return ErrWebSessionNotFound
	}
	entry.MustChangePassword = mustChange
	s.sessions[id] = entry
	return nil
}

// Get retrieves an existing, non-expired session by ID.
// Returns ErrWebSessionNotFound if the session does not exist or has expired.
func (s *inMemoryWebSessionStore) Get(id WebSessionID) (WebSessionEntry, error) {
	if id == "" {
		return WebSessionEntry{}, ErrWebSessionNotFound
	}
	s.mu.RLock()
	entry, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return WebSessionEntry{}, ErrWebSessionNotFound
	}
	if time.Now().UTC().After(entry.ExpiresAt) {
		s.Delete(id)
		return WebSessionEntry{}, ErrWebSessionNotFound
	}
	return entry, nil
}

// Delete removes a session by ID. No-op if the session does not exist.
func (s *inMemoryWebSessionStore) Delete(id WebSessionID) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// ListAll returns a copy of all non-expired sessions, sorted by CreatedAt ascending.
func (s *inMemoryWebSessionStore) ListAll() []WebSessionEntry {
	now := time.Now().UTC()
	s.mu.RLock()
	entries := make([]WebSessionEntry, 0, len(s.sessions))
	for _, e := range s.sessions {
		if e.ExpiresAt.After(now) {
			entries = append(entries, e)
		}
	}
	s.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})
	return entries
}

// Close stops the background sweeper goroutine and waits for it to exit.
func (s *inMemoryWebSessionStore) Close() error {
	close(s.stopCh)
	<-s.doneCh
	return nil
}

// sweeper runs in a goroutine and removes expired sessions every sweepInterval.
func (s *inMemoryWebSessionStore) sweeper() {
	defer close(s.doneCh)
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.sweep()
		}
	}
}

func (s *inMemoryWebSessionStore) sweep() {
	now := time.Now().UTC()
	s.mu.Lock()
	for id, entry := range s.sessions {
		if now.After(entry.ExpiresAt) {
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
}

// generateHex16 produces 16 cryptographically random bytes encoded as a
// 32-character lowercase hexadecimal string.
func generateHex16() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
