package scheduler

import "context"

// PluginBridge abstracts every persistence operation the engine needs.
//
// Phase A intentionally ships only the interface: no concrete implementation.
// Phase B will provide a SQLite-backed bridge once we know how the plugin loader
// exposes a *sql.DB to core code. Tests in Phase A use an in-memory mock.
//
// All time strings are RFC3339 in UTC. Pointers carry "absent" semantics:
// nil error means "no error", nil nextFireAt means "no next fire scheduled".
type PluginBridge interface {
	// LoadEnabledJobs returns every enabled job: used by the tick loop.
	LoadEnabledJobs(ctx context.Context) ([]JobRecord, error)

	// LoadJob returns a single job by ID, regardless of enabled state.
	LoadJob(ctx context.Context, jobID int64) (JobRecord, error)

	// InsertJobRun persists a new run row and returns its assigned ID.
	InsertJobRun(ctx context.Context, rec JobRunRecord) (int64, error)

	// UpdateJobRun finalizes an existing run row with terminal status,
	// optional error message, and total duration.
	UpdateJobRun(ctx context.Context, runID int64, status RunStatus, errMsg *string, durationMs int64) error

	// UpdateJobLastRun updates the denormalized "last run" snapshot on the job row.
	UpdateJobLastRun(ctx context.Context, jobID int64, runAt string, status RunStatus, errMsg *string, durationMs int64) error

	// UpdateJobNextFire updates the next scheduled fire timestamp. nil clears it.
	UpdateJobNextFire(ctx context.Context, jobID int64, nextFireAt *string) error

	// PromoteRunToActive marks an existing job_runs row as the active firing.
	// Used by the manual-trigger path: the plugin pre-inserts a placeholder
	// row with status="running" and hands the engine its row id; the engine
	// then takes ownership of that row rather than inserting a duplicate.
	//
	// Semantically: update triggered_at to the real firing time so the run
	// history reflects when the work actually started (not when the operator
	// clicked the button). Status, error, and duration are left to
	// UpdateJobRun on the terminal transition.
	//
	// Returns a wrapped "not found" error when runID does not exist.
	PromoteRunToActive(ctx context.Context, runID int64, triggeredAt string) error

	// GetJobRun fetches a single run row by ID. Used by the inbound webhook
	// callback handler to look up the owning job (and therefore agent) before
	// verifying the HMAC signature on the callback body.
	GetJobRun(ctx context.Context, runID int64) (JobRunRecord, error)

	// UpdateJobRunOutput stores the output payload returned by the webhook
	// receiver. May be called separately from UpdateJobRun (terminal status)
	// because the sync and async paths set output and final status at
	// different points in time.
	UpdateJobRunOutput(ctx context.Context, runID int64, output string) error

	// UpdateJobRunDispatchStatus marks the dispatch lifecycle independently
	// of the terminal RunStatus. Used by the webhook flow to record that the
	// outbound POST succeeded (dispatched) before the receiver has produced
	// any output yet (async mode).
	UpdateJobRunDispatchStatus(ctx context.Context, runID int64, status DispatchStatus) error
}
