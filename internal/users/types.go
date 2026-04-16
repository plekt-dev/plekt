package users

import (
	"errors"
	"time"
)

// Role represents a user's role in the system.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// User represents a user in the system.
type User struct {
	ID                 int64
	Username           string
	PasswordHash       string
	Role               Role
	MustChangePassword bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Sentinel errors for user operations.
var (
	ErrUserNotFound          = errors.New("user not found")
	ErrUserAlreadyExists     = errors.New("username already exists")
	ErrInvalidCredentials    = errors.New("invalid username or password")
	ErrPasswordTooShort      = errors.New("password does not meet minimum length requirement")
	ErrUsersUnavailable      = errors.New("user store unavailable")
	ErrCannotDeleteLastAdmin = errors.New("cannot delete the last admin user")
)
