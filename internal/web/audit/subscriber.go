package audit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// AuditLogSubscriber listens to auth and admin events and persists them to
// an AuditLogStore. Errors from Append are swallowed so the bus is never
// blocked. Unknown payload types produce an entry with only EventName set.
type AuditLogSubscriber struct {
	store         AuditLogStore
	bus           eventbus.EventBus
	subscriptions []eventbus.Subscription
}

// NewAuditLogSubscriber wires the subscriber to the bus and returns it.
// The caller must call Close() to unsubscribe.
func NewAuditLogSubscriber(store AuditLogStore, bus eventbus.EventBus) *AuditLogSubscriber {
	s := &AuditLogSubscriber{
		store: store,
		bus:   bus,
	}

	// Subscribe to wildcard "*" to capture every event that flows through the bus.
	// This ensures new event types are automatically audited without code changes.
	sub := bus.Subscribe("*", func(ctx context.Context, e eventbus.Event) {
		s.handle(ctx, e)
	})
	s.subscriptions = append(s.subscriptions, sub)

	return s
}

// Close unsubscribes from all events.
func (s *AuditLogSubscriber) Close() {
	for _, sub := range s.subscriptions {
		s.bus.Unsubscribe(sub)
	}
}

// handle maps the event payload to an AuditLogEntry and appends it.
func (s *AuditLogSubscriber) handle(ctx context.Context, e eventbus.Event) {
	entry := AuditLogEntry{
		EventName: e.Name,
	}

	switch p := e.Payload.(type) {
	// ── Auth ──
	case eventbus.WebLoginAttemptPayload:
		entry.RemoteAddr = p.RemoteAddr
		entry.OccurredAt = p.OccurredAt
	case eventbus.WebLoginSuccessPayload:
		entry.RemoteAddr = p.RemoteAddr
		entry.SessionID = p.SessionID
		entry.OccurredAt = p.OccurredAt
	case eventbus.WebLoginFailedPayload:
		entry.RemoteAddr = p.RemoteAddr
		entry.Detail = p.Reason
		entry.OccurredAt = p.OccurredAt
	case eventbus.WebLogoutPayload:
		entry.RemoteAddr = p.RemoteAddr
		entry.SessionID = p.SessionID
		entry.OccurredAt = p.OccurredAt

	// ── Tokens ──
	case eventbus.TokenCreatedPayload:
		entry.PluginName = p.PluginName
		entry.OccurredAt = p.CreatedAt
	case eventbus.TokenRotatedPayload:
		entry.PluginName = p.PluginName
		entry.OccurredAt = p.RotatedAt
	case eventbus.TokenValidationFailedPayload:
		entry.PluginName = p.PluginName
		entry.RemoteAddr = p.RemoteAddr
		entry.OccurredAt = p.OccurredAt

	// ── Admin ──
	case eventbus.AdminSessionRevokedPayload:
		entry.SessionID = p.RevokedSessionID
		entry.RemoteAddr = p.RemoteAddr
		entry.OccurredAt = p.OccurredAt
	case eventbus.AdminSettingsSavedPayload:
		entry.RemoteAddr = p.RemoteAddr
		entry.SessionID = p.ActorSessionID
		entry.Detail = fmt.Sprintf("changed: %v", p.ChangedKeys)
		entry.OccurredAt = p.OccurredAt

	// ── User management ──
	case eventbus.UserCreatedPayload:
		entry.Detail = fmt.Sprintf("user=%s role=%s", p.Username, p.Role)
		entry.OccurredAt = parseTimeOrNow(p.CreatedAt)
	case eventbus.UserDeletedPayload:
		entry.SessionID = p.ActorSessionID
		entry.Detail = fmt.Sprintf("user_id=%d", p.UserID)
		entry.OccurredAt = parseTimeOrNow(p.DeletedAt)
	case eventbus.UserRoleChangedPayload:
		entry.SessionID = p.ActorSessionID
		entry.Detail = fmt.Sprintf("user_id=%d %s→%s", p.UserID, p.OldRole, p.NewRole)
		entry.OccurredAt = parseTimeOrNow(p.ChangedAt)
	case eventbus.UserPasswordChangedPayload:
		entry.SessionID = p.ActorSessionID
		entry.Detail = fmt.Sprintf("user_id=%d", p.UserID)
		entry.OccurredAt = parseTimeOrNow(p.ChangedAt)

	// ── Plugin admin (web) ──
	case eventbus.WebPluginLoadRequestedPayload:
		entry.RemoteAddr = p.RemoteAddr
		entry.Detail = p.Dir
		entry.OccurredAt = p.OccurredAt
	case eventbus.WebPluginUnloadRequestedPayload:
		entry.PluginName = p.PluginName
		entry.RemoteAddr = p.RemoteAddr
		entry.OccurredAt = p.OccurredAt
	case eventbus.WebPluginReloadRequestedPayload:
		entry.PluginName = p.PluginName
		entry.RemoteAddr = p.RemoteAddr
		entry.OccurredAt = p.OccurredAt

	// ── Plugin lifecycle ──
	case eventbus.PluginLoadedPayload:
		entry.PluginName = p.Name
		entry.Detail = fmt.Sprintf("v%s dir=%s", p.Version, p.Dir)
	case eventbus.PluginUnloadedPayload:
		entry.PluginName = p.Name
	case eventbus.PluginUnloadingPayload:
		entry.PluginName = p.Name
	case eventbus.PluginErrorPayload:
		entry.PluginName = p.Name
		entry.Detail = p.Error
	case eventbus.PluginReloadedPayload:
		entry.PluginName = p.Name
		entry.Detail = fmt.Sprintf("v%s dir=%s", p.Version, p.Dir)
	case eventbus.PluginSchemaMigratedPayload:
		entry.PluginName = p.PluginName
		entry.Detail = fmt.Sprintf("tables=%d indexes=%d", p.TablesApplied, p.IndexesApplied)
	case eventbus.PluginMigrationFailedPayload:
		entry.PluginName = p.PluginName
		entry.Detail = p.Error
	case eventbus.PluginDiscoveredPayload:
		entry.PluginName = p.Name
		entry.Detail = fmt.Sprintf("v%s valid=%v", p.Version, p.ManifestValid)
	case eventbus.PluginScanCompletedPayload:
		entry.Detail = fmt.Sprintf("total=%d valid=%d loaded=%d", p.Total, p.Valid, p.AlreadyLoaded)
	case eventbus.PluginScanFailedPayload:
		entry.Detail = p.Error
	case eventbus.PluginRegistryRestoredPayload:
		entry.Detail = fmt.Sprintf("restored=%d failed=%d", p.Restored, p.Failed)

	// ── MCP ──
	case eventbus.MCPToolCalledPayload:
		entry.PluginName = p.PluginName
		entry.Detail = fmt.Sprintf("tool=%s %dms err=%v", p.ToolName, p.DurationMs, p.IsError)

	// ── Tasks ──
	case eventbus.TaskCreatedPayload:
		entry.Detail = fmt.Sprintf("id=%d %s", p.TaskID, p.Title)
		entry.OccurredAt = parseTimeOrNow(p.CreatedAt)
	case eventbus.TaskUpdatedPayload:
		entry.Detail = fmt.Sprintf("id=%d %s→%s", p.TaskID, p.PreviousStatus, p.NewStatus)
		entry.OccurredAt = parseTimeOrNow(p.UpdatedAt)
	case eventbus.TaskDeletedPayload:
		entry.Detail = fmt.Sprintf("id=%d", p.TaskID)
		entry.OccurredAt = parseTimeOrNow(p.DeletedAt)
	case eventbus.TaskCompletedPayload:
		entry.Detail = fmt.Sprintf("id=%d %s", p.TaskID, p.Title)
		entry.OccurredAt = parseTimeOrNow(p.CompletedAt)

	// ── Comments ──
	case eventbus.CommentCreatedPayload:
		entry.Detail = fmt.Sprintf("comment=%d task=%d by %s(%s)", p.CommentID, p.TaskID, p.AuthorName, p.AuthorType)
		entry.OccurredAt = parseTimeOrNow(p.CreatedAt)
	case eventbus.CommentDeletedPayload:
		entry.Detail = fmt.Sprintf("comment=%d task=%d", p.CommentID, p.TaskID)
		entry.OccurredAt = parseTimeOrNow(p.DeletedAt)

	// ── Notes ──
	case eventbus.NotesCreatedPayload:
		entry.Detail = fmt.Sprintf("id=%d kind=%s author=%s", p.ID, p.Kind, p.Author)
	case eventbus.NotesUpdatedPayload:
		entry.Detail = fmt.Sprintf("id=%d kind=%s fields=%v", p.ID, p.Kind, p.ChangedFields)
	case eventbus.NotesDeletedPayload:
		entry.Detail = fmt.Sprintf("id=%d kind=%s cascade=%d", p.ID, p.Kind, p.CascadeCount)

	// ── Projects ──
	case eventbus.ProjectCreatedPayload:
		entry.Detail = fmt.Sprintf("id=%d %s", p.ProjectID, p.Name)
		entry.OccurredAt = parseTimeOrNow(p.CreatedAt)
	case eventbus.ProjectUpdatedPayload:
		entry.Detail = fmt.Sprintf("id=%d %s", p.ProjectID, p.Name)
		entry.OccurredAt = parseTimeOrNow(p.UpdatedAt)
	case eventbus.ProjectArchivedPayload:
		entry.Detail = fmt.Sprintf("id=%d", p.ProjectID)
		entry.OccurredAt = parseTimeOrNow(p.ArchivedAt)
	case eventbus.ProjectDeletedPayload:
		entry.Detail = fmt.Sprintf("id=%d", p.ProjectID)
		entry.OccurredAt = parseTimeOrNow(p.DeletedAt)

	// ── Pomodoro ──
	case eventbus.PomodoroStartedPayload:
		entry.Detail = fmt.Sprintf("session=%d type=%s", p.SessionID, p.SessionType)
		entry.OccurredAt = parseTimeOrNow(p.StartedAt)
	case eventbus.PomodoroCompletedPayload:
		entry.Detail = fmt.Sprintf("session=%d type=%s", p.SessionID, p.SessionType)
		entry.OccurredAt = parseTimeOrNow(p.EndedAt)
	case eventbus.PomodoroInterruptedPayload:
		entry.Detail = fmt.Sprintf("session=%d type=%s", p.SessionID, p.SessionType)
		entry.OccurredAt = parseTimeOrNow(p.EndedAt)

	// ── Dashboard ──
	case eventbus.DashboardWidgetRegisteredPayload:
		entry.PluginName = p.PluginName
		entry.Detail = fmt.Sprintf("widgets=%v", p.WidgetIDs)
	case eventbus.DashboardWidgetUnregisteredPayload:
		entry.PluginName = p.PluginName
	case eventbus.DashboardWidgetFetchErrorPayload:
		entry.PluginName = p.PluginName
		entry.Detail = fmt.Sprintf("widget=%s err=%s", p.WidgetKey, p.Error)
	case eventbus.DashboardLayoutSavedPayload:
		entry.SessionID = p.SessionID
		entry.Detail = fmt.Sprintf("widgets=%d", p.WidgetCount)

	default:
		// Unknown payload type: append entry with only EventName set, no panic.
	}

	// Fill OccurredAt if not set by the case handler.
	if entry.OccurredAt.IsZero() {
		entry.OccurredAt = time.Now().UTC()
	}

	// The bus must not block, but audit loss should be visible.
	if err := s.store.Append(ctx, entry); err != nil {
		slog.Warn("audit: failed to append entry", "event", entry.EventName, "error", err)
	}
}

// parseTimeOrNow parses an RFC 3339 timestamp string, returning time.Now().UTC()
// on failure (some payloads use string timestamps instead of time.Time).
func parseTimeOrNow(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}
