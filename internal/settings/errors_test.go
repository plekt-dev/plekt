package settings_test

import (
	"errors"
	"testing"

	"github.com/plekt-dev/plekt/internal/settings"
)

func TestValidate(t *testing.T) {
	valid := settings.Settings{
		AdminEmail:        "admin@example.com",
		SessionTTLMinutes: 60,
		PasswordMinLength: 12,
	}

	tests := []struct {
		name    string
		mutate  func(s *settings.Settings)
		wantErr error
	}{
		{
			name:    "valid full settings",
			mutate:  func(s *settings.Settings) {},
			wantErr: nil,
		},
		{
			name:    "empty admin email allowed",
			mutate:  func(s *settings.Settings) { s.AdminEmail = "" },
			wantErr: nil,
		},
		{
			name:    "invalid admin email",
			mutate:  func(s *settings.Settings) { s.AdminEmail = "notanemail" },
			wantErr: settings.ErrAdminEmailInvalid,
		},
		{
			name:    "invalid admin email missing domain",
			mutate:  func(s *settings.Settings) { s.AdminEmail = "user@" },
			wantErr: settings.ErrAdminEmailInvalid,
		},
		{
			name:    "valid admin email",
			mutate:  func(s *settings.Settings) { s.AdminEmail = "user@domain.com" },
			wantErr: nil,
		},
		{
			name:    "negative session TTL",
			mutate:  func(s *settings.Settings) { s.SessionTTLMinutes = -1 },
			wantErr: settings.ErrSessionTTLNegative,
		},
		{
			name:    "zero session TTL allowed",
			mutate:  func(s *settings.Settings) { s.SessionTTLMinutes = 0 },
			wantErr: nil,
		},
		{
			name:    "negative password min length",
			mutate:  func(s *settings.Settings) { s.PasswordMinLength = -1 },
			wantErr: settings.ErrPasswordMinLengthInvalid,
		},
		{
			name:    "zero password min length invalid",
			mutate:  func(s *settings.Settings) { s.PasswordMinLength = 0 },
			wantErr: nil,
		},
		{
			name:    "positive password min length valid",
			mutate:  func(s *settings.Settings) { s.PasswordMinLength = 8 },
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := valid
			tc.mutate(&s)
			err := settings.Validate(s)
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("Validate() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
