package scheduler

import "time"

// Event name constants emitted/consumed by the scheduler engine.
const (
	EventJobFired         = "scheduler.job.fired"
	EventRunCompleted     = "scheduler.run.completed"
	EventTriggerRequested = "scheduler.trigger.requested"
	EventJobCreated       = "scheduler.job.created"
	EventJobUpdated       = "scheduler.job.updated"
	EventJobDeleted       = "scheduler.job.deleted"
)

// JobFiredPayload is emitted when a job firing begins. Consumers (typically
// the agent runtime) react to this to actually do work.
type JobFiredPayload struct {
	JobID           int64     `json:"job_id"`
	JobName         string    `json:"job_name"`
	TaskID          *int64    `json:"task_id,omitempty"`
	AssigneeAgentID string    `json:"assignee_agent_id"`
	Prompt          string    `json:"prompt"`
	TriggeredAt     time.Time `json:"triggered_at"`
	Manual          bool      `json:"manual"`
	RunID           int64     `json:"run_id"`
}

// JobLifecyclePayload is emitted on job CRUD lifecycle events
// (created/updated/deleted). Action mirrors the event suffix.
type JobLifecyclePayload struct {
	JobID     int64  `json:"job_id"`
	JobName   string `json:"job_name"`
	ChangedBy string `json:"changed_by"`
	Action    string `json:"action"`
}

// JobRunCompletedPayload is emitted after a firing finishes (success or error).
type JobRunCompletedPayload struct {
	RunID       int64     `json:"run_id"`
	JobID       int64     `json:"job_id"`
	JobName     string    `json:"job_name"`
	Status      RunStatus `json:"status"`
	Error       *string   `json:"error,omitempty"`
	DurationMs  int64     `json:"duration_ms"`
	TriggeredAt time.Time `json:"triggered_at"`
	Manual      bool      `json:"manual"`
}

// TriggerRequestedPayload is consumed by the engine; external code (plugin
// trigger_job_now, internal admin UI, etc.) emits it to ask for an immediate
// firing.
//
// RunID is OPTIONAL. When set, the plugin has already pre-allocated a
// job_runs row (status="running") and the engine will promote that same row
// instead of inserting a new one, guaranteeing exactly one run row per
// manual trigger. When nil (or omitted from JSON), the engine inserts a
// fresh run row as before: used by internal callers that don't pre-allocate.
type TriggerRequestedPayload struct {
	JobID int64  `json:"job_id"`
	RunID *int64 `json:"run_id,omitempty"`
}
