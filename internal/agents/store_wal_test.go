package agents_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/plekt-dev/plekt/internal/agents"
)

func TestNewSQLiteAgentStore_WALMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "agents.db")
	store, err := agents.NewSQLiteAgentStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteAgentStore: %v", err)
	}
	defer store.Close()

	// Verify WAL mode is active by opening the same DB and checking pragma
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want \"wal\"", mode)
	}
}

func TestNewSQLiteAgentStore_ConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "agents.db")
	store, err := agents.NewSQLiteAgentStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteAgentStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	// Create one agent
	ag, err := store.CreateAgent(ctx, "testbot", "token123")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Concurrent reads must not block each other
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.GetAgentByID(ctx, ag.ID)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent read error: %v", err)
	}
}
