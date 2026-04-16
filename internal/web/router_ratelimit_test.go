package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stubAuthOKHandler implements WebAuthHandler with POST /login returning 200.
type stubAuthOKHandler struct{}

func (s *stubAuthOKHandler) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (s *stubAuthOKHandler) HandleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (s *stubAuthOKHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func hammerLogin(t *testing.T, handler http.Handler, n int) int {
	t.Helper()
	last := 0
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		last = rr.Code
	}
	return last
}

func TestBuildLoginHandler_DefaultRateLimit(t *testing.T) {
	// Ensure env bypass is not set for this test.
	t.Setenv(loginRateLimitDisabledEnv, "")

	r := WebRouter{cfg: WebRouterConfig{Auth: &stubAuthOKHandler{}}}
	handler := r.buildLoginHandler()

	// Defaults: 5 attempts. 6th must be 429.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("attempt %d: got %d, want 200", i+1, rr.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("6th attempt: got %d, want 429", rr.Code)
	}
}

func TestBuildLoginHandler_CustomConfig(t *testing.T) {
	t.Setenv(loginRateLimitDisabledEnv, "")

	r := WebRouter{cfg: WebRouterConfig{
		Auth:                      &stubAuthOKHandler{},
		LoginRateLimitMaxAttempts: 2,
		LoginRateLimitWindow:      1 * time.Minute,
	}}
	handler := r.buildLoginHandler()

	// Custom: 2 attempts. 3rd must be 429.
	hammerLogin(t, handler, 2)
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("3rd attempt with max=2: got %d, want 429", rr.Code)
	}
}

func TestBuildLoginHandler_EnvBypass(t *testing.T) {
	t.Setenv(loginRateLimitDisabledEnv, "1")

	r := WebRouter{cfg: WebRouterConfig{Auth: &stubAuthOKHandler{}}}
	handler := r.buildLoginHandler()

	// With bypass: 100 attempts all succeed.
	code := hammerLogin(t, handler, 100)
	if code != http.StatusOK {
		t.Errorf("100th attempt with bypass: got %d, want 200", code)
	}
}

func TestBuildLoginHandler_EnvBypassOnlyAccepts1(t *testing.T) {
	// Any value other than "1" must NOT disable the limiter.
	t.Setenv(loginRateLimitDisabledEnv, "true")

	r := WebRouter{cfg: WebRouterConfig{Auth: &stubAuthOKHandler{}}}
	handler := r.buildLoginHandler()

	hammerLogin(t, handler, 5)
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("env=\"true\" should not bypass: got %d, want 429", rr.Code)
	}
}
