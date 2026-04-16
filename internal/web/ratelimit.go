package web

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimitEntry tracks login attempts for a single IP address.
type rateLimitEntry struct {
	count       int
	windowStart time.Time
}

// LoginRateLimiter enforces per-IP rate limiting on login attempts.
type LoginRateLimiter struct {
	mu          sync.Mutex
	entries     map[string]*rateLimitEntry
	maxAttempts int
	window      time.Duration
	nowFunc     func() time.Time // injectable clock for testing
}

// NewLoginRateLimiter creates a rate limiter allowing maxAttempts per window per IP.
func NewLoginRateLimiter(maxAttempts int, window time.Duration) *LoginRateLimiter {
	return &LoginRateLimiter{
		entries:     make(map[string]*rateLimitEntry),
		maxAttempts: maxAttempts,
		window:      window,
		nowFunc:     time.Now,
	}
}

// extractIP returns the IP portion of the remote address, stripping the port.
func extractIP(r *http.Request) string {
	// Prefer X-Forwarded-For if present (reverse proxy scenario).
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Allow checks whether the IP is within the rate limit. Returns remaining
// seconds in the window if blocked (second return value > 0).
func (rl *LoginRateLimiter) Allow(ip string) (allowed bool, retryAfter int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFunc()
	entry, ok := rl.entries[ip]
	if !ok {
		rl.entries[ip] = &rateLimitEntry{count: 1, windowStart: now}
		return true, 0
	}

	// Window expired: reset.
	if now.Sub(entry.windowStart) >= rl.window {
		entry.count = 1
		entry.windowStart = now
		return true, 0
	}

	if entry.count >= rl.maxAttempts {
		remaining := int(rl.window.Seconds() - now.Sub(entry.windowStart).Seconds())
		if remaining < 1 {
			remaining = 1
		}
		return false, remaining
	}

	entry.count++
	return true, 0
}

// LoginRateLimitMiddleware returns an HTTP middleware that rate-limits POST
// requests using the given LoginRateLimiter. Returns 429 Too Many Requests
// when the limit is exceeded, with a Retry-After header.
func LoginRateLimitMiddleware(rl *LoginRateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := extractIP(r)
			allowed, retryAfter := rl.Allow(ip)
			if !allowed {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
				http.Error(w, "Too many login attempts. Try again later.", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
