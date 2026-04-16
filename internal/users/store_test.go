package users_test

import (
	"context"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/users"
)

func newTestStore(t *testing.T) users.UserStore {
	t.Helper()
	store, err := users.NewSQLiteUserStore(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteUserStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteUserStore_Create(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		username   string
		passHash   string
		role       users.Role
		mustChange bool
		wantErr    error
	}{
		{
			name:       "happy path admin",
			username:   "alice",
			passHash:   "$2a$12$hashhash",
			role:       users.RoleAdmin,
			mustChange: false,
		},
		{
			name:       "happy path viewer",
			username:   "bob",
			passHash:   "$2a$12$otherhash",
			role:       users.RoleUser,
			mustChange: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newTestStore(t)
			before := time.Now().Add(-time.Second)

			u, err := store.Create(context.Background(), tc.username, tc.passHash, tc.role, tc.mustChange)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if u.ID == 0 {
				t.Error("ID should be non-zero")
			}
			if u.Username != tc.username {
				t.Errorf("Username = %q, want %q", u.Username, tc.username)
			}
			if u.PasswordHash != tc.passHash {
				t.Errorf("PasswordHash = %q, want %q", u.PasswordHash, tc.passHash)
			}
			if u.Role != tc.role {
				t.Errorf("Role = %q, want %q", u.Role, tc.role)
			}
			if u.MustChangePassword != tc.mustChange {
				t.Errorf("MustChangePassword = %v, want %v", u.MustChangePassword, tc.mustChange)
			}
			if u.CreatedAt.Before(before) {
				t.Errorf("CreatedAt %v before test start %v", u.CreatedAt, before)
			}
		})
	}
}

func TestSQLiteUserStore_Create_DuplicateUsername(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)

	_, err := store.Create(context.Background(), "alice", "hash1", users.RoleAdmin, false)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err = store.Create(context.Background(), "alice", "hash2", users.RoleUser, false)
	if err == nil {
		t.Fatal("expected error for duplicate username, got nil")
	}
	if err != users.ErrUserAlreadyExists {
		t.Errorf("error = %v, want ErrUserAlreadyExists", err)
	}
}

func TestSQLiteUserStore_Create_DuplicateUsername_CaseInsensitive(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)

	_, err := store.Create(context.Background(), "alice", "hash1", users.RoleAdmin, false)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err = store.Create(context.Background(), "ALICE", "hash2", users.RoleUser, false)
	if err == nil {
		t.Fatal("expected error for case-insensitive duplicate username, got nil")
	}
	if err != users.ErrUserAlreadyExists {
		t.Errorf("error = %v, want ErrUserAlreadyExists", err)
	}
}

func TestSQLiteUserStore_GetByID(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	created, err := store.Create(context.Background(), "alice", "hash", users.RoleAdmin, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tests := []struct {
		name    string
		id      int64
		wantErr error
	}{
		{name: "found", id: created.ID, wantErr: nil},
		{name: "not found", id: 99999, wantErr: users.ErrUserNotFound},
		{name: "zero id", id: 0, wantErr: users.ErrUserNotFound},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// No t.Parallel(): subtests share the parent's in-memory DB store.
			got, err := store.GetByID(context.Background(), tc.id)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Errorf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if got.ID != created.ID {
				t.Errorf("ID = %d, want %d", got.ID, created.ID)
			}
			if got.Username != created.Username {
				t.Errorf("Username = %q, want %q", got.Username, created.Username)
			}
		})
	}
}

func TestSQLiteUserStore_GetByUsername(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	created, err := store.Create(context.Background(), "alice", "hash", users.RoleAdmin, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tests := []struct {
		name     string
		username string
		wantErr  error
	}{
		{name: "found exact", username: "alice", wantErr: nil},
		{name: "found uppercase", username: "ALICE", wantErr: nil},
		{name: "found mixed case", username: "Alice", wantErr: nil},
		{name: "not found", username: "bob", wantErr: users.ErrUserNotFound},
		{name: "empty", username: "", wantErr: users.ErrUserNotFound},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// No t.Parallel(): subtests share the parent's in-memory DB store.
			got, err := store.GetByUsername(context.Background(), tc.username)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Errorf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetByUsername: %v", err)
			}
			if got.ID != created.ID {
				t.Errorf("ID = %d, want %d", got.ID, created.ID)
			}
		})
	}
}

func TestSQLiteUserStore_List(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		got, err := store.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("List = %d entries, want 0", len(got))
		}
	})

	t.Run("multiple users", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		for _, name := range []string{"alice", "bob", "carol"} {
			if _, err := store.Create(context.Background(), name, "hash", users.RoleUser, false); err != nil {
				t.Fatalf("Create %s: %v", name, err)
			}
		}
		got, err := store.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Errorf("List = %d entries, want 3", len(got))
		}
	})
}

func TestSQLiteUserStore_Count(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		n, err := store.Count(context.Background())
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if n != 0 {
			t.Errorf("Count = %d, want 0", n)
		}
	})

	t.Run("after inserts", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		for _, name := range []string{"alice", "bob"} {
			if _, err := store.Create(context.Background(), name, "hash", users.RoleUser, false); err != nil {
				t.Fatalf("Create %s: %v", name, err)
			}
		}
		n, err := store.Count(context.Background())
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if n != 2 {
			t.Errorf("Count = %d, want 2", n)
		}
	})
}

func TestSQLiteUserStore_UpdateRole(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		u, _ := store.Create(context.Background(), "alice", "hash", users.RoleUser, false)
		err := store.UpdateRole(context.Background(), u.ID, users.RoleAdmin)
		if err != nil {
			t.Fatalf("UpdateRole: %v", err)
		}
		got, _ := store.GetByID(context.Background(), u.ID)
		if got.Role != users.RoleAdmin {
			t.Errorf("Role = %q, want admin", got.Role)
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		err := store.UpdateRole(context.Background(), 99999, users.RoleAdmin)
		if err != users.ErrUserNotFound {
			t.Errorf("error = %v, want ErrUserNotFound", err)
		}
	})
}

func TestSQLiteUserStore_UpdatePasswordHash(t *testing.T) {
	t.Parallel()

	t.Run("success mustChange false", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		u, _ := store.Create(context.Background(), "alice", "oldhash", users.RoleAdmin, true)
		err := store.UpdatePasswordHash(context.Background(), u.ID, "newhash", false)
		if err != nil {
			t.Fatalf("UpdatePasswordHash: %v", err)
		}
		got, _ := store.GetByID(context.Background(), u.ID)
		if got.PasswordHash != "newhash" {
			t.Errorf("PasswordHash = %q, want newhash", got.PasswordHash)
		}
		if got.MustChangePassword != false {
			t.Errorf("MustChangePassword = %v, want false", got.MustChangePassword)
		}
	})

	t.Run("mustChange propagated true", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		u, _ := store.Create(context.Background(), "bob", "oldhash", users.RoleUser, false)
		err := store.UpdatePasswordHash(context.Background(), u.ID, "newhash", true)
		if err != nil {
			t.Fatalf("UpdatePasswordHash: %v", err)
		}
		got, _ := store.GetByID(context.Background(), u.ID)
		if got.MustChangePassword != true {
			t.Errorf("MustChangePassword = %v, want true", got.MustChangePassword)
		}
	})
}

func TestSQLiteUserStore_SetMustChangePassword(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	u, _ := store.Create(context.Background(), "alice", "hash", users.RoleAdmin, false)

	// Toggle to true
	err := store.SetMustChangePassword(context.Background(), u.ID, true)
	if err != nil {
		t.Fatalf("SetMustChangePassword true: %v", err)
	}
	got, _ := store.GetByID(context.Background(), u.ID)
	if !got.MustChangePassword {
		t.Error("expected MustChangePassword = true")
	}

	// Toggle back to false
	err = store.SetMustChangePassword(context.Background(), u.ID, false)
	if err != nil {
		t.Fatalf("SetMustChangePassword false: %v", err)
	}
	got, _ = store.GetByID(context.Background(), u.ID)
	if got.MustChangePassword {
		t.Error("expected MustChangePassword = false")
	}
}

func TestSQLiteUserStore_Delete(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		u, _ := store.Create(context.Background(), "alice", "hash", users.RoleAdmin, false)
		err := store.Delete(context.Background(), u.ID)
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err = store.GetByID(context.Background(), u.ID)
		if err != users.ErrUserNotFound {
			t.Errorf("GetByID after delete: %v, want ErrUserNotFound", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		err := store.Delete(context.Background(), 99999)
		if err != users.ErrUserNotFound {
			t.Errorf("error = %v, want ErrUserNotFound", err)
		}
	})
}

func TestSQLiteUserStore_CountByRole(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	_, _ = store.Create(context.Background(), "admin1", "h", users.RoleAdmin, false)
	_, _ = store.Create(context.Background(), "admin2", "h", users.RoleAdmin, false)
	_, _ = store.Create(context.Background(), "viewer1", "h", users.RoleUser, false)

	admins, err := store.CountByRole(context.Background(), users.RoleAdmin)
	if err != nil {
		t.Fatalf("CountByRole admin: %v", err)
	}
	if admins != 2 {
		t.Errorf("admins = %d, want 2", admins)
	}

	viewers, err := store.CountByRole(context.Background(), users.RoleUser)
	if err != nil {
		t.Fatalf("CountByRole viewer: %v", err)
	}
	if viewers != 1 {
		t.Errorf("viewers = %d, want 1", viewers)
	}
}
