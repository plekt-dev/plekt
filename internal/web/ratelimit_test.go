package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginRateLimiter_AllowsUpToMax(t *testing.T) {
	rl := NewLoginRateLimiter(5, 15*time.Minute)
	ip := "192.168.1.1"

	for i := 0; i < 5; i++ {
		allowed, _ := rl.Allow(ip)
		if !allowed {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}

	// 6th attempt should be blocked.
	allowed, retryAfter := rl.Allow(ip)
	if allowed {
		t.Error("6th attempt should be blocked")
	}
	if retryAfter <= 0 {
		t.Errorf("retryAfter should be positive, got %d", retryAfter)
	}
}

func TestLoginRateLimiter_DifferentIPsIndependent(t *testing.T) {
	rl := NewLoginRateLimiter(2, 15*time.Minute)

	for i := 0; i < 2; i++ {
		ok, _ := rl.Allow("10.0.0.1")
		if !ok {
			t.Fatalf("10.0.0.1 attempt %d should be allowed", i+1)
		}
	}

	// 10.0.0.1 is now blocked
	ok, _ := rl.Allow("10.0.0.1")
	if ok {
		t.Error("10.0.0.1 should be blocked")
	}

	// 10.0.0.2 should still be allowed
	ok, _ = rl.Allow("10.0.0.2")
	if !ok {
		t.Error("10.0.0.2 should be allowed")
	}
}

func TestLoginRateLimiter_WindowExpiry(t *testing.T) {
	rl := NewLoginRateLimiter(2, 100*time.Millisecond)
	// Use a fake clock so we can advance time.
	now := time.Now()
	rl.nowFunc = func() time.Time { return now }

	ip := "1.2.3.4"
	rl.Allow(ip)
	rl.Allow(ip)

	// Blocked
	ok, _ := rl.Allow(ip)
	if ok {
		t.Error("should be blocked before window expires")
	}

	// Advance past window
	now = now.Add(200 * time.Millisecond)
	ok, _ = rl.Allow(ip)
	if !ok {
		t.Error("should be allowed after window expires")
	}
}

func TestLoginRateLimitMiddleware_HTTP429(t *testing.T) {
	rl := NewLoginRateLimiter(2, 15*time.Minute)
	handler := LoginRateLimitMiddleware(rl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 2 requests pass
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	// 3rd request returns 429
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestExtractIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	ip := extractIP(req)
	if ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}
}

func TestExtractIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "10.0.0.1:9999"
	ip := extractIP(req)
	if ip != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", ip)
	}
}
