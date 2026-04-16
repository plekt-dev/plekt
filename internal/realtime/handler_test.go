package realtime

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// noFlushWriter is an http.ResponseWriter that explicitly does NOT
// implement http.Flusher, used to exercise the SSE handler's Flusher
// assertion path.
type noFlushWriter struct {
	header http.Header
	status int
}

func (w *noFlushWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *noFlushWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *noFlushWriter) WriteHeader(code int)        { w.status = code }

func TestSSEHandler_NoFlusherReturns500(t *testing.T) {
	bus := eventbus.NewInMemoryBus()
	defer bus.Close()
	h, err := NewInMemoryHub(InMemoryHubConfig{Bus: bus})
	if err != nil {
		t.Fatalf("NewInMemoryHub: %v", err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer h.Stop()

	handler := NewSSEHandler(h)
	rec := &noFlushWriter{}
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	handler.ServeHTTP(rec, req)

	if rec.status != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rec.status)
	}
}

// startTestSSEServer spins up an httptest.Server that serves the SSE
// handler directly (no middleware; auth is tested separately in
// router_test.go).
func startTestSSEServer(t *testing.T, heartbeat time.Duration) (*httptest.Server, *InMemoryHub, eventbus.EventBus) {
	t.Helper()
	bus := eventbus.NewInMemoryBus()
	h, err := NewInMemoryHub(InMemoryHubConfig{Bus: bus})
	if err != nil {
		t.Fatalf("NewInMemoryHub: %v", err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	handler := NewSSEHandler(h)
	if heartbeat > 0 {
		handler.SetHeartbeatInterval(heartbeat)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		srv.Close()
		h.Stop()
		_ = bus.Close()
	})
	return srv, h, bus
}

func TestSSEHandler_HeartbeatWritten(t *testing.T) {
	srv, _, _ := startTestSSEServer(t, 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q; want text/event-stream", ct)
	}

	buf := make([]byte, 4096)
	var collected strings.Builder
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = resp.Request.Body // no-op
		n, err := resp.Body.Read(buf)
		if n > 0 {
			collected.Write(buf[:n])
			if strings.Contains(collected.String(), ": ping") {
				return // success
			}
		}
		if err != nil {
			break
		}
	}
	if !strings.Contains(collected.String(), ": ping") {
		t.Fatalf("no heartbeat seen; got=%q", collected.String())
	}
}

func TestSSEHandler_EventDelivered(t *testing.T) {
	srv, _, bus := startTestSSEServer(t, 500*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	// Drain the initial retry frame before emitting.
	br := bufio.NewReader(resp.Body)
	readUntilBlankLine(t, br)

	// Give the server a tick to register us with the hub.
	time.Sleep(30 * time.Millisecond)
	bus.Emit(context.Background(), eventbus.Event{
		Name:         eventbus.EventTaskUpdated,
		SourcePlugin: "tasks-plugin",
		Payload:      eventbus.TaskUpdatedPayload{TaskID: 42, NewStatus: "done"},
	})

	frame := readUntilBlankLine(t, br)
	if !strings.Contains(frame, "event: "+eventbus.EventTaskUpdated) {
		t.Fatalf("frame missing event line: %q", frame)
	}
	if !strings.Contains(frame, `"task_id":42`) {
		t.Fatalf("frame missing payload: %q", frame)
	}
	if !strings.Contains(frame, "id: ") {
		t.Fatalf("frame missing id line: %q", frame)
	}
}

func TestSSEHandler_AtCapacityReturns503(t *testing.T) {
	bus := eventbus.NewInMemoryBus()
	defer bus.Close()
	h, err := NewInMemoryHub(InMemoryHubConfig{Bus: bus, MaxClients: 1})
	if err != nil {
		t.Fatalf("NewInMemoryHub: %v", err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer h.Stop()

	// Take the only slot and never release it.
	_, _, err = h.Register(context.Background(), "hog")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	handler := NewSSEHandler(h)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" {
		t.Errorf("missing Retry-After header")
	}
}

// readUntilBlankLine reads one SSE frame (lines until blank line) and
// returns the accumulated text. Fails on timeout / EOF.
func readUntilBlankLine(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var sb strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF && sb.Len() > 0 {
				return sb.String()
			}
			t.Fatalf("read frame: %v (got %q)", err, sb.String())
		}
		sb.WriteString(line)
		if line == "\n" || line == "\r\n" {
			return sb.String()
		}
	}
}
