package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// mockBridge is a thread-safe in-memory PluginBridge for tests. Phase A does
// not touch SQLite: Phase B will introduce a real bridge backed by *sql.DB.
type mockBridge struct {
	mu sync.Mutex

	jobs    map[int64]JobRecord
	runs    map[int64]JobRunRecord
	nextRun int64

	insertCalls  int32
	updateRun    int32
	updateLast   int32
	updateNext   int32
	loadEnabled  int32
	promoteCalls int32
	insertErr    error
	insertErrFor map[int64]bool // job IDs whose first InsertJobRun should fail

	// Slow down InsertJobRun for stop-wait tests.
	insertDelay time.Duration
}

func newMockBridge() *mockBridge {
	return &mockBridge{
		jobs:         make(map[int64]JobRecord),
		runs:         make(map[int64]JobRunRecord),
		insertErrFor: make(map[int64]bool),
	}
}

func (m *mockBridge) addJob(j JobRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = j
}

func (m *mockBridge) getJob(id int64) (JobRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

func (m *mockBridge) LoadEnabledJobs(ctx context.Context) ([]JobRecord, error) {
	atomic.AddInt32(&m.loadEnabled, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]JobRecord, 0, len(m.jobs))
	for _, j := range m.jobs {
		if j.Enabled {
			out = append(out, j)
		}
	}
	return out, nil
}

func (m *mockBridge) LoadJob(ctx context.Context, jobID int64) (JobRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return JobRecord{}, fmt.Errorf("job %d not found", jobID)
	}
	return j, nil
}

func (m *mockBridge) InsertJobRun(ctx context.Context, rec JobRunRecord) (int64, error) {
	atomic.AddInt32(&m.insertCalls, 1)
	if m.insertDelay > 0 {
		time.Sleep(m.insertDelay)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.insertErrFor[rec.JobID] {
		delete(m.insertErrFor, rec.JobID)
		return 0, errors.New("simulated insert failure")
	}
	if m.insertErr != nil {
		return 0, m.insertErr
	}
	m.nextRun++
	rec.ID = m.nextRun
	m.runs[rec.ID] = rec
	return rec.ID, nil
}

func (m *mockBridge) UpdateJobRun(ctx context.Context, runID int64, status RunStatus, errMsg *string, durationMs int64) error {
	atomic.AddInt32(&m.updateRun, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("run %d not found", runID)
	}
	r.Status = status
	r.Error = errMsg
	r.DurationMs = durationMs
	m.runs[runID] = r
	return nil
}

func (m *mockBridge) UpdateJobLastRun(ctx context.Context, jobID int64, runAt string, status RunStatus, errMsg *string, durationMs int64) error {
	atomic.AddInt32(&m.updateLast, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("job %d not found", jobID)
	}
	j.LastRunAt = &runAt
	s := status
	j.LastStatus = &s
	j.LastError = errMsg
	d := durationMs
	j.LastDurationMs = &d
	m.jobs[jobID] = j
	return nil
}

func (m *mockBridge) UpdateJobNextFire(ctx context.Context, jobID int64, nextFireAt *string) error {
	atomic.AddInt32(&m.updateNext, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("job %d not found", jobID)
	}
	j.NextFireAt = nextFireAt
	m.jobs[jobID] = j
	return nil
}

func (m *mockBridge) GetJobRun(ctx context.Context, runID int64) (JobRunRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok {
		return JobRunRecord{}, fmt.Errorf("run %d not found", runID)
	}
	return r, nil
}

func (m *mockBridge) UpdateJobRunOutput(ctx context.Context, runID int64, output string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("run %d not found", runID)
	}
	v := output
	r.Output = &v
	m.runs[runID] = r
	return nil
}

func (m *mockBridge) UpdateJobRunDispatchStatus(ctx context.Context, runID int64, status DispatchStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("run %d not found", runID)
	}
	r.DispatchStatus = status
	m.runs[runID] = r
	return nil
}

func (m *mockBridge) PromoteRunToActive(ctx context.Context, runID int64, triggeredAt string) error {
	atomic.AddInt32(&m.promoteCalls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("run %d not found", runID)
	}
	r.TriggeredAt = triggeredAt
	m.runs[runID] = r
	return nil
}

// firedCounter wraps the bus to count EventJobFired emissions for assertions.
type firedCounter struct {
	count atomic.Int32
	last  atomic.Value // JobFiredPayload
}

func (f *firedCounter) handler(ctx context.Context, ev eventbus.Event) {
	f.count.Add(1)
	if p, ok := ev.Payload.(JobFiredPayload); ok {
		f.last.Store(p)
	}
}

func newTestEngine(t *testing.T, bridge PluginBridge, tick time.Duration, maxConcurrent int) (Engine, eventbus.EventBus, *firedCounter) {
	t.Helper()
	bus := eventbus.NewInMemoryBus()
	fc := &firedCounter{}
	bus.Subscribe(EventJobFired, fc.handler)
	eng := NewEngine(Config{
		TickInterval:         tick,
		MaxConcurrentFirings: maxConcurrent,
		PluginName:           "scheduler-plugin",
	}, bridge, bus, NewCronValidator(), nil)
	return eng, bus, fc
}

// waitFor polls cond up to timeout. Used instead of fixed sleeps.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waitFor timeout: %s", msg)
}

func nowMinusFmt(d time.Duration) *string {
	s := time.Now().UTC().Add(-d).Format(time.RFC3339)
	return &s
}

func TestEngine_FiresJobAtScheduledTime(t *testing.T) {
	bridge := newMockBridge()
	bridge.addJob(JobRecord{
		ID: 1, Name: "daily", CronExpr: "0 9 * * *", Enabled: true,
		AssigneeAgentID: "agent-1", Prompt: "do work",
		NextFireAt: nowMinusFmt(time.Second),
	})
	eng, bus, fc := newTestEngine(t, bridge, 50*time.Millisecond, 5)
	defer bus.Close()

	ctx := context.Background()
	eng.Start(ctx)
	defer eng.Stop()

	waitFor(t, 2*time.Second, func() bool { return fc.count.Load() >= 1 }, "expected at least 1 fire")

	if atomic.LoadInt32(&bridge.insertCalls) < 1 {
		t.Errorf("expected InsertJobRun called, got %d", bridge.insertCalls)
	}
	if atomic.LoadInt32(&bridge.updateNext) < 1 {
		t.Errorf("expected UpdateJobNextFire called, got %d", bridge.updateNext)
	}
	last, _ := fc.last.Load().(JobFiredPayload)
	if last.Manual {
		t.Errorf("expected manual=false")
	}
	if last.RunID == 0 {
		t.Errorf("expected RunID set")
	}
}

func TestEngine_SkipsDisabledJobs(t *testing.T) {
	bridge := newMockBridge()
	bridge.addJob(JobRecord{
		ID: 1, Name: "off", CronExpr: "* * * * *", Enabled: false,
		NextFireAt: nowMinusFmt(time.Hour),
	})
	eng, bus, fc := newTestEngine(t, bridge, 30*time.Millisecond, 5)
	defer bus.Close()
	eng.Start(context.Background())
	defer eng.Stop()

	time.Sleep(200 * time.Millisecond)
	if fc.count.Load() != 0 {
		t.Errorf("disabled job fired %d times", fc.count.Load())
	}
}

func TestEngine_TriggerNow_IgnoresEnabled(t *testing.T) {
	bridge := newMockBridge()
	bridge.addJob(JobRecord{
		ID: 7, Name: "manual", CronExpr: "0 9 * * *", Enabled: false,
		AssigneeAgentID: "agent-x", Prompt: "manual prompt",
	})
	eng, bus, fc := newTestEngine(t, bridge, time.Hour, 5)
	defer bus.Close()
	eng.Start(context.Background())
	defer eng.Stop()

	runID, err := eng.TriggerNow(context.Background(), 7)
	if err != nil {
		t.Fatalf("TriggerNow: %v", err)
	}
	if runID == 0 {
		t.Fatal("expected runID")
	}
	waitFor(t, time.Second, func() bool { return fc.count.Load() == 1 }, "expected 1 fire")
	last, _ := fc.last.Load().(JobFiredPayload)
	if !last.Manual {
		t.Errorf("expected manual=true")
	}
}

func TestEngine_TriggerNow_ViaEventBus(t *testing.T) {
	bridge := newMockBridge()
	bridge.addJob(JobRecord{
		ID: 42, Name: "via-bus", CronExpr: "0 9 * * *", Enabled: false,
		AssigneeAgentID: "agent-x", Prompt: "p",
	})
	eng, bus, fc := newTestEngine(t, bridge, time.Hour, 5)
	defer bus.Close()
	eng.Start(context.Background())
	defer eng.Stop()

	bus.Emit(context.Background(), eventbus.Event{
		Name:    EventTriggerRequested,
		Payload: TriggerRequestedPayload{JobID: 42},
	})
	waitFor(t, 2*time.Second, func() bool { return fc.count.Load() >= 1 }, "expected fire from bus trigger")
	last, _ := fc.last.Load().(JobFiredPayload)
	if !last.Manual {
		t.Errorf("expected manual=true for bus-triggered fire")
	}
}

func TestEngine_ConcurrentJobsDoNotDeadlock(t *testing.T) {
	bridge := newMockBridge()
	for i := int64(1); i <= 20; i++ {
		bridge.addJob(JobRecord{
			ID: i, Name: fmt.Sprintf("j%d", i), CronExpr: "*/5 * * * *", Enabled: true,
			AssigneeAgentID: "agent", Prompt: "p",
			NextFireAt: nowMinusFmt(time.Second),
		})
	}
	tick := 50 * time.Millisecond
	eng, bus, fc := newTestEngine(t, bridge, tick, 5)
	defer bus.Close()

	eng.Start(context.Background())
	defer eng.Stop()

	waitFor(t, 5*tick+2*time.Second, func() bool { return fc.count.Load() >= 20 }, "expected all 20 jobs to fire")
}

func TestEngine_StopWaitsForInflight(t *testing.T) {
	bridge := newMockBridge()
	bridge.insertDelay = 200 * time.Millisecond
	bridge.addJob(JobRecord{
		ID: 1, Name: "slow", CronExpr: "* * * * *", Enabled: true,
		NextFireAt: nowMinusFmt(time.Second),
	})
	eng, bus, fc := newTestEngine(t, bridge, 20*time.Millisecond, 5)
	defer bus.Close()
	eng.Start(context.Background())

	// Wait until at least one full firing cycle completes (event emitted).
	// Using fc.count instead of bridge.insertCalls avoids the race where
	// Stop() is called while InsertJobRun is still in-flight.
	waitFor(t, 2*time.Second, func() bool { return fc.count.Load() >= 1 }, "fire emitted")

	stopDone := make(chan struct{})
	go func() {
		eng.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return")
	}
}

func TestEngine_FiringErrorDoesNotStopTicker(t *testing.T) {
	bridge := newMockBridge()
	bridge.insertErrFor[1] = true // first insert for job 1 fails
	bridge.addJob(JobRecord{
		ID: 1, Name: "flaky", CronExpr: "*/5 * * * *", Enabled: true,
		NextFireAt: nowMinusFmt(time.Second),
	})
	eng, bus, fc := newTestEngine(t, bridge, 30*time.Millisecond, 5)
	defer bus.Close()
	eng.Start(context.Background())
	defer eng.Stop()

	// First tick should fail to insert. Subsequent ticks should succeed and fire.
	waitFor(t, 2*time.Second, func() bool { return fc.count.Load() >= 1 }, "engine should recover and fire")
	if atomic.LoadInt32(&bridge.loadEnabled) < 2 {
		t.Errorf("expected ticker to keep ticking, loadEnabled=%d", bridge.loadEnabled)
	}
}

// TestEngine_TriggerNow_PromotesExistingRun exercises the Fix-2 path: the
// caller has pre-inserted a run row (as the scheduler-plugin does in
// trigger_job_now) and hands the engine its id. The engine must NOT insert a
// second row: promoteCalls must go up, insertCalls must stay flat.
func TestEngine_TriggerNow_PromotesExistingRun(t *testing.T) {
	bridge := newMockBridge()
	bridge.addJob(JobRecord{
		ID: 42, Name: "promote", CronExpr: "0 9 * * *", Enabled: false,
		AssigneeAgentID: "agent-x", Prompt: "p",
	})
	// Pre-insert the placeholder run row the way the plugin would.
	preRunID, err := bridge.InsertJobRun(context.Background(), JobRunRecord{
		JobID:       42,
		TriggeredAt: "2020-01-01T00:00:00Z", // sentinel old time
		Status:      RunStatusRunning,
		Manual:      true,
	})
	if err != nil {
		t.Fatalf("pre-insert: %v", err)
	}
	insertsBefore := atomic.LoadInt32(&bridge.insertCalls)

	eng, bus, fc := newTestEngine(t, bridge, time.Hour, 5)
	defer bus.Close()
	eng.Start(context.Background())
	defer eng.Stop()

	if err := eng.TriggerNowWithRun(context.Background(), 42, preRunID); err != nil {
		t.Fatalf("TriggerNowWithRun: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return fc.count.Load() >= 1 }, "fire")

	// Exactly one fire, no additional InsertJobRun calls, exactly one promotion.
	if atomic.LoadInt32(&bridge.insertCalls) != insertsBefore {
		t.Errorf("engine inserted a new run row: before=%d after=%d",
			insertsBefore, atomic.LoadInt32(&bridge.insertCalls))
	}
	if got := atomic.LoadInt32(&bridge.promoteCalls); got != 1 {
		t.Errorf("want 1 promote call, got %d", got)
	}

	// The pre-inserted row should have its triggered_at refreshed (no longer 2020).
	bridge.mu.Lock()
	r := bridge.runs[preRunID]
	bridge.mu.Unlock()
	if r.TriggeredAt == "2020-01-01T00:00:00Z" {
		t.Errorf("triggered_at was not refreshed on promotion")
	}

	// The fired event must carry the pre-allocated run id.
	last, _ := fc.last.Load().(JobFiredPayload)
	if last.RunID != preRunID {
		t.Errorf("JobFiredPayload.RunID = %d, want %d", last.RunID, preRunID)
	}
	if !last.Manual {
		t.Errorf("expected manual=true")
	}
}

// TestEngine_TriggerNow_ViaEventBus_WithRunID checks that when a bus trigger
// arrives with run_id set (the plugin path), the engine goes through the
// promote-existing path end-to-end.
func TestEngine_TriggerNow_ViaEventBus_WithRunID(t *testing.T) {
	bridge := newMockBridge()
	bridge.addJob(JobRecord{
		ID: 7, Name: "bus-with-run", CronExpr: "0 9 * * *", Enabled: false,
		AssigneeAgentID: "agent", Prompt: "p",
	})
	preRunID, err := bridge.InsertJobRun(context.Background(), JobRunRecord{
		JobID: 7, TriggeredAt: "2020-01-01T00:00:00Z", Status: RunStatusRunning, Manual: true,
	})
	if err != nil {
		t.Fatalf("pre-insert: %v", err)
	}
	insertsBefore := atomic.LoadInt32(&bridge.insertCalls)

	eng, bus, fc := newTestEngine(t, bridge, time.Hour, 5)
	defer bus.Close()
	eng.Start(context.Background())
	defer eng.Stop()

	bus.Emit(context.Background(), eventbus.Event{
		Name:    EventTriggerRequested,
		Payload: TriggerRequestedPayload{JobID: 7, RunID: &preRunID},
	})
	waitFor(t, 2*time.Second, func() bool { return fc.count.Load() >= 1 }, "fire via bus")

	if atomic.LoadInt32(&bridge.insertCalls) != insertsBefore {
		t.Errorf("engine inserted a new run row via bus path: before=%d after=%d",
			insertsBefore, atomic.LoadInt32(&bridge.insertCalls))
	}
	if got := atomic.LoadInt32(&bridge.promoteCalls); got != 1 {
		t.Errorf("want 1 promote via bus, got %d", got)
	}
}

// TestExtractTriggerPayload_MapWithRunID covers the JSON-decoded path that
// plugin code exercises: mc_event::emit turns a struct into map[string]any
// where numeric fields are float64.
func TestExtractTriggerPayload_MapWithRunID(t *testing.T) {
	payload := map[string]any{
		"job_id": float64(42),
		"run_id": float64(7),
	}
	jobID, runID, ok := extractTriggerPayload(payload)
	if !ok {
		t.Fatal("expected ok")
	}
	if jobID != 42 {
		t.Errorf("jobID = %d, want 42", jobID)
	}
	if runID == nil || *runID != 7 {
		t.Errorf("runID = %v, want 7", runID)
	}
}

func TestExtractTriggerPayload_MapWithoutRunID(t *testing.T) {
	payload := map[string]any{"job_id": float64(9)}
	jobID, runID, ok := extractTriggerPayload(payload)
	if !ok || jobID != 9 || runID != nil {
		t.Errorf("got jobID=%d runID=%v ok=%v", jobID, runID, ok)
	}
}

func TestExtractTriggerPayload_BadRunIDFallsThrough(t *testing.T) {
	// A garbage run_id should be ignored, not drop the whole trigger.
	payload := map[string]any{"job_id": float64(3), "run_id": "not-a-number"}
	jobID, runID, ok := extractTriggerPayload(payload)
	if !ok || jobID != 3 || runID != nil {
		t.Errorf("got jobID=%d runID=%v ok=%v", jobID, runID, ok)
	}
}
