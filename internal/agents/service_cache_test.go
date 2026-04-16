package agents_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/plekt-dev/plekt/internal/agents"
)

func TestDefaultAgentService_TokenCache_HitAvoidsDuplicate(t *testing.T) {
	dir := t.TempDir()
	store, err := agents.NewSQLiteAgentStore(filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	svc := agents.NewAgentService(store, nil)

	ag, err := svc.Create(ctx, "bot")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First call: hits DB
	got1, _, err := svc.ResolveByToken(ctx, ag.Token)
	if err != nil {
		t.Fatalf("first ResolveByToken: %v", err)
	}
	if got1.ID != ag.ID {
		t.Errorf("agent ID = %d, want %d", got1.ID, ag.ID)
	}

	// Second call: should be served from cache (same result)
	got2, _, err := svc.ResolveByToken(ctx, ag.Token)
	if err != nil {
		t.Fatalf("second ResolveByToken: %v", err)
	}
	if got2.ID != ag.ID {
		t.Errorf("cached agent ID = %d, want %d", got2.ID, ag.ID)
	}
}

func TestDefaultAgentService_TokenCache_InvalidatedOnRotate(t *testing.T) {
	dir := t.TempDir()
	store, err := agents.NewSQLiteAgentStore(filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	svc := agents.NewAgentService(store, nil)

	ag, err := svc.Create(ctx, "bot")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	oldToken := ag.Token

	// Populate cache
	_, _, err = svc.ResolveByToken(ctx, oldToken)
	if err != nil {
		t.Fatalf("ResolveByToken: %v", err)
	}

	// Rotate token: must invalidate cache
	newToken, err := svc.RotateToken(ctx, ag.ID)
	if err != nil {
		t.Fatalf("RotateToken: %v", err)
	}

	// Old token must no longer resolve
	_, _, err = svc.ResolveByToken(ctx, oldToken)
	if err == nil {
		t.Error("expected error for old token after rotation, got nil")
	}

	// New token must resolve
	got, _, err := svc.ResolveByToken(ctx, newToken)
	if err != nil {
		t.Fatalf("ResolveByToken new token: %v", err)
	}
	if got.ID != ag.ID {
		t.Errorf("agent ID = %d, want %d", got.ID, ag.ID)
	}
}

func TestDefaultAgentService_TokenCache_InvalidatedOnDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := agents.NewSQLiteAgentStore(filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	svc := agents.NewAgentService(store, nil)

	ag, err := svc.Create(ctx, "bot")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Populate cache
	_, _, err = svc.ResolveByToken(ctx, ag.Token)
	if err != nil {
		t.Fatalf("ResolveByToken: %v", err)
	}

	// Delete agent: must invalidate cache
	if err := svc.Delete(ctx, ag.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Token must no longer resolve
	_, _, err = svc.ResolveByToken(ctx, ag.Token)
	if err == nil {
		t.Error("expected error for deleted agent token, got nil")
	}
}

func TestDefaultAgentService_TokenCache_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	store, err := agents.NewSQLiteAgentStore(filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	svc := agents.NewAgentService(store, nil)

	ag, err := svc.Create(ctx, "bot")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := svc.ResolveByToken(ctx, ag.Token)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent ResolveByToken error: %v", err)
	}
}
