package realtime

import "errors"

// ErrHubAtCapacity is returned by Hub.Register when the hub already has
// the maximum number of clients connected.
var ErrHubAtCapacity = errors.New("realtime: hub at maximum client capacity")

// ErrFlusherUnsupported is returned by the SSE handler when the
// underlying http.ResponseWriter does not support flushing. SSE is
// unusable without a Flusher.
var ErrFlusherUnsupported = errors.New("realtime: ResponseWriter does not implement http.Flusher")

// ErrHubAlreadyStarted is returned by Hub.Start if the hub has already
// been started.
var ErrHubAlreadyStarted = errors.New("realtime: hub already started")

// ErrHubNotStarted is returned by Hub operations that require a started
// hub if the hub has not yet been started.
var ErrHubNotStarted = errors.New("realtime: hub not started")
