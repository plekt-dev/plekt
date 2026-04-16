package webhooks

import (
	"context"
	"net/http"
	"time"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/scheduler"
)

// Dispatcher subscribes to scheduler.job.fired events and delivers each
// firing to the configured agent's webhook endpoint. Implementations are
// expected to be safe for concurrent use; Stop must wait for in-flight
// deliveries to finish.
type Dispatcher interface {
	// Start subscribes to the event bus and begins processing firings.
	// Returns an error if the bus is nil or already started.
	Start(ctx context.Context) error
	// Stop unsubscribes and waits for in-flight deliveries to complete.
	// Idempotent.
	Stop()
}

// Config holds the dispatcher's collaborators. All fields except RetryAttempts
// and RetryBackoff are required.
type Config struct {
	// Bus is the event bus to subscribe to scheduler.job.fired on.
	Bus eventbus.EventBus
	// Agents is used to look up agent webhook configuration by name.
	Agents agents.AgentService
	// Bridge is the scheduler PluginBridge used to update job_runs rows
	// (output, dispatch_status, terminal status) on delivery completion.
	Bridge scheduler.PluginBridge
	// HTTPClient is the client used for outbound POSTs. If nil, a client
	// with a 30 second timeout is created.
	HTTPClient *http.Client
	// CallbackBaseURL is the absolute URL prefix that receivers should call
	// back to with run results. The dispatcher appends "/api/runs/{run_id}/result"
	// when building the outbound payload. Trailing slash is tolerated.
	CallbackBaseURL string
	// RetryAttempts is the number of POST attempts before giving up. Default 3.
	RetryAttempts int
	// RetryBackoff is the base backoff between retries. Each retry doubles it
	// (exponential). Default 2s, producing 2s/4s/8s.
	RetryBackoff time.Duration
}

// Default values used when Config fields are zero.
const (
	defaultRetryAttempts = 3
	defaultRetryBackoff  = 2 * time.Second
	defaultHTTPTimeout   = 30 * time.Second
)

// OutboundPayload is the JSON body POSTed to the agent's webhook endpoint.
// All fields are mirrored from scheduler.JobFiredPayload plus a callback URL
// that the receiver uses to deliver results asynchronously.
type OutboundPayload struct {
	JobID       int64     `json:"job_id"`
	RunID       int64     `json:"run_id"`
	JobName     string    `json:"job_name"`
	AgentName   string    `json:"agent_name"`
	Prompt      string    `json:"prompt"`
	TriggeredAt time.Time `json:"triggered_at"`
	Manual      bool      `json:"manual"`
	CallbackURL string    `json:"callback_url"`
}

// CallbackResponse is the JSON shape we accept on the inbound callback path
// (POST /api/runs/{run_id}/result) AND inline in sync mode (the body of a
// 2xx response to the outbound POST). Output is the only required field on
// success; Error short-circuits to a failed run.
type CallbackResponse struct {
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}
