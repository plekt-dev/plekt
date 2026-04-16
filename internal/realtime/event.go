// Package realtime exposes a server-sent-events (SSE) hub that bridges
// the internal event bus to connected web UI clients. It provides a
// filtered, rate-limited, backpressure-aware fan-out channel for realtime
// UI updates (task moves, pomodoro state, notes changes, etc.).
package realtime

import (
	"encoding/json"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
)

// Event is the wire format delivered to SSE clients. Payload is kept as
// raw JSON so that handlers on the client side can deserialize the
// plugin-specific shape without the hub having to know about it.
type Event struct {
	Name      string          `json:"name"`
	Source    string          `json:"source"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"ts"`
	Seq       uint64          `json:"seq"`
}

// ShutdownEventName is emitted as a synthetic event on each client's
// channel just before Stop closes that channel. Clients may use it to
// distinguish a graceful server shutdown from a network disconnect.
const ShutdownEventName = "mc.shutdown"

// Tunable defaults. They are exported so tests and operators can
// observe the values, but the Hub reads its configuration through
// InMemoryHubConfig rather than from these package globals.
const (
	DefaultMaxClients  = 1000
	DefaultBacklogSize = 64
	MaxPayloadBytes    = 64 * 1024
	clientChanBuffer   = 16
	defaultHeartbeat   = 15 * time.Second
)

// AllowedEvents is the allowlist of event bus names that the realtime
// hub will forward to SSE clients. Anything not in this map is dropped
// silently: the hub is NOT a generic event firehose.
var AllowedEvents = map[string]struct{}{
	eventbus.EventTaskCreated:         {},
	eventbus.EventTaskUpdated:         {},
	eventbus.EventTaskDeleted:         {},
	eventbus.EventTaskCompleted:       {},
	eventbus.EventCommentCreated:      {},
	eventbus.EventCommentDeleted:      {},
	eventbus.EventPomodoroStarted:     {},
	eventbus.EventPomodoroCompleted:   {},
	eventbus.EventPomodoroInterrupted: {},
	eventbus.EventNotesCreated:        {},
	eventbus.EventNotesUpdated:        {},
	eventbus.EventNotesDeleted:        {},
	eventbus.EventProjectCreated:      {},
	eventbus.EventProjectUpdated:      {},
	eventbus.EventProjectArchived:     {},
	eventbus.EventProjectDeleted:      {},
	eventbus.EventCoreUpdateAvailable: {},
}

// IsAllowed reports whether eventName is on the realtime allowlist.
func IsAllowed(eventName string) bool {
	_, ok := AllowedEvents[eventName]
	return ok
}
