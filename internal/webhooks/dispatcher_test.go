package webhooks_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/agents"
	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/scheduler"
	"github.com/plekt-dev/plekt/internal/webhooks"
)

// fakeAgentService is a minimal AgentService implementation for tests. Only
// GetByName is exercised; the rest panic to flag accidental coupling.
type fakeAgentService struct {
	mu     sync.Mutex
	byName map[string]agents.Agent
}

func (f *fakeAgentService) GetByName(_ context.Context, name string) (agents.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byName[name]
	if !ok {
		return agents.Agent{}, agents.ErrAgentNotFound
	}
	return a, nil
}

func (f *fakeAgentService) Create(context.Context, string) (agents.Agent, error) {
	panic("not implemented")
}
func (f *fakeAgentService) GetByID(context.Context, int64) (agents.Agent, error) {
	panic("not implemented")
}
func (f *fakeAgentService) List(context.Context) ([]agents.Agent, error) { panic("not implemented") }
func (f *fakeAgentService) RotateToken(context.Context, int64) (string, error) {
	panic("not implemented")
}
func (f *fakeAgentService) UpdateWebhook(context.Context, int64, string, string) error {
	panic("not implemented")
}
func (f *fakeAgentService) SetWebhookSecret(context.Context, int64, string) error {
	panic("not implemented")
}
func (f *fakeAgentService) HasWebhookSecret(context.Context, int64) (bool, error) {
	panic("not implemented")
}
func (f *fakeAgentService) Delete(context.Context, int64) error { panic("not implemented") }
func (f *fakeAgentService) SetPermissions(context.Context, int64, []agents.AgentPermission) error {
	panic("not implemented")
}
func (f *fakeAgentService) ListPermissions(context.Context, int64) ([]agents.AgentPermission, error) {
	panic("not implemented")
}
func (f *fakeAgentService) ResolveByToken(context.Context, string) (agents.Agent, []agents.AgentPermission, error) {
	panic("not implemented")
}

// fakeBridge records calls so tests can assert on dispatcher behavior. The
// scheduler.PluginBridge surface is large; only the methods the dispatcher
// touches store interesting state.
type fakeBridge struct {
	mu sync.Mutex

	output         map[int64]string
	dispatchStatus map[int64]scheduler.DispatchStatus
	terminalStatus map[int64]scheduler.RunStatus
	terminalError  map[int64]string
}

func newFakeBridge() *fakeBridge {
	return &fakeBridge{
		output:         make(map[int64]string),
		dispatchStatus: make(map[int64]scheduler.DispatchStatus),
		terminalStatus: make(map[int64]scheduler.RunStatus),
		terminalError:  make(map[int64]string),
	}
}

func (b *fakeBridge) LoadEnabledJobs(context.Context) ([]scheduler.JobRecord, error) {
	return nil, nil
}
func (b *fakeBridge) LoadJob(context.Context, int64) (scheduler.JobRecord, error) {
	return scheduler.JobRecord{}, nil
}
func (b *fakeBridge) InsertJobRun(context.Context, scheduler.JobRunRecord) (int64, error) {
	return 0, nil
}
func (b *fakeBridge) UpdateJobRun(_ context.Context, runID int64, status scheduler.RunStatus, errMsg *string, _ int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.terminalStatus[runID] = status
	if errMsg != nil {
		b.terminalError[runID] = *errMsg
	}
	return nil
}
func (b *fakeBridge) UpdateJobLastRun(context.Context, int64, string, scheduler.RunStatus, *string, int64) error {
	return nil
}
func (b *fakeBridge) UpdateJobNextFire(context.Context, int64, *string) error { return nil }
func (b *fakeBridge) PromoteRunToActive(context.Context, int64, string) error { return nil }
func (b *fakeBridge) GetJobRun(context.Context, int64) (scheduler.JobRunRecord, error) {
	return scheduler.JobRunRecord{}, nil
}
func (b *fakeBridge) UpdateJobRunOutput(_ context.Context, runID int64, output string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.output[runID] = output
	return nil
}
func (b *fakeBridge) UpdateJobRunDispatchStatus(_ context.Context, runID int64, status scheduler.DispatchStatus) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dispatchStatus[runID] = status
	return nil
}

// snapshot returns copies of all maps so tests can read without holding the lock.
func (b *fakeBridge) snapshot() (map[int64]string, map[int64]scheduler.DispatchStatus, map[int64]scheduler.RunStatus, map[int64]string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := func(m map[int64]string) map[int64]string {
		o := make(map[int64]string, len(m))
		for k, v := range m {
			o[k] = v
		}
		return o
	}
	dsCp := make(map[int64]scheduler.DispatchStatus, len(b.dispatchStatus))
	for k, v := range b.dispatchStatus {
		dsCp[k] = v
	}
	tsCp := make(map[int64]scheduler.RunStatus, len(b.terminalStatus))
	for k, v := range b.terminalStatus {
		tsCp[k] = v
	}
	return cp(b.output), dsCp, tsCp, cp(b.terminalError)
}

// helper: build a dispatcher wired to the given test server URL.
func newTestDispatcher(t *testing.T, agent agents.Agent, serverURL string, retries int) (webhooks.Dispatcher, eventbus.EventBus, *fakeBridge) {
	t.Helper()
	bus := eventbus.NewInMemoryBus()
	bridge := newFakeBridge()
	agentSvc := &fakeAgentService{byName: map[string]agents.Agent{agent.Name: agent}}
	if serverURL != "" {
		agent.WebhookURL = serverURL
		agentSvc.byName[agent.Name] = agent
	}
	d := webhooks.New(webhooks.Config{
		Bus:             bus,
		Agents:          agentSvc,
		Bridge:          bridge,
		HTTPClient:      &http.Client{Timeout: 2 * time.Second},
		CallbackBaseURL: "http://core.test",
		RetryAttempts:   retries,
		RetryBackoff:    5 * time.Millisecond,
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(d.Stop)
	return d, bus, bridge
}

// emitFire publishes a JobFiredPayload onto the bus and waits a short while
// for the goroutine handler to drain.
func emitFire(t *testing.T, bus eventbus.EventBus, runID int64, agentName, prompt string) {
	t.Helper()
	bus.Emit(context.Background(), eventbus.Event{
		Name: scheduler.EventJobFired,
		Payload: scheduler.JobFiredPayload{
			JobID:           42,
			JobName:         "test-job",
			AssigneeAgentID: agentName,
			Prompt:          prompt,
			TriggeredAt:     time.Now().UTC(),
			RunID:           runID,
		},
	})
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestDispatcher_HappyPathSync(t *testing.T) {
	var hits atomic.Int32
	var gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotSig = r.Header.Get(webhooks.SignatureHeader)
		gotBody, _ = io.ReadAll(r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":"hello world"}`))
	}))
	t.Cleanup(srv.Close)

	agent := agents.Agent{
		ID:            1,
		Name:          "claude",
		WebhookSecret: "supersecret",
		WebhookMode:   agents.WebhookModeSync,
	}
	_, bus, bridge := newTestDispatcher(t, agent, srv.URL, 1)

	emitFire(t, bus, 100, "claude", "say hi")

	waitFor(t, func() bool {
		out, _, _, _ := bridge.snapshot()
		return out[100] == "hello world"
	}, "output written")

	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1", hits.Load())
	}
	if gotSig == "" || gotSig[:7] != "sha256=" {
		t.Errorf("missing/invalid signature header: %q", gotSig)
	}
	if !webhooks.Verify("supersecret", gotBody, gotSig) {
		t.Errorf("server-side signature verification failed")
	}
	var payload webhooks.OutboundPayload
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Prompt != "say hi" || payload.RunID != 100 {
		t.Errorf("payload mismatch: %+v", payload)
	}
	if payload.CallbackURL != "http://core.test/api/runs/100/result" {
		t.Errorf("callback URL = %q", payload.CallbackURL)
	}

	_, ds, ts, _ := bridge.snapshot()
	if ts[100] != scheduler.RunStatusSuccess {
		t.Errorf("terminal status = %q, want success", ts[100])
	}
	if ds[100] != scheduler.DispatchStatusDelivered {
		t.Errorf("dispatch status = %q, want delivered", ds[100])
	}
}

func TestDispatcher_HappyPathAsync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	agent := agents.Agent{
		ID:            2,
		Name:          "asyncbot",
		WebhookSecret: "s",
		WebhookMode:   agents.WebhookModeAsync,
	}
	_, bus, bridge := newTestDispatcher(t, agent, srv.URL, 1)

	emitFire(t, bus, 200, "asyncbot", "p")

	waitFor(t, func() bool {
		_, ds, _, _ := bridge.snapshot()
		return ds[200] == scheduler.DispatchStatusDispatched
	}, "dispatched")

	// Async path must NOT finalize the run: that's the callback's job.
	_, _, ts, _ := bridge.snapshot()
	if _, ok := ts[200]; ok {
		t.Errorf("async run should not be finalized by dispatcher, got terminal status %q", ts[200])
	}
}

func TestDispatcher_RetryOn5xx(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	agent := agents.Agent{Name: "x", WebhookSecret: "s", WebhookMode: agents.WebhookModeAsync}
	_, bus, bridge := newTestDispatcher(t, agent, srv.URL, 3)

	emitFire(t, bus, 300, "x", "p")

	waitFor(t, func() bool {
		_, _, ts, _ := bridge.snapshot()
		return ts[300] == scheduler.RunStatusError
	}, "errored after retries")

	if hits.Load() != 3 {
		t.Errorf("server hits = %d, want 3 (retries)", hits.Load())
	}
}

func TestDispatcher_NoRetryOn4xx(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	agent := agents.Agent{Name: "x", WebhookSecret: "s", WebhookMode: agents.WebhookModeAsync}
	_, bus, bridge := newTestDispatcher(t, agent, srv.URL, 3)

	emitFire(t, bus, 400, "x", "p")

	waitFor(t, func() bool {
		_, _, ts, _ := bridge.snapshot()
		return ts[400] == scheduler.RunStatusError
	}, "errored")

	if hits.Load() != 1 {
		t.Errorf("4xx must not retry; hits = %d", hits.Load())
	}
}

func TestDispatcher_MissingWebhookURL(t *testing.T) {
	agent := agents.Agent{Name: "noconfig", WebhookSecret: "s", WebhookMode: agents.WebhookModeAsync}
	// Pass empty serverURL: agent.WebhookURL stays empty.
	_, bus, bridge := newTestDispatcher(t, agent, "", 3)

	emitFire(t, bus, 500, "noconfig", "p")

	waitFor(t, func() bool {
		_, _, ts, errs := bridge.snapshot()
		return ts[500] == scheduler.RunStatusError && errs[500] != ""
	}, "errored on missing url")
}

func TestDispatcher_UnknownAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("server should not be called when agent is unknown")
	}))
	t.Cleanup(srv.Close)

	agent := agents.Agent{Name: "exists", WebhookSecret: "s", WebhookURL: srv.URL}
	_, bus, bridge := newTestDispatcher(t, agent, srv.URL, 1)

	// Fire with a different name: lookup should fail.
	emitFire(t, bus, 600, "ghost", "p")

	waitFor(t, func() bool {
		_, _, ts, _ := bridge.snapshot()
		return ts[600] == scheduler.RunStatusError
	}, "unknown agent errored")
}

func TestDispatcher_StartTwice(t *testing.T) {
	d := webhooks.New(webhooks.Config{
		Bus:    eventbus.NewInMemoryBus(),
		Agents: &fakeAgentService{byName: map[string]agents.Agent{}},
		Bridge: newFakeBridge(),
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer d.Stop()
	if err := d.Start(context.Background()); err == nil {
		t.Fatal("second Start should fail")
	}
}
