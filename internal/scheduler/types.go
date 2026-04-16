// Package scheduler implements the cron-driven job firing engine for the
// scheduler-plugin. Phase A scope: pure core (types, payloads, bridge interface,
// cron validator, engine) with no SQLite, no host functions, no plugin wiring.
//
// All time values are stored in UTC. Cron expressions are interpreted in the
// job's configured IANA timezone (empty string == UTC).
package scheduler

import "time"

// RunStatus represents the lifecycle state of a single job execution.
type RunStatus string

const (
	RunStatusRunning RunStatus = "running"
	RunStatusSuccess RunStatus = "success"
	RunStatusError   RunStatus = "error"
)

// DeliveryMode describes how a fired job is delivered to its consumer.
// Phase A only supports eventbus delivery: future modes (e.g. direct host call)
// can be added without breaking the contract.
type DeliveryMode string

const (
	DeliveryModeEventBus DeliveryMode = "eventbus"
)

// JobRecord is the in-engine representation of a scheduled job. It mirrors the
// row that the SQLite-backed PluginBridge will return in Phase B.
//
// NextFireAt and LastRunAt are stored as RFC3339 UTC strings to keep the
// SQLite layer trivial. Nil pointer means "not yet computed" / "never run".
type JobRecord struct {
	ID              int64
	Name            string
	CronExpr        string
	Timezone        string // IANA name; empty == UTC
	Enabled         bool
	TaskID          *int64 // optional task linkage
	AssigneeAgentID string
	Prompt          string
	DeliveryMode    DeliveryMode
	NextFireAt      *string // RFC3339 UTC
	LastRunAt       *string // RFC3339 UTC
	LastStatus      *RunStatus
	LastError       *string
	LastDurationMs  *int64
	CreatedAt       string
	UpdatedAt       string
}

// DispatchStatus tracks where a run is in the webhook delivery lifecycle,
// independent of the terminal RunStatus. The webhook flow uses both:
// dispatch_status follows pending → dispatched → delivered (or error),
// while status remains "running" until the receiver callback finalizes it.
type DispatchStatus string

const (
	DispatchStatusPending    DispatchStatus = "pending"
	DispatchStatusDispatched DispatchStatus = "dispatched"
	DispatchStatusDelivered  DispatchStatus = "delivered"
	DispatchStatusError      DispatchStatus = "error"
)

// JobRunRecord captures one execution attempt of a job.
type JobRunRecord struct {
	ID             int64
	JobID          int64
	TriggeredAt    string // RFC3339 UTC
	Status         RunStatus
	Error          *string
	DurationMs     int64
	Manual         bool
	Output         *string        // populated by webhook callback
	DispatchStatus DispatchStatus // webhook delivery lifecycle
}

// Config controls Engine behavior. Zero values fall back to sensible defaults
// via NewEngine.
type Config struct {
	// TickInterval is how often the engine polls for due jobs. Default 60s.
	TickInterval time.Duration
	// MaxConcurrentFirings caps the number of in-flight firings. Default 10.
	MaxConcurrentFirings int
	// PluginName is the SourcePlugin field used on emitted EventBus events.
	PluginName string
}

// DefaultTickInterval is used when Config.TickInterval is zero.
const DefaultTickInterval = 60 * time.Second

// DefaultMaxConcurrentFirings is used when Config.MaxConcurrentFirings is zero.
const DefaultMaxConcurrentFirings = 10
