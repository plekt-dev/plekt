package web_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/editor"
	"github.com/plekt-dev/plekt/internal/web"
)

// These integration tests use the real goldmark renderer (not stubs)
// to verify the full HTTP pipeline: request → parse → render → sanitize → response.

func realHandler() *web.EditorHandler {
	r := editor.NewRenderer(nil)
	opts := editor.DefaultRenderOptions("")
	return web.NewEditorHandler(r, opts)
}

func postPreview(t *testing.T, h *web.EditorHandler, markdown string) *web.PreviewResponse {
	t.Helper()
	body, _ := json.Marshal(web.PreviewRequest{Markdown: markdown})
	req := httptest.NewRequest(http.MethodPost, "/api/preview-markdown", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePreview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp web.PreviewResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &resp
}

// --- Full pipeline integration tests ---

func TestIntegrationHandler_Bold(t *testing.T) {
	h := realHandler()
	resp := postPreview(t, h, "**hello world**")
	if !strings.Contains(resp.HTML, "<strong>hello world</strong>") {
		t.Errorf("expected bold HTML, got: %s", resp.HTML)
	}
}

func TestIntegrationHandler_Strikethrough(t *testing.T) {
	h := realHandler()
	resp := postPreview(t, h, "~~deleted~~")
	if !strings.Contains(resp.HTML, "<del>deleted</del>") {
		t.Errorf("expected strikethrough HTML, got: %s", resp.HTML)
	}
}

func TestIntegrationHandler_TaskList(t *testing.T) {
	h := realHandler()
	resp := postPreview(t, h, "- [x] Done\n- [ ] Todo")
	if !strings.Contains(resp.HTML, "checkbox") {
		t.Errorf("expected checkboxes in task list, got: %s", resp.HTML)
	}
}

func TestIntegrationHandler_Highlight(t *testing.T) {
	h := realHandler()
	resp := postPreview(t, h, "==important==")
	if !strings.Contains(resp.HTML, "<mark>") {
		t.Errorf("expected <mark> tag, got: %s", resp.HTML)
	}
}

func TestIntegrationHandler_MermaidDiagram(t *testing.T) {
	h := realHandler()
	resp := postPreview(t, h, "```mermaid\ngraph TD\n    A --> B\n```")
	if !strings.Contains(resp.HTML, `class="mermaid"`) {
		t.Errorf("expected mermaid class, got: %s", resp.HTML)
	}
	if strings.Contains(resp.HTML, "language-mermaid") {
		t.Errorf("mermaid should NOT be rendered as code block, got: %s", resp.HTML)
	}
}

func TestIntegrationHandler_Table(t *testing.T) {
	h := realHandler()
	resp := postPreview(t, h, "| A | B |\n|---|---|\n| 1 | 2 |")
	if !strings.Contains(resp.HTML, "<table>") {
		t.Errorf("expected table HTML, got: %s", resp.HTML)
	}
}

func TestIntegrationHandler_DefinitionList(t *testing.T) {
	h := realHandler()
	resp := postPreview(t, h, "Term\n:   Definition here")
	if !strings.Contains(resp.HTML, "<dl>") {
		t.Errorf("expected <dl> in output, got: %s", resp.HTML)
	}
}

func TestIntegrationHandler_Footnote(t *testing.T) {
	h := realHandler()
	resp := postPreview(t, h, "Text[^1]\n\n[^1]: Footnote content.")
	if !strings.Contains(resp.HTML, "Footnote content") {
		t.Errorf("expected footnote content, got: %s", resp.HTML)
	}
}

func TestIntegrationHandler_MixedDocument(t *testing.T) {
	h := realHandler()
	md := `# Title

**Bold** and ~~strike~~ and ==highlight==.

- [x] Done
- [ ] Todo

` + "```mermaid\ngraph LR\n    A --> B\n```" + `

| Col1 | Col2 |
|------|------|
| A    | B    |

Term
:   Definition

---
`
	resp := postPreview(t, h, md)

	checks := map[string]string{
		"heading":       "<h1>",
		"bold":          "<strong>",
		"strikethrough": "<del>",
		"highlight":     "<mark>",
		"checkbox":      "checkbox",
		"mermaid":       "mermaid",
		"table":         "<table>",
		"definition":    "<dl>",
		"hr":            "<hr",
	}

	for label, want := range checks {
		if !strings.Contains(resp.HTML, want) {
			t.Errorf("[mixed] missing %s (%q) in response HTML", label, want)
		}
	}
}

func TestIntegrationHandler_XSSWithExtensions(t *testing.T) {
	h := realHandler()

	// XSS in strikethrough context
	resp := postPreview(t, h, `~~<script>alert(1)</script>~~`)
	if strings.Contains(resp.HTML, "<script>") {
		t.Errorf("XSS not sanitized in strikethrough context: %s", resp.HTML)
	}

	// XSS in highlight context
	resp = postPreview(t, h, `==<img src=x onerror="alert(1)">==`)
	if strings.Contains(resp.HTML, "onerror") {
		t.Errorf("XSS not sanitized in highlight context: %s", resp.HTML)
	}
}

func TestIntegrationHandler_EmptyMarkdown(t *testing.T) {
	h := realHandler()
	resp := postPreview(t, h, "")
	if resp.HTML != "" {
		t.Errorf("expected empty HTML for empty markdown, got: %q", resp.HTML)
	}
}

func TestIntegrationHandler_ContentTypeJSON(t *testing.T) {
	h := realHandler()
	body, _ := json.Marshal(web.PreviewRequest{Markdown: "# Test"})
	req := httptest.NewRequest(http.MethodPost, "/api/preview-markdown", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePreview(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content-type, got: %q", ct)
	}
}
