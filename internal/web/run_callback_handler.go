package web

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/scheduler"
	"github.com/plekt-dev/plekt/internal/webhooks"
)

// RunCallbackHandler serves POST /api/runs/{run_id}/result. The handler is
// the inbound side of the webhook delivery contract: when an external relay
// has finished executing a scheduled prompt, it POSTs the result here. The
// signature is verified against the agent's webhook_secret using HMAC-SHA256.
//
// This is the only public-facing endpoint that does NOT require a user
// session: authentication comes from the HMAC signature instead. The relay
// process is treated as a peer service, not a logged-in user.
type RunCallbackHandler interface {
	Handle(w http.ResponseWriter, r *http.Request)
}

type runCallbackHandler struct {
	bridge scheduler.PluginBridge
	agents agents.AgentService
}

// NewRunCallbackHandler constructs a callback handler from its collaborators.
func NewRunCallbackHandler(bridge scheduler.PluginBridge, agentSvc agents.AgentService) RunCallbackHandler {
	return &runCallbackHandler{bridge: bridge, agents: agentSvc}
}

// Handle processes a single inbound callback. The implementation is intentionally
// linear so the security-critical ordering (lookup → verify → finalize) is
// obvious to readers.
func (h *runCallbackHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runIDStr := r.PathValue("run_id")
	runID, err := strconv.ParseInt(runIDStr, 10, 64)
	if err != nil || runID <= 0 {
		http.Error(w, "bad run id", http.StatusBadRequest)
		return
	}

	// Read raw body once: we need it twice (once for HMAC verification,
	// once for JSON parsing). Capping at 1 MiB protects against malicious
	// receivers that try to OOM the core.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Look up the run, the owning job, and the agent in that order. We need
	// the agent's webhook_secret to verify the signature, and the run's
	// triggered_at to compute duration.
	run, err := h.bridge.GetJobRun(ctx, runID)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}

	// Idempotency: if the run is already in a terminal state, refuse the
	// callback. This prevents duplicate deliveries from a misbehaving relay
	// from overwriting a finalized run.
	if run.Status == scheduler.RunStatusSuccess || run.Status == scheduler.RunStatusError {
		http.Error(w, "run already finalized", http.StatusConflict)
		return
	}

	job, err := h.bridge.LoadJob(ctx, run.JobID)
	if err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	agent, err := h.agents.GetByName(ctx, job.AssigneeAgentID)
	if err != nil {
		// The agent may have been renamed or deleted between firing and
		// callback. Treat this as 404 since the relay can't recover.
		if errors.Is(err, agents.ErrAgentNotFound) {
			http.Error(w, "agent not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if agent.WebhookSecret == "" {
		// Without a secret we cannot verify anything; refuse to write.
		http.Error(w, "agent has no webhook secret", http.StatusUnauthorized)
		return
	}

	sig := r.Header.Get(webhooks.SignatureHeader)
	if !webhooks.Verify(agent.WebhookSecret, body, sig) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var cb webhooks.CallbackResponse
	if err := json.Unmarshal(body, &cb); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	// Compute duration relative to the run's triggered_at.
	durationMs := durationSinceTriggered(run.TriggeredAt)

	if cb.Error != "" {
		em := cb.Error
		if err := h.bridge.UpdateJobRun(ctx, runID, scheduler.RunStatusError, &em, durationMs); err != nil {
			slog.Warn("callback: UpdateJobRun (error) failed", "run_id", runID, "err", err)
		}
		if dsErr := h.bridge.UpdateJobRunDispatchStatus(ctx, runID, scheduler.DispatchStatusError); dsErr != nil {
			slog.Warn("callback: UpdateJobRunDispatchStatus failed", "run_id", runID, "err", dsErr)
		}
		if lrErr := h.bridge.UpdateJobLastRun(ctx, run.JobID, run.TriggeredAt, scheduler.RunStatusError, &em, durationMs); lrErr != nil {
			slog.Warn("callback: UpdateJobLastRun failed", "run_id", runID, "err", lrErr)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.bridge.UpdateJobRunOutput(ctx, runID, cb.Output); err != nil {
		slog.Warn("callback: UpdateJobRunOutput failed", "run_id", runID, "err", err)
		http.Error(w, "persist output failed", http.StatusInternalServerError)
		return
	}
	if err := h.bridge.UpdateJobRun(ctx, runID, scheduler.RunStatusSuccess, nil, durationMs); err != nil {
		slog.Warn("callback: UpdateJobRun (success) failed", "run_id", runID, "err", err)
	}
	if dsErr := h.bridge.UpdateJobRunDispatchStatus(ctx, runID, scheduler.DispatchStatusDelivered); dsErr != nil {
		slog.Warn("callback: UpdateJobRunDispatchStatus failed", "run_id", runID, "err", dsErr)
	}
	if lrErr := h.bridge.UpdateJobLastRun(ctx, run.JobID, run.TriggeredAt, scheduler.RunStatusSuccess, nil, durationMs); lrErr != nil {
		slog.Warn("callback: UpdateJobLastRun failed", "run_id", runID, "err", lrErr)
	}

	w.WriteHeader(http.StatusNoContent)
}

// durationSinceTriggered parses the run's triggered_at (RFC3339 string) and
// returns the elapsed time in milliseconds. If parsing fails (corrupt row)
// it returns 0 rather than failing the callback: duration is observability,
// not correctness.
func durationSinceTriggered(triggeredAt string) int64 {
	t, err := time.Parse(time.RFC3339, triggeredAt)
	if err != nil {
		return 0
	}
	d := time.Since(t).Milliseconds()
	if d < 0 {
		return 0
	}
	return d
}
