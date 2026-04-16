package eventbus

import "time"

// Event name constants.
const (
	EventPluginLoaded    = "plugin.loaded"
	EventPluginUnloaded  = "plugin.unloaded"
	EventPluginUnloading = "plugin.unloading"
	EventPluginError     = "plugin.error"
	EventPluginReloaded  = "plugin.reloaded"
)

// PluginLoadedPayload is emitted after a plugin is successfully loaded.
type PluginLoadedPayload struct {
	Name    string
	Version string
	Dir     string
}

// PluginUnloadedPayload is emitted after a plugin is fully unloaded.
type PluginUnloadedPayload struct {
	Name string
}

// PluginUnloadingPayload is emitted when a plugin begins unloading.
type PluginUnloadingPayload struct {
	Name string
}

// PluginErrorPayload is emitted when a plugin encounters a fatal error.
type PluginErrorPayload struct {
	Name  string
	Error string
}

// PluginReloadedPayload is emitted after a plugin successfully reloads.
type PluginReloadedPayload struct {
	Name    string
	Version string
	Dir     string
}

const (
	EventPluginSchemaMigrated  = "plugin.schema.migrated"
	EventPluginMigrationFailed = "plugin.migration.failed"
)

const EventMCPToolCalled = "mcp.tool_called"

// MCPToolCalledPayload is emitted each time an MCP tool is invoked.
type MCPToolCalledPayload struct {
	ToolName   string `json:"tool_name"`
	PluginName string `json:"plugin_name"`
	DurationMs int64  `json:"duration_ms"`
	IsError    bool   `json:"is_error"`
}

// PluginSchemaMigratedPayload is emitted when schema migration succeeds.
type PluginSchemaMigratedPayload struct {
	PluginName     string
	TablesApplied  int
	IndexesApplied int
}

// PluginMigrationFailedPayload is emitted when schema migration fails.
type PluginMigrationFailedPayload struct {
	PluginName string
	Error      string // sanitized, never raw SQL error
}

// Auth token event name constants.
const (
	EventTokenCreated          = "auth.token.created"
	EventTokenRotated          = "auth.token.rotated"
	EventTokenValidationFailed = "auth.token.validation_failed"
)

// TokenCreatedPayload is emitted when a new bearer token is generated for a plugin.
type TokenCreatedPayload struct {
	PluginName string    `json:"plugin_name"`
	CreatedAt  time.Time `json:"created_at"`
}

// TokenRotatedPayload is emitted when a plugin's bearer token is rotated.
type TokenRotatedPayload struct {
	PluginName string    `json:"plugin_name"`
	RotatedAt  time.Time `json:"rotated_at"`
}

// TokenValidationFailedPayload is emitted when bearer token validation fails on an MCP request.
// No token values are included: only the remote address.
type TokenValidationFailedPayload struct {
	PluginName string    `json:"plugin_name"`
	RemoteAddr string    `json:"remote_addr"`
	OccurredAt time.Time `json:"occurred_at"`
}

// Task event name constants.
const (
	EventTaskCreated   = "task.created"
	EventTaskUpdated   = "task.updated"
	EventTaskDeleted   = "task.deleted"
	EventTaskCompleted = "task.completed"
)

// TaskCreatedPayload is emitted when a new task is created.
type TaskCreatedPayload struct {
	TaskID    int64  `json:"task_id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Priority  int    `json:"priority"`
	CreatedAt string `json:"created_at"`
}

// TaskUpdatedPayload is emitted when a task is updated.
type TaskUpdatedPayload struct {
	TaskID         int64  `json:"task_id"`
	PreviousStatus string `json:"previous_status,omitempty"`
	NewStatus      string `json:"new_status"`
	UpdatedAt      string `json:"updated_at"`
}

// TaskDeletedPayload is emitted when a task is deleted.
type TaskDeletedPayload struct {
	TaskID    int64  `json:"task_id"`
	DeletedAt string `json:"deleted_at"`
}

// TaskCompletedPayload is emitted when a task transitions to "done" status.
type TaskCompletedPayload struct {
	TaskID      int64  `json:"task_id"`
	Title       string `json:"title"`
	CompletedAt string `json:"completed_at"`
}

// Comment event name constants.
const (
	EventCommentCreated = "comment.created"
	EventCommentDeleted = "comment.deleted"
)

// CommentCreatedPayload is emitted when a comment is added to a task.
type CommentCreatedPayload struct {
	CommentID  int64  `json:"comment_id"`
	TaskID     int64  `json:"task_id"`
	AuthorName string `json:"author_name"`
	AuthorType string `json:"author_type"`
	CreatedAt  string `json:"created_at"`
}

// CommentDeletedPayload is emitted when a comment is deleted.
type CommentDeletedPayload struct {
	CommentID int64  `json:"comment_id"`
	TaskID    int64  `json:"task_id"`
	DeletedAt string `json:"deleted_at"`
}

// Web auth event name constants.
const (
	EventWebLoginAttempt = "web.auth.login_attempt"
	EventWebLoginSuccess = "web.auth.login_success"
	EventWebLoginFailed  = "web.auth.login_failed"
	EventWebLogout       = "web.auth.logout"
)

// WebLoginAttemptPayload is emitted on every login attempt, before validation.
type WebLoginAttemptPayload struct {
	RemoteAddr string    `json:"remote_addr"`
	OccurredAt time.Time `json:"occurred_at"`
}

// WebLoginSuccessPayload is emitted when a login attempt succeeds.
type WebLoginSuccessPayload struct {
	RemoteAddr string    `json:"remote_addr"`
	SessionID  string    `json:"session_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

// WebLoginFailedPayload is emitted when a login attempt fails.
// Reason is one of "invalid_credential" or "rate_limited".
type WebLoginFailedPayload struct {
	RemoteAddr string    `json:"remote_addr"`
	Reason     string    `json:"reason"`
	OccurredAt time.Time `json:"occurred_at"`
}

// WebLogoutPayload is emitted when a session is terminated via logout.
type WebLogoutPayload struct {
	RemoteAddr string    `json:"remote_addr"`
	SessionID  string    `json:"session_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

// Web plugin admin event name constants.
const (
	EventWebPluginLoadRequested   = "web.plugin.load_requested"
	EventWebPluginUnloadRequested = "web.plugin.unload_requested"
	EventWebPluginReloadRequested = "web.plugin.reload_requested"
)

// WebPluginLoadRequestedPayload is emitted when a user requests to load a plugin via the web UI.
type WebPluginLoadRequestedPayload struct {
	Dir        string    `json:"dir"`
	RemoteAddr string    `json:"remote_addr"`
	OccurredAt time.Time `json:"occurred_at"`
}

// WebPluginUnloadRequestedPayload is emitted when a user requests to unload a plugin via the web UI.
type WebPluginUnloadRequestedPayload struct {
	PluginName string    `json:"plugin_name"`
	RemoteAddr string    `json:"remote_addr"`
	OccurredAt time.Time `json:"occurred_at"`
}

// WebPluginReloadRequestedPayload is emitted when a user requests to reload a plugin via the web UI.
type WebPluginReloadRequestedPayload struct {
	PluginName string    `json:"plugin_name"`
	RemoteAddr string    `json:"remote_addr"`
	OccurredAt time.Time `json:"occurred_at"`
}

// Plugin permissions event name constants.
const (
	EventPluginPermissionsPresented   = "plugin.permissions.presented"
	EventPluginPermissionsGranted     = "plugin.permissions.granted"
	EventPluginPermissionsDenied      = "plugin.permissions.denied"
	EventPluginPermissionsHostGranted = "plugin.permissions.host_granted"
	EventPluginPermissionsHostRevoked = "plugin.permissions.host_revoked"
)

// PluginPermissionsPresentedPayload is emitted when the install permissions
// modal is shown to the operator for a candidate plugin directory.
type PluginPermissionsPresentedPayload struct {
	PluginName   string   `json:"plugin_name"`
	Dir          string   `json:"dir"`
	Capabilities []string `json:"capabilities"`
}

// PluginPermissionsGrantedPayload is emitted when the operator confirms
// install with a specific set of capabilities and host grants.
type PluginPermissionsGrantedPayload struct {
	PluginName   string   `json:"plugin_name"`
	Capabilities []string `json:"capabilities"`
	GrantedHosts []string `json:"granted_hosts"`
	GrantedBy    string   `json:"granted_by"`
}

// PluginPermissionsDeniedPayload is emitted when the operator dismisses the
// install modal without granting permissions.
type PluginPermissionsDeniedPayload struct {
	PluginName string `json:"plugin_name"`
	Dir        string `json:"dir"`
	DeniedBy   string `json:"denied_by"`
}

// PluginPermissionsHostGrantedPayload is emitted when a single host grant is
// added to the store (either during install or later via Permissions page).
type PluginPermissionsHostGrantedPayload struct {
	PluginName string `json:"plugin_name"`
	Host       string `json:"host"`
	GrantedBy  string `json:"granted_by"`
	Source     string `json:"source"`
}

// PluginPermissionsHostRevokedPayload is emitted when an operator revokes a
// previously granted host.
type PluginPermissionsHostRevokedPayload struct {
	PluginName string `json:"plugin_name"`
	Host       string `json:"host"`
	RevokedBy  string `json:"revoked_by"`
}

// Admin session event name constants.
const EventAdminSessionRevoked = "web.admin.session_revoked"

// AdminSessionRevokedPayload is emitted when an admin revokes a web session.
type AdminSessionRevokedPayload struct {
	RevokedSessionID string    `json:"revoked_session_id"`
	ActorSessionID   string    `json:"actor_session_id"`
	RemoteAddr       string    `json:"remote_addr"`
	OccurredAt       time.Time `json:"occurred_at"`
}

// Admin settings event name constants.
const EventAdminSettingsSaved = "web.admin.settings_saved"

// AdminSettingsSavedPayload is emitted when an admin saves global settings via the web UI.
type AdminSettingsSavedPayload struct {
	ChangedKeys    []string  `json:"changed_keys"`
	ActorSessionID string    `json:"actor_session_id"`
	RemoteAddr     string    `json:"remote_addr"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// Dashboard event name constants.
const (
	EventDashboardWidgetRegistered   = "dashboard.widget.registered"
	EventDashboardWidgetUnregistered = "dashboard.widget.unregistered"
	EventDashboardWidgetFetchError   = "dashboard.widget.fetch_error"
	EventDashboardLayoutSaved        = "dashboard.layout.saved"
)

// DashboardWidgetRegisteredPayload is emitted when widgets are registered for a plugin.
type DashboardWidgetRegisteredPayload struct {
	PluginName string   `json:"plugin_name"`
	WidgetIDs  []string `json:"widget_ids"`
}

// DashboardWidgetUnregisteredPayload is emitted when a plugin's widgets are unregistered.
type DashboardWidgetUnregisteredPayload struct {
	PluginName string `json:"plugin_name"`
}

// DashboardWidgetFetchErrorPayload is emitted when fetching widget data fails.
type DashboardWidgetFetchErrorPayload struct {
	WidgetKey  string `json:"widget_key"`
	PluginName string `json:"plugin_name"`
	Error      string `json:"error"`
}

// DashboardLayoutSavedPayload is emitted when a user saves their dashboard layout.
type DashboardLayoutSavedPayload struct {
	SessionID   string `json:"session_id"`
	WidgetCount int    `json:"widget_count"`
}

// Notes plugin event name constants (notes-plugin v2: unified notes + docs).
const (
	EventNotesCreated = "notes.created"
	EventNotesUpdated = "notes.updated"
	EventNotesDeleted = "notes.deleted"
)

// NotesCreatedPayload is emitted when any entry (note or doc) is created.
type NotesCreatedPayload struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	ProjectID *int64 `json:"project_id,omitempty"`
	Author    string `json:"author"`
}

// NotesUpdatedPayload is emitted when any entry is updated (including promote/demote/move).
type NotesUpdatedPayload struct {
	ID            int64    `json:"id"`
	Kind          string   `json:"kind"`
	ProjectID     *int64   `json:"project_id,omitempty"`
	ChangedFields []string `json:"changed_fields,omitempty"`
}

// NotesDeletedPayload is emitted when any entry is deleted.
type NotesDeletedPayload struct {
	ID           int64  `json:"id"`
	Kind         string `json:"kind"`
	ProjectID    *int64 `json:"project_id,omitempty"`
	CascadeCount int    `json:"cascade_count"`
}

// User management event name constants.
const (
	EventUserCreated         = "web.user.created"
	EventUserDeleted         = "web.user.deleted"
	EventUserRoleChanged     = "web.user.role_changed"
	EventUserPasswordChanged = "web.user.password_changed"
)

// UserCreatedPayload is emitted when a new user is created.
type UserCreatedPayload struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

// UserDeletedPayload is emitted when a user is deleted.
type UserDeletedPayload struct {
	UserID         int64  `json:"user_id"`
	DeletedAt      string `json:"deleted_at"`
	ActorSessionID string `json:"actor_session_id"`
}

// UserRoleChangedPayload is emitted when a user's role is changed.
type UserRoleChangedPayload struct {
	UserID         int64  `json:"user_id"`
	OldRole        string `json:"old_role"`
	NewRole        string `json:"new_role"`
	ActorSessionID string `json:"actor_session_id"`
	ChangedAt      string `json:"changed_at"`
}

// UserPasswordChangedPayload is emitted when a user's password is changed.
type UserPasswordChangedPayload struct {
	UserID         int64  `json:"user_id"`
	ActorSessionID string `json:"actor_session_id"`
	ChangedAt      string `json:"changed_at"`
}

// Plugin discovery and registry event name constants.
const (
	EventPluginDiscovered       = "plugin.discovered"
	EventPluginScanCompleted    = "plugin.scan.completed"
	EventPluginScanFailed       = "plugin.scan.failed"
	EventPluginRegistryRestored = "plugin.registry.restored"
)

// PluginDiscoveredPayload is emitted when a plugin directory is found during a scan.
type PluginDiscoveredPayload struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Dir           string `json:"dir"`
	ManifestValid bool   `json:"manifest_valid"`
}

// PluginScanCompletedPayload is emitted when a plugin directory scan completes.
type PluginScanCompletedPayload struct {
	Total         int `json:"total"`
	Valid         int `json:"valid"`
	AlreadyLoaded int `json:"already_loaded"`
}

// PluginScanFailedPayload is emitted when a plugin directory scan fails.
type PluginScanFailedPayload struct {
	Error string `json:"error"`
}

// PluginRegistryRestoredPayload is emitted after registry restoration completes at startup.
type PluginRegistryRestoredPayload struct {
	Restored int `json:"restored"`
	Failed   int `json:"failed"`
}

// Project event name constants.
const (
	EventProjectCreated  = "project.created"
	EventProjectUpdated  = "project.updated"
	EventProjectArchived = "project.archived"
	EventProjectDeleted  = "project.deleted"
)

// ProjectCreatedPayload is emitted when a new project is created.
type ProjectCreatedPayload struct {
	ProjectID int64  `json:"project_id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Icon      string `json:"icon"`
	CreatedAt string `json:"created_at"`
}

// ProjectUpdatedPayload is emitted when a project is updated.
type ProjectUpdatedPayload struct {
	ProjectID int64  `json:"project_id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Icon      string `json:"icon"`
	UpdatedAt string `json:"updated_at"`
}

// ProjectArchivedPayload is emitted when a project is archived.
type ProjectArchivedPayload struct {
	ProjectID  int64  `json:"project_id"`
	ArchivedAt string `json:"archived_at"`
}

// ProjectDeletedPayload is emitted when a project is deleted.
type ProjectDeletedPayload struct {
	ProjectID int64  `json:"project_id"`
	DeletedAt string `json:"deleted_at"`
}

// Agent event name constants.
const (
	EventAgentCreated            = "agent.created"
	EventAgentDeleted            = "agent.deleted"
	EventAgentTokenRotated       = "agent.token.rotated"
	EventAgentPermissionsChanged = "agent.permissions.changed"
)

// AgentCreatedPayload is emitted when a new agent is created.
type AgentCreatedPayload struct {
	AgentID   int64     `json:"agent_id"`
	AgentName string    `json:"agent_name"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentDeletedPayload is emitted when an agent is deleted.
type AgentDeletedPayload struct {
	AgentID   int64     `json:"agent_id"`
	AgentName string    `json:"agent_name"`
	DeletedAt time.Time `json:"deleted_at"`
}

// AgentTokenRotatedPayload is emitted when an agent's token is rotated.
type AgentTokenRotatedPayload struct {
	AgentID   int64     `json:"agent_id"`
	AgentName string    `json:"agent_name"`
	RotatedAt time.Time `json:"rotated_at"`
}

// AgentPermissionsChangedPayload is emitted when an agent's permissions are updated.
type AgentPermissionsChangedPayload struct {
	AgentID         int64     `json:"agent_id"`
	AgentName       string    `json:"agent_name"`
	PermissionCount int       `json:"permission_count"`
	ChangedAt       time.Time `json:"changed_at"`
}

// Pomodoro event name constants.
const (
	EventPomodoroStarted     = "pomodoro.started"
	EventPomodoroCompleted   = "pomodoro.completed"
	EventPomodoroInterrupted = "pomodoro.interrupted"
)

// PomodoroStartedPayload is emitted when a new pomodoro session begins.
type PomodoroStartedPayload struct {
	SessionID   int64  `json:"session_id"`
	SessionType string `json:"session_type"`
	StartedAt   string `json:"started_at"`
}

// PomodoroCompletedPayload is emitted when a pomodoro session ends without interruption.
type PomodoroCompletedPayload struct {
	SessionID   int64  `json:"session_id"`
	SessionType string `json:"session_type"`
	StartedAt   string `json:"started_at"`
	EndedAt     string `json:"ended_at"`
}

// PomodoroInterruptedPayload is emitted when a pomodoro session is stopped early.
type PomodoroInterruptedPayload struct {
	SessionID   int64  `json:"session_id"`
	SessionType string `json:"session_type"`
	StartedAt   string `json:"started_at"`
	EndedAt     string `json:"ended_at"`
}

// Core auto-updater event name constants.
const (
	EventCoreUpdateAvailable = "core.update.available"
	EventCoreUpdateApplied   = "core.update.applied"
	EventCoreUpdateFailed    = "core.update.failed"
)

// CoreUpdateAvailablePayload is emitted when a newer core version is detected.
type CoreUpdateAvailablePayload struct {
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version"`
	ReleaseNotes   string    `json:"release_notes"`
	ReleasedAt     time.Time `json:"released_at"`
	DetectedAt     time.Time `json:"detected_at"`
}

// CoreUpdateAppliedPayload is emitted when a core update binary has been swapped in.
type CoreUpdateAppliedPayload struct {
	PreviousVersion string    `json:"previous_version"`
	NewVersion      string    `json:"new_version"`
	AppliedAt       time.Time `json:"applied_at"`
}

// CoreUpdateFailedPayload is emitted when an update check or apply fails.
type CoreUpdateFailedPayload struct {
	Operation  string    `json:"operation"` // "check" or "apply"
	Error      string    `json:"error"`
	OccurredAt time.Time `json:"occurred_at"`
}
