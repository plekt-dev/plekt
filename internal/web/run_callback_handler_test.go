package web_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/scheduler"
	"github.com/plekt-dev/plekt/internal/web"
	"github.com/plekt-dev/plekt/internal/webhooks"
)

// stubBridge is the minimum PluginBridge surface needed by RunCallbackHandler.
// All methods are concurrency-safe so tests can poll without races.
type stubBridge struct {
	mu        sync.Mutex
	runs      map[int64]scheduler.JobRunRecord
	jobs      map[int64]scheduler.JobRecord
	output    map[int64]string
	terminal  map[int64]scheduler.RunStatus
	terminalE map[int64]string
	dispatch  map[int64]scheduler.DispatchStatus
}

func newStubBridge() *stubBridge {
	return &stubBridge{
		runs:      map[int64]scheduler.JobRunRecord{},
		jobs:      map[int64]scheduler.JobRecord{},
		output:    map[int64]string{},
		terminal:  map[int64]scheduler.RunStatus{},
		terminalE: map[int64]string{},
		dispatch:  map[int64]scheduler.DispatchStatus{},
	}
}
func (s *stubBridge) addRun(r scheduler.JobRunRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[r.ID] = r
}
func (s *stubBridge) addJob(j scheduler.JobRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j
}

// Bridge interface implementations:
func (s *stubBridge) LoadEnabledJobs(context.Context) ([]scheduler.JobRecord, error) {
	return nil, nil
}
func (s *stubBridge) LoadJob(_ context.Context, jobID int64) (scheduler.JobRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[jobID]
	if !ok {
		return scheduler.JobRecord{}, http.ErrNoCookie // any error
	}
	return j, nil
}
func (s *stubBridge) InsertJobRun(context.Context, scheduler.JobRunRecord) (int64, error) {
	return 0, nil
}
func (s *stubBridge) UpdateJobRun(_ context.Context, runID int64, status scheduler.RunStatus, errMsg *string, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminal[runID] = status
	if errMsg != nil {
		s.terminalE[runID] = *errMsg
	}
	r := s.runs[runID]
	r.Status = status
	s.runs[runID] = r
	return nil
}
func (s *stubBridge) UpdateJobLastRun(context.Context, int64, string, scheduler.RunStatus, *string, int64) error {
	return nil
}
func (s *stubBridge) UpdateJobNextFire(context.Context, int64, *string) error { return nil }
func (s *stubBridge) PromoteRunToActive(context.Context, int64, string) error { return nil }
func (s *stubBridge) GetJobRun(_ context.Context, runID int64) (scheduler.JobRunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return scheduler.JobRunRecord{}, http.ErrNoCookie
	}
	return r, nil
}
func (s *stubBridge) UpdateJobRunOutput(_ context.Context, runID int64, output string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output[runID] = output
	return nil
}
func (s *stubBridge) UpdateJobRunDispatchStatus(_ context.Context, runID int64, st scheduler.DispatchStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatch[runID] = st
	return nil
}

// stubAgentSvc returns a single configured agent by name.
type stubAgentSvc struct {
	a agents.Agent
}

func (s *stubAgentSvc) GetByName(_ context.Context, name string) (agents.Agent, error) {
	if name != s.a.Name {
		return agents.Agent{}, agents.ErrAgentNotFound
	}
	return s.a, nil
}

// Stubs for the rest of the AgentService surface: never invoked by the handler.
func (s *stubAgentSvc) Create(context.Context, string) (agents.Agent, error) {
	panic("nope")
}
func (s *stubAgentSvc) GetByID(context.Context, int64) (agents.Agent, error) { panic("nope") }
func (s *stubAgentSvc) List(context.Context) ([]agents.Agent, error)         { panic("nope") }
func (s *stubAgentSvc) RotateToken(context.Context, int64) (string, error)   { panic("nope") }
func (s *stubAgentSvc) UpdateWebhook(context.Context, int64, string, string) error {
	panic("nope")
}
func (s *stubAgentSvc) SetWebhookSecret(context.Context, int64, string) error { panic("nope") }
func (s *stubAgentSvc) HasWebhookSecret(context.Context, int64) (bool, error) { panic("nope") }
func (s *stubAgentSvc) Delete(context.Context, int64) error                   { panic("nope") }
func (s *stubAgentSvc) SetPermissions(context.Context, int64, []agents.AgentPermission) error {
	panic("nope")
}
func (s *stubAgentSvc) ListPermissions(context.Context, int64) ([]agents.AgentPermission, error) {
	panic("nope")
}
func (s *stubAgentSvc) ResolveByToken(context.Context, string) (agents.Agent, []agents.AgentPermission, error) {
	panic("nope")
}

// callbackRequest builds an http.Request with a path value, body, and an
// optional signature header.
func callbackRequest(t *testing.T, runID int64, body []byte, secret string) *http.Request {
	t.Helper()
	idStr := strconv.FormatInt(runID, 10)
	r := httptest.NewRequest(http.MethodPost, "/api/runs/"+idStr+"/result", bytes.NewReader(body))
	r.SetPathValue("run_id", idStr)
	if secret != "" {
		r.Header.Set(webhooks.SignatureHeader, webhooks.Sign(secret, body))
	}
	return r
}

func TestRunCallback_HappyPathSuccess(t *testing.T) {
	br := newStubBridge()
	br.addRun(scheduler.JobRunRecord{
		ID: 100, JobID: 7, Status: scheduler.RunStatusRunning,
		TriggeredAt: time.Now().UTC().Format(time.RFC3339),
	})
	br.addJob(scheduler.JobRecord{ID: 7, AssigneeAgentID: "claude"})

	ag := agents.Agent{Name: "claude", WebhookSecret: "shh"}
	h := web.NewRunCallbackHandler(br, &stubAgentSvc{a: ag})

	body := []byte(`{"output":"hello from relay"}`)
	req := callbackRequest(t, 100, body, "shh")
	rr := httptest.NewRecorder()
	h.Handle(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", rr.Code, rr.Body.String())
	}
	if got := br.output[100]; got != "hello from relay" {
		t.Errorf("output = %q", got)
	}
	if br.terminal[100] != scheduler.RunStatusSuccess {
		t.Errorf("terminal = %q", br.terminal[100])
	}
	if br.dispatch[100] != scheduler.DispatchStatusDelivered {
		t.Errorf("dispatch = %q", br.dispatch[100])
	}
}

func TestRunCallback_ErrorBody(t *testing.T) {
	br := newStubBridge()
	br.addRun(scheduler.JobRunRecord{
		ID: 101, JobID: 7, Status: scheduler.RunStatusRunning,
		TriggeredAt: time.Now().UTC().Format(time.RFC3339),
	})
	br.addJob(scheduler.JobRecord{ID: 7, AssigneeAgentID: "claude"})

	h := web.NewRunCallbackHandler(br, &stubAgentSvc{a: agents.Agent{Name: "claude", WebhookSecret: "shh"}})
	body := []byte(`{"error":"claude exited 1"}`)
	rr := httptest.NewRecorder()
	h.Handle(rr, callbackRequest(t, 101, body, "shh"))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rr.Code)
	}
	if br.terminal[101] != scheduler.RunStatusError {
		t.Errorf("terminal = %q", br.terminal[101])
	}
	if br.terminalE[101] != "claude exited 1" {
		t.Errorf("error msg = %q", br.terminalE[101])
	}
	if br.dispatch[101] != scheduler.DispatchStatusError {
		t.Errorf("dispatch = %q", br.dispatch[101])
	}
}

func TestRunCallback_InvalidSignature(t *testing.T) {
	br := newStubBridge()
	br.addRun(scheduler.JobRunRecord{
		ID: 102, JobID: 7, Status: scheduler.RunStatusRunning,
		TriggeredAt: time.Now().UTC().Format(time.RFC3339),
	})
	br.addJob(scheduler.JobRecord{ID: 7, AssigneeAgentID: "claude"})

	h := web.NewRunCallbackHandler(br, &stubAgentSvc{a: agents.Agent{Name: "claude", WebhookSecret: "right"}})
	body := []byte(`{"output":"x"}`)
	// Sign with wrong secret.
	req := callbackRequest(t, 102, body, "wrong")
	rr := httptest.NewRecorder()
	h.Handle(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	if _, ok := br.terminal[102]; ok {
		t.Error("run must not be finalized on invalid signature")
	}
}

func TestRunCallback_AlreadyTerminal(t *testing.T) {
	br := newStubBridge()
	br.addRun(scheduler.JobRunRecord{
		ID: 103, JobID: 7, Status: scheduler.RunStatusSuccess,
		TriggeredAt: time.Now().UTC().Format(time.RFC3339),
	})
	br.addJob(scheduler.JobRecord{ID: 7, AssigneeAgentID: "claude"})

	h := web.NewRunCallbackHandler(br, &stubAgentSvc{a: agents.Agent{Name: "claude", WebhookSecret: "shh"}})
	rr := httptest.NewRecorder()
	h.Handle(rr, callbackRequest(t, 103, []byte(`{"output":"x"}`), "shh"))

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}

func TestRunCallback_MissingRun(t *testing.T) {
	br := newStubBridge()
	h := web.NewRunCallbackHandler(br, &stubAgentSvc{a: agents.Agent{Name: "claude", WebhookSecret: "shh"}})
	rr := httptest.NewRecorder()
	h.Handle(rr, callbackRequest(t, 999, []byte(`{}`), "shh"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestRunCallback_WrongMethod(t *testing.T) {
	br := newStubBridge()
	h := web.NewRunCallbackHandler(br, &stubAgentSvc{a: agents.Agent{Name: "claude", WebhookSecret: "shh"}})
	r := httptest.NewRequest(http.MethodGet, "/api/runs/1/result", nil)
	r.SetPathValue("run_id", "1")
	rr := httptest.NewRecorder()
	h.Handle(rr, r)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rr.Code)
	}
}
