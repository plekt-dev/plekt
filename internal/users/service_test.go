package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/users"
)

// mockUserStore is a minimal mock for service tests.
type mockUserStore struct {
	users          map[int64]*users.User
	byUsername     map[string]*users.User
	nextID         int64
	createErr      error
	getByIDErr     error
	getByUsErr     error
	listErr        error
	countErr       int // count to return when no error
	updateRoleErr  error
	updatePassErr  error
	setMustErr     error
	deleteErr      error
	countByRoleMap map[users.Role]int
}

func newMockStore() *mockUserStore {
	return &mockUserStore{
		users:          make(map[int64]*users.User),
		byUsername:     make(map[string]*users.User),
		nextID:         1,
		countByRoleMap: make(map[users.Role]int),
	}
}

func (m *mockUserStore) Create(ctx context.Context, username, passwordHash string, role users.Role, mustChangePassword bool) (users.User, error) {
	if m.createErr != nil {
		return users.User{}, m.createErr
	}
	if _, exists := m.byUsername[username]; exists {
		return users.User{}, users.ErrUserAlreadyExists
	}
	u := &users.User{
		ID:                 m.nextID,
		Username:           username,
		PasswordHash:       passwordHash,
		Role:               role,
		MustChangePassword: mustChangePassword,
	}
	m.nextID++
	m.users[u.ID] = u
	m.byUsername[username] = u
	// update countByRole
	m.countByRoleMap[role]++
	return *u, nil
}

func (m *mockUserStore) GetByID(ctx context.Context, id int64) (users.User, error) {
	if m.getByIDErr != nil {
		return users.User{}, m.getByIDErr
	}
	u, ok := m.users[id]
	if !ok {
		return users.User{}, users.ErrUserNotFound
	}
	return *u, nil
}

func (m *mockUserStore) GetByUsername(ctx context.Context, username string) (users.User, error) {
	if m.getByUsErr != nil {
		return users.User{}, m.getByUsErr
	}
	u, ok := m.byUsername[username]
	if !ok {
		return users.User{}, users.ErrUserNotFound
	}
	return *u, nil
}

func (m *mockUserStore) List(ctx context.Context) ([]users.User, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	result := make([]users.User, 0, len(m.users))
	for _, u := range m.users {
		result = append(result, *u)
	}
	return result, nil
}

func (m *mockUserStore) Count(ctx context.Context) (int, error) {
	return len(m.users), nil
}

func (m *mockUserStore) UpdateRole(ctx context.Context, id int64, role users.Role) error {
	if m.updateRoleErr != nil {
		return m.updateRoleErr
	}
	u, ok := m.users[id]
	if !ok {
		return users.ErrUserNotFound
	}
	oldRole := u.Role
	u.Role = role
	m.countByRoleMap[oldRole]--
	m.countByRoleMap[role]++
	return nil
}

func (m *mockUserStore) UpdatePasswordHash(ctx context.Context, id int64, hash string, mustChange bool) error {
	if m.updatePassErr != nil {
		return m.updatePassErr
	}
	u, ok := m.users[id]
	if !ok {
		return users.ErrUserNotFound
	}
	u.PasswordHash = hash
	u.MustChangePassword = mustChange
	return nil
}

func (m *mockUserStore) SetMustChangePassword(ctx context.Context, id int64, mustChange bool) error {
	if m.setMustErr != nil {
		return m.setMustErr
	}
	u, ok := m.users[id]
	if !ok {
		return users.ErrUserNotFound
	}
	u.MustChangePassword = mustChange
	return nil
}

func (m *mockUserStore) Delete(ctx context.Context, id int64) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	u, ok := m.users[id]
	if !ok {
		return users.ErrUserNotFound
	}
	m.countByRoleMap[u.Role]--
	delete(m.byUsername, u.Username)
	delete(m.users, id)
	return nil
}

func (m *mockUserStore) CountByRole(ctx context.Context, role users.Role) (int, error) {
	return m.countByRoleMap[role], nil
}

func (m *mockUserStore) Close() error { return nil }

func TestUserService_Create(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		username   string
		password   string
		role       users.Role
		mustChange bool
		minLen     int
		wantErr    error
	}{
		{
			name:       "valid creation",
			username:   "alice",
			password:   "longpassword1234",
			role:       users.RoleAdmin,
			mustChange: false,
			minLen:     12,
			wantErr:    nil,
		},
		{
			name:     "password too short",
			username: "bob",
			password: "short",
			role:     users.RoleUser,
			minLen:   12,
			wantErr:  users.ErrPasswordTooShort,
		},
		{
			name:     "password exactly minLen",
			username: "carol",
			password: "exactly12chr",
			role:     users.RoleUser,
			minLen:   12,
			wantErr:  nil,
		},
		{
			name:     "duplicate username",
			username: "alice",
			password: "validpassword123",
			role:     users.RoleUser,
			minLen:   12,
			wantErr:  users.ErrUserAlreadyExists,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			// Pre-populate "alice" for duplicate test
			if tc.name == "duplicate username" {
				store.byUsername["alice"] = &users.User{ID: 1, Username: "alice"}
				store.createErr = users.ErrUserAlreadyExists
			}
			svc := users.NewUserService(store, tc.minLen)
			u, err := svc.Create(context.Background(), tc.username, tc.password, tc.role, tc.mustChange)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if u.Username != tc.username {
					t.Errorf("Username = %q, want %q", u.Username, tc.username)
				}
				// Password hash should NOT equal the original password
				if u.PasswordHash == tc.password {
					t.Error("password must be hashed, not stored plaintext")
				}
				if len(u.PasswordHash) == 0 {
					t.Error("PasswordHash should not be empty")
				}
			}
		})
	}
}

func TestUserService_Authenticate(t *testing.T) {
	t.Parallel()

	const validPass = "correctpassword12"
	const minLen = 12

	t.Run("correct password", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		// Create user properly so password is hashed
		_, err := svc.Create(context.Background(), "alice", validPass, users.RoleAdmin, false)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		u, err := svc.Authenticate(context.Background(), "alice", validPass)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if u.Username != "alice" {
			t.Errorf("Username = %q, want alice", u.Username)
		}
	})

	t.Run("wrong password returns ErrInvalidCredentials", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		_, _ = svc.Create(context.Background(), "alice", validPass, users.RoleAdmin, false)
		_, err := svc.Authenticate(context.Background(), "alice", "wrongpassword123")
		if err != users.ErrInvalidCredentials {
			t.Errorf("error = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("user not found returns ErrInvalidCredentials (no username enumeration)", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		_, err := svc.Authenticate(context.Background(), "nonexistent", "somepassword")
		if err != users.ErrInvalidCredentials {
			t.Errorf("error = %v, want ErrInvalidCredentials (no username enumeration)", err)
		}
	})

	t.Run("empty username returns ErrInvalidCredentials", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		_, err := svc.Authenticate(context.Background(), "", "somepassword")
		if err != users.ErrInvalidCredentials {
			t.Errorf("error = %v, want ErrInvalidCredentials", err)
		}
	})
}

func TestUserService_ChangePassword(t *testing.T) {
	t.Parallel()

	const minLen = 12
	const validPass = "initialpassword12"
	const newPass = "newpassword12345"

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		u, _ := svc.Create(context.Background(), "alice", validPass, users.RoleAdmin, false)
		err := svc.ChangePassword(context.Background(), u.ID, validPass, newPass, minLen)
		if err != nil {
			t.Fatalf("ChangePassword: %v", err)
		}
		// Should be able to auth with new password
		_, err = svc.Authenticate(context.Background(), "alice", newPass)
		if err != nil {
			t.Errorf("Authenticate with new password: %v", err)
		}
	})

	t.Run("wrong current password", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		u, _ := svc.Create(context.Background(), "alice", validPass, users.RoleAdmin, false)
		err := svc.ChangePassword(context.Background(), u.ID, "wrongcurrentpass", newPass, minLen)
		if err != users.ErrInvalidCredentials {
			t.Errorf("error = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("new password too short", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		u, _ := svc.Create(context.Background(), "alice", validPass, users.RoleAdmin, false)
		err := svc.ChangePassword(context.Background(), u.ID, validPass, "short", minLen)
		if err != users.ErrPasswordTooShort {
			t.Errorf("error = %v, want ErrPasswordTooShort", err)
		}
	})
}

func TestUserService_DeleteUser(t *testing.T) {
	t.Parallel()

	const minLen = 12

	t.Run("success delete viewer", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		admin, _ := svc.Create(context.Background(), "admin", "adminpass1234", users.RoleAdmin, false)
		regular, _ := svc.Create(context.Background(), "regular", "regularpass1234", users.RoleUser, false)
		_ = admin
		err := svc.DeleteUser(context.Background(), regular.ID)
		if err != nil {
			t.Fatalf("DeleteUser: %v", err)
		}
	})

	t.Run("last admin blocked", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		admin, _ := svc.Create(context.Background(), "admin", "adminpass1234", users.RoleAdmin, false)
		err := svc.DeleteUser(context.Background(), admin.ID)
		if err != users.ErrCannotDeleteLastAdmin {
			t.Errorf("error = %v, want ErrCannotDeleteLastAdmin", err)
		}
	})

	t.Run("two admins then delete one succeeds", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		admin1, _ := svc.Create(context.Background(), "admin1", "adminpass1234", users.RoleAdmin, false)
		_, _ = svc.Create(context.Background(), "admin2", "adminpass5678", users.RoleAdmin, false)
		err := svc.DeleteUser(context.Background(), admin1.ID)
		if err != nil {
			t.Fatalf("DeleteUser: %v", err)
		}
	})
}

func TestUserService_ChangeRole(t *testing.T) {
	t.Parallel()

	const minLen = 12

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		u, _ := svc.Create(context.Background(), "alice", "alicepass1234", users.RoleUser, false)
		err := svc.ChangeRole(context.Background(), u.ID, users.RoleAdmin)
		if err != nil {
			t.Fatalf("ChangeRole: %v", err)
		}
		got, _ := svc.GetUser(context.Background(), u.ID)
		if got.Role != users.RoleAdmin {
			t.Errorf("Role = %q, want admin", got.Role)
		}
	})

	t.Run("user not found", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		err := svc.ChangeRole(context.Background(), 99999, users.RoleAdmin)
		if err != users.ErrUserNotFound {
			t.Errorf("error = %v, want ErrUserNotFound", err)
		}
	})
}

func TestUserService_AdminSetPassword(t *testing.T) {
	t.Parallel()

	const minLen = 12

	t.Run("success with mustChange true", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		u, _ := svc.Create(context.Background(), "alice", "alicepass1234", users.RoleAdmin, false)
		err := svc.AdminSetPassword(context.Background(), u.ID, "newadminpass12", true, minLen)
		if err != nil {
			t.Fatalf("AdminSetPassword: %v", err)
		}
		got, _ := store.GetByID(context.Background(), u.ID)
		if !got.MustChangePassword {
			t.Error("expected MustChangePassword = true")
		}
	})

	t.Run("new password too short", func(t *testing.T) {
		t.Parallel()
		store := newMockStore()
		svc := users.NewUserService(store, minLen)
		u, _ := svc.Create(context.Background(), "alice", "alicepass1234", users.RoleAdmin, false)
		err := svc.AdminSetPassword(context.Background(), u.ID, "short", false, minLen)
		if err != users.ErrPasswordTooShort {
			t.Errorf("error = %v, want ErrPasswordTooShort", err)
		}
	})
}

// TestUserService_Authenticate_TimingSafe verifies that authentication for a
// non-existent user takes roughly the same time as for an existing user with
// a wrong password (VULN-09: timing-safe login).
func TestUserService_Authenticate_TimingSafe(t *testing.T) {
	t.Parallel()

	const minLen = 12
	const validPass = "correctpassword12"

	store := newMockStore()
	svc := users.NewUserService(store, minLen)
	_, err := svc.Create(context.Background(), "existing", validPass, users.RoleAdmin, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Warm up bcrypt (first call may be slower due to initialization).
	_, _ = svc.Authenticate(context.Background(), "existing", "wrongpass123456")

	// Measure wrong password for existing user.
	const iterations = 3
	var existingTotal time.Duration
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, _ = svc.Authenticate(context.Background(), "existing", "wrongpass123456")
		existingTotal += time.Since(start)
	}
	existingAvg := existingTotal / iterations

	// Measure non-existent user.
	var nonexistentTotal time.Duration
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, _ = svc.Authenticate(context.Background(), "nonexistent", "wrongpass123456")
		nonexistentTotal += time.Since(start)
	}
	nonexistentAvg := nonexistentTotal / iterations

	// Both should take roughly the same time (within 2x tolerance).
	ratio := float64(nonexistentAvg) / float64(existingAvg)
	if ratio < 0.5 || ratio > 2.0 {
		t.Errorf("timing ratio = %.2f (nonexistent=%v, existing=%v); want within 0.5-2.0x",
			ratio, nonexistentAvg, existingAvg)
	}
}

func TestUserService_CountUsers(t *testing.T) {
	t.Parallel()

	const minLen = 12

	store := newMockStore()
	svc := users.NewUserService(store, minLen)

	n, err := svc.CountUsers(context.Background())
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("CountUsers = %d, want 0", n)
	}

	_, _ = svc.Create(context.Background(), "alice", "alicepass1234", users.RoleAdmin, false)
	n, err = svc.CountUsers(context.Background())
	if err != nil {
		t.Fatalf("CountUsers after create: %v", err)
	}
	if n != 1 {
		t.Errorf("CountUsers = %d, want 1", n)
	}
}
