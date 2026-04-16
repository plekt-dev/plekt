package web

import (
	"crypto/subtle"
	"net/http"
)

// CSRFProvider generates and validates CSRF tokens for web sessions.
type CSRFProvider interface {
	// TokenForSession returns the CSRF token for the given session entry.
	TokenForSession(entry WebSessionEntry) string
	// Validate checks that submitted equals the session's CSRF token using
	// constant-time comparison. Returns ErrCSRFTokenInvalid on mismatch.
	Validate(entry WebSessionEntry, submitted string) error
}

// defaultCSRFProvider is the production CSRFProvider implementation.
type defaultCSRFProvider struct{}

// NewCSRFProvider constructs the default CSRF provider.
func NewCSRFProvider() CSRFProvider {
	return &defaultCSRFProvider{}
}

// TokenForSession returns the CSRFToken field of entry.
func (p *defaultCSRFProvider) TokenForSession(entry WebSessionEntry) string {
	return entry.CSRFToken
}

// Validate uses crypto/subtle.ConstantTimeCompare to prevent timing attacks.
// Returns ErrCSRFTokenInvalid if the tokens do not match.
func (p *defaultCSRFProvider) Validate(entry WebSessionEntry, submitted string) error {
	expected := []byte(entry.CSRFToken)
	got := []byte(submitted)
	// ConstantTimeCompare returns 1 only when both slices are equal length
	// and have equal content. Any length mismatch also returns 0.
	if subtle.ConstantTimeCompare(expected, got) != 1 {
		return ErrCSRFTokenInvalid
	}
	return nil
}

// CSRFTokenFromRequest extracts the CSRF token for the current session.
// Returns empty string if the session cookie is missing or expired.
func CSRFTokenFromRequest(r *http.Request, sessions WebSessionStore, csrf CSRFProvider) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	entry, err := sessions.Get(cookie.Value)
	if err != nil {
		return ""
	}
	return csrf.TokenForSession(entry)
}
