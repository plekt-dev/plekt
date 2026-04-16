package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/plekt-dev/plekt/internal/web"
)

func TestDefaultCSRFProvider_TokenForSession(t *testing.T) {
	t.Parallel()
	p := web.NewCSRFProvider()

	entry := web.WebSessionEntry{
		ID:        "abc123",
		CSRFToken: "mytoken12345678901234567890123456",
	}

	got := p.TokenForSession(entry)
	if got != entry.CSRFToken {
		t.Errorf("TokenForSession = %q, want %q", got, entry.CSRFToken)
	}
}

func TestDefaultCSRFProvider_Validate(t *testing.T) {
	t.Parallel()
	p := web.NewCSRFProvider()

	entry := web.WebSessionEntry{
		ID:        "sessionid1234567890123456789012",
		CSRFToken: "validtoken1234567890123456789012",
	}

	tests := []struct {
		name      string
		submitted string
		wantErr   error
	}{
		{
			name:      "valid token",
			submitted: "validtoken1234567890123456789012",
			wantErr:   nil,
		},
		{
			name:      "invalid token",
			submitted: "wrongtoken1234567890123456789012",
			wantErr:   web.ErrCSRFTokenInvalid,
		},
		{
			name:      "empty submitted token",
			submitted: "",
			wantErr:   web.ErrCSRFTokenInvalid,
		},
		{
			name:      "partial token",
			submitted: "validtoken",
			wantErr:   web.ErrCSRFTokenInvalid,
		},
		{
			name:      "almost correct token",
			submitted: "validtoken1234567890123456789013",
			wantErr:   web.ErrCSRFTokenInvalid,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := p.Validate(entry, tc.submitted)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestDefaultCSRFProvider_ConstantTimeCompare(t *testing.T) {
	// This test ensures that the comparison is not short-circuiting on length.
	// We test that a token of different length still returns an error, not a panic.
	t.Parallel()
	p := web.NewCSRFProvider()

	entry := web.WebSessionEntry{
		ID:        "sessionid1234567890123456789012",
		CSRFToken: "aabbccdd1234567890abcdef12345678",
	}

	// Length-mismatch cases
	cases := []string{
		"",
		"short",
		"aabbccdd1234567890abcdef1234567",   // 31 chars
		"aabbccdd1234567890abcdef123456789", // 33 chars
	}

	for _, submitted := range cases {
		err := p.Validate(entry, submitted)
		if err == nil {
			t.Errorf("expected ErrCSRFTokenInvalid for %q, got nil", submitted)
		}
	}
}

func TestCSRFTokenFromRequest(t *testing.T) {
	t.Parallel()

	sessions, err := web.NewInMemoryWebSessionStore(func() int { return 0 })
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	defer sessions.Close()
	csrf := web.NewCSRFProvider()

	entry, err := sessions.Create("127.0.0.1", 0, "", "", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	t.Run("valid session cookie returns token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "mc_session", Value: entry.ID})
		got := web.CSRFTokenFromRequest(req, sessions, csrf)
		if got == "" {
			t.Error("expected non-empty CSRF token, got empty")
		}
		if got != entry.CSRFToken {
			t.Errorf("CSRFToken = %q, want %q", got, entry.CSRFToken)
		}
	})

	t.Run("no cookie returns empty", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		got := web.CSRFTokenFromRequest(req, sessions, csrf)
		if got != "" {
			t.Errorf("expected empty token, got %q", got)
		}
	})

	t.Run("invalid session ID returns empty", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "mc_session", Value: "nonexistent"})
		got := web.CSRFTokenFromRequest(req, sessions, csrf)
		if got != "" {
			t.Errorf("expected empty token, got %q", got)
		}
	})
}
