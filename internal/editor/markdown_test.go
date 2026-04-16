package editor_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/editor"
)

// --- PlantUMLEncoder stub ---

type stubEncoder struct {
	encoded string
	err     error
}

func (s *stubEncoder) Encode(src string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.encoded, nil
}

// --- DefaultRenderOptions ---

func TestDefaultRenderOptions(t *testing.T) {
	opts := editor.DefaultRenderOptions("https://example.com/plantuml")
	if !opts.Sanitize {
		t.Error("expected Sanitize=true")
	}
	if opts.PlantUMLBaseURL != "https://example.com/plantuml" {
		t.Errorf("unexpected PlantUMLBaseURL: %q", opts.PlantUMLBaseURL)
	}
}

func TestDefaultRenderOptions_EmptyBase(t *testing.T) {
	opts := editor.DefaultRenderOptions("")
	if opts.PlantUMLBaseURL != "" {
		t.Errorf("expected empty plantuml base URL (local mode), got %q", opts.PlantUMLBaseURL)
	}
}

// --- PlantUMLImgURL ---

func TestPlantUMLImgURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		encoded string
		want    string
	}{
		{
			name:    "standard",
			baseURL: "http://localhost:9999/plantuml",
			encoded: "abc123",
			want:    "http://localhost:9999/plantuml/svg/abc123",
		},
		{
			name:    "trailing slash",
			baseURL: "http://localhost:9999/plantuml/",
			encoded: "abc123",
			want:    "http://localhost:9999/plantuml/svg/abc123",
		},
		{
			name:    "empty encoded",
			baseURL: "http://localhost:9999/plantuml",
			encoded: "",
			want:    "http://localhost:9999/plantuml/svg/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := editor.PlantUMLImgURL(tc.baseURL, tc.encoded)
			if got != tc.want {
				t.Errorf("PlantUMLImgURL(%q, %q) = %q; want %q", tc.baseURL, tc.encoded, got, tc.want)
			}
		})
	}
}

// --- Renderer (NewRenderer) ---

func TestRenderer_PlainMarkdown(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		contains string
	}{
		{
			name:     "heading",
			src:      "# Hello",
			contains: "<h1>Hello</h1>",
		},
		{
			name:     "bold",
			src:      "**bold**",
			contains: "<strong>bold</strong>",
		},
		{
			name:     "italic",
			src:      "_italic_",
			contains: "<em>italic</em>",
		},
		{
			name:     "code inline",
			src:      "`code`",
			contains: "<code>code</code>",
		},
		{
			name:     "link",
			src:      "[link](https://example.com)",
			contains: `href="https://example.com"`,
		},
		{
			name:     "unordered list",
			src:      "- item",
			contains: "<li>item</li>",
		},
		{
			name:     "empty",
			src:      "",
			contains: "",
		},
	}

	r := editor.NewRenderer(nil)
	opts := editor.DefaultRenderOptions("")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html, err := r.Render([]byte(tc.src), opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.contains != "" && !strings.Contains(string(html), tc.contains) {
				t.Errorf("expected output to contain %q, got %q", tc.contains, string(html))
			}
		})
	}
}

func TestRenderer_XSSSanitization(t *testing.T) {
	r := editor.NewRenderer(nil)
	opts := editor.DefaultRenderOptions("")
	opts.Sanitize = true

	cases := []struct {
		name        string
		src         string
		notContains string
	}{
		{
			name:        "script tag",
			src:         "<script>alert('xss')</script>",
			notContains: "<script>",
		},
		{
			name:        "onerror attribute",
			src:         `<img src=x onerror="alert(1)">`,
			notContains: "onerror",
		},
		{
			name:        "javascript href",
			src:         `[click me](javascript:alert(1))`,
			notContains: "javascript:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html, err := r.Render([]byte(tc.src), opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(string(html), tc.notContains) {
				t.Errorf("expected sanitized output to NOT contain %q, got %q", tc.notContains, string(html))
			}
		})
	}
}

func TestRenderer_SanitizeDisabled(t *testing.T) {
	r := editor.NewRenderer(nil)
	opts := editor.DefaultRenderOptions("")
	opts.Sanitize = false

	// Raw HTML should pass through when sanitize is off
	src := "<strong>raw</strong>"
	html, err := r.Render([]byte(src), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(html), "raw") {
		t.Errorf("expected raw content to appear, got %q", string(html))
	}
}

func TestRenderer_PlantUMLBlock_NilEncoder(t *testing.T) {
	r := editor.NewRenderer(nil)
	opts := editor.DefaultRenderOptions("")

	src := "```plantuml\n@startuml\nA -> B\n@enduml\n```"
	html, err := r.Render([]byte(src), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With nil encoder, should render as <pre>
	output := string(html)
	if strings.Contains(output, "<img") {
		t.Errorf("expected no <img> tag when encoder is nil, got %q", output)
	}
	if !strings.Contains(output, "<pre") {
		t.Errorf("expected <pre> tag when encoder is nil, got %q", output)
	}
}

func TestRenderer_PlantUMLBlock_WithLocalServer(t *testing.T) {
	enc := &stubEncoder{encoded: "ABC123"}
	r := editor.NewRenderer(enc)
	// Local PlantUML server: renders as <img>
	opts := editor.RenderOptions{Sanitize: true, PlantUMLBaseURL: "http://localhost:8080/plantuml"}

	src := "```plantuml\n@startuml\nA -> B\n@enduml\n```"
	html, err := r.Render([]byte(src), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := string(html)
	if !strings.Contains(output, "<img") {
		t.Errorf("expected <img> tag for local server, got %q", output)
	}
	if !strings.Contains(output, "ABC123") {
		t.Errorf("expected encoded string in output, got %q", output)
	}
}

func TestRenderer_PlantUMLBlock_DefaultFallback(t *testing.T) {
	enc := &stubEncoder{encoded: "ABC123"}
	r := editor.NewRenderer(enc)
	// Default public URL: should render as styled code block (no external calls)
	opts := editor.DefaultRenderOptions("")

	src := "```plantuml\n@startuml\nA -> B\n@enduml\n```"
	html, err := r.Render([]byte(src), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := string(html)
	if !strings.Contains(output, "plantuml-block") {
		t.Errorf("expected plantuml-block class for default (no external), got %q", output)
	}
	if strings.Contains(output, "<img") {
		t.Errorf("should NOT render <img> with default public URL, got %q", output)
	}
}

func TestRenderer_PlantUMLBlock_EncoderError(t *testing.T) {
	enc := &stubEncoder{err: errors.New("encode failure")}
	r := editor.NewRenderer(enc)
	// Local server but encoder fails: still renders as styled block
	opts := editor.RenderOptions{Sanitize: true, PlantUMLBaseURL: "http://localhost:8080/plantuml"}

	src := "```plantuml\n@startuml\nA -> B\n@enduml\n```"
	html, err := r.Render([]byte(src), opts)
	if err != nil {
		t.Fatalf("unexpected error from Render: %v", err)
	}
	output := string(html)
	// Should render as styled block fallback
	if !strings.Contains(output, "plantuml-block") {
		t.Errorf("expected plantuml-block fallback on encoder error, got %q", output)
	}
}

func TestRenderer_NilBytes(t *testing.T) {
	r := editor.NewRenderer(nil)
	opts := editor.DefaultRenderOptions("")
	html, err := r.Render(nil, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = html
}

func TestErrPlantUMLEncode(t *testing.T) {
	if editor.ErrPlantUMLEncode == nil {
		t.Error("ErrPlantUMLEncode should not be nil")
	}
	if editor.ErrPlantUMLEncode.Error() == "" {
		t.Error("ErrPlantUMLEncode should have a non-empty message")
	}
}

// --- EditorMode constants ---

func TestEditorModeConstants(t *testing.T) {
	if editor.ModeEdit != "edit" {
		t.Errorf("ModeEdit = %q; want %q", editor.ModeEdit, "edit")
	}
	if editor.ModePreview != "preview" {
		t.Errorf("ModePreview = %q; want %q", editor.ModePreview, "preview")
	}
	if editor.ModeSplit != "split" {
		t.Errorf("ModeSplit = %q; want %q", editor.ModeSplit, "split")
	}
}
