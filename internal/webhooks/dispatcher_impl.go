package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/scheduler"
)

// userAgent is sent on all outbound webhook POSTs so receivers can identify
// the source. Format mirrors common webhook conventions.
const userAgent = "Plekt/Webhook/1"

// dispatcher is the default Dispatcher backed by net/http.
type dispatcher struct {
	cfg Config

	startMu sync.Mutex
	started bool
	sub     eventbus.Subscription

	// inflight tracks goroutines spawned by handle so Stop can wait for them.
	inflight sync.WaitGroup

	// stopCtx is cancelled by Stop to abort retry sleeps and outstanding
	// HTTP requests cooperatively.
	stopCtx    context.Context
	stopCancel context.CancelFunc
}

// New constructs a Dispatcher from the given Config. Required collaborators
// (Bus, Agents, Bridge) must be non-nil; missing optional fields fall back to
// safe defaults.
func New(cfg Config) Dispatcher {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if cfg.RetryAttempts <= 0 {
		cfg.RetryAttempts = defaultRetryAttempts
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = defaultRetryBackoff
	}
	return &dispatcher{cfg: cfg}
}

// Start subscribes to scheduler.job.fired and returns immediately. Each event
// is handled in a separate goroutine so the bus is never blocked.
func (d *dispatcher) Start(ctx context.Context) error {
	d.startMu.Lock()
	defer d.startMu.Unlock()

	if d.started {
		return errors.New("webhooks: dispatcher already started")
	}
	if d.cfg.Bus == nil {
		return errors.New("webhooks: nil EventBus")
	}
	if d.cfg.Agents == nil {
		return errors.New("webhooks: nil AgentService")
	}
	if d.cfg.Bridge == nil {
		return errors.New("webhooks: nil PluginBridge")
	}

	d.stopCtx, d.stopCancel = context.WithCancel(context.Background())
	d.sub = d.cfg.Bus.Subscribe(scheduler.EventJobFired, d.onFired)
	d.started = true

	slog.Info("webhooks: dispatcher started",
		"retry_attempts", d.cfg.RetryAttempts,
		"retry_backoff", d.cfg.RetryBackoff,
		"callback_base_url", d.cfg.CallbackBaseURL,
	)
	return nil
}

// Stop unsubscribes and waits for in-flight deliveries to finish.
func (d *dispatcher) Stop() {
	d.startMu.Lock()
	if !d.started {
		d.startMu.Unlock()
		return
	}
	d.cfg.Bus.Unsubscribe(d.sub)
	d.stopCancel()
	d.started = false
	d.startMu.Unlock()

	d.inflight.Wait()
	slog.Info("webhooks: dispatcher stopped")
}

// onFired is the bus handler. It immediately spawns a goroutine so the bus
// is never blocked by HTTP latency.
func (d *dispatcher) onFired(_ context.Context, ev eventbus.Event) {
	payload, ok := ev.Payload.(scheduler.JobFiredPayload)
	if !ok {
		slog.Warn("webhooks: dropped event with wrong payload type",
			"event", ev.Name, "payload_type", fmt.Sprintf("%T", ev.Payload))
		return
	}

	d.inflight.Add(1)
	go func() {
		defer d.inflight.Done()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("webhooks: handler panic recovered",
					"run_id", payload.RunID, "panic", fmt.Sprintf("%v", r))
			}
		}()
		d.handle(payload)
	}()
}

// handle is the per-firing pipeline: lookup agent → build payload → POST → finalize.
func (d *dispatcher) handle(payload scheduler.JobFiredPayload) {
	ctx := d.stopCtx

	agent, err := d.cfg.Agents.GetByName(ctx, payload.AssigneeAgentID)
	if err != nil {
		d.failRun(ctx, payload.RunID, fmt.Sprintf("agent %q not found: %v", payload.AssigneeAgentID, err))
		return
	}
	if agent.WebhookURL == "" {
		d.failRun(ctx, payload.RunID, "agent has no webhook configured")
		return
	}
	if agent.WebhookSecret == "" {
		// Refuse to deliver unsigned. The receiver depends on HMAC for trust.
		d.failRun(ctx, payload.RunID, "agent webhook secret is empty")
		return
	}

	out := OutboundPayload{
		JobID:       payload.JobID,
		RunID:       payload.RunID,
		JobName:     payload.JobName,
		AgentName:   agent.Name,
		Prompt:      payload.Prompt,
		TriggeredAt: payload.TriggeredAt,
		Manual:      payload.Manual,
		CallbackURL: d.callbackURL(payload.RunID),
	}
	body, err := json.Marshal(out)
	if err != nil {
		d.failRun(ctx, payload.RunID, fmt.Sprintf("marshal payload: %v", err))
		return
	}

	resp, respBody, err := d.postWithRetry(ctx, agent.WebhookURL, agent.WebhookSecret, body)
	if err != nil {
		d.failRun(ctx, payload.RunID, fmt.Sprintf("webhook POST: %v", err))
		return
	}

	// Mark dispatched on the run row regardless of mode: the outbound side
	// completed successfully. The terminal status comes either from the sync
	// response body or the async callback.
	if err := d.cfg.Bridge.UpdateJobRunDispatchStatus(ctx, payload.RunID, scheduler.DispatchStatusDispatched); err != nil {
		slog.Warn("webhooks: failed to mark dispatched", "run_id", payload.RunID, "err", err)
	}

	// Sync mode: receiver echoed the output inline. We finalize the run now.
	// Detection: agent.WebhookMode == sync OR the response body decodes into a
	// non-empty CallbackResponse. Being lenient lets sync receivers work even
	// with the agent flagged async.
	if agent.WebhookMode == agents.WebhookModeSync || len(bytes.TrimSpace(respBody)) > 0 {
		var cb CallbackResponse
		if jerr := json.Unmarshal(respBody, &cb); jerr == nil && (cb.Output != "" || cb.Error != "") {
			d.finalizeFromCallback(ctx, payload, cb)
			return
		}
		if agent.WebhookMode == agents.WebhookModeSync {
			d.failRun(ctx, payload.RunID, "sync mode: receiver returned no parsable output")
			return
		}
	}

	// Async mode: nothing more to do here. The receiver will POST back to
	// /api/runs/{run_id}/result and the callback handler will finalize the run.
	_ = resp // currently unused; reserved for future header inspection
}

// callbackURL builds the absolute URL the receiver should POST results to.
// Tolerates a trailing slash on CallbackBaseURL.
func (d *dispatcher) callbackURL(runID int64) string {
	base := strings.TrimRight(d.cfg.CallbackBaseURL, "/")
	return fmt.Sprintf("%s/api/runs/%d/result", base, runID)
}

// postWithRetry POSTs body to url with HMAC signature, retrying on network
// errors and 5xx responses. 4xx responses are returned without retry: those
// are configuration errors on the receiver side and retrying makes no sense.
func (d *dispatcher) postWithRetry(ctx context.Context, url, secret string, body []byte) (*http.Response, []byte, error) {
	sig := Sign(secret, body)
	var lastErr error
	backoff := d.cfg.RetryBackoff

	for attempt := 1; attempt <= d.cfg.RetryAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set(SignatureHeader, sig)

		resp, err := d.cfg.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("webhooks: POST failed", "attempt", attempt, "err", err)
			if !d.sleepBackoff(ctx, backoff) {
				return nil, nil, fmt.Errorf("aborted: %w", lastErr)
			}
			backoff *= 2
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, respBody, nil
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// Don't retry client errors.
			return resp, respBody, fmt.Errorf("receiver returned %d: %s", resp.StatusCode, truncate(respBody, 200))
		}
		// 5xx: retry.
		lastErr = fmt.Errorf("receiver returned %d", resp.StatusCode)
		slog.Warn("webhooks: server error", "attempt", attempt, "status", resp.StatusCode)
		if attempt < d.cfg.RetryAttempts {
			if !d.sleepBackoff(ctx, backoff) {
				return nil, nil, fmt.Errorf("aborted: %w", lastErr)
			}
			backoff *= 2
		}
	}
	return nil, nil, fmt.Errorf("exhausted %d attempts: %w", d.cfg.RetryAttempts, lastErr)
}

// sleepBackoff returns false if the dispatcher was stopped before the
// backoff expired (giving up early on shutdown).
func (d *dispatcher) sleepBackoff(ctx context.Context, dur time.Duration) bool {
	if dur <= 0 {
		return true
	}
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// finalizeFromCallback writes the receiver's CallbackResponse onto the run
// row, choosing success or error based on the body, and updates the
// terminal status + dispatch_status accordingly.
func (d *dispatcher) finalizeFromCallback(ctx context.Context, payload scheduler.JobFiredPayload, cb CallbackResponse) {
	durationMs := time.Since(payload.TriggeredAt).Milliseconds()
	if cb.Error != "" {
		errMsg := cb.Error
		if err := d.cfg.Bridge.UpdateJobRun(ctx, payload.RunID, scheduler.RunStatusError, &errMsg, durationMs); err != nil {
			slog.Warn("webhooks: UpdateJobRun (error) failed", "run_id", payload.RunID, "err", err)
		}
		if dsErr := d.cfg.Bridge.UpdateJobRunDispatchStatus(ctx, payload.RunID, scheduler.DispatchStatusError); dsErr != nil {
			slog.Warn("webhooks: UpdateJobRunDispatchStatus failed", "run_id", payload.RunID, "err", dsErr)
		}
		if lrErr := d.cfg.Bridge.UpdateJobLastRun(ctx, payload.JobID, payload.TriggeredAt.UTC().Format(time.RFC3339), scheduler.RunStatusError, &errMsg, durationMs); lrErr != nil {
			slog.Warn("webhooks: UpdateJobLastRun failed", "job_id", payload.JobID, "err", lrErr)
		}
		return
	}
	if err := d.cfg.Bridge.UpdateJobRunOutput(ctx, payload.RunID, cb.Output); err != nil {
		slog.Warn("webhooks: UpdateJobRunOutput failed", "run_id", payload.RunID, "err", err)
	}
	if err := d.cfg.Bridge.UpdateJobRun(ctx, payload.RunID, scheduler.RunStatusSuccess, nil, durationMs); err != nil {
		slog.Warn("webhooks: UpdateJobRun (success) failed", "run_id", payload.RunID, "err", err)
	}
	if dsErr := d.cfg.Bridge.UpdateJobRunDispatchStatus(ctx, payload.RunID, scheduler.DispatchStatusDelivered); dsErr != nil {
		slog.Warn("webhooks: UpdateJobRunDispatchStatus failed", "run_id", payload.RunID, "err", dsErr)
	}
	if lrErr := d.cfg.Bridge.UpdateJobLastRun(ctx, payload.JobID, payload.TriggeredAt.UTC().Format(time.RFC3339), scheduler.RunStatusSuccess, nil, durationMs); lrErr != nil {
		slog.Warn("webhooks: UpdateJobLastRun failed", "job_id", payload.JobID, "err", lrErr)
	}
}

// failRun marks a run as errored when something prevents the outbound POST
// (missing config, marshal failure, exhausted retries, etc).
func (d *dispatcher) failRun(ctx context.Context, runID int64, msg string) {
	slog.Warn("webhooks: run failed", "run_id", runID, "reason", msg)
	em := msg
	if err := d.cfg.Bridge.UpdateJobRun(ctx, runID, scheduler.RunStatusError, &em, 0); err != nil {
		slog.Warn("webhooks: UpdateJobRun on fail path failed", "run_id", runID, "err", err)
	}
	if dsErr := d.cfg.Bridge.UpdateJobRunDispatchStatus(ctx, runID, scheduler.DispatchStatusError); dsErr != nil {
		slog.Warn("webhooks: UpdateJobRunDispatchStatus failed", "run_id", runID, "err", dsErr)
	}
}

// truncate clips a byte slice to at most n bytes for safe inclusion in error
// messages, appending ellipsis when clipped.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
