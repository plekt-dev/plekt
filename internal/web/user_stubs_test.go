package web_test

import (
	"context"

	"github.com/plekt-dev/plekt/internal/users"
)

// stubUserService is a minimal stub for users.UserService in web tests.
// It pre-loads a single user with username "admin" and the given password hash.
type stubUserService struct {
	users       map[string]*users.User
	countReturn int
	authErr     error
	createErr   error
}

func newStubUserService() *stubUserService {
	return &stubUserService{
		users: make(map[string]*users.User),
	}
}

// addUser pre-populates the stub with a user (password already hashed externally).
func (s *stubUserService) addUser(u users.User) {
	s.users[u.Username] = &u
	s.countReturn++
}

func (s *stubUserService) Create(ctx context.Context, username, password string, role users.Role, mustChangePassword bool) (users.User, error) {
	if s.createErr != nil {
		return users.User{}, s.createErr
	}
	u := users.User{
		ID:                 int64(len(s.users) + 1),
		Username:           username,
		PasswordHash:       password,
		Role:               role,
		MustChangePassword: mustChangePassword,
	}
	s.users[username] = &u
	s.countReturn++
	return u, nil
}

func (s *stubUserService) Authenticate(ctx context.Context, username, password string) (users.User, error) {
	if s.authErr != nil {
		return users.User{}, s.authErr
	}
	u, ok := s.users[username]
	if !ok {
		return users.User{}, users.ErrInvalidCredentials
	}
	if u.PasswordHash != password {
		return users.User{}, users.ErrInvalidCredentials
	}
	return *u, nil
}

func (s *stubUserService) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword string, minLength int) error {
	return nil
}

func (s *stubUserService) AdminSetPassword(ctx context.Context, userID int64, newPassword string, mustChange bool, minLength int) error {
	return nil
}

func (s *stubUserService) ListUsers(ctx context.Context) ([]users.User, error) {
	result := make([]users.User, 0, len(s.users))
	for _, u := range s.users {
		result = append(result, *u)
	}
	return result, nil
}

func (s *stubUserService) GetUser(ctx context.Context, id int64) (users.User, error) {
	for _, u := range s.users {
		if u.ID == id {
			return *u, nil
		}
	}
	return users.User{}, users.ErrUserNotFound
}

func (s *stubUserService) ChangeRole(ctx context.Context, userID int64, newRole users.Role) error {
	return nil
}

func (s *stubUserService) DeleteUser(ctx context.Context, userID int64) error {
	return nil
}

func (s *stubUserService) CountUsers(ctx context.Context) (int, error) {
	return s.countReturn, nil
}

func (s *stubUserService) Close() error { return nil }
