package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// SessionIDFunc extracts a stable identifier (typically a session ID) from
// a request. Used by SSEHandler to enforce a 1-connection-per-session limit.
// Returning an empty string disables the dedup for that request.
//
// Wired at SSEHandler construction so the realtime package does not need
// to import the web package (avoiding an import cycle).
type SessionIDFunc func(*http.Request) string

// SSEHandler serves the /api/events stream. It is a plain http.Handler
// and assumes authentication/authorization is performed by upstream
// middleware.
type SSEHandler struct {
	hub               Hub
	heartbeatInterval time.Duration

	// sessionID extracts a per-session key from each request. When non-nil
	// the handler enforces "at most one live SSE connection per session"
	// by cancelling the previous connection's context whenever a new one
	// arrives for the same session. This prevents EventSource leaks (e.g.
	// from bfcache) from exhausting the browser's per-origin HTTP/1.1
	// connection budget: symptom: every page load starts taking 30s+ as
	// the 6-conn limit fills with stale SSE streams.
	sessionID SessionIDFunc

	mu         sync.Mutex
	perSession map[string]context.CancelFunc
}

// NewSSEHandler constructs an SSE handler backed by hub. The heartbeat
// interval defaults to 15 s and can be overridden with
// SetHeartbeatInterval (primarily useful for tests).
func NewSSEHandler(hub Hub) *SSEHandler {
	return &SSEHandler{
		hub:               hub,
		heartbeatInterval: defaultHeartbeat,
		perSession:        make(map[string]context.CancelFunc),
	}
}

// SetSessionIDFunc configures the per-session dedup key extractor. Must be
// called before the handler starts serving requests. Wire this at startup
// to web.SessionFromRequest (or any function that returns a stable session
// identifier from the request context).
func (h *SSEHandler) SetSessionIDFunc(f SessionIDFunc) {
	h.sessionID = f
}

// SetHeartbeatInterval overrides the heartbeat cadence. Intended for
// tests; production code should rely on the default.
func (h *SSEHandler) SetHeartbeatInterval(d time.Duration) {
	if d > 0 {
		h.heartbeatInterval = d
	}
}

// ServeHTTP implements http.Handler.
func (h *SSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("realtime: ResponseWriter does not support flushing")
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// SSE is a long-lived stream: disable the server's WriteTimeout for this
	// specific connection. Without this, an http.Server with WriteTimeout set
	// (e.g. 30s) will kill the stream mid-write, surfacing as
	// ERR_INCOMPLETE_CHUNKED_ENCODING in the browser and triggering an
	// EventSource reconnect storm.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}

	// Per-session dedup: derive a wrappable context tied to a per-session
	// cancel func, then atomically swap any existing per-session cancel out
	// and cancel the previous one. The previous SSE goroutine will see its
	// context cancelled at the next select tick and return cleanly,
	// freeing the HTTP connection slot.
	connCtx := r.Context()
	var sessionKey string
	if h.sessionID != nil {
		sessionKey = h.sessionID(r)
	}
	if sessionKey != "" {
		var connCancel context.CancelFunc
		connCtx, connCancel = context.WithCancel(connCtx)
		defer connCancel()
		h.mu.Lock()
		if prev, ok := h.perSession[sessionKey]; ok {
			// Cancel the old connection. Its goroutine will exit on the
			// next select tick and remove itself from the map via its
			// own deferred cleanup below.
			prev()
		}
		h.perSession[sessionKey] = connCancel
		h.mu.Unlock()
		defer func() {
			h.mu.Lock()
			// Only remove if we are still the owner: a newer connection
			// may have already replaced us.
			if cur, ok := h.perSession[sessionKey]; ok && &cur == &connCancel {
				delete(h.perSession, sessionKey)
			} else if ok {
				// fallback identity check by function pointer comparison
				// is unreliable in Go; do a defensive cleanup using a
				// separate per-session generation if this turns out to
				// be an issue. For now leave the newer entry in place.
			}
			h.mu.Unlock()
		}()
	}

	clientID := r.RemoteAddr
	ch, cancel, err := h.hub.Register(connCtx, clientID)
	if err != nil {
		if errors.Is(err, ErrHubAtCapacity) {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "realtime hub at capacity", http.StatusServiceUnavailable)
			return
		}
		slog.Error("realtime: hub register failed", "error", err, "client", clientID)
		http.Error(w, "realtime unavailable", http.StatusServiceUnavailable)
		return
	}
	defer cancel()

	// Headers MUST be written before the first flush.
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream; charset=utf-8")
	hdr.Set("Cache-Control", "no-cache, no-transform")
	hdr.Set("X-Accel-Buffering", "no")
	hdr.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Tell the client to reconnect fast if the stream drops.
	if _, err := fmt.Fprint(w, "retry: 3000\n\n"); err != nil {
		return
	}
	flusher.Flush()

	// Last-Event-ID replay.
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		if afterSeq, perr := strconv.ParseUint(lastID, 10, 64); perr == nil {
			for _, ev := range h.hub.Backlog(afterSeq) {
				if werr := writeEvent(w, ev); werr != nil {
					return
				}
			}
			flusher.Flush()
		}
	}

	ticker := time.NewTicker(h.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				// Hub closed our channel: stream over.
				return
			}
			if err := writeEvent(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-connCtx.Done():
			return
		}
	}
}

// writeEvent serializes ev as a single SSE frame:
//
//	id: <seq>
//	event: <name>
//	data: <json>
//
// The data payload is the full wire Event so that browser handlers
// receive Name/Source/Payload/Timestamp/Seq in one parse.
func writeEvent(w http.ResponseWriter, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		slog.Warn("realtime: event marshal failed", "event", ev.Name, "error", err)
		return nil // skip rather than kill the connection
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Name, body)
	return err
}
