package web

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"
)

// SlowRequestLogMiddleware logs every HTTP request that takes longer than
// the configured threshold. Helps spot which endpoints are blocking under
// load without needing to attach a profiler. Disabled when threshold is 0.
//
// Threshold source: MC_SLOW_LOG_MS env var (milliseconds). Default 500ms
// when MC_SLOW_LOG=1 is set, off otherwise. SSE / static / health are
// excluded because they intentionally run long or are too noisy.
func SlowRequestLogMiddleware() func(http.Handler) http.Handler {
	if os.Getenv("MC_SLOW_LOG") != "1" {
		return func(next http.Handler) http.Handler { return next }
	}
	threshold := 500 * time.Millisecond
	if v := os.Getenv("MC_SLOW_LOG_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			threshold = time.Duration(ms) * time.Millisecond
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isLayoutFreePath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			next.ServeHTTP(w, r)
			elapsed := time.Since(start)
			if elapsed >= threshold {
				slog.Warn("slow http request",
					"method", r.Method,
					"path", r.URL.Path,
					"query", r.URL.RawQuery,
					"elapsed_ms", elapsed.Milliseconds(),
				)
			}
		})
	}
}
