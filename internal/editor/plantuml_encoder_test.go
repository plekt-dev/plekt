package editor_test

import (
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/editor"
)

func TestPlantUMLEncoder_Encode(t *testing.T) {
	enc := editor.NewPlantUMLEncoder()

	cases := []struct {
		name    string
		src     string
		wantErr bool
		check   func(t *testing.T, encoded string)
	}{
		{
			name: "simple diagram",
			src:  "@startuml\nA -> B\n@enduml",
			check: func(t *testing.T, encoded string) {
				if encoded == "" {
					t.Error("expected non-empty encoded string")
				}
				// PlantUML encoding uses only alphanumeric + '-' + '_'
				for _, c := range encoded {
					if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-' || c == '_') {
						t.Errorf("unexpected character %q in encoded string", string(c))
					}
				}
			},
		},
		{
			name: "empty string",
			src:  "",
			check: func(t *testing.T, encoded string) {
				if encoded != "" {
					t.Errorf("expected empty encoded for empty src, got %q", encoded)
				}
			},
		},
		{
			name: "whitespace only",
			src:  "   \n  ",
			check: func(t *testing.T, encoded string) {
				if encoded != "" {
					t.Errorf("expected empty encoded for whitespace src, got %q", encoded)
				}
			},
		},
		{
			name: "complex diagram",
			src:  "@startuml\nactor User\nUser -> System: Request\nSystem --> User: Response\n@enduml",
			check: func(t *testing.T, encoded string) {
				if encoded == "" {
					t.Error("expected non-empty encoded string")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := enc.Encode(tc.src)
			if tc.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, encoded)
			}
		})
	}
}

func TestPlantUMLEncoder_RendersAsImg(t *testing.T) {
	enc := editor.NewPlantUMLEncoder()
	r := editor.NewRenderer(enc)
	opts := editor.DefaultRenderOptions("http://localhost:9999/plantuml")

	src := "```plantuml\n@startuml\nA -> B: Hello\n@enduml\n```"
	html, err := r.Render([]byte(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	output := string(html)

	if !strings.Contains(output, "<img") {
		t.Errorf("expected <img> tag for PlantUML, got:\n%s", output)
	}
	if !strings.Contains(output, "localhost:9999/plantuml/svg/") {
		t.Errorf("expected plantuml SVG URL, got:\n%s", output)
	}
	if strings.Contains(output, "plantuml-error") {
		t.Errorf("unexpected plantuml-error class, got:\n%s", output)
	}
}
