package web

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/users"
	"github.com/plekt-dev/plekt/internal/web/audit"
	"github.com/plekt-dev/plekt/internal/web/templates"
)

// WebAdminHandler handles admin UI pages.
type WebAdminHandler interface {
	HandleUserProfile(w http.ResponseWriter, r *http.Request)
	HandleSessionRevoke(w http.ResponseWriter, r *http.Request)
	HandleAuditLog(w http.ResponseWriter, r *http.Request)
	HandleUserList(w http.ResponseWriter, r *http.Request)
	HandleUserCreate(w http.ResponseWriter, r *http.Request)
	HandleUserDelete(w http.ResponseWriter, r *http.Request)
	HandleUserChangeRole(w http.ResponseWriter, r *http.Request)
	HandleUserResetPassword(w http.ResponseWriter, r *http.Request)
}

// WebAdminHandlerConfig holds all dependencies for the admin handler.
type WebAdminHandlerConfig struct {
	Sessions WebSessionStore
	AuditLog audit.AuditLogStore
	Bus      eventbus.EventBus // nil allowed
	CSRF     CSRFProvider
	Users    users.UserService // nil disables user management
}

// defaultWebAdminHandler is the production implementation of WebAdminHandler.
type defaultWebAdminHandler struct {
	sessions WebSessionStore
	auditLog audit.AuditLogStore
	bus      eventbus.EventBus
	csrf     CSRFProvider
	users    users.UserService
}

// NewWebAdminHandler constructs a WebAdminHandler. Returns an error if Sessions,
// AuditLog, or CSRF is nil.
func NewWebAdminHandler(cfg WebAdminHandlerConfig) (WebAdminHandler, error) {
	if cfg.Sessions == nil {
		return nil, errors.New("admin handler: Sessions must not be nil")
	}
	if cfg.AuditLog == nil {
		return nil, errors.New("admin handler: AuditLog must not be nil")
	}
	if cfg.CSRF == nil {
		return nil, errors.New("admin handler: CSRF must not be nil")
	}
	return &defaultWebAdminHandler{
		sessions: cfg.Sessions,
		auditLog: cfg.AuditLog,
		bus:      cfg.Bus,
		csrf:     cfg.CSRF,
		users:    cfg.Users,
	}, nil
}

// HandleUserProfile renders the combined user profile + sessions page (GET /user).
func (h *defaultWebAdminHandler) HandleUserProfile(w http.ResponseWriter, r *http.Request) {
	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	csrfToken := h.csrf.TokenForSession(session)
	all := h.sessions.ListAll()

	rows := make([]templates.AdminSessionRowData, 0, len(all))
	for _, s := range all {
		rows = append(rows, templates.AdminSessionRowData{
			ID:         s.ID,
			RemoteAddr: s.RemoteAddr,
			CreatedAt:  s.CreatedAt,
			ExpiresAt:  s.ExpiresAt,
			IsCurrent:  s.ID == session.ID,
		})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].IsCurrent != rows[j].IsCurrent {
			return rows[i].IsCurrent
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})

	data := templates.UserProfilePageData{
		Sessions:  rows,
		CSRFToken: csrfToken,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.UserProfilePage(data).Render(r.Context(), w)
}

// HandleSessionRevoke deletes a session by ID (POST /user/sessions/{id}/revoke).
func (h *defaultWebAdminHandler) HandleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	targetID := r.PathValue("id")
	if targetID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	h.sessions.Delete(targetID)

	h.emitRevoke(r.Context(), targetID, session.ID, r.RemoteAddr)

	if targetID == session.ID {
		clearSessionCookie(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/user", http.StatusSeeOther)
}

// auditCategories defines the event prefix categories available for filtering.
var auditCategories = []templates.AuditCategory{
	{Prefix: "", Label: "audit.cat.all"},
	{Prefix: "auth", Label: "audit.cat.auth"},
	{Prefix: "plugin", Label: "audit.cat.plugins"},
	{Prefix: "mcp", Label: "audit.cat.mcp"},
	{Prefix: "other", Label: "audit.cat.other"},
}

// auditCategoryPrefixes maps a dropdown value to event_name prefixes for SQL LIKE.
var auditPrefixMap = map[string][]string{
	"auth":   {"web.auth.", "auth.token.", "web.user.", "web.admin."},
	"plugin": {"plugin.", "web.plugin."},
	"mcp":    {"mcp."},
	"other":  {"task.", "comment.", "notes.", "project.", "pomodoro.", "dashboard."},
}

func auditCategoryPrefixes(category string) []string {
	return auditPrefixMap[category] // nil for "" (all)
}

// auditStatPrefixGroups returns prefix groups for CountByPrefixes, in order matching auditCategories.
var auditStatPrefixGroups = [][]string{
	nil, // all
	{"web.auth.", "auth.token.", "web.user.", "web.admin."}, // auth
	{"plugin.", "web.plugin."},                              // plugins
	{"mcp."},                                                // mcp
	{"task.", "comment.", "notes.", "project.", "pomodoro.", "dashboard."}, // other
}

var auditStatClasses = []string{"", "badge-auth", "badge-plugin", "badge-mcp", "badge-default"}

// HandleAuditLog renders the audit log page (GET /admin/audit).
func (h *defaultWebAdminHandler) HandleAuditLog(w http.ResponseWriter, r *http.Request) {
	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	csrfToken := h.csrf.TokenForSession(session)

	q := r.URL.Query()
	search := q.Get("q")
	category := q.Get("category")
	limitStr := q.Get("limit")
	offsetStr := q.Get("offset")

	limit := 100
	if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(offsetStr); err == nil && v >= 0 {
		offset = v
	}

	filter := audit.AuditFilter{
		EventPrefixes: auditCategoryPrefixes(category),
		Search:        search,
		Limit:         limit,
		Offset:        offset,
	}

	entries, total, listErr := h.auditLog.ListFiltered(r.Context(), filter)

	var storeErrMsg string
	if listErr != nil {
		storeErrMsg = "Audit log unavailable: " + listErr.Error()
		entries = nil
	}

	entryData := make([]templates.AuditLogEntryData, 0, len(entries))
	for _, e := range entries {
		entryData = append(entryData, templates.AuditLogEntryData{
			EventName:  e.EventName,
			RemoteAddr: e.RemoteAddr,
			SessionID:  e.SessionID,
			PluginName: e.PluginName,
			Detail:     e.Detail,
			OccurredAt: e.OccurredAt,
		})
	}

	// Compute category counts for stats bar.
	var stats []templates.AuditStat
	counts, countErr := h.auditLog.CountByPrefixes(r.Context(), auditStatPrefixGroups)
	if countErr == nil {
		for i, cat := range auditCategories {
			stats = append(stats, templates.AuditStat{
				Label: cat.Label,
				Count: counts[i],
				Class: auditStatClasses[i],
			})
		}
	}

	data := templates.AuditLogPageData{
		Entries:    entryData,
		CSRFToken:  csrfToken,
		StoreError: storeErrMsg,
		Search:     search,
		Category:   category,
		Categories: auditCategories,
		Stats:      stats,
		Total:      total,
		Limit:      limit,
		Offset:     offset,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") != "" {
		_ = templates.AuditLogTable(data).Render(r.Context(), w)
	} else {
		_ = templates.AuditLogPage(data).Render(r.Context(), w)
	}
}

// HandleUserList renders the user management page (GET /admin/users).
func (h *defaultWebAdminHandler) HandleUserList(w http.ResponseWriter, r *http.Request) {
	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	csrfToken := h.csrf.TokenForSession(session)
	var errMsg string
	var rows []templates.AdminUserRowData

	if h.users != nil {
		userList, listErr := h.users.ListUsers(r.Context())
		if listErr != nil {
			errMsg = "Could not load users: " + listErr.Error()
		} else {
			rows = make([]templates.AdminUserRowData, 0, len(userList))
			for _, u := range userList {
				rows = append(rows, templates.AdminUserRowData{
					ID:                 u.ID,
					Username:           u.Username,
					Role:               string(u.Role),
					MustChangePassword: u.MustChangePassword,
					CreatedAt:          u.CreatedAt,
				})
			}
		}
	}

	data := templates.AdminUsersPageData{
		Users:     rows,
		CSRFToken: csrfToken,
		Error:     errMsg,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.AdminUsersPage(data).Render(r.Context(), w)
}

// HandleUserCreate creates a new user (POST /admin/users).
func (h *defaultWebAdminHandler) HandleUserCreate(w http.ResponseWriter, r *http.Request) {
	if h.users == nil {
		http.Error(w, "user management not available", http.StatusServiceUnavailable)
		return
	}

	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = session

	username := r.FormValue("username")
	password := r.FormValue("password")
	roleStr := r.FormValue("role")

	role := users.RoleUser
	if roleStr == "admin" {
		role = users.RoleAdmin
	}

	user, err := h.users.Create(r.Context(), username, password, role, false)
	if err != nil {
		// Re-render with error
		csrfToken := h.csrf.TokenForSession(session)
		userList, _ := h.users.ListUsers(r.Context())
		rows := userListToRows(userList)
		data := templates.AdminUsersPageData{
			Users:     rows,
			CSRFToken: csrfToken,
			Error:     "Failed to create user: " + err.Error(),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = templates.AdminUsersPage(data).Render(r.Context(), w)
		return
	}

	h.emitUserCreated(r.Context(), user, session.ID)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// HandleUserDelete deletes a user (POST /admin/users/{id}/delete).
func (h *defaultWebAdminHandler) HandleUserDelete(w http.ResponseWriter, r *http.Request) {
	if h.users == nil {
		http.Error(w, "user management not available", http.StatusServiceUnavailable)
		return
	}

	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	userID := parseUserID(idStr)
	if userID == 0 {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	// Protect the initial admin (ID=1) from being deleted.
	if userID == 1 {
		http.Error(w, "cannot delete the initial admin", http.StatusForbidden)
		return
	}

	if err := h.users.DeleteUser(r.Context(), userID); err != nil {
		csrfToken := h.csrf.TokenForSession(session)
		userList, _ := h.users.ListUsers(r.Context())
		rows := userListToRows(userList)
		data := templates.AdminUsersPageData{
			Users:     rows,
			CSRFToken: csrfToken,
			Error:     "Failed to delete user: " + err.Error(),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = templates.AdminUsersPage(data).Render(r.Context(), w)
		return
	}

	h.emitUserDeleted(r.Context(), userID, session.ID)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// HandleUserChangeRole changes a user's role (POST /admin/users/{id}/change-role).
func (h *defaultWebAdminHandler) HandleUserChangeRole(w http.ResponseWriter, r *http.Request) {
	if h.users == nil {
		http.Error(w, "user management not available", http.StatusServiceUnavailable)
		return
	}

	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	userID := parseUserID(idStr)
	if userID == 0 {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	// Get old role for event
	existing, _ := h.users.GetUser(r.Context(), userID)
	oldRole := string(existing.Role)

	roleStr := r.FormValue("role")
	newRole := users.RoleUser
	if roleStr == "admin" {
		newRole = users.RoleAdmin
	}

	// Protect the initial admin (ID=1) from being demoted.
	if userID == 1 && newRole == users.RoleUser {
		http.Error(w, "cannot demote the initial admin", http.StatusForbidden)
		return
	}

	if err := h.users.ChangeRole(r.Context(), userID, newRole); err != nil {
		http.Error(w, "failed to change role: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.emitRoleChanged(r.Context(), userID, oldRole, string(newRole), session.ID)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// HandleUserResetPassword resets a user's password (POST /admin/users/{id}/reset-password).
// Requires new_password form value. Sets must_change_password=true so the user
// must change their password on next login.
func (h *defaultWebAdminHandler) HandleUserResetPassword(w http.ResponseWriter, r *http.Request) {
	if h.users == nil {
		http.Error(w, "user management not available", http.StatusServiceUnavailable)
		return
	}

	session, err := SessionFromRequest(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	idStr := r.PathValue("id")
	userID := parseUserID(idStr)
	if userID == 0 {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	newPassword := r.FormValue("new_password")
	if newPassword == "" {
		http.Error(w, "new_password is required", http.StatusBadRequest)
		return
	}

	if err := h.users.AdminSetPassword(r.Context(), userID, newPassword, true, 12); err != nil {
		http.Error(w, "failed to reset password: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.emitPasswordChanged(r.Context(), userID, session.ID)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// emitRevoke publishes an EventAdminSessionRevoked event if a bus is configured.
func (h *defaultWebAdminHandler) emitRevoke(ctx context.Context, revokedID, actorID, remoteAddr string) {
	if h.bus == nil {
		return
	}
	h.bus.Emit(ctx, eventbus.Event{
		Name: eventbus.EventAdminSessionRevoked,
		Payload: eventbus.AdminSessionRevokedPayload{
			RevokedSessionID: revokedID,
			ActorSessionID:   actorID,
			RemoteAddr:       remoteAddr,
			OccurredAt:       time.Now().UTC(),
		},
	})
}

func (h *defaultWebAdminHandler) emitUserCreated(ctx context.Context, u users.User, actorSessionID string) {
	if h.bus == nil {
		return
	}
	h.bus.Emit(ctx, eventbus.Event{
		Name: eventbus.EventUserCreated,
		Payload: eventbus.UserCreatedPayload{
			UserID:    u.ID,
			Username:  u.Username,
			Role:      string(u.Role),
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
	})
}

func (h *defaultWebAdminHandler) emitUserDeleted(ctx context.Context, userID int64, actorSessionID string) {
	if h.bus == nil {
		return
	}
	h.bus.Emit(ctx, eventbus.Event{
		Name: eventbus.EventUserDeleted,
		Payload: eventbus.UserDeletedPayload{
			UserID:         userID,
			DeletedAt:      time.Now().UTC().Format(time.RFC3339),
			ActorSessionID: actorSessionID,
		},
	})
}

func (h *defaultWebAdminHandler) emitRoleChanged(ctx context.Context, userID int64, oldRole, newRole, actorSessionID string) {
	if h.bus == nil {
		return
	}
	h.bus.Emit(ctx, eventbus.Event{
		Name: eventbus.EventUserRoleChanged,
		Payload: eventbus.UserRoleChangedPayload{
			UserID:         userID,
			OldRole:        oldRole,
			NewRole:        newRole,
			ActorSessionID: actorSessionID,
			ChangedAt:      time.Now().UTC().Format(time.RFC3339),
		},
	})
}

func (h *defaultWebAdminHandler) emitPasswordChanged(ctx context.Context, userID int64, actorSessionID string) {
	if h.bus == nil {
		return
	}
	h.bus.Emit(ctx, eventbus.Event{
		Name: eventbus.EventUserPasswordChanged,
		Payload: eventbus.UserPasswordChangedPayload{
			UserID:         userID,
			ActorSessionID: actorSessionID,
			ChangedAt:      time.Now().UTC().Format(time.RFC3339),
		},
	})
}

// userListToRows converts a users.User slice to AdminUserRowData slice.
func userListToRows(userList []users.User) []templates.AdminUserRowData {
	rows := make([]templates.AdminUserRowData, 0, len(userList))
	for _, u := range userList {
		rows = append(rows, templates.AdminUserRowData{
			ID:                 u.ID,
			Username:           u.Username,
			Role:               string(u.Role),
			MustChangePassword: u.MustChangePassword,
			CreatedAt:          u.CreatedAt,
		})
	}
	return rows
}

// parseUserID parses a string user ID from a path value.
func parseUserID(s string) int64 {
	if s == "" {
		return 0
	}
	var id int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		id = id*10 + int64(c-'0')
	}
	return id
}
