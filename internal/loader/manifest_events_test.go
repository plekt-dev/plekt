package loader

import (
	"errors"
	"testing"
)

func TestValidateManifestEvents(t *testing.T) {
	type testCase struct {
		name    string
		decl    EventsDeclaration
		wantErr error
	}

	cases := []testCase{
		{
			name:    "empty Emits and Subscribes returns nil",
			decl:    EventsDeclaration{},
			wantErr: nil,
		},
		{
			name: "valid names returns nil",
			decl: EventsDeclaration{
				Emits:      []string{"task.created", "task.deleted"},
				Subscribes: []string{"plugin.loaded"},
			},
			wantErr: nil,
		},
		{
			name: "empty string in Emits returns ErrInvalidEventName",
			decl: EventsDeclaration{
				Emits: []string{"valid.event", ""},
			},
			wantErr: ErrInvalidEventName,
		},
		{
			name: "whitespace-only in Emits returns ErrInvalidEventName",
			decl: EventsDeclaration{
				Emits: []string{"   "},
			},
			wantErr: ErrInvalidEventName,
		},
		{
			name: "whitespace-only in Subscribes returns ErrInvalidEventName",
			decl: EventsDeclaration{
				Subscribes: []string{"\t"},
			},
			wantErr: ErrInvalidEventName,
		},
		{
			name: "empty string in Subscribes returns ErrInvalidEventName",
			decl: EventsDeclaration{
				Subscribes: []string{"good.event", ""},
			},
			wantErr: ErrInvalidEventName,
		},
		{
			name: "multiple valid names returns nil",
			decl: EventsDeclaration{
				Emits:      []string{"a.b", "c.d", "e.f"},
				Subscribes: []string{"x.y", "z.w"},
			},
			wantErr: nil,
		},
		{
			name: "tab-only string in Emits returns ErrInvalidEventName",
			decl: EventsDeclaration{
				Emits: []string{"\t\t"},
			},
			wantErr: ErrInvalidEventName,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateManifestEvents(tc.decl)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("ValidateManifestEvents() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("ValidateManifestEvents() unexpected error: %v", err)
			}
		})
	}
}
