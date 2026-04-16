package web_test

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/plekt-dev/plekt/internal/editor"
	"github.com/plekt-dev/plekt/internal/web"
)

// --- stub renderer ---

type stubRenderer struct {
	out template.HTML
	err error
}

func (s *stubRenderer) Render(_ []byte, _ editor.RenderOptions) (template.HTML, error) {
	return s.out, s.err
}

// --- tests ---

func TestEditorHandler_HandlePreview_HappyPath(t *testing.T) {
	r := &stubRenderer{out: template.HTML("<p>hello</p>")}
	h := web.NewEditorHandler(r, editor.DefaultRenderOptions(""))

	body := mustMarshal(t, map[string]string{"markdown": "hello"})
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
	if resp.HTML != "<p>hello</p>" {
		t.Errorf("unexpected HTML: %q", resp.HTML)
	}
}

func TestEditorHandler_HandlePreview_EmptyMarkdown(t *testing.T) {
	r := &stubRenderer{out: template.HTML("")}
	h := web.NewEditorHandler(r, editor.DefaultRenderOptions(""))

	body := mustMarshal(t, map[string]string{"markdown": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/preview-markdown", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePreview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestEditorHandler_HandlePreview_BadJSON(t *testing.T) {
	r := &stubRenderer{out: template.HTML("<p>ok</p>")}
	h := web.NewEditorHandler(r, editor.DefaultRenderOptions(""))

	req := httptest.NewRequest(http.MethodPost, "/api/preview-markdown", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePreview(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestEditorHandler_HandlePreview_BodyTooLarge(t *testing.T) {
	r := &stubRenderer{out: template.HTML("<p>ok</p>")}
	h := web.NewEditorHandler(r, editor.DefaultRenderOptions(""))

	// Build a body larger than 512KB
	huge := strings.Repeat("a", 513*1024)
	payload, _ := json.Marshal(map[string]string{"markdown": huge})
	req := httptest.NewRequest(http.MethodPost, "/api/preview-markdown", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePreview(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for oversized body, got %d", w.Code)
	}
}

func TestEditorHandler_HandlePreview_RenderError(t *testing.T) {
	r := &stubRenderer{err: &testRenderError{"render failed"}}
	h := web.NewEditorHandler(r, editor.DefaultRenderOptions(""))

	body := mustMarshal(t, map[string]string{"markdown": "# hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/preview-markdown", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePreview(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestEditorHandler_HandlePreview_ContentType(t *testing.T) {
	r := &stubRenderer{out: template.HTML("<p>ok</p>")}
	h := web.NewEditorHandler(r, editor.DefaultRenderOptions(""))

	body := mustMarshal(t, map[string]string{"markdown": "ok"})
	req := httptest.NewRequest(http.MethodPost, "/api/preview-markdown", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandlePreview(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected application/json content-type, got %q", ct)
	}
}

// --- helpers ---

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

type testRenderError struct{ msg string }

func (e *testRenderError) Error() string { return e.msg }
