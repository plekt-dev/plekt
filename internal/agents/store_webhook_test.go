package agents_test

import (
	"context"
	"errors"
	"testing"

	"github.com/plekt-dev/plekt/internal/agents"
)

func TestWebhookConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	a, err := store.CreateAgent(ctx, "scheduled-agent", "tok-1")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Fresh agent has empty webhook config and async default mode.
	if a.WebhookURL != "" || a.WebhookSecret != "" {
		t.Fatalf("expected empty webhook config, got url=%q secret=%q", a.WebhookURL, a.WebhookSecret)
	}
	if a.WebhookMode != agents.WebhookModeAsync {
		t.Fatalf("expected default mode %q, got %q", agents.WebhookModeAsync, a.WebhookMode)
	}

	// Configure URL + sync mode.
	if err := store.UpdateWebhookConfig(ctx, a.ID, "http://localhost:8765/", agents.WebhookModeSync); err != nil {
		t.Fatalf("UpdateWebhookConfig: %v", err)
	}
	if err := store.UpdateWebhookSecret(ctx, a.ID, "hex-secret-32"); err != nil {
		t.Fatalf("UpdateWebhookSecret: %v", err)
	}

	got, err := store.GetAgentByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if got.WebhookURL != "http://localhost:8765/" {
		t.Errorf("WebhookURL = %q", got.WebhookURL)
	}
	if got.WebhookMode != agents.WebhookModeSync {
		t.Errorf("WebhookMode = %q", got.WebhookMode)
	}
	if got.WebhookSecret != "hex-secret-32" {
		t.Errorf("WebhookSecret = %q", got.WebhookSecret)
	}

	// Lookup by name returns the same record (used by webhook dispatcher).
	byName, err := store.GetAgentByName(ctx, "scheduled-agent")
	if err != nil {
		t.Fatalf("GetAgentByName: %v", err)
	}
	if byName.ID != a.ID || byName.WebhookSecret != "hex-secret-32" {
		t.Errorf("GetAgentByName mismatch: %+v", byName)
	}

	// Updating just config preserves secret.
	if err := store.UpdateWebhookConfig(ctx, a.ID, "http://relay/", agents.WebhookModeAsync); err != nil {
		t.Fatalf("UpdateWebhookConfig (second): %v", err)
	}
	got2, _ := store.GetAgentByID(ctx, a.ID)
	if got2.WebhookSecret != "hex-secret-32" {
		t.Errorf("secret was wiped by config update: %q", got2.WebhookSecret)
	}
	if got2.WebhookURL != "http://relay/" || got2.WebhookMode != agents.WebhookModeAsync {
		t.Errorf("config not updated: %+v", got2)
	}
}

func TestWebhookConfigRejectsInvalidMode(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	a, _ := store.CreateAgent(ctx, "x", "tok-x")
	err := store.UpdateWebhookConfig(ctx, a.ID, "http://x/", "fire-and-pray")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestWebhookConfigUnknownAgent(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	err := store.UpdateWebhookConfig(ctx, 9999, "http://x/", agents.WebhookModeAsync)
	if !errors.Is(err, agents.ErrAgentNotFound) {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestGetAgentByNameNotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	_, err := store.GetAgentByName(ctx, "nope")
	if !errors.Is(err, agents.ErrAgentNotFound) {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}
