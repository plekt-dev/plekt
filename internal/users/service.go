package users

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

// dummyHash is a pre-computed bcrypt hash used to ensure constant-time
// responses when authenticating non-existent users (timing-safe login).
// Uses the same cost as real password hashes so the timing is equivalent.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-safe-dummy"), bcryptCost)

// UserService defines high-level user management operations.
type UserService interface {
	Create(ctx context.Context, username, password string, role Role, mustChangePassword bool) (User, error)
	Authenticate(ctx context.Context, username, password string) (User, error)
	ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword string, minLength int) error
	AdminSetPassword(ctx context.Context, userID int64, newPassword string, mustChange bool, minLength int) error
	ListUsers(ctx context.Context) ([]User, error)
	GetUser(ctx context.Context, id int64) (User, error)
	ChangeRole(ctx context.Context, userID int64, newRole Role) error
	DeleteUser(ctx context.Context, userID int64) error
	CountUsers(ctx context.Context) (int, error)
	Close() error
}

// defaultUserService is the production UserService implementation.
type defaultUserService struct {
	store             UserStore
	minPasswordLength int
}

// NewUserService constructs a UserService backed by the given store.
func NewUserService(store UserStore, minPasswordLength int) UserService {
	return &defaultUserService{
		store:             store,
		minPasswordLength: minPasswordLength,
	}
}

// Create hashes the password and creates a new user.
func (s *defaultUserService) Create(ctx context.Context, username, password string, role Role, mustChangePassword bool) (User, error) {
	if len(password) < s.minPasswordLength {
		return User{}, ErrPasswordTooShort
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return User{}, err
	}
	return s.store.Create(ctx, username, string(hash), role, mustChangePassword)
}

// Authenticate verifies credentials and returns the user on success.
// Returns ErrInvalidCredentials for both "user not found" and "wrong password"
// to prevent username enumeration.
func (s *defaultUserService) Authenticate(ctx context.Context, username, password string) (User, error) {
	u, err := s.store.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Perform a dummy bcrypt comparison to ensure the response time
			// is the same whether the user exists or not, preventing
			// timing-based username enumeration.
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

// ChangePassword verifies the current password, then updates to the new one.
func (s *defaultUserService) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword string, minLength int) error {
	if minLength <= 0 {
		minLength = s.minPasswordLength
	}
	if len(newPassword) < minLength {
		return ErrPasswordTooShort
	}
	u, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(currentPassword)); err != nil {
		return ErrInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return err
	}
	return s.store.UpdatePasswordHash(ctx, userID, string(hash), false)
}

// AdminSetPassword sets a new password without checking the current one.
func (s *defaultUserService) AdminSetPassword(ctx context.Context, userID int64, newPassword string, mustChange bool, minLength int) error {
	if minLength <= 0 {
		minLength = s.minPasswordLength
	}
	if len(newPassword) < minLength {
		return ErrPasswordTooShort
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return err
	}
	return s.store.UpdatePasswordHash(ctx, userID, string(hash), mustChange)
}

// ListUsers returns all users.
func (s *defaultUserService) ListUsers(ctx context.Context) ([]User, error) {
	return s.store.List(ctx)
}

// GetUser retrieves a user by ID.
func (s *defaultUserService) GetUser(ctx context.Context, id int64) (User, error) {
	return s.store.GetByID(ctx, id)
}

// ChangeRole updates the role of a user.
func (s *defaultUserService) ChangeRole(ctx context.Context, userID int64, newRole Role) error {
	return s.store.UpdateRole(ctx, userID, newRole)
}

// DeleteUser deletes a user. Returns ErrCannotDeleteLastAdmin if the user is
// the last admin.
func (s *defaultUserService) DeleteUser(ctx context.Context, userID int64) error {
	u, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Role == RoleAdmin {
		count, err := s.store.CountByRole(ctx, RoleAdmin)
		if err != nil {
			return err
		}
		if count <= 1 {
			return ErrCannotDeleteLastAdmin
		}
	}
	return s.store.Delete(ctx, userID)
}

// CountUsers returns the total number of users.
func (s *defaultUserService) CountUsers(ctx context.Context) (int, error) {
	return s.store.Count(ctx)
}

// Close closes the underlying store.
func (s *defaultUserService) Close() error {
	return s.store.Close()
}
