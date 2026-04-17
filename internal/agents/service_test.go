package agents_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/eventbus"
)

func newTestService(t *testing.T) agents.AgentService {
	t.Helper()
	dir := t.TempDir()
	store, err := agents.NewSQLiteAgentStore(filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatalf("NewSQLiteAgentStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// nil bus: events are optional
	return agents.NewAgentService(store, nil)
}

func TestServiceCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		svc := newTestService(t)
		a, err := svc.Create(ctx, "claude")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.ID == 0 {
			t.Error("expected non-zero ID")
		}
		if a.Name != "claude" {
			t.Errorf("got name %q, want %q", a.Name, "claude")
		}
		if len(a.Token) != 64 {
			t.Errorf("expected 64-char hex token, got len %d: %q", len(a.Token), a.Token)
		}
	})

	t.Run("empty name returns ErrAgentNameEmpty", func(t *testing.T) {
		svc := newTestService(t)
		_, err := svc.Create(ctx, "")
		if err != agents.ErrAgentNameEmpty {
			t.Errorf("got %v, want ErrAgentNameEmpty", err)
		}
	})

	t.Run("duplicate name returns ErrAgentAlreadyExists", func(t *testing.T) {
		svc := newTestService(t)
		_, err := svc.Create(ctx, "claude")
		if err != nil {
			t.Fatalf("first create: %v", err)
		}
		_, err = svc.Create(ctx, "claude")
		if err != agents.ErrAgentAlreadyExists {
			t.Errorf("got %v, want ErrAgentAlreadyExists", err)
		}
	})
}

func TestServiceGetByID(t *testing.T) {
	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		svc := newTestService(t)
		created, _ := svc.Create(ctx, "agent")
		got, err := svc.GetByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("got ID %d, want %d", got.ID, created.ID)
		}
	})

	t.Run("not found returns ErrAgentNotFound", func(t *testing.T) {
		svc := newTestService(t)
		_, err := svc.GetByID(ctx, 9999)
		if err != agents.ErrAgentNotFound {
			t.Errorf("got %v, want ErrAgentNotFound", err)
		}
	})
}

func TestServiceList(t *testing.T) {
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		svc := newTestService(t)
		list, err := svc.List(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("expected empty, got %d", len(list))
		}
	})

	t.Run("with data", func(t *testing.T) {
		svc := newTestService(t)
		svc.Create(ctx, "a1")
		svc.Create(ctx, "a2")
		list, err := svc.List(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(list) != 2 {
			t.Errorf("expected 2, got %d", len(list))
		}
	})
}

func TestServiceRotateToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success returns different token", func(t *testing.T) {
		svc := newTestService(t)
		a, _ := svc.Create(ctx, "agent")
		newTok, err := svc.RotateToken(ctx, a.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if newTok == a.Token {
			t.Error("rotated token must differ from old token")
		}
		if len(newTok) != 64 {
			t.Errorf("expected 64-char hex token, got len %d", len(newTok))
		}
	})

	t.Run("not found returns ErrAgentNotFound", func(t *testing.T) {
		svc := newTestService(t)
		_, err := svc.RotateToken(ctx, 9999)
		if err != agents.ErrAgentNotFound {
			t.Errorf("got %v, want ErrAgentNotFound", err)
		}
	})
}

func TestServiceDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		svc := newTestService(t)
		a, _ := svc.Create(ctx, "agent")
		err := svc.Delete(ctx, a.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err = svc.GetByID(ctx, a.ID)
		if err != agents.ErrAgentNotFound {
			t.Errorf("expected ErrAgentNotFound after delete, got %v", err)
		}
	})

	t.Run("not found returns ErrAgentNotFound", func(t *testing.T) {
		svc := newTestService(t)
		err := svc.Delete(ctx, 9999)
		if err != agents.ErrAgentNotFound {
			t.Errorf("got %v, want ErrAgentNotFound", err)
		}
	})
}

func TestServiceSetAndListPermissions(t *testing.T) {
	ctx := context.Background()

	t.Run("roundtrip", func(t *testing.T) {
		svc := newTestService(t)
		a, _ := svc.Create(ctx, "agent")
		perms := []agents.AgentPermission{
			{AgentID: a.ID, PluginName: "tasks", ToolName: "list_tasks"},
			{AgentID: a.ID, PluginName: "notes", ToolName: "*"},
		}
		err := svc.SetPermissions(ctx, a.ID, perms)
		if err != nil {
			t.Fatalf("SetPermissions: %v", err)
		}
		got, err := svc.ListPermissions(ctx, a.ID)
		if err != nil {
			t.Fatalf("ListPermissions: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 perms, got %d", len(got))
		}
	})

	t.Run("agent not found returns ErrAgentNotFound", func(t *testing.T) {
		svc := newTestService(t)
		err := svc.SetPermissions(ctx, 9999, []agents.AgentPermission{})
		if err != agents.ErrAgentNotFound {
			t.Errorf("got %v, want ErrAgentNotFound", err)
		}
	})

	// Regression: SetPermissions used to leave the ResolveByToken cache
	// untouched. New agents are created with wildcard `*/*` perms; if the
	// admin then narrowed them, the cached entry kept the old wildcard for
	// the cache TTL window and MCP requests were silently still allowed.
	t.Run("invalidates token cache so the next resolve reflects new perms", func(t *testing.T) {
		svc := newTestService(t)
		a, _ := svc.Create(ctx, "narrowed")

		// Prime the cache: the first ResolveByToken sees the default
		// wildcard set the constructor wrote.
		_, perms, err := svc.ResolveByToken(ctx, a.Token)
		if err != nil {
			t.Fatalf("ResolveByToken (priming): %v", err)
		}
		if len(perms) == 0 {
			t.Fatalf("expected at least one default perm to be cached, got 0")
		}

		// Strip every permission.
		if err := svc.SetPermissions(ctx, a.ID, nil); err != nil {
			t.Fatalf("SetPermissions: %v", err)
		}

		// Without cache invalidation this call returns the stale wildcard
		// and the test fails.
		_, perms, err = svc.ResolveByToken(ctx, a.Token)
		if err != nil {
			t.Fatalf("ResolveByToken (post-restrict): %v", err)
		}
		if len(perms) != 0 {
			t.Fatalf("expected 0 perms after SetPermissions(nil), got %d: %+v", len(perms), perms)
		}
	})
}

func TestServiceResolveByToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success returns agent and perms", func(t *testing.T) {
		svc := newTestService(t)
		a, _ := svc.Create(ctx, "agent")
		svc.SetPermissions(ctx, a.ID, []agents.AgentPermission{
			{AgentID: a.ID, PluginName: "tasks", ToolName: "list_tasks"},
		})
		gotAgent, gotPerms, err := svc.ResolveByToken(ctx, a.Token)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAgent.ID != a.ID {
			t.Errorf("got ID %d, want %d", gotAgent.ID, a.ID)
		}
		if len(gotPerms) != 1 {
			t.Errorf("expected 1 perm, got %d", len(gotPerms))
		}
	})

	t.Run("not found returns ErrAgentNotFound", func(t *testing.T) {
		svc := newTestService(t)
		_, _, err := svc.ResolveByToken(ctx, "nonexistent-token")
		if err != agents.ErrAgentNotFound {
			t.Errorf("got %v, want ErrAgentNotFound", err)
		}
	})

	// Production scenario: many agents hit /mcp at the same time, each
	// with its own bearer token. The cache is keyed by token but the
	// underlying perm slices share a *sql.DB pool and a single mutex.
	// This test fans out N tokens with distinct narrow perms and pounds
	// ResolveByToken from a goroutine per token, asserting each call
	// sees only the perms that belong to its token. A cross-token leak
	// (wrong agent returned, or wrong perm slice) fails the test.
	t.Run("concurrent resolves never leak perms across tokens", func(t *testing.T) {
		const agentCount = 10
		const callsPerAgent = 200

		svc := newTestService(t)

		type fixture struct {
			id    int64
			token string
			tool  string
		}
		fixtures := make([]fixture, agentCount)
		for i := 0; i < agentCount; i++ {
			a, err := svc.Create(ctx, "agent-"+string(rune('A'+i)))
			if err != nil {
				t.Fatalf("Create %d: %v", i, err)
			}
			tool := "tool_" + string(rune('a'+i))
			if err := svc.SetPermissions(ctx, a.ID, []agents.AgentPermission{
				{AgentID: a.ID, PluginName: "p" + string(rune('a'+i)), ToolName: tool},
			}); err != nil {
				t.Fatalf("SetPermissions %d: %v", i, err)
			}
			fixtures[i] = fixture{id: a.ID, token: a.Token, tool: tool}
		}

		var wg sync.WaitGroup
		errs := make(chan error, agentCount*callsPerAgent)

		for _, f := range fixtures {
			wg.Add(1)
			go func(f fixture) {
				defer wg.Done()
				for i := 0; i < callsPerAgent; i++ {
					gotAgent, gotPerms, err := svc.ResolveByToken(ctx, f.token)
					if err != nil {
						errs <- err
						return
					}
					if gotAgent.ID != f.id {
						errs <- fmt.Errorf("token %s: got agent ID %d, want %d", f.token[:8], gotAgent.ID, f.id)
						return
					}
					if len(gotPerms) != 1 || gotPerms[0].ToolName != f.tool {
						errs <- fmt.Errorf("token %s: got perms %+v, want single tool %q", f.token[:8], gotPerms, f.tool)
						return
					}
				}
			}(f)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	})
}

func TestServiceWithRealBus(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := agents.NewSQLiteAgentStore(filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatalf("NewSQLiteAgentStore: %v", err)
	}
	defer store.Close()

	bus := eventbus.NewInMemoryBus()
	defer bus.Close()

	svc := agents.NewAgentService(store, bus)

	var mu sync.Mutex
	received := map[string]int{}
	bus.Subscribe(eventbus.EventAgentCreated, func(_ context.Context, e eventbus.Event) {
		mu.Lock()
		received[e.Name]++
		mu.Unlock()
	})
	bus.Subscribe(eventbus.EventAgentDeleted, func(_ context.Context, e eventbus.Event) {
		mu.Lock()
		received[e.Name]++
		mu.Unlock()
	})
	bus.Subscribe(eventbus.EventAgentTokenRotated, func(_ context.Context, e eventbus.Event) {
		mu.Lock()
		received[e.Name]++
		mu.Unlock()
	})
	bus.Subscribe(eventbus.EventAgentPermissionsChanged, func(_ context.Context, e eventbus.Event) {
		mu.Lock()
		received[e.Name]++
		mu.Unlock()
	})

	a, err := svc.Create(ctx, "bus-agent")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.RotateToken(ctx, a.ID)
	if err != nil {
		t.Fatalf("RotateToken: %v", err)
	}

	err = svc.SetPermissions(ctx, a.ID, []agents.AgentPermission{
		{AgentID: a.ID, PluginName: "tasks", ToolName: "*"},
	})
	if err != nil {
		t.Fatalf("SetPermissions: %v", err)
	}

	err = svc.Delete(ctx, a.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Wait for async delivery.
	bus.Close()

	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{
		eventbus.EventAgentCreated,
		eventbus.EventAgentDeleted,
		eventbus.EventAgentTokenRotated,
		eventbus.EventAgentPermissionsChanged,
	} {
		if received[name] != 1 {
			t.Errorf("event %q: got %d deliveries, want 1", name, received[name])
		}
	}
}

func TestIsToolAllowed(t *testing.T) {
	perms := []agents.AgentPermission{
		{AgentID: 1, PluginName: "tasks", ToolName: "list_tasks"},
		{AgentID: 1, PluginName: "notes", ToolName: "*"},
		{AgentID: 1, PluginName: agents.BuiltinPluginName, ToolName: "ping"},
	}

	tests := []struct {
		name       string
		pluginName string
		toolName   string
		want       bool
	}{
		{"exact match", "tasks", "list_tasks", true},
		{"exact miss", "tasks", "create_task", false},
		{"wildcard match", "notes", "any_tool", true},
		{"wildcard on different plugin", "tasks", "any_tool", false},
		{"builtin exact match", agents.BuiltinPluginName, "ping", true},
		{"builtin miss", agents.BuiltinPluginName, "shutdown", false},
		{"no perms", "unknown", "tool", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := agents.IsToolAllowed(perms, tc.pluginName, tc.toolName)
			if got != tc.want {
				t.Errorf("IsToolAllowed(%q, %q) = %v, want %v", tc.pluginName, tc.toolName, got, tc.want)
			}
		})
	}

	t.Run("empty perms", func(t *testing.T) {
		got := agents.IsToolAllowed(nil, "tasks", "list_tasks")
		if got {
			t.Error("expected false for nil perms")
		}
	})

	t.Run("wildcard tool name", func(t *testing.T) {
		p := []agents.AgentPermission{
			{AgentID: 1, PluginName: "tasks", ToolName: agents.WildcardTool},
		}
		if !agents.IsToolAllowed(p, "tasks", "any_tool_name") {
			t.Error("expected wildcard tool to allow any tool")
		}
	})
}
