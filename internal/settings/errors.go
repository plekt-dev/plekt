package settings

import (
	"errors"
	"net/mail"
)

// Validation sentinel errors.
var (
	ErrAdminEmailInvalid        = errors.New("admin_email is not a valid email address")
	ErrSessionTTLNegative       = errors.New("session_ttl_minutes must be >= 0")
	ErrPasswordMinLengthInvalid = errors.New("password_min_length must be >= 1")
)

// Validate checks constraints on s. Returns the first error encountered, or nil.
// Empty AdminEmail is allowed (treated as "not configured").
func Validate(s Settings) error {
	if s.AdminEmail != "" {
		if _, err := mail.ParseAddress(s.AdminEmail); err != nil {
			return ErrAdminEmailInvalid
		}
	}
	if s.SessionTTLMinutes < 0 {
		return ErrSessionTTLNegative
	}
	if s.PasswordMinLength < 0 {
		return ErrPasswordMinLengthInvalid
	}
	return nil
}
