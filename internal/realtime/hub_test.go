package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

func newTestHub(t *testing.T, maxClients, backlog int) (*InMemoryHub, eventbus.EventBus) {
	t.Helper()
	bus := eventbus.NewInMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	h, err := NewInMemoryHub(InMemoryHubConfig{
		Bus:         bus,
		MaxClients:  maxClients,
		BacklogSize: backlog,
	})
	if err != nil {
		t.Fatalf("NewInMemoryHub: %v", err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(h.Stop)
	return h, bus
}

func emit(bus eventbus.EventBus, name string, payload any) {
	bus.Emit(context.Background(), eventbus.Event{
		Name:         name,
		SourcePlugin: "test-plugin",
		Payload:      payload,
	})
}

// receive reads one event or fails after timeout.
func receive(t *testing.T, ch <-chan Event, timeout time.Duration) (Event, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(timeout):
		return Event{}, false
	}
}

func TestHub_FanOut(t *testing.T) {
	h, bus := newTestHub(t, 0, 0)

	const n = 3
	chans := make([]<-chan Event, n)
	for i := 0; i < n; i++ {
		ch, cancel, err := h.Register(context.Background(), "c")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		t.Cleanup(cancel)
		chans[i] = ch
	}

	// Emit the two events and wait briefly between them so the bus'
	// per-emit goroutines do not race: the in-memory bus does not
	// guarantee inter-emit ordering.
	emit(bus, eventbus.EventTaskCreated, eventbus.TaskCreatedPayload{TaskID: 1, Title: "a"})
	time.Sleep(20 * time.Millisecond)
	emit(bus, eventbus.EventTaskUpdated, eventbus.TaskUpdatedPayload{TaskID: 1, NewStatus: "done"})

	for i, ch := range chans {
		ev1, ok := receive(t, ch, 500*time.Millisecond)
		if !ok {
			t.Fatalf("client %d: no first event", i)
		}
		if ev1.Name != eventbus.EventTaskCreated || ev1.Source != "test-plugin" {
			t.Errorf("client %d: unexpected first event %+v", i, ev1)
		}
		if ev1.Seq == 0 {
			t.Errorf("client %d: seq not assigned", i)
		}
		var body map[string]any
		if err := json.Unmarshal(ev1.Payload, &body); err != nil {
			t.Errorf("client %d: payload parse: %v", i, err)
		}

		ev2, ok := receive(t, ch, 500*time.Millisecond)
		if !ok {
			t.Fatalf("client %d: no second event", i)
		}
		if ev2.Name != eventbus.EventTaskUpdated {
			t.Errorf("client %d: wrong second event: %s", i, ev2.Name)
		}
		if ev2.Seq <= ev1.Seq {
			t.Errorf("client %d: seq not monotonic: %d -> %d", i, ev1.Seq, ev2.Seq)
		}
	}
}

func TestHub_AllowlistFilter(t *testing.T) {
	h, bus := newTestHub(t, 0, 0)
	ch, cancel, err := h.Register(context.Background(), "c")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(cancel)

	// plugin.loaded is NOT in the allowlist.
	emit(bus, eventbus.EventPluginLoaded, eventbus.PluginLoadedPayload{Name: "x"})
	if _, ok := receive(t, ch, 50*time.Millisecond); ok {
		t.Fatalf("received disallowed event")
	}

	emit(bus, eventbus.EventTaskUpdated, eventbus.TaskUpdatedPayload{TaskID: 7})
	ev, ok := receive(t, ch, 500*time.Millisecond)
	if !ok {
		t.Fatalf("expected allowed event, got nothing")
	}
	if ev.Name != eventbus.EventTaskUpdated {
		t.Errorf("wrong event: %s", ev.Name)
	}
}

func TestHub_SlowClientDropsOldest(t *testing.T) {
	h, bus := newTestHub(t, 0, 0)
	ch, cancel, err := h.Register(context.Background(), "slow")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(cancel)

	const total = 20
	for i := 0; i < total; i++ {
		emit(bus, eventbus.EventTaskUpdated, eventbus.TaskUpdatedPayload{TaskID: int64(i + 1)})
		time.Sleep(2 * time.Millisecond) // preserve inter-emit ordering
	}

	// Wait for bus to deliver all events into the hub.
	time.Sleep(200 * time.Millisecond)

	// Drain whatever the channel holds now.
	got := make([]Event, 0, clientChanBuffer)
drain:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break drain
			}
			got = append(got, ev)
		default:
			break drain
		}
	}
	if len(got) == 0 || len(got) > clientChanBuffer {
		t.Fatalf("unexpected received count: %d (buffer=%d)", len(got), clientChanBuffer)
	}
	// Seqs must be strictly increasing.
	for i := 1; i < len(got); i++ {
		if got[i].Seq <= got[i-1].Seq {
			t.Fatalf("non-monotonic seq at %d: %d -> %d", i, got[i-1].Seq, got[i].Seq)
		}
	}
	// The latest event (seq==total) must have been delivered: we
	// drop OLDEST, not newest.
	if got[len(got)-1].Seq != uint64(total) {
		t.Fatalf("latest seq = %d; want %d", got[len(got)-1].Seq, total)
	}
}

func TestHub_MaxClients(t *testing.T) {
	h, _ := newTestHub(t, 2, 0)
	_, c1, err := h.Register(context.Background(), "a")
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	defer c1()
	_, c2, err := h.Register(context.Background(), "b")
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	defer c2()
	if _, _, err := h.Register(context.Background(), "c"); err != ErrHubAtCapacity {
		t.Fatalf("third register err = %v; want ErrHubAtCapacity", err)
	}
}

func TestHub_StopClosesClients(t *testing.T) {
	h, _ := newTestHub(t, 0, 0)
	ch, cancel, err := h.Register(context.Background(), "c")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(cancel)

	h.Stop()

	deadline := time.After(500 * time.Millisecond)
	sawClose := false
	for !sawClose {
		select {
		case _, ok := <-ch:
			if !ok {
				sawClose = true
			}
			// drain any shutdown event first
		case <-deadline:
			t.Fatal("channel not closed after Stop")
		}
	}

	// Second Stop must be a no-op.
	h.Stop()
}

func TestHub_PayloadSizeCap(t *testing.T) {
	h, bus := newTestHub(t, 0, 0)
	ch, cancel, err := h.Register(context.Background(), "c")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(cancel)

	// Emit an over-cap event via a raw json.RawMessage payload.
	bigStr := strings.Repeat("x", MaxPayloadBytes+16)
	bigPayload := map[string]string{"blob": bigStr}
	emit(bus, eventbus.EventTaskUpdated, bigPayload)
	if _, ok := receive(t, ch, 100*time.Millisecond); ok {
		t.Fatalf("over-cap event was delivered")
	}

	// A normal event after the drop must still arrive.
	emit(bus, eventbus.EventTaskUpdated, eventbus.TaskUpdatedPayload{TaskID: 99})
	ev, ok := receive(t, ch, 500*time.Millisecond)
	if !ok {
		t.Fatalf("normal event not delivered")
	}
	if ev.Name != eventbus.EventTaskUpdated {
		t.Errorf("wrong event: %s", ev.Name)
	}
	// Sanity: the payload should clearly not be the 64KB blob.
	if bytes.Contains(ev.Payload, []byte("xxxxxxxxxxx")) {
		t.Errorf("unexpected payload content")
	}
}

func TestHub_LastEventIDReplay(t *testing.T) {
	h, bus := newTestHub(t, 0, 8)

	emit(bus, eventbus.EventTaskCreated, eventbus.TaskCreatedPayload{TaskID: 1})
	time.Sleep(20 * time.Millisecond)
	emit(bus, eventbus.EventTaskUpdated, eventbus.TaskUpdatedPayload{TaskID: 1})
	time.Sleep(20 * time.Millisecond)
	emit(bus, eventbus.EventTaskCompleted, eventbus.TaskCompletedPayload{TaskID: 1})

	// Give the bus a moment to deliver into the backlog.
	time.Sleep(100 * time.Millisecond)

	backlog := h.Backlog(1) // everything with Seq > 1 ⇒ seqs 2 and 3
	if len(backlog) != 2 {
		t.Fatalf("Backlog(1) len = %d; want 2 (seqs=%v)", len(backlog), seqs(backlog))
	}
	if backlog[0].Seq != 2 || backlog[1].Seq != 3 {
		t.Fatalf("unexpected backlog seqs: %v", seqs(backlog))
	}
}

// TestHub_ConcurrentEmitAndStop stresses the Stop-vs-fanOut interleaving.
// Prior to the fix, Stop could close a client channel while an in-flight
// handleBusEvent was about to send on it, causing "send on closed channel" panic.
func TestHub_ConcurrentEmitAndStop(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		bus := eventbus.NewInMemoryBus()
		hub, err := NewInMemoryHub(InMemoryHubConfig{Bus: bus})
		if err != nil {
			t.Fatalf("iter %d: new hub: %v", iter, err)
		}
		if err := hub.Start(context.Background()); err != nil {
			t.Fatalf("iter %d: start: %v", iter, err)
		}
		// Register several clients.
		cancels := make([]func(), 0, 8)
		for i := 0; i < 8; i++ {
			_, cancel, err := hub.Register(context.Background(), fmt.Sprintf("c%d", i))
			if err != nil {
				t.Fatalf("iter %d: register: %v", iter, err)
			}
			cancels = append(cancels, cancel)
		}
		// Concurrent burst of allowed events + Stop.
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				bus.Emit(context.Background(), eventbus.Event{
					Name:         eventbus.EventTaskUpdated,
					SourcePlugin: "tasks-plugin",
					Payload:      map[string]any{"i": j},
				})
			}
		}()
		// Give emitter a head start so some goroutines are in flight.
		time.Sleep(time.Microsecond * 100)
		hub.Stop()
		wg.Wait()
		for _, c := range cancels {
			c()
		}
		_ = bus.Close()
	}
}

func seqs(evs []Event) []uint64 {
	out := make([]uint64, len(evs))
	for i, e := range evs {
		out[i] = e.Seq
	}
	return out
}
