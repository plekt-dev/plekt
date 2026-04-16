package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// schemaDDL is the canonical Phase B schema. Phase C will encode the same
// shape in the plugin's schema.yaml. Keep the two in sync.
const schemaDDL = `
CREATE TABLE jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    cron_expr TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    task_id INTEGER,
    delivery TEXT NOT NULL DEFAULT 'eventbus',
    enabled INTEGER NOT NULL DEFAULT 1,
    next_fire_at TEXT,
    last_run_at TEXT,
    last_run_status TEXT,
    last_error TEXT,
    last_duration_ms INTEGER,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE job_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    triggered_at TEXT NOT NULL,
    manual INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    error TEXT,
    duration_ms INTEGER NOT NULL DEFAULT 0
);
`

// openBridgeTestDB opens an isolated in-memory SQLite database and applies the
// schema. The DB closes automatically on test cleanup.
func openBridgeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Unique DSN per test so cache=shared does not cross-pollinate.
	dsn := fmt.Sprintf("file:sched_%s?mode=memory&cache=shared", t.Name())
	// Test names may contain "/" for subtests; normalize.
	dsn = strings.ReplaceAll(dsn, "/", "_")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(context.Background(), schemaDDL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	// Enforce FK cascade for delete semantics.
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("fk pragma: %v", err)
	}
	return db
}

// insertTestJob is a minimal INSERT helper so tests can focus on the bridge
// methods under test, not on setup boilerplate.
func insertTestJob(t *testing.T, db *sql.DB, name string, enabled bool, nextFire *string) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	en := 0
	if enabled {
		en = 1
	}
	res, err := db.Exec(`
INSERT INTO jobs (name, cron_expr, timezone, prompt, agent_id, task_id, delivery, enabled, next_fire_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, "*/5 * * * *", "UTC", "do work", "agent-1", nil, "eventbus", en, nullableString(nextFire), now, now)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestSQLiteBridge_NilDB(t *testing.T) {
	b := NewSQLiteBridge(nil)
	if _, err := b.LoadEnabledJobs(context.Background()); err == nil {
		t.Fatalf("expected error on nil db")
	}
}

func TestSQLiteBridge_LoadEnabledJobs_FiltersDisabled(t *testing.T) {
	db := openBridgeTestDB(t)
	b := NewSQLiteBridge(db)
	now := time.Now().UTC().Format(time.RFC3339)
	insertTestJob(t, db, "on1", true, &now)
	insertTestJob(t, db, "on2", true, &now)
	insertTestJob(t, db, "off", false, &now)

	jobs, err := b.LoadEnabledJobs(context.Background())
	if err != nil {
		t.Fatalf("LoadEnabledJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 enabled jobs, got %d", len(jobs))
	}
	for _, j := range jobs {
		if !j.Enabled {
			t.Errorf("unexpected disabled job in result: %+v", j)
		}
		if j.NextFireAt == nil || *j.NextFireAt == "" {
			t.Errorf("next_fire_at not round-tripped: %+v", j)
		}
		if j.DeliveryMode != DeliveryModeEventBus {
			t.Errorf("delivery mode: got %q want %q", j.DeliveryMode, DeliveryModeEventBus)
		}
	}
}

func TestSQLiteBridge_LoadJob_NotFound(t *testing.T) {
	db := openBridgeTestDB(t)
	b := NewSQLiteBridge(db)
	_, err := b.LoadJob(context.Background(), 999)
	if err == nil {
		t.Fatalf("expected not-found error")
	}
}

func TestSQLiteBridge_LoadJob_RoundTripsNullables(t *testing.T) {
	db := openBridgeTestDB(t)
	b := NewSQLiteBridge(db)
	id := insertTestJob(t, db, "j1", false, nil) // next_fire_at=NULL

	j, err := b.LoadJob(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadJob: %v", err)
	}
	if j.Enabled {
		t.Errorf("want disabled")
	}
	if j.NextFireAt != nil {
		t.Errorf("want nil NextFireAt, got %v", *j.NextFireAt)
	}
	if j.TaskID != nil {
		t.Errorf("want nil TaskID, got %v", *j.TaskID)
	}
	if j.LastStatus != nil || j.LastError != nil || j.LastDurationMs != nil || j.LastRunAt != nil {
		t.Errorf("want nil last_* fields for fresh job, got %+v", j)
	}
}

func TestSQLiteBridge_InsertAndUpdateJobRun(t *testing.T) {
	db := openBridgeTestDB(t)
	b := NewSQLiteBridge(db)
	jobID := insertTestJob(t, db, "j1", true, nil)

	runID, err := b.InsertJobRun(context.Background(), JobRunRecord{
		JobID:       jobID,
		TriggeredAt: time.Now().UTC().Format(time.RFC3339),
		Status:      RunStatusRunning,
		Manual:      true,
	})
	if err != nil {
		t.Fatalf("InsertJobRun: %v", err)
	}
	if runID == 0 {
		t.Fatal("want runID")
	}

	errMsg := "boom"
	if err := b.UpdateJobRun(context.Background(), runID, RunStatusError, &errMsg, 1234); err != nil {
		t.Fatalf("UpdateJobRun: %v", err)
	}

	// Verify row state.
	var (
		status, gotErr sql.NullString
		manual         int
		dur            int64
	)
	err = db.QueryRow(`SELECT status, error, duration_ms, manual FROM job_runs WHERE id = ?`, runID).
		Scan(&status, &gotErr, &dur, &manual)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if status.String != string(RunStatusError) {
		t.Errorf("status = %q", status.String)
	}
	if !gotErr.Valid || gotErr.String != "boom" {
		t.Errorf("error = %v", gotErr)
	}
	if dur != 1234 {
		t.Errorf("duration = %d", dur)
	}
	if manual != 1 {
		t.Errorf("manual flag not persisted")
	}

	// Updating a non-existent run must error.
	if err := b.UpdateJobRun(context.Background(), 99999, RunStatusSuccess, nil, 0); err == nil {
		t.Errorf("expected error on missing run")
	}
}

func TestSQLiteBridge_UpdateJobLastRun(t *testing.T) {
	db := openBridgeTestDB(t)
	b := NewSQLiteBridge(db)
	jobID := insertTestJob(t, db, "j1", true, nil)

	runAt := time.Now().UTC().Format(time.RFC3339)
	if err := b.UpdateJobLastRun(context.Background(), jobID, runAt, RunStatusSuccess, nil, 42); err != nil {
		t.Fatalf("UpdateJobLastRun: %v", err)
	}

	j, err := b.LoadJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("LoadJob: %v", err)
	}
	if j.LastStatus == nil || *j.LastStatus != RunStatusSuccess {
		t.Errorf("last_status not persisted: %+v", j.LastStatus)
	}
	if j.LastDurationMs == nil || *j.LastDurationMs != 42 {
		t.Errorf("last_duration_ms not persisted: %+v", j.LastDurationMs)
	}
	if j.LastError != nil {
		t.Errorf("nil errMsg should persist as NULL, got %v", *j.LastError)
	}
	if j.LastRunAt == nil || *j.LastRunAt != runAt {
		t.Errorf("last_run_at mismatch")
	}

	// Missing job.
	if err := b.UpdateJobLastRun(context.Background(), 9999, runAt, RunStatusError, nil, 0); err == nil {
		t.Errorf("expected error on missing job")
	}
}

func TestSQLiteBridge_UpdateJobNextFire_SetAndClear(t *testing.T) {
	db := openBridgeTestDB(t)
	b := NewSQLiteBridge(db)
	jobID := insertTestJob(t, db, "j1", true, nil)

	next := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if err := b.UpdateJobNextFire(context.Background(), jobID, &next); err != nil {
		t.Fatalf("set next: %v", err)
	}
	j, _ := b.LoadJob(context.Background(), jobID)
	if j.NextFireAt == nil || *j.NextFireAt != next {
		t.Errorf("next_fire_at not set: %+v", j.NextFireAt)
	}

	if err := b.UpdateJobNextFire(context.Background(), jobID, nil); err != nil {
		t.Fatalf("clear next: %v", err)
	}
	j, _ = b.LoadJob(context.Background(), jobID)
	if j.NextFireAt != nil {
		t.Errorf("next_fire_at not cleared: got %v", *j.NextFireAt)
	}

	if err := b.UpdateJobNextFire(context.Background(), 9999, nil); err == nil {
		t.Errorf("expected error on missing job")
	}
}

func TestSQLiteBridge_PromoteRunToActive(t *testing.T) {
	db := openBridgeTestDB(t)
	b := NewSQLiteBridge(db)
	jobID := insertTestJob(t, db, "j1", true, nil)

	// Plugin-style placeholder insert.
	runID, err := b.InsertJobRun(context.Background(), JobRunRecord{
		JobID:       jobID,
		TriggeredAt: "2020-01-01T00:00:00Z",
		Status:      RunStatusRunning,
		Manual:      true,
	})
	if err != nil {
		t.Fatalf("InsertJobRun: %v", err)
	}

	newTriggered := "2026-04-06T12:00:00Z"
	if err := b.PromoteRunToActive(context.Background(), runID, newTriggered); err != nil {
		t.Fatalf("PromoteRunToActive: %v", err)
	}

	var got string
	if err := db.QueryRow("SELECT triggered_at FROM job_runs WHERE id = ?", runID).Scan(&got); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != newTriggered {
		t.Errorf("triggered_at = %q, want %q", got, newTriggered)
	}

	// Missing run must error.
	if err := b.PromoteRunToActive(context.Background(), 9999, newTriggered); err == nil {
		t.Errorf("expected error on missing run")
	}
}

func TestSQLiteBridge_CascadeDeleteRuns(t *testing.T) {
	db := openBridgeTestDB(t)
	b := NewSQLiteBridge(db)
	jobID := insertTestJob(t, db, "j1", true, nil)
	if _, err := b.InsertJobRun(context.Background(), JobRunRecord{
		JobID:       jobID,
		TriggeredAt: time.Now().UTC().Format(time.RFC3339),
		Status:      RunStatusSuccess,
	}); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	if _, err := db.Exec("DELETE FROM jobs WHERE id = ?", jobID); err != nil {
		t.Fatalf("delete job: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM job_runs WHERE job_id = ?", jobID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("cascade delete failed: %d rows remain", n)
	}
}
