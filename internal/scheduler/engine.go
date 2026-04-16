package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// Engine is the cron-driven firing loop. It polls the bridge on a fixed tick,
// fires due jobs through a bounded worker pool, and listens for ad-hoc trigger
// requests on the EventBus.
type Engine interface {
	// Start launches the tick loop and EventBus subscription. It returns
	// immediately; the loop runs until Stop is called or ctx is canceled.
	Start(ctx context.Context)
	// Stop halts the tick loop, unsubscribes from the bus, and blocks until all
	// in-flight firings have finished.
	Stop()
	// TriggerNow fires a job immediately, ignoring its enabled flag. A fresh
	// job_runs row is inserted and its id returned. Intended for Go callers
	// (tests, internal code) that have no pre-allocated run id.
	TriggerNow(ctx context.Context, jobID int64) (int64, error)
	// TriggerNowWithRun fires a job immediately against an already-inserted
	// run row. The engine promotes the existing row (updates triggered_at)
	// rather than inserting a new one, so a manual trigger produces exactly
	// one row in job_runs. Used by the plugin trigger_job_now path, which
	// needs a stable run id to return to the caller before the firing happens.
	TriggerNowWithRun(ctx context.Context, jobID, runID int64) error
}

// engine is the concrete implementation of Engine.
//
// Concurrency model:
//   - one tick goroutine drives the schedule
//   - each due job is fired in its own goroutine, gated by `sem` (buffered chan)
//   - inflight tracks every firing goroutine so Stop can wait
//   - all goroutines recover panics so a misbehaving handler cannot crash the loop
type engine struct {
	cfg       Config
	bridge    PluginBridge
	bus       eventbus.EventBus
	validator CronValidator
	logger    *slog.Logger

	sem      chan struct{}
	inflight sync.WaitGroup

	stopOnce sync.Once
	stopCh   chan struct{}
	loopDone chan struct{}
	sub      eventbus.Subscription
	subbed   bool
}

// NewEngine constructs an Engine. cfg defaults are applied for any zero field.
// bridge, bus, and validator must be non-nil. logger may be nil (slog.Default
// is used in that case).
func NewEngine(cfg Config, bridge PluginBridge, bus eventbus.EventBus, validator CronValidator, logger *slog.Logger) Engine {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = DefaultTickInterval
	}
	if cfg.MaxConcurrentFirings <= 0 {
		cfg.MaxConcurrentFirings = DefaultMaxConcurrentFirings
	}
	if cfg.PluginName == "" {
		cfg.PluginName = "scheduler-plugin"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &engine{
		cfg:       cfg,
		bridge:    bridge,
		bus:       bus,
		validator: validator,
		logger:    logger,
		sem:       make(chan struct{}, cfg.MaxConcurrentFirings),
		stopCh:    make(chan struct{}),
		loopDone:  make(chan struct{}),
	}
}

// Start subscribes to trigger requests and launches the tick loop.
func (e *engine) Start(ctx context.Context) {
	e.sub = e.bus.Subscribe(EventTriggerRequested, e.handleTriggerRequest)
	e.subbed = true
	go e.runLoop(ctx)
}

// Stop is idempotent; subsequent calls are no-ops.
func (e *engine) Stop() {
	e.stopOnce.Do(func() {
		if e.subbed {
			e.bus.Unsubscribe(e.sub)
		}
		close(e.stopCh)
		<-e.loopDone
		e.inflight.Wait()
	})
}

func (e *engine) runLoop(ctx context.Context) {
	defer close(e.loopDone)
	defer func() {
		if r := recover(); r != nil {
			e.logger.Error("scheduler tick loop panic", "panic", r)
		}
	}()

	// Run an immediate first tick so tests (and human users) don't wait a full
	// interval before the first firing.
	e.tick(ctx)

	t := time.NewTicker(e.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			e.tick(ctx)
		}
	}
}

// tick polls the bridge for due jobs and dispatches firings.
func (e *engine) tick(ctx context.Context) {
	jobs, err := e.bridge.LoadEnabledJobs(ctx)
	if err != nil {
		e.logger.Error("scheduler: LoadEnabledJobs failed", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, job := range jobs {
		if !e.isDue(job, now) {
			continue
		}
		e.dispatch(ctx, job, false)
	}
}

// isDue returns true when next_fire_at is set and <= now.
func (e *engine) isDue(job JobRecord, now time.Time) bool {
	if !job.Enabled || job.NextFireAt == nil {
		return false
	}
	t, err := time.Parse(time.RFC3339, *job.NextFireAt)
	if err != nil {
		e.logger.Warn("scheduler: invalid next_fire_at, skipping", "job_id", job.ID, "value", *job.NextFireAt, "err", err)
		return false
	}
	return !t.After(now)
}

// dispatch acquires a worker slot and fires the job in a new goroutine.
// Acquisition blocks if MaxConcurrentFirings is reached: that's intentional
// backpressure on the tick loop.
func (e *engine) dispatch(ctx context.Context, job JobRecord, manual bool) {
	select {
	case e.sem <- struct{}{}:
	case <-e.stopCh:
		return
	case <-ctx.Done():
		return
	}
	e.inflight.Add(1)
	go func() {
		defer e.inflight.Done()
		defer func() { <-e.sem }()
		defer func() {
			if r := recover(); r != nil {
				e.logger.Error("scheduler: firing panic recovered", "job_id", job.ID, "panic", r)
			}
		}()
		e.fire(context.WithoutCancel(ctx), job, manual)
	}()
}

// fire executes the full firing pipeline for one job.
//
// Phase A "execution" is just the EventBus emit: actual agent execution is
// out-of-process and listens for EventJobFired. We mark a run as success once
// the emit returns, and as error only if a bridge call fails before the emit.
func (e *engine) fire(ctx context.Context, job JobRecord, manual bool) {
	start := time.Now().UTC()
	triggeredAt := start

	run := JobRunRecord{
		JobID:       job.ID,
		TriggeredAt: triggeredAt.Format(time.RFC3339),
		Status:      RunStatusRunning,
		Manual:      manual,
	}
	runID, err := e.bridge.InsertJobRun(ctx, run)
	if err != nil {
		e.logger.Error("scheduler: InsertJobRun failed", "job_id", job.ID, "err", err)
		return
	}

	// Visible firing log so an operator watching the server stdout can verify
	// the job actually ran. Includes the prompt so smoke tests don't need to
	// open the DB to inspect what the agent would have received.
	e.logger.Info("scheduler: FIRING job",
		"job_id", job.ID,
		"name", job.Name,
		"agent", job.AssigneeAgentID,
		"manual", manual,
		"run_id", runID,
		"triggered_at", triggeredAt.Format(time.RFC3339),
		"prompt", job.Prompt,
	)

	// Emit the fired event. Even though Emit is fire-and-forget, we wrap it in
	// a recover so a future blocking implementation cannot kill us.
	emitErr := safeEmit(func() {
		e.bus.Emit(ctx, eventbus.Event{
			Name:         EventJobFired,
			SourcePlugin: e.cfg.PluginName,
			Payload: JobFiredPayload{
				JobID:           job.ID,
				JobName:         job.Name,
				TaskID:          job.TaskID,
				AssigneeAgentID: job.AssigneeAgentID,
				Prompt:          job.Prompt,
				TriggeredAt:     triggeredAt,
				Manual:          manual,
				RunID:           runID,
			},
		})
	})

	// Run finalization is owned by the webhook dispatcher / callback handler.
	// The engine only marks the run terminal when the *emit itself* failed
	// in that case there is no downstream consumer that could ever finalize it.
	status := RunStatusSuccess
	var errMsg *string
	durationMs := time.Since(start).Milliseconds()
	if emitErr != nil {
		status = RunStatusError
		s := emitErr.Error()
		errMsg = &s
		if err := e.bridge.UpdateJobRun(ctx, runID, status, errMsg, durationMs); err != nil {
			e.logger.Error("scheduler: UpdateJobRun failed", "run_id", runID, "err", err)
		}
		if err := e.bridge.UpdateJobLastRun(ctx, job.ID, triggeredAt.Format(time.RFC3339), status, errMsg, durationMs); err != nil {
			e.logger.Error("scheduler: UpdateJobLastRun failed", "job_id", job.ID, "err", err)
		}
	}

	// Compute next fire time only for scheduled (non-manual) jobs whose cron
	// expression is parseable. Manual triggers do not move the schedule
	// the next scheduled fire stays whatever it already was.
	if !manual {
		next, nerr := e.validator.NextAfter(job.CronExpr, job.Timezone, time.Now().UTC())
		if nerr != nil {
			e.logger.Warn("scheduler: failed to compute next fire", "job_id", job.ID, "err", nerr)
			if clearErr := e.bridge.UpdateJobNextFire(ctx, job.ID, nil); clearErr != nil {
				e.logger.Error("scheduler: failed to clear next_fire_at (stale value may cause repeated firings)", "job_id", job.ID, "err", clearErr)
			}
		} else {
			s := next.Format(time.RFC3339)
			if err := e.bridge.UpdateJobNextFire(ctx, job.ID, &s); err != nil {
				e.logger.Error("scheduler: UpdateJobNextFire failed", "job_id", job.ID, "err", err)
			}
		}
	}

	// Best-effort completion event.
	_ = safeEmit(func() {
		e.bus.Emit(ctx, eventbus.Event{
			Name:         EventRunCompleted,
			SourcePlugin: e.cfg.PluginName,
			Payload: JobRunCompletedPayload{
				RunID:       runID,
				JobID:       job.ID,
				JobName:     job.Name,
				Status:      status,
				Error:       errMsg,
				DurationMs:  durationMs,
				TriggeredAt: triggeredAt,
				Manual:      manual,
			},
		})
	})
}

// TriggerNow loads the job, ignores its enabled flag, inserts a fresh run
// row, and dispatches a manual firing.
func (e *engine) TriggerNow(ctx context.Context, jobID int64) (int64, error) {
	job, err := e.bridge.LoadJob(ctx, jobID)
	if err != nil {
		return 0, fmt.Errorf("load job %d: %w", jobID, err)
	}
	if err := e.acquireSlot(ctx); err != nil {
		return 0, err
	}
	e.inflight.Add(1)

	// Insert the run row up-front so we can return its ID even though the rest
	// of the firing finishes asynchronously.
	triggeredAt := time.Now().UTC()
	run := JobRunRecord{
		JobID:       job.ID,
		TriggeredAt: triggeredAt.Format(time.RFC3339),
		Status:      RunStatusRunning,
		Manual:      true,
	}
	runID, err := e.bridge.InsertJobRun(ctx, run)
	if err != nil {
		<-e.sem
		e.inflight.Done()
		return 0, fmt.Errorf("insert run: %w", err)
	}

	e.dispatchManual(ctx, job, runID, triggeredAt)
	return runID, nil
}

// TriggerNowWithRun fires a job immediately against an existing run row
// (pre-inserted by the plugin). The engine promotes that row by refreshing
// triggered_at, then runs the normal manual-firing pipeline. Returns an error
// if the job or run does not exist, or if the engine is stopping.
func (e *engine) TriggerNowWithRun(ctx context.Context, jobID, runID int64) error {
	job, err := e.bridge.LoadJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job %d: %w", jobID, err)
	}
	if err := e.acquireSlot(ctx); err != nil {
		return err
	}
	e.inflight.Add(1)

	triggeredAt := time.Now().UTC()
	if err := e.bridge.PromoteRunToActive(ctx, runID, triggeredAt.Format(time.RFC3339)); err != nil {
		<-e.sem
		e.inflight.Done()
		return fmt.Errorf("promote run: %w", err)
	}

	e.dispatchManual(ctx, job, runID, triggeredAt)
	return nil
}

// acquireSlot takes one concurrency slot or returns an error if the engine is
// stopping / the context is canceled. Extracted so TriggerNow and
// TriggerNowWithRun share the identical wait semantics.
func (e *engine) acquireSlot(ctx context.Context) error {
	select {
	case e.sem <- struct{}{}:
		return nil
	case <-e.stopCh:
		return errors.New("engine stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// dispatchManual spawns the firing goroutine for a manual trigger. Both
// TriggerNow and TriggerNowWithRun funnel through here so the recover/cleanup
// discipline lives in one place.
func (e *engine) dispatchManual(ctx context.Context, job JobRecord, runID int64, triggeredAt time.Time) {
	go func() {
		defer e.inflight.Done()
		defer func() { <-e.sem }()
		defer func() {
			if r := recover(); r != nil {
				e.logger.Error("scheduler: manual firing panic recovered", "job_id", job.ID, "panic", r)
			}
		}()
		e.finishManualFiring(context.WithoutCancel(ctx), job, runID, triggeredAt)
	}()
}

// finishManualFiring continues a manual firing after the run row is inserted.
// Mirrors the latter half of fire() but skips the next-fire bookkeeping.
func (e *engine) finishManualFiring(ctx context.Context, job JobRecord, runID int64, triggeredAt time.Time) {
	emitErr := safeEmit(func() {
		e.bus.Emit(ctx, eventbus.Event{
			Name:         EventJobFired,
			SourcePlugin: e.cfg.PluginName,
			Payload: JobFiredPayload{
				JobID:           job.ID,
				JobName:         job.Name,
				TaskID:          job.TaskID,
				AssigneeAgentID: job.AssigneeAgentID,
				Prompt:          job.Prompt,
				TriggeredAt:     triggeredAt,
				Manual:          true,
				RunID:           runID,
			},
		})
	})

	// See finishFiring: only the emit-failure path finalizes here.
	status := RunStatusSuccess
	var errMsg *string
	durationMs := time.Since(triggeredAt).Milliseconds()
	if emitErr != nil {
		status = RunStatusError
		s := emitErr.Error()
		errMsg = &s
		if err := e.bridge.UpdateJobRun(ctx, runID, status, errMsg, durationMs); err != nil {
			e.logger.Error("scheduler: UpdateJobRun failed (manual)", "run_id", runID, "err", err)
		}
		if err := e.bridge.UpdateJobLastRun(ctx, job.ID, triggeredAt.Format(time.RFC3339), status, errMsg, durationMs); err != nil {
			e.logger.Error("scheduler: UpdateJobLastRun failed (manual)", "job_id", job.ID, "err", err)
		}
	}
	_ = safeEmit(func() {
		e.bus.Emit(ctx, eventbus.Event{
			Name:         EventRunCompleted,
			SourcePlugin: e.cfg.PluginName,
			Payload: JobRunCompletedPayload{
				RunID:       runID,
				JobID:       job.ID,
				JobName:     job.Name,
				Status:      status,
				Error:       errMsg,
				DurationMs:  durationMs,
				TriggeredAt: triggeredAt,
				Manual:      true,
			},
		})
	})
}

// handleTriggerRequest is the EventBus subscriber for ad-hoc triggers.
// It must not block the bus, so the actual trigger dispatch runs in its own
// goroutine. Errors are logged: there is no caller to return them to.
//
// The payload can arrive in two shapes:
//  1. Native Go caller (tests, internal code): TriggerRequestedPayload struct.
//  2. WASM plugin via mc_event::emit: map[string]any decoded from JSON, where
//     job_id / run_id land as float64 (json.Unmarshal default) or int64
//     depending on how the host-side emit path typed them. Both are accepted.
//
// When the payload carries a RunID, the engine calls TriggerNowWithRun
// (promotes the existing row): that is the plugin path. When RunID is absent,
// the engine calls TriggerNow which inserts a fresh row.
func (e *engine) handleTriggerRequest(ctx context.Context, ev eventbus.Event) {
	jobID, runID, ok := extractTriggerPayload(ev.Payload)
	if !ok {
		e.logger.Warn("scheduler: trigger event with unrecognized payload",
			"type", fmt.Sprintf("%T", ev.Payload))
		return
	}
	go func() {
		detached := context.WithoutCancel(ctx)
		if runID != nil {
			if err := e.TriggerNowWithRun(detached, jobID, *runID); err != nil {
				e.logger.Error("scheduler: trigger via eventbus failed",
					"job_id", jobID, "run_id", *runID, "err", err)
			}
			return
		}
		if _, err := e.TriggerNow(detached, jobID); err != nil {
			e.logger.Error("scheduler: trigger via eventbus failed", "job_id", jobID, "err", err)
		}
	}()
}

// extractTriggerPayload pulls job_id (required) and run_id (optional) out of
// a TriggerRequestedPayload or a map[string]any (as delivered through the
// WASM -> mc_event::emit path). The third return is false when the payload
// cannot be interpreted at all.
func extractTriggerPayload(payload any) (jobID int64, runID *int64, ok bool) {
	switch p := payload.(type) {
	case TriggerRequestedPayload:
		return p.JobID, p.RunID, true
	case *TriggerRequestedPayload:
		if p == nil {
			return 0, nil, false
		}
		return p.JobID, p.RunID, true
	case map[string]any:
		raw, okJob := p["job_id"]
		if !okJob {
			return 0, nil, false
		}
		jID, okCoerce := coerceInt64(raw)
		if !okCoerce {
			return 0, nil, false
		}
		if rawRun, hasRun := p["run_id"]; hasRun && rawRun != nil {
			rID, okRun := coerceInt64(rawRun)
			if okRun {
				return jID, &rID, true
			}
			// run_id present but unparsable: ignore it, fall through to the
			// "no run id" path rather than failing the whole trigger. A bad
			// run_id is less destructive than losing the firing entirely.
		}
		return jID, nil, true
	default:
		return 0, nil, false
	}
}

// coerceInt64 accepts the numeric types that JSON decoders and Go callers can
// hand us for a job_id field. Non-integer floats are rejected.
func coerceInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case uint:
		return int64(n), true
	case float64:
		if n != float64(int64(n)) {
			return 0, false
		}
		return int64(n), true
	case float32:
		f := float64(n)
		if f != float64(int64(f)) {
			return 0, false
		}
		return int64(f), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

// safeEmit invokes fn and converts any panic into an error so the firing
// pipeline can record it instead of crashing.
func safeEmit(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("emit panic: %v", r)
		}
	}()
	fn()
	return nil
}
