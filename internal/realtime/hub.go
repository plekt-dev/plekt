package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// Hub is the interface consumed by the SSE handler. Implementations fan
// out filtered bus events to registered client channels and maintain a
// small ring-buffer backlog for Last-Event-ID replay.
type Hub interface {
	// Register allocates a new client channel. Callers must invoke the
	// returned cancel function exactly once to release resources.
	Register(ctx context.Context, clientID string) (<-chan Event, func(), error)
	// Start subscribes the hub to the event bus. It is non-blocking and
	// must be called exactly once.
	Start(ctx context.Context) error
	// Stop unsubscribes from the bus and closes every client channel.
	// It is idempotent.
	Stop()
	// Backlog returns events with Seq > afterSeq, in ascending seq
	// order (newest last), for Last-Event-ID replay.
	Backlog(afterSeq uint64) []Event
}

// InMemoryHubConfig configures a new in-memory hub.
type InMemoryHubConfig struct {
	// Bus is the event bus to subscribe to. Required.
	Bus eventbus.EventBus
	// MaxClients caps concurrent SSE connections. 0 ⇒ DefaultMaxClients.
	MaxClients int
	// BacklogSize is the ring buffer capacity for Last-Event-ID replay.
	// 0 ⇒ DefaultBacklogSize. -1 ⇒ backlog disabled.
	BacklogSize int
}

// InMemoryHub is the default Hub implementation. It is safe for
// concurrent use.
type InMemoryHub struct {
	bus         eventbus.EventBus
	maxClients  int
	backlogSize int // 0 means disabled, >0 is the ring capacity

	seq         atomic.Uint64
	started     atomic.Bool
	stopped     atomic.Bool
	sub         eventbus.Subscription
	clientCount atomic.Int64

	// processMu serializes bookkeeping inside handleBusEvent. The
	// event bus delivers each emit in its own goroutine, so without
	// this lock concurrent bus emits would race on the backlog ring
	// and on individual client channels.
	processMu sync.Mutex

	clientsMu  sync.RWMutex
	nextClient uint64
	clients    map[uint64]chan Event

	backlogMu sync.Mutex
	backlog   []Event // ring
	backHead  int     // index of next write slot
	backLen   int
}

// NewInMemoryHub constructs an InMemoryHub. It does NOT subscribe to the
// event bus; call Start for that.
func NewInMemoryHub(cfg InMemoryHubConfig) (*InMemoryHub, error) {
	if cfg.Bus == nil {
		return nil, errNilBus
	}
	max := cfg.MaxClients
	if max <= 0 {
		max = DefaultMaxClients
	}
	back := cfg.BacklogSize
	switch {
	case back == 0:
		back = DefaultBacklogSize
	case back < 0:
		back = 0 // disabled
	}
	h := &InMemoryHub{
		bus:         cfg.Bus,
		maxClients:  max,
		backlogSize: back,
		clients:     make(map[uint64]chan Event),
	}
	if back > 0 {
		h.backlog = make([]Event, back)
	}
	return h, nil
}

// sentinel for NewInMemoryHub: declared here (not in errors.go) because
// it is a constructor-only error that never escapes the package.
var errNilBus = errSentinel("realtime: bus is required")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// Start subscribes the hub to the "*" wildcard event on the bus. It is
// non-blocking; the bus delivers to the handler in its own goroutine.
func (h *InMemoryHub) Start(_ context.Context) error {
	if !h.started.CompareAndSwap(false, true) {
		return ErrHubAlreadyStarted
	}
	h.sub = h.bus.Subscribe("*", h.handleBusEvent)
	return nil
}

// Stop unsubscribes the hub from the bus, delivers a synthetic shutdown
// event to every connected client, and closes all client channels.
// Stop is idempotent.
func (h *InMemoryHub) Stop() {
	if !h.stopped.CompareAndSwap(false, true) {
		return
	}
	if h.started.Load() {
		h.bus.Unsubscribe(h.sub)
	}
	// Wait for any in-flight handleBusEvent to finish. processMu guards
	// the full fan-out (including sends on client channels). Without
	// holding it here, a concurrent in-flight fan-out: already past its
	// stopped-check and holding a local slice of client channels: could
	// send on a channel we are about to close, panicking with "send on
	// closed channel". Acquiring processMu before closing channels
	// serialises us after any such in-flight handler. The bus has
	// already been unsubscribed above, so no new handleBusEvent
	// invocations will be started.
	h.processMu.Lock()
	defer h.processMu.Unlock()
	shutdownEvt := Event{
		Name:      ShutdownEventName,
		Source:    "realtime",
		Payload:   json.RawMessage(`{}`),
		Timestamp: time.Now().UTC(),
		Seq:       h.seq.Add(1),
	}
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()
	for id, ch := range h.clients {
		// Best-effort: try to send the shutdown frame, then close.
		select {
		case ch <- shutdownEvt:
		default:
			// channel full, drop the frame: the close still unblocks
			// the reader below.
		}
		close(ch)
		delete(h.clients, id)
		h.clientCount.Add(-1)
	}
}

// Register allocates a new buffered channel for a client. The returned
// cancel function must be invoked exactly once; subsequent calls are
// no-ops. cancel is safe to call after Stop.
func (h *InMemoryHub) Register(_ context.Context, clientID string) (<-chan Event, func(), error) {
	if h.stopped.Load() {
		return nil, nil, ErrHubNotStarted
	}
	if h.clientCount.Load() >= int64(h.maxClients) {
		return nil, nil, ErrHubAtCapacity
	}
	ch := make(chan Event, clientChanBuffer)

	h.clientsMu.Lock()
	// Re-check capacity under lock to avoid a benign race past the cap.
	// Do this BEFORE allocating an ID so rejected callers do not burn
	// a client-id slot.
	if h.clientCount.Load() >= int64(h.maxClients) {
		h.clientsMu.Unlock()
		return nil, nil, ErrHubAtCapacity
	}
	h.nextClient++
	id := h.nextClient
	h.clients[id] = ch
	h.clientCount.Add(1)
	h.clientsMu.Unlock()

	_ = clientID // reserved for future per-client labelling / metrics
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.clientsMu.Lock()
			if existing, ok := h.clients[id]; ok {
				delete(h.clients, id)
				h.clientCount.Add(-1)
				h.clientsMu.Unlock()
				close(existing)
				return
			}
			h.clientsMu.Unlock()
		})
	}
	return ch, cancel, nil
}

// Backlog returns every buffered event with Seq > afterSeq, oldest
// first. The returned slice is a copy and safe to mutate.
func (h *InMemoryHub) Backlog(afterSeq uint64) []Event {
	if h.backlogSize == 0 {
		return nil
	}
	h.backlogMu.Lock()
	defer h.backlogMu.Unlock()
	if h.backLen == 0 {
		return nil
	}
	out := make([]Event, 0, h.backLen)
	// oldest element is at (backHead - backLen) mod size
	start := (h.backHead - h.backLen + h.backlogSize) % h.backlogSize
	for i := 0; i < h.backLen; i++ {
		ev := h.backlog[(start+i)%h.backlogSize]
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out
}

// handleBusEvent is the subscription callback. Runs in a goroutine
// owned by the bus.
func (h *InMemoryHub) handleBusEvent(_ context.Context, e eventbus.Event) {
	if h.stopped.Load() {
		return
	}
	if !IsAllowed(e.Name) {
		return
	}
	// Serialize seq assignment, backlog append, and fan-out so that
	// concurrent deliveries from the bus cannot interleave their
	// writes to the ring buffer or to any one client channel.
	h.processMu.Lock()
	defer h.processMu.Unlock()
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		slog.Warn("realtime: payload marshal failed", "event", e.Name, "error", err)
		return
	}
	if len(payload) > MaxPayloadBytes {
		slog.Warn("realtime: payload over cap, dropping",
			"event", e.Name, "size", len(payload), "cap", MaxPayloadBytes)
		return
	}
	evt := Event{
		Name:      e.Name,
		Source:    e.SourcePlugin,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
		Seq:       h.seq.Add(1),
	}
	h.pushBacklog(evt)
	h.fanOut(evt)
}

func (h *InMemoryHub) pushBacklog(evt Event) {
	if h.backlogSize == 0 {
		return
	}
	h.backlogMu.Lock()
	h.backlog[h.backHead] = evt
	h.backHead = (h.backHead + 1) % h.backlogSize
	if h.backLen < h.backlogSize {
		h.backLen++
	}
	h.backlogMu.Unlock()
}

// fanOut delivers evt to every client channel non-blockingly. If a
// channel is full we drop the oldest buffered event and retry once;
// if still full we give up on that client rather than block the bus.
func (h *InMemoryHub) fanOut(evt Event) {
	h.clientsMu.RLock()
	chans := make([]chan Event, 0, len(h.clients))
	for _, ch := range h.clients {
		chans = append(chans, ch)
	}
	h.clientsMu.RUnlock()

	for _, ch := range chans {
		select {
		case ch <- evt:
		default:
			// Drop-oldest: pull one event off (if any) and retry.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- evt:
			default:
				// Still full: client is hopelessly slow. Skip.
			}
		}
	}
}
