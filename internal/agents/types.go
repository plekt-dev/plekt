package agents

import (
	"errors"
	"time"
)

var (
	ErrAgentNotFound      = errors.New("agent not found")
	ErrAgentAlreadyExists = errors.New("agent already exists")
	ErrAgentNameEmpty     = errors.New("agent name must not be empty")
)

const BuiltinPluginName = "__builtin"
const WildcardTool = "*"

// WebhookMode controls how the webhook dispatcher treats the receiver's response.
//
//   - WebhookModeAsync: receiver acknowledges the request (2xx, body ignored)
//     and finalizes the run later via POST /api/runs/{run_id}/result.
//   - WebhookModeSync : receiver returns the output inline as JSON in the
//     response body. The dispatcher writes it immediately.
const (
	WebhookModeAsync = "async"
	WebhookModeSync  = "sync"
)

type Agent struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Token         string    `json:"-"`
	WebhookURL    string    `json:"webhook_url,omitempty"`
	WebhookSecret string    `json:"-"` // never exposed in JSON
	WebhookMode   string    `json:"webhook_mode,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type AgentPermission struct {
	AgentID    int64  `json:"agent_id"`
	PluginName string `json:"plugin_name"`
	ToolName   string `json:"tool_name"`
}
