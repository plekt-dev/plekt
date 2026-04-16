package mcp

import (
	"sync"
	"testing"
)

func TestSessionStore_CreateGet(t *testing.T) {
	store := NewSessionStore()

	entry := SessionEntry{
		PluginName: "myplugin",
		Scope:      SessionScopePlugin,
	}
	id := store.Create(entry)

	if id == "" {
		t.Fatal("Create returned empty session ID")
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("Get(%q) returned false", id)
	}
	if got.PluginName != entry.PluginName {
		t.Errorf("PluginName = %q, want %q", got.PluginName, entry.PluginName)
	}
	if got.Scope != entry.Scope {
		t.Errorf("Scope = %v, want %v", got.Scope, entry.Scope)
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
}

func TestSessionStore_Delete(t *testing.T) {
	store := NewSessionStore()

	entry := SessionEntry{PluginName: "myplugin", Scope: SessionScopePlugin}
	id := store.Create(entry)

	store.Delete(id)
	_, ok := store.Get(id)
	if ok {
		t.Errorf("Get returned true after Delete for id %q", id)
	}
}

func TestSessionStore_GetNonExistent(t *testing.T) {
	store := NewSessionStore()
	_, ok := store.Get("does-not-exist")
	if ok {
		t.Error("Get returned true for non-existent session")
	}
}

func TestSessionStore_DeleteNonExistent(t *testing.T) {
	store := NewSessionStore()
	// Must not panic.
	store.Delete("ghost-id")
}

func TestSessionStore_UniqueIDs(t *testing.T) {
	store := NewSessionStore()
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := store.Create(SessionEntry{PluginName: "p", Scope: SessionScopePlugin})
		if ids[id] {
			t.Fatalf("duplicate session ID generated: %q", id)
		}
		ids[id] = true
	}
}

func TestSessionStore_Concurrent(t *testing.T) {
	store := NewSessionStore()
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := make([]string, 0, 100)

	// Create 100 sessions concurrently.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := store.Create(SessionEntry{PluginName: "p", Scope: SessionScopePlugin})
			mu.Lock()
			created = append(created, id)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Concurrently get and delete all sessions.
	for _, id := range created {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.Get(id)
			store.Delete(id)
		}()
	}
	wg.Wait()

	// All must be gone.
	mu.Lock()
	defer mu.Unlock()
	for _, id := range created {
		if _, ok := store.Get(id); ok {
			t.Errorf("session %q still exists after Delete", id)
		}
	}
}

func TestSessionStore_FederatedScope(t *testing.T) {
	store := NewSessionStore()
	entry := SessionEntry{PluginName: "", Scope: SessionScopeFederated}
	id := store.Create(entry)

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("Get returned false for federated session")
	}
	if got.Scope != SessionScopeFederated {
		t.Errorf("Scope = %v, want %v", got.Scope, SessionScopeFederated)
	}
}
