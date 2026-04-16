package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// sqliteBridge is the concrete PluginBridge backed by the scheduler-plugin's
// per-plugin SQLite database. Phase B ships it; Phase C provisions the schema
// via the plugin's schema.yaml.
//
// Table layout (see Phase C schema.yaml):
//
//	jobs(
//	    id INTEGER PRIMARY KEY,
//	    name TEXT UNIQUE NOT NULL,
//	    cron_expr TEXT NOT NULL,
//	    timezone TEXT NOT NULL DEFAULT '',
//	    prompt TEXT NOT NULL DEFAULT '',
//	    agent_id TEXT NOT NULL DEFAULT '',
//	    task_id INTEGER,                    -- nullable
//	    delivery TEXT NOT NULL DEFAULT 'eventbus',
//	    enabled INTEGER NOT NULL DEFAULT 1, -- 0/1
//	    next_fire_at TEXT,                  -- nullable, RFC3339 UTC
//	    last_run_at TEXT,                   -- nullable, RFC3339 UTC
//	    last_run_status TEXT,               -- nullable
//	    last_error TEXT,                    -- nullable
//	    last_duration_ms INTEGER,           -- nullable
//	    created_at TEXT NOT NULL,
//	    updated_at TEXT NOT NULL
//	)
//
//	job_runs(
//	    id INTEGER PRIMARY KEY,
//	    job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
//	    triggered_at TEXT NOT NULL,
//	    manual INTEGER NOT NULL DEFAULT 0,
//	    status TEXT NOT NULL,
//	    error TEXT,
//	    duration_ms INTEGER NOT NULL DEFAULT 0
//	)
//
// All SQL is parameterized. No raw string interpolation.
type sqliteBridge struct {
	db *sql.DB
}

// NewSQLiteBridge constructs a PluginBridge over the given database handle.
// The caller retains ownership of db and is responsible for closing it.
func NewSQLiteBridge(db *sql.DB) PluginBridge {
	return &sqliteBridge{db: db}
}

// ErrBridgeNilDB is returned when NewSQLiteBridge is misused with a nil handle.
var ErrBridgeNilDB = errors.New("scheduler: nil *sql.DB passed to bridge")

// LoadEnabledJobs returns all jobs whose enabled flag is set.
func (b *sqliteBridge) LoadEnabledJobs(ctx context.Context) ([]JobRecord, error) {
	if b.db == nil {
		return nil, ErrBridgeNilDB
	}
	const q = `
SELECT id, name, cron_expr, timezone, prompt, agent_id, task_id, delivery, enabled,
       next_fire_at, last_run_at, last_run_status, last_error, last_duration_ms,
       created_at, updated_at
FROM jobs
WHERE enabled = 1`
	rows, err := b.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("scheduler: query enabled jobs: %w", err)
	}
	defer rows.Close()

	var out []JobRecord
	for rows.Next() {
		rec, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: iterate jobs: %w", err)
	}
	return out, nil
}

// LoadJob fetches a single job by ID regardless of enabled state.
func (b *sqliteBridge) LoadJob(ctx context.Context, jobID int64) (JobRecord, error) {
	if b.db == nil {
		return JobRecord{}, ErrBridgeNilDB
	}
	const q = `
SELECT id, name, cron_expr, timezone, prompt, agent_id, task_id, delivery, enabled,
       next_fire_at, last_run_at, last_run_status, last_error, last_duration_ms,
       created_at, updated_at
FROM jobs
WHERE id = ?`
	row := b.db.QueryRowContext(ctx, q, jobID)
	rec, err := scanJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return JobRecord{}, fmt.Errorf("scheduler: job %d not found", jobID)
		}
		return JobRecord{}, err
	}
	return rec, nil
}

// InsertJobRun persists a new run row and returns its assigned ID.
func (b *sqliteBridge) InsertJobRun(ctx context.Context, rec JobRunRecord) (int64, error) {
	if b.db == nil {
		return 0, ErrBridgeNilDB
	}
	const q = `
INSERT INTO job_runs (job_id, triggered_at, manual, status, error, duration_ms)
VALUES (?, ?, ?, ?, ?, ?)`
	manual := 0
	if rec.Manual {
		manual = 1
	}
	res, err := b.db.ExecContext(ctx, q,
		rec.JobID,
		rec.TriggeredAt,
		manual,
		string(rec.Status),
		nullableString(rec.Error),
		rec.DurationMs,
	)
	if err != nil {
		return 0, fmt.Errorf("scheduler: insert job_run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("scheduler: last insert id: %w", err)
	}
	return id, nil
}

// UpdateJobRun finalizes a run row with its terminal status.
func (b *sqliteBridge) UpdateJobRun(ctx context.Context, runID int64, status RunStatus, errMsg *string, durationMs int64) error {
	if b.db == nil {
		return ErrBridgeNilDB
	}
	const q = `UPDATE job_runs SET status = ?, error = ?, duration_ms = ? WHERE id = ?`
	res, err := b.db.ExecContext(ctx, q, string(status), nullableString(errMsg), durationMs, runID)
	if err != nil {
		return fmt.Errorf("scheduler: update job_run: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("scheduler: run %d not found", runID)
	}
	return nil
}

// UpdateJobLastRun updates the denormalized "last run" columns on the job row
// and also bumps updated_at.
func (b *sqliteBridge) UpdateJobLastRun(ctx context.Context, jobID int64, runAt string, status RunStatus, errMsg *string, durationMs int64) error {
	if b.db == nil {
		return ErrBridgeNilDB
	}
	const q = `
UPDATE jobs
SET last_run_at = ?, last_run_status = ?, last_error = ?, last_duration_ms = ?, updated_at = ?
WHERE id = ?`
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := b.db.ExecContext(ctx, q,
		runAt,
		string(status),
		nullableString(errMsg),
		durationMs,
		now,
		jobID,
	)
	if err != nil {
		return fmt.Errorf("scheduler: update job last_run: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("scheduler: job %d not found", jobID)
	}
	return nil
}

// PromoteRunToActive refreshes triggered_at on an existing job_runs row that
// the plugin pre-inserted as a placeholder for a manual trigger. This is how
// the engine takes ownership of the row instead of inserting a duplicate.
func (b *sqliteBridge) PromoteRunToActive(ctx context.Context, runID int64, triggeredAt string) error {
	if b.db == nil {
		return ErrBridgeNilDB
	}
	const q = `UPDATE job_runs SET triggered_at = ? WHERE id = ?`
	res, err := b.db.ExecContext(ctx, q, triggeredAt, runID)
	if err != nil {
		return fmt.Errorf("scheduler: promote run: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("scheduler: run %d not found", runID)
	}
	return nil
}

// GetJobRun fetches a single run row by ID.
func (b *sqliteBridge) GetJobRun(ctx context.Context, runID int64) (JobRunRecord, error) {
	if b.db == nil {
		return JobRunRecord{}, ErrBridgeNilDB
	}
	const q = `
SELECT id, job_id, triggered_at, manual, status, error, duration_ms, output, dispatch_status
FROM job_runs
WHERE id = ?`
	var (
		rec        JobRunRecord
		manual     int64
		status     string
		errMsg     sql.NullString
		output     sql.NullString
		dispStatus sql.NullString
	)
	err := b.db.QueryRowContext(ctx, q, runID).Scan(
		&rec.ID, &rec.JobID, &rec.TriggeredAt, &manual, &status, &errMsg, &rec.DurationMs, &output, &dispStatus,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return JobRunRecord{}, fmt.Errorf("scheduler: run %d not found", runID)
		}
		return JobRunRecord{}, fmt.Errorf("scheduler: get job_run: %w", err)
	}
	rec.Manual = manual != 0
	rec.Status = RunStatus(status)
	if errMsg.Valid {
		v := errMsg.String
		rec.Error = &v
	}
	if output.Valid {
		v := output.String
		rec.Output = &v
	}
	if dispStatus.Valid && dispStatus.String != "" {
		rec.DispatchStatus = DispatchStatus(dispStatus.String)
	} else {
		rec.DispatchStatus = DispatchStatusPending
	}
	return rec, nil
}

// UpdateJobRunOutput stores the receiver-provided output blob on a run row.
func (b *sqliteBridge) UpdateJobRunOutput(ctx context.Context, runID int64, output string) error {
	if b.db == nil {
		return ErrBridgeNilDB
	}
	const q = `UPDATE job_runs SET output = ? WHERE id = ?`
	res, err := b.db.ExecContext(ctx, q, output, runID)
	if err != nil {
		return fmt.Errorf("scheduler: update job_run output: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("scheduler: run %d not found", runID)
	}
	return nil
}

// UpdateJobRunDispatchStatus advances the dispatch lifecycle on a run row.
func (b *sqliteBridge) UpdateJobRunDispatchStatus(ctx context.Context, runID int64, status DispatchStatus) error {
	if b.db == nil {
		return ErrBridgeNilDB
	}
	const q = `UPDATE job_runs SET dispatch_status = ? WHERE id = ?`
	res, err := b.db.ExecContext(ctx, q, string(status), runID)
	if err != nil {
		return fmt.Errorf("scheduler: update job_run dispatch_status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("scheduler: run %d not found", runID)
	}
	return nil
}

// UpdateJobNextFire sets or clears the next_fire_at column.
func (b *sqliteBridge) UpdateJobNextFire(ctx context.Context, jobID int64, nextFireAt *string) error {
	if b.db == nil {
		return ErrBridgeNilDB
	}
	const q = `UPDATE jobs SET next_fire_at = ?, updated_at = ? WHERE id = ?`
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := b.db.ExecContext(ctx, q, nullableString(nextFireAt), now, jobID)
	if err != nil {
		return fmt.Errorf("scheduler: update next_fire_at: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("scheduler: job %d not found", jobID)
	}
	return nil
}

// rowScanner is the subset of *sql.Row / *sql.Rows we need. Lets scanJob work
// with both the single-row and multi-row paths without duplication.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanJob reads one job row from a rowScanner. Handles all nullable columns.
func scanJob(s rowScanner) (JobRecord, error) {
	var (
		rec            JobRecord
		tz             sql.NullString
		prompt         sql.NullString
		agentID        sql.NullString
		taskID         sql.NullInt64
		delivery       sql.NullString
		enabled        int64
		nextFireAt     sql.NullString
		lastRunAt      sql.NullString
		lastRunStatus  sql.NullString
		lastError      sql.NullString
		lastDurationMs sql.NullInt64
	)
	err := s.Scan(
		&rec.ID,
		&rec.Name,
		&rec.CronExpr,
		&tz,
		&prompt,
		&agentID,
		&taskID,
		&delivery,
		&enabled,
		&nextFireAt,
		&lastRunAt,
		&lastRunStatus,
		&lastError,
		&lastDurationMs,
		&rec.CreatedAt,
		&rec.UpdatedAt,
	)
	if err != nil {
		return JobRecord{}, err
	}
	if tz.Valid {
		rec.Timezone = tz.String
	}
	if prompt.Valid {
		rec.Prompt = prompt.String
	}
	if agentID.Valid {
		rec.AssigneeAgentID = agentID.String
	}
	if taskID.Valid {
		v := taskID.Int64
		rec.TaskID = &v
	}
	if delivery.Valid && delivery.String != "" {
		rec.DeliveryMode = DeliveryMode(delivery.String)
	} else {
		rec.DeliveryMode = DeliveryModeEventBus
	}
	rec.Enabled = enabled != 0
	if nextFireAt.Valid {
		v := nextFireAt.String
		rec.NextFireAt = &v
	}
	if lastRunAt.Valid {
		v := lastRunAt.String
		rec.LastRunAt = &v
	}
	if lastRunStatus.Valid {
		s := RunStatus(lastRunStatus.String)
		rec.LastStatus = &s
	}
	if lastError.Valid {
		v := lastError.String
		rec.LastError = &v
	}
	if lastDurationMs.Valid {
		v := lastDurationMs.Int64
		rec.LastDurationMs = &v
	}
	return rec, nil
}

// nullableString converts a *string to a driver-friendly value that produces
// SQL NULL when the pointer is nil, and the string value otherwise.
func nullableString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
