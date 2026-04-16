package agents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// AgentService provides high-level agent management operations.
type AgentService interface {
	Create(ctx context.Context, name string) (Agent, error)
	GetByID(ctx context.Context, id int64) (Agent, error)
	GetByName(ctx context.Context, name string) (Agent, error)
	List(ctx context.Context) ([]Agent, error)
	RotateToken(ctx context.Context, id int64) (newToken string, err error)
	UpdateWebhook(ctx context.Context, id int64, webhookURL, mode string) error
	SetWebhookSecret(ctx context.Context, id int64, secret string) error
	HasWebhookSecret(ctx context.Context, id int64) (bool, error)
	Delete(ctx context.Context, id int64) error
	SetPermissions(ctx context.Context, agentID int64, perms []AgentPermission) error
	ListPermissions(ctx context.Context, agentID int64) ([]AgentPermission, error)
	ResolveByToken(ctx context.Context, token string) (Agent, []AgentPermission, error)
}

// WildcardPlugin grants access to all plugins when used as PluginName.
const WildcardPlugin = "*"

// IsToolAllowed reports whether the given plugin/tool pair is permitted by perms.
// It checks for universal wildcard (*/*), plugin wildcard (plugin/*),
// and exact match (plugin/tool).
// Pure function: no side effects.
func IsToolAllowed(perms []AgentPermission, pluginName, toolName string) bool {
	for _, p := range perms {
		// Universal wildcard: */* grants access to everything.
		if p.PluginName == WildcardPlugin && p.ToolName == WildcardTool {
			return true
		}
		if p.PluginName != pluginName {
			continue
		}
		if p.ToolName == toolName || p.ToolName == WildcardTool {
			return true
		}
	}
	return false
}

// tokenCacheEntry holds a cached ResolveByToken result with an expiry time.
type tokenCacheEntry struct {
	agent Agent
	perms []AgentPermission
	exp   time.Time
}

// DefaultAgentService implements AgentService using an AgentStore and optional EventBus.
type DefaultAgentService struct {
	store         AgentStore
	bus           eventbus.EventBus // may be nil
	tokenCacheMu  sync.RWMutex
	tokenCache    map[string]tokenCacheEntry
	tokenCacheTTL time.Duration
}

// NewAgentService constructs a DefaultAgentService.
// bus may be nil; when nil, events are not emitted.
func NewAgentService(store AgentStore, bus eventbus.EventBus) *DefaultAgentService {
	return &DefaultAgentService{
		store:         store,
		bus:           bus,
		tokenCache:    make(map[string]tokenCacheEntry),
		tokenCacheTTL: 30 * time.Second,
	}
}

// Create validates the name, generates a token, persists the agent,
// and emits EventAgentCreated.
func (svc *DefaultAgentService) Create(ctx context.Context, name string) (Agent, error) {
	if name == "" {
		return Agent{}, ErrAgentNameEmpty
	}
	token, err := generateAgentToken()
	if err != nil {
		return Agent{}, fmt.Errorf("generate token: %w", err)
	}
	a, err := svc.store.CreateAgent(ctx, name, token)
	if err != nil {
		return Agent{}, err
	}
	// Grant universal access by default. The operator can restrict later via UI.
	if permErr := svc.store.SetPermissions(ctx, a.ID, []AgentPermission{
		{AgentID: a.ID, PluginName: WildcardPlugin, ToolName: WildcardTool},
	}); permErr != nil {
		slog.Warn("agents: failed to set default permissions for new agent", "agent_id", a.ID, "error", permErr)
	}
	svc.emit(ctx, eventbus.Event{
		Name:         eventbus.EventAgentCreated,
		SourcePlugin: BuiltinPluginName,
		Payload: eventbus.AgentCreatedPayload{
			AgentID:   a.ID,
			AgentName: a.Name,
			CreatedAt: a.CreatedAt,
		},
	})
	return a, nil
}

// GetByID retrieves an agent by its ID.
func (svc *DefaultAgentService) GetByID(ctx context.Context, id int64) (Agent, error) {
	return svc.store.GetAgentByID(ctx, id)
}

// GetByName retrieves an agent by its unique name.
func (svc *DefaultAgentService) GetByName(ctx context.Context, name string) (Agent, error) {
	return svc.store.GetAgentByName(ctx, name)
}

// List returns all agents.
func (svc *DefaultAgentService) List(ctx context.Context) ([]Agent, error) {
	return svc.store.ListAgents(ctx)
}

// UpdateWebhook persists the agent's webhook URL and mode.
func (svc *DefaultAgentService) UpdateWebhook(ctx context.Context, id int64, webhookURL, mode string) error {
	return svc.store.UpdateWebhookConfig(ctx, id, webhookURL, mode)
}

// SetWebhookSecret writes a new HMAC secret. Pass an empty string to clear it.
func (svc *DefaultAgentService) SetWebhookSecret(ctx context.Context, id int64, secret string) error {
	return svc.store.UpdateWebhookSecret(ctx, id, secret)
}

// HasWebhookSecret returns true when the agent has a non-empty HMAC secret
// configured. The plaintext value is never returned to callers.
func (svc *DefaultAgentService) HasWebhookSecret(ctx context.Context, id int64) (bool, error) {
	a, err := svc.store.GetAgentByID(ctx, id)
	if err != nil {
		return false, err
	}
	return a.WebhookSecret != "", nil
}

// RotateToken verifies the agent exists, generates a new token,
// updates the store, and emits EventAgentTokenRotated.
func (svc *DefaultAgentService) RotateToken(ctx context.Context, id int64) (string, error) {
	svc.invalidateTokenCache()
	a, err := svc.store.GetAgentByID(ctx, id)
	if err != nil {
		return "", err
	}
	newToken, err := generateAgentToken()
	if err != nil {
		return "", fmt.Errorf("generate rotation token: %w", err)
	}
	if err := svc.store.UpdateAgentToken(ctx, id, newToken); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	svc.emit(ctx, eventbus.Event{
		Name:         eventbus.EventAgentTokenRotated,
		SourcePlugin: BuiltinPluginName,
		Payload: eventbus.AgentTokenRotatedPayload{
			AgentID:   a.ID,
			AgentName: a.Name,
			RotatedAt: now,
		},
	})
	return newToken, nil
}

// Delete verifies the agent exists, removes it from the store (permissions
// cascade), and emits EventAgentDeleted.
func (svc *DefaultAgentService) Delete(ctx context.Context, id int64) error {
	svc.invalidateTokenCache()
	a, err := svc.store.GetAgentByID(ctx, id)
	if err != nil {
		return err
	}
	if err := svc.store.DeleteAgent(ctx, id); err != nil {
		return err
	}
	now := time.Now().UTC()
	svc.emit(ctx, eventbus.Event{
		Name:         eventbus.EventAgentDeleted,
		SourcePlugin: BuiltinPluginName,
		Payload: eventbus.AgentDeletedPayload{
			AgentID:   a.ID,
			AgentName: a.Name,
			DeletedAt: now,
		},
	})
	return nil
}

// SetPermissions verifies the agent exists, atomically replaces its
// permissions, and emits EventAgentPermissionsChanged.
func (svc *DefaultAgentService) SetPermissions(ctx context.Context, agentID int64, perms []AgentPermission) error {
	a, err := svc.store.GetAgentByID(ctx, agentID)
	if err != nil {
		return err
	}
	if err := svc.store.SetPermissions(ctx, agentID, perms); err != nil {
		return err
	}
	now := time.Now().UTC()
	svc.emit(ctx, eventbus.Event{
		Name:         eventbus.EventAgentPermissionsChanged,
		SourcePlugin: BuiltinPluginName,
		Payload: eventbus.AgentPermissionsChangedPayload{
			AgentID:         a.ID,
			AgentName:       a.Name,
			PermissionCount: len(perms),
			ChangedAt:       now,
		},
	})
	return nil
}

// ListPermissions returns all permissions for the given agentID.
func (svc *DefaultAgentService) ListPermissions(ctx context.Context, agentID int64) ([]AgentPermission, error) {
	return svc.store.ListPermissions(ctx, agentID)
}

// ResolveByToken looks up an agent by token and returns the agent along
// with its permissions. Results are cached for tokenCacheTTL to avoid
// repeated DB hits on every MCP request. The constant-time comparison is
// the caller's responsibility (middleware layer).
func (svc *DefaultAgentService) ResolveByToken(ctx context.Context, token string) (Agent, []AgentPermission, error) {
	now := time.Now()

	// Fast path: check cache under read lock.
	svc.tokenCacheMu.RLock()
	entry, ok := svc.tokenCache[token]
	svc.tokenCacheMu.RUnlock()
	if ok && now.Before(entry.exp) {
		return entry.agent, entry.perms, nil
	}

	// Slow path: query the store.
	a, err := svc.store.GetAgentByToken(ctx, token)
	if err != nil {
		return Agent{}, nil, err
	}
	perms, err := svc.store.ListPermissions(ctx, a.ID)
	if err != nil {
		return Agent{}, nil, err
	}

	// Store result in cache under write lock.
	svc.tokenCacheMu.Lock()
	svc.tokenCache[token] = tokenCacheEntry{
		agent: a,
		perms: perms,
		exp:   now.Add(svc.tokenCacheTTL),
	}
	svc.tokenCacheMu.Unlock()

	return a, perms, nil
}

// invalidateTokenCache replaces the cache map with a new empty one,
// effectively expiring all cached entries. Call this whenever token
// or agent state changes (RotateToken, Delete).
func (svc *DefaultAgentService) invalidateTokenCache() {
	svc.tokenCacheMu.Lock()
	svc.tokenCache = make(map[string]tokenCacheEntry)
	svc.tokenCacheMu.Unlock()
}

// emit publishes an event to the bus if one is configured.
// Safe to call with a nil bus.
func (svc *DefaultAgentService) emit(ctx context.Context, e eventbus.Event) {
	if svc.bus != nil {
		svc.bus.Emit(ctx, e)
	}
}

// generateAgentToken produces 32 cryptographically random bytes encoded as
// 64 lowercase hexadecimal characters.
func generateAgentToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
