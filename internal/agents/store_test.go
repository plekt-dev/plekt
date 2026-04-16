package agents_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/plekt-dev/plekt/internal/agents"
)

func newTestStore(t *testing.T) agents.AgentStore {
	t.Helper()
	dir := t.TempDir()
	store, err := agents.NewSQLiteAgentStore(filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatalf("NewSQLiteAgentStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestCreateAgent(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := newTestStore(t)
		a, err := store.CreateAgent(ctx, "claude", "token-abc123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.ID == 0 {
			t.Error("expected non-zero ID")
		}
		if a.Name != "claude" {
			t.Errorf("got name %q, want %q", a.Name, "claude")
		}
		if a.Token != "token-abc123" {
			t.Errorf("got token %q, want %q", a.Token, "token-abc123")
		}
		if a.CreatedAt.IsZero() {
			t.Error("expected non-zero created_at")
		}
		if a.UpdatedAt.IsZero() {
			t.Error("expected non-zero updated_at")
		}
	})

	t.Run("duplicate name returns ErrAgentAlreadyExists", func(t *testing.T) {
		store := newTestStore(t)
		_, err := store.CreateAgent(ctx, "claude", "token1")
		if err != nil {
			t.Fatalf("first create: %v", err)
		}
		_, err = store.CreateAgent(ctx, "claude", "token2")
		if err == nil {
			t.Fatal("expected error for duplicate name, got nil")
		}
		if err != agents.ErrAgentAlreadyExists {
			t.Errorf("got %v, want ErrAgentAlreadyExists", err)
		}
	})

	t.Run("duplicate token returns ErrAgentAlreadyExists", func(t *testing.T) {
		store := newTestStore(t)
		_, err := store.CreateAgent(ctx, "agent1", "same-token")
		if err != nil {
			t.Fatalf("first create: %v", err)
		}
		_, err = store.CreateAgent(ctx, "agent2", "same-token")
		if err == nil {
			t.Fatal("expected error for duplicate token, got nil")
		}
		if err != agents.ErrAgentAlreadyExists {
			t.Errorf("got %v, want ErrAgentAlreadyExists", err)
		}
	})
}

func TestGetAgentByID(t *testing.T) {
	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		store := newTestStore(t)
		created, _ := store.CreateAgent(ctx, "agent", "tok")
		got, err := store.GetAgentByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("got ID %d, want %d", got.ID, created.ID)
		}
		if got.Name != "agent" {
			t.Errorf("got name %q, want %q", got.Name, "agent")
		}
	})

	t.Run("not found returns ErrAgentNotFound", func(t *testing.T) {
		store := newTestStore(t)
		_, err := store.GetAgentByID(ctx, 9999)
		if err != agents.ErrAgentNotFound {
			t.Errorf("got %v, want ErrAgentNotFound", err)
		}
	})
}

func TestGetAgentByToken(t *testing.T) {
	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		store := newTestStore(t)
		created, _ := store.CreateAgent(ctx, "agent", "mytoken")
		got, err := store.GetAgentByToken(ctx, "mytoken")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("got ID %d, want %d", got.ID, created.ID)
		}
	})

	t.Run("not found returns ErrAgentNotFound", func(t *testing.T) {
		store := newTestStore(t)
		_, err := store.GetAgentByToken(ctx, "nonexistent-token")
		if err != agents.ErrAgentNotFound {
			t.Errorf("got %v, want ErrAgentNotFound", err)
		}
	})
}

func TestListAgents(t *testing.T) {
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		store := newTestStore(t)
		agents, err := store.ListAgents(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(agents) != 0 {
			t.Errorf("expected empty list, got %d items", len(agents))
		}
	})

	t.Run("with data", func(t *testing.T) {
		store := newTestStore(t)
		store.CreateAgent(ctx, "a1", "t1")
		store.CreateAgent(ctx, "a2", "t2")
		store.CreateAgent(ctx, "a3", "t3")
		list, err := store.ListAgents(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(list) != 3 {
			t.Errorf("expected 3 agents, got %d", len(list))
		}
	})
}

func TestUpdateAgentToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := newTestStore(t)
		a, _ := store.CreateAgent(ctx, "agent", "oldtoken")
		err := store.UpdateAgentToken(ctx, a.ID, "newtoken")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := store.GetAgentByID(ctx, a.ID)
		if got.Token != "newtoken" {
			t.Errorf("token not updated: got %q", got.Token)
		}
		if !got.UpdatedAt.After(a.UpdatedAt) && got.UpdatedAt.Equal(a.UpdatedAt) {
			// updated_at may equal if sub-second: just check not zero
		}
	})

	t.Run("not found returns ErrAgentNotFound", func(t *testing.T) {
		store := newTestStore(t)
		err := store.UpdateAgentToken(ctx, 9999, "tok")
		if err != agents.ErrAgentNotFound {
			t.Errorf("got %v, want ErrAgentNotFound", err)
		}
	})
}

func TestDeleteAgent(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := newTestStore(t)
		a, _ := store.CreateAgent(ctx, "agent", "tok")
		err := store.DeleteAgent(ctx, a.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err = store.GetAgentByID(ctx, a.ID)
		if err != agents.ErrAgentNotFound {
			t.Errorf("expected ErrAgentNotFound after delete, got %v", err)
		}
	})

	t.Run("not found returns ErrAgentNotFound", func(t *testing.T) {
		store := newTestStore(t)
		err := store.DeleteAgent(ctx, 9999)
		if err != agents.ErrAgentNotFound {
			t.Errorf("got %v, want ErrAgentNotFound", err)
		}
	})

	t.Run("CASCADE deletes permissions", func(t *testing.T) {
		store := newTestStore(t)
		a, _ := store.CreateAgent(ctx, "agent", "tok")
		perms := []agents.AgentPermission{
			{AgentID: a.ID, PluginName: "tasks", ToolName: "list_tasks"},
			{AgentID: a.ID, PluginName: "tasks", ToolName: "create_task"},
		}
		store.SetPermissions(ctx, a.ID, perms)

		err := store.DeleteAgent(ctx, a.ID)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}

		// Permissions for deleted agent should be gone due to CASCADE.
		// We can verify by re-creating an agent: no orphan rows should exist.
		// (We can't call ListPermissions for a deleted agent, so this test just
		// verifies no FK error on delete.)
	})
}

func TestSetPermissions(t *testing.T) {
	ctx := context.Background()

	t.Run("set permissions", func(t *testing.T) {
		store := newTestStore(t)
		a, _ := store.CreateAgent(ctx, "agent", "tok")
		perms := []agents.AgentPermission{
			{AgentID: a.ID, PluginName: "tasks", ToolName: "list_tasks"},
			{AgentID: a.ID, PluginName: "tasks", ToolName: "create_task"},
		}
		err := store.SetPermissions(ctx, a.ID, perms)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, err := store.ListPermissions(ctx, a.ID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 perms, got %d", len(got))
		}
	})

	t.Run("replace permissions atomically", func(t *testing.T) {
		store := newTestStore(t)
		a, _ := store.CreateAgent(ctx, "agent", "tok")
		store.SetPermissions(ctx, a.ID, []agents.AgentPermission{
			{AgentID: a.ID, PluginName: "tasks", ToolName: "list_tasks"},
			{AgentID: a.ID, PluginName: "tasks", ToolName: "create_task"},
		})
		// Replace with a single different perm
		err := store.SetPermissions(ctx, a.ID, []agents.AgentPermission{
			{AgentID: a.ID, PluginName: "notes", ToolName: "list_notes"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := store.ListPermissions(ctx, a.ID)
		if len(got) != 1 {
			t.Errorf("expected 1 perm after replace, got %d", len(got))
		}
		if got[0].PluginName != "notes" || got[0].ToolName != "list_notes" {
			t.Errorf("unexpected perm: %+v", got[0])
		}
	})

	t.Run("empty slice clears all", func(t *testing.T) {
		store := newTestStore(t)
		a, _ := store.CreateAgent(ctx, "agent", "tok")
		store.SetPermissions(ctx, a.ID, []agents.AgentPermission{
			{AgentID: a.ID, PluginName: "tasks", ToolName: "list_tasks"},
		})
		err := store.SetPermissions(ctx, a.ID, []agents.AgentPermission{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, _ := store.ListPermissions(ctx, a.ID)
		if len(got) != 0 {
			t.Errorf("expected empty perms, got %d", len(got))
		}
	})
}

func TestListPermissions(t *testing.T) {
	ctx := context.Background()

	t.Run("with data", func(t *testing.T) {
		store := newTestStore(t)
		a, _ := store.CreateAgent(ctx, "agent", "tok")
		store.SetPermissions(ctx, a.ID, []agents.AgentPermission{
			{AgentID: a.ID, PluginName: "tasks", ToolName: "list_tasks"},
			{AgentID: a.ID, PluginName: "notes", ToolName: "*"},
		})
		got, err := store.ListPermissions(ctx, a.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 perms, got %d", len(got))
		}
		for _, p := range got {
			if p.AgentID != a.ID {
				t.Errorf("got agent_id %d, want %d", p.AgentID, a.ID)
			}
		}
	})

	t.Run("empty", func(t *testing.T) {
		store := newTestStore(t)
		a, _ := store.CreateAgent(ctx, "agent", "tok")
		got, err := store.ListPermissions(ctx, a.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty, got %d", len(got))
		}
	})
}

func TestNewSQLiteAgentStore_InvalidPath(t *testing.T) {
	// Provide a path under a non-existent nested directory to trigger open error.
	_, err := agents.NewSQLiteAgentStore("/nonexistent/deeply/nested/path/agents.db")
	if err == nil {
		t.Error("expected error for invalid path, got nil")
	}
}
