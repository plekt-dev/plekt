package editor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/editor"
)

// loadFixture reads a markdown file from the testdata directory.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to load fixture %q: %v", name, err)
	}
	return data
}

// renderFixture renders a fixture file with the real goldmark renderer and default options.
func renderFixture(t *testing.T, name string) string {
	t.Helper()
	src := loadFixture(t, name)
	r := editor.NewRenderer(nil)
	opts := editor.DefaultRenderOptions("")
	html, err := r.Render(src, opts)
	if err != nil {
		t.Fatalf("render %q failed: %v", name, err)
	}
	return string(html)
}

// --- Strikethrough ---

func TestIntegration_Strikethrough(t *testing.T) {
	html := renderFixture(t, "strikethrough.md")

	if !strings.Contains(html, "<del>deleted</del>") {
		t.Errorf("expected <del>deleted</del> in output, got:\n%s", html)
	}
	if !strings.Contains(html, "<del>strikethrough</del>") {
		t.Errorf("expected <del>strikethrough</del> in output, got:\n%s", html)
	}
}

// --- Task Lists ---

func TestIntegration_TaskList(t *testing.T) {
	html := renderFixture(t, "task_list.md")

	// Checked items should have checked attribute
	if !strings.Contains(html, `type="checkbox"`) {
		t.Errorf("expected checkbox inputs in task list, got:\n%s", html)
	}

	// Count checkboxes
	checkboxCount := strings.Count(html, `type="checkbox"`)
	if checkboxCount != 3 {
		t.Errorf("expected 3 checkboxes, got %d in:\n%s", checkboxCount, html)
	}

	// Checked items
	checkedCount := strings.Count(html, "checked")
	if checkedCount < 2 {
		t.Errorf("expected at least 2 checked checkboxes, got %d in:\n%s", checkedCount, html)
	}
}

// --- Footnotes ---

func TestIntegration_Footnotes(t *testing.T) {
	html := renderFixture(t, "footnotes.md")

	// Should contain footnote references (sup links)
	if !strings.Contains(html, "footnote") {
		t.Errorf("expected footnote references in output, got:\n%s", html)
	}

	// Should contain footnote content
	if !strings.Contains(html, "First footnote content") {
		t.Errorf("expected footnote content in output, got:\n%s", html)
	}
	if !strings.Contains(html, "Named footnote content") {
		t.Errorf("expected named footnote content in output, got:\n%s", html)
	}
}

// --- Table with alignment ---

func TestIntegration_TableAlignment(t *testing.T) {
	html := renderFixture(t, "table_advanced.md")

	if !strings.Contains(html, "<table>") {
		t.Errorf("expected <table> in output, got:\n%s", html)
	}
	if !strings.Contains(html, "<th") {
		t.Errorf("expected <th> headers in output, got:\n%s", html)
	}
	// Table should have rows
	tdCount := strings.Count(html, "<td")
	if tdCount < 6 {
		t.Errorf("expected at least 6 <td> cells, got %d in:\n%s", tdCount, html)
	}
}

// --- Mermaid diagrams ---

func TestIntegration_MermaidDiagram(t *testing.T) {
	html := renderFixture(t, "mermaid.md")

	// Mermaid blocks should be rendered as <pre class="mermaid">
	// for client-side rendering, NOT as a plain code block
	if strings.Contains(html, `class="language-mermaid"`) {
		t.Errorf("mermaid block should NOT be rendered as plain code block, got:\n%s", html)
	}

	// Should contain mermaid class for client-side rendering
	if !strings.Contains(html, `class="mermaid"`) {
		t.Errorf("expected mermaid class in output, got:\n%s", html)
	}

	// Should contain diagram source for client-side rendering
	if !strings.Contains(html, "graph TD") {
		t.Errorf("expected diagram source in output, got:\n%s", html)
	}

	// Arrow syntax should be preserved (HTML-escaped for safety, mermaid.js reads textContent)
	if !strings.Contains(html, "Decision") {
		t.Errorf("expected Decision node in mermaid output, got:\n%s", html)
	}
}

func TestIntegration_MermaidPreservesArrowSyntax(t *testing.T) {
	r := editor.NewRenderer(nil)
	opts := editor.DefaultRenderOptions("")

	// Mermaid with arrows that contain HTML-sensitive chars
	src := "```mermaid\ngraph LR\n    A-->|Yes|B\n    B-->C\n```"
	html, err := r.Render([]byte(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	output := string(html)
	t.Logf("Mermaid output:\n%s", output)

	if !strings.Contains(output, `class="mermaid"`) {
		t.Errorf("expected pre.mermaid, got:\n%s", output)
	}
	// The content is HTML-escaped inside <pre>, which is correct.
	// Browser will display --&gt; as --> and mermaid.js reads textContent.
	if !strings.Contains(output, "A--") {
		t.Errorf("expected arrow syntax preserved in mermaid output, got:\n%s", output)
	}
}

func TestIntegration_PlantUML_PreservesContent(t *testing.T) {
	enc := &stubEncoder{encoded: "PLANT_OK"}
	r := editor.NewRenderer(enc)
	opts := editor.DefaultRenderOptions("https://plantuml.local")

	src := "```plantuml\n@startuml\nactor User\nUser -> System: Request\nSystem --> User: Response\n@enduml\n```"
	html, err := r.Render([]byte(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	output := string(html)

	if !strings.Contains(output, "PLANT_OK") {
		t.Errorf("expected encoded PlantUML, got:\n%s", output)
	}
	if !strings.Contains(output, "<img") {
		t.Errorf("expected img tag for PlantUML, got:\n%s", output)
	}
}

// --- All headings ---

func TestIntegration_AllHeadings(t *testing.T) {
	html := renderFixture(t, "headings_all.md")

	for i := 1; i <= 6; i++ {
		tag := "<h" + string(rune('0'+i)) + ">"
		if !strings.Contains(html, tag) {
			t.Errorf("expected %s in output, got:\n%s", tag, html)
		}
	}
}

// --- Definition lists ---

func TestIntegration_DefinitionList(t *testing.T) {
	html := renderFixture(t, "definition_list.md")

	if !strings.Contains(html, "<dl>") {
		t.Errorf("expected <dl> in definition list output, got:\n%s", html)
	}
	if !strings.Contains(html, "<dt>") {
		t.Errorf("expected <dt> in definition list output, got:\n%s", html)
	}
	if !strings.Contains(html, "<dd>") {
		t.Errorf("expected <dd> in definition list output, got:\n%s", html)
	}
	if !strings.Contains(html, "Definition for term 1") {
		t.Errorf("expected definition content in output, got:\n%s", html)
	}
}

// --- Autolinks ---

func TestIntegration_Autolinks(t *testing.T) {
	html := renderFixture(t, "autolinks.md")

	if !strings.Contains(html, `href="https://example.com"`) {
		t.Errorf("expected autolinked URL in output, got:\n%s", html)
	}
	if !strings.Contains(html, `href="mailto:user@example.com"`) {
		t.Errorf("expected autolinked email in output, got:\n%s", html)
	}
}

// --- Highlight ---

func TestIntegration_Highlight(t *testing.T) {
	html := renderFixture(t, "highlight.md")

	if !strings.Contains(html, "<mark>") {
		t.Errorf("expected <mark> tag for ==highlighted== text, got:\n%s", html)
	}
	if !strings.Contains(html, "highlighted") {
		t.Errorf("expected highlighted content in output, got:\n%s", html)
	}
}

// --- Mixed content (full document) ---

func TestIntegration_MixedContent(t *testing.T) {
	html := renderFixture(t, "mixed_content.md")

	checks := []struct {
		label    string
		contains string
	}{
		{"h1", "<h1>"},
		{"h2", "<h2>"},
		{"h3", "<h3>"},
		{"bold", "<strong>"},
		{"italic", "<em>"},
		{"task checkbox", `type="checkbox"`},
		{"strikethrough", "<del>"},
		{"code block", "<pre>"},
		{"blockquote", "<blockquote>"},
		{"table", "<table>"},
		{"horizontal rule", "<hr"},
		{"mermaid", "mermaid"},
		{"footnote content", "security documentation"},
	}

	for _, c := range checks {
		if !strings.Contains(html, c.contains) {
			t.Errorf("[mixed] expected %s (%q) in output, got:\n%s", c.label, c.contains, html)
		}
	}
}

// --- Sanitization with extended features ---

func TestIntegration_SanitizationPreservesExtended(t *testing.T) {
	r := editor.NewRenderer(nil)
	opts := editor.DefaultRenderOptions("")
	opts.Sanitize = true

	// Strikethrough should survive sanitization
	html, err := r.Render([]byte("~~deleted~~"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "<del>") {
		t.Errorf("sanitization stripped <del> tag, got: %s", html)
	}

	// Task list checkboxes should survive sanitization
	html, err = r.Render([]byte("- [x] done\n- [ ] todo"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "checkbox") {
		t.Errorf("sanitization stripped checkbox, got: %s", html)
	}

	// Highlight should survive sanitization
	html, err = r.Render([]byte("==highlighted=="), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "<mark>") {
		t.Errorf("sanitization stripped <mark> tag, got: %s", html)
	}
}

// --- Renderer with encoder + extended features ---

func TestIntegration_PlantUML_WithExtendedMarkdown(t *testing.T) {
	enc := &stubEncoder{encoded: "ENCODED123"}
	r := editor.NewRenderer(enc)
	opts := editor.DefaultRenderOptions("https://plantuml.example.com")

	src := "# Title\n\n~~old~~ **new**\n\n```plantuml\n@startuml\nA -> B\n@enduml\n```\n\n- [x] done\n"
	html, err := r.Render([]byte(src), opts)
	if err != nil {
		t.Fatal(err)
	}
	output := string(html)

	if !strings.Contains(output, "<h1>") {
		t.Error("expected heading")
	}
	if !strings.Contains(output, "<del>old</del>") {
		t.Errorf("expected strikethrough, got:\n%s", output)
	}
	if !strings.Contains(output, "<img") {
		t.Error("expected PlantUML img")
	}
	if !strings.Contains(output, "ENCODED123") {
		t.Error("expected encoded PlantUML")
	}
	if !strings.Contains(output, "checkbox") {
		t.Errorf("expected task list checkbox, got:\n%s", output)
	}
}
