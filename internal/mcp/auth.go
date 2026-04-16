package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/eventbus"
)

type contextKey int

const agentContextKey contextKey = iota

// AuthenticatedAgent holds the resolved agent and its permissions,
// stored in the request context after successful authentication.
type AuthenticatedAgent struct {
	Agent       agents.Agent
	Permissions []agents.AgentPermission
}

// AgentFromContext retrieves the authenticated agent from the request context.
// Returns ok=false if no agent is present (unauthenticated request).
func AgentFromContext(ctx context.Context) (AuthenticatedAgent, bool) {
	aa, ok := ctx.Value(agentContextKey).(AuthenticatedAgent)
	return aa, ok
}

// dummyToken is used for constant-time comparison when agent lookup fails,
// preventing timing oracle attacks.
const dummyToken = "0000000000000000000000000000000000000000000000000000000000000000"

// ErrAgentPermissionDenied is returned when an authenticated agent lacks
// permission for the requested plugin or tool.
var ErrAgentPermissionDenied = errors.New("agent permission denied")

// AgentAuthMiddleware authenticates incoming requests using the agents system.
//
// For plugin-scoped endpoints (pluginScope != ""), the resolved agent must also
// have at least one permission entry for that plugin.
//
// On failure the middleware writes a JSON-RPC error response,
// emits EventTokenValidationFailed (if bus != nil), and does NOT call next.
func AgentAuthMiddleware(pluginScope string, svc agents.AgentService, bus eventbus.EventBus, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := extractBearerToken(r)
		if err != nil {
			scopeName := pluginScope
			if scopeName == "" {
				scopeName = "federated"
			}
			emitValidationFailed(r.Context(), bus, scopeName, r.RemoteAddr)
			writeError(w, nil, ErrMissingBearerToken)
			return
		}

		ag, perms, resolveErr := svc.ResolveByToken(r.Context(), tok)

		// Always do constant-time compare to prevent timing oracle.
		expected := dummyToken
		if resolveErr == nil {
			expected = ag.Token
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(expected)) != 1 || resolveErr != nil {
			scopeName := pluginScope
			if scopeName == "" {
				scopeName = "federated"
			}
			emitValidationFailed(r.Context(), bus, scopeName, r.RemoteAddr)
			writeError(w, nil, ErrInvalidBearerToken)
			return
		}

		// For plugin-scoped endpoints, verify agent has at least one permission for this plugin.
		if pluginScope != "" {
			hasAccess := false
			for _, p := range perms {
				if p.PluginName == pluginScope {
					hasAccess = true
					break
				}
			}
			if !hasAccess {
				emitValidationFailed(r.Context(), bus, pluginScope, r.RemoteAddr)
				writeError(w, nil, ErrInvalidBearerToken)
				return
			}
		}

		ctx := context.WithValue(r.Context(), agentContextKey, AuthenticatedAgent{
			Agent:       ag,
			Permissions: perms,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractBearerToken parses the "Authorization: Bearer <token>" header from r.
// Returns ErrMissingBearerToken if the header is absent or malformed.
func extractBearerToken(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", ErrMissingBearerToken
	}
	// Must start with "Bearer " (case-sensitive per RFC 6750).
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return "", ErrMissingBearerToken
	}
	token := strings.TrimSpace(auth[len(prefix):])
	if token == "" {
		return "", ErrMissingBearerToken
	}
	return token, nil
}

// emitValidationFailed emits EventTokenValidationFailed on bus (if non-nil).
// The payload contains only the plugin name, remote address, and timestamp
// never any token value.
func emitValidationFailed(ctx context.Context, bus eventbus.EventBus, pluginName, remoteAddr string) {
	if bus == nil {
		return
	}
	bus.Emit(ctx, eventbus.Event{
		Name:         eventbus.EventTokenValidationFailed,
		SourcePlugin: pluginName,
		Payload: eventbus.TokenValidationFailedPayload{
			PluginName: pluginName,
			RemoteAddr: remoteAddr,
			OccurredAt: time.Now().UTC(),
		},
	})
}
