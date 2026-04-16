package web

import (
	"encoding/json"
	"net/http"

	"github.com/plekt-dev/plekt/internal/editor"
)

// EditorHandler handles HTTP requests for the rich text editor.
type EditorHandler struct {
	renderer editor.Renderer
	opts     editor.RenderOptions
}

// PreviewRequest is the JSON body for the preview endpoint.
type PreviewRequest struct {
	Markdown string `json:"markdown"`
}

// PreviewResponse is the JSON body returned by the preview endpoint.
type PreviewResponse struct {
	HTML string `json:"html"`
}

// NewEditorHandler creates an EditorHandler with the given renderer and options.
func NewEditorHandler(r editor.Renderer, opts editor.RenderOptions) *EditorHandler {
	return &EditorHandler{renderer: r, opts: opts}
}

// HandlePreview renders the supplied markdown and returns the HTML as JSON.
// Returns 400 on bad JSON or body too large, 500 on render failure.
func (h *EditorHandler) HandlePreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)

	var req PreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	rendered, err := h.renderer.Render([]byte(req.Markdown), h.opts)
	if err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(PreviewResponse{HTML: string(rendered)})
}
