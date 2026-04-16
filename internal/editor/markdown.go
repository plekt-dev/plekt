package editor

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// EditorMode controls the display mode of the rich text editor.
type EditorMode string

const (
	ModeEdit    EditorMode = "edit"
	ModePreview EditorMode = "preview"
	ModeSplit   EditorMode = "split"
)

// RenderOptions controls rendering behaviour.
type RenderOptions struct {
	Sanitize        bool
	PlantUMLBaseURL string
}

// DefaultRenderOptions returns sensible production defaults.
// PlantUML renders as styled code blocks by default (no external services).
// Set plantUMLBaseURL to a local PlantUML server to render as images.
func DefaultRenderOptions(plantUMLBaseURL string) RenderOptions {
	return RenderOptions{
		Sanitize:        true,
		PlantUMLBaseURL: plantUMLBaseURL,
	}
}

// ErrPlantUMLEncode is returned when PlantUML diagram encoding fails.
var ErrPlantUMLEncode = errors.New("editor: plantuml encode failed")

// PlantUMLEncoder encodes PlantUML diagram source into the compact URL-safe
// encoding expected by the PlantUML server.
type PlantUMLEncoder interface {
	Encode(src string) (string, error)
}

// PlantUMLImgURL builds the full SVG image URL for a PlantUML diagram.
func PlantUMLImgURL(baseURL, encoded string) string {
	base := strings.TrimRight(baseURL, "/")
	return fmt.Sprintf("%s/svg/%s", base, encoded)
}

// Renderer converts Markdown source to sanitized HTML.
type Renderer interface {
	Render(src []byte, opts RenderOptions) (template.HTML, error)
}

// markdownRenderer implements Renderer using goldmark + bluemonday.
type markdownRenderer struct {
	encoder PlantUMLEncoder
	gm      goldmark.Markdown
}

// plantumlBlockRe matches rendered fenced code blocks with language "plantuml".
// goldmark renders them as: <pre><code class="language-plantuml">...</code></pre>
var plantumlBlockRe = regexp.MustCompile(`(?s)<pre><code class="language-plantuml">(.*?)</code></pre>`)

// mermaidBlockRe matches rendered fenced code blocks with language "mermaid".
var mermaidBlockRe = regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)

// highlightRe matches ==text== for mark/highlight syntax.
var highlightRe = regexp.MustCompile(`==([^=\n]+?)==`)

// NewRenderer creates a Renderer.
// If encoder is nil, PlantUML fenced blocks are rendered as plain <pre>.
func NewRenderer(encoder PlantUMLEncoder) Renderer {
	gm := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Table,
			extension.TaskList,
			extension.DefinitionList,
			extension.Footnote,
			extension.Typographer,
		),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
			html.WithXHTML(),
			html.WithUnsafe(), // needed for task list checkboxes and footnotes
		),
	)

	return &markdownRenderer{encoder: encoder, gm: gm}
}

// Render converts src Markdown to HTML.
func (r *markdownRenderer) Render(src []byte, opts RenderOptions) (template.HTML, error) {
	if len(src) == 0 {
		return "", nil
	}

	var buf bytes.Buffer
	if err := r.gm.Convert(src, &buf); err != nil {
		return "", fmt.Errorf("editor: markdown render: %w", err)
	}

	output := buf.String()

	// Post-process plantuml fenced blocks in the rendered HTML.
	output = r.replacePlantUMLBlocks(output, opts.PlantUMLBaseURL)

	// Post-process mermaid fenced blocks: convert to <pre class="mermaid">
	// for client-side rendering by mermaid.js.
	output = replaceMermaidBlocks(output)

	// Post-process ==highlight== syntax into <mark> tags.
	// Applied to the rendered HTML: only matches text nodes, not inside tags.
	output = replaceHighlightMarks(output)

	if opts.Sanitize {
		p := bluemonday.UGCPolicy()
		// Allow PlantUML and Mermaid elements.
		p.AllowImages()
		p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("img", "pre", "code", "div", "span", "figure")
		p.AllowAttrs("src", "alt").OnElements("img")
		// Allow task list checkboxes.
		p.AllowAttrs("type", "checked", "disabled").OnElements("input")
		// Allow mark/highlight tags.
		p.AllowElements("mark", "dl", "dt", "dd", "del", "input", "sup", "sub", "section", "details", "summary")
		// Allow footnote attributes.
		p.AllowAttrs("id", "href", "role").OnElements("a", "li", "section", "sup")
		sanitized := p.Sanitize(output)
		return template.HTML(sanitized), nil
	}

	return template.HTML(output), nil
}

// replacePlantUMLBlocks replaces goldmark-rendered plantuml code blocks with
// <img> tags (if encoder set) or <pre> fallback tags.
func (r *markdownRenderer) replacePlantUMLBlocks(output, baseURL string) string {
	return plantumlBlockRe.ReplaceAllStringFunc(output, func(match string) string {
		// Extract the inner content (HTML-unescaped by goldmark).
		sub := plantumlBlockRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		// goldmark HTML-encodes the content: decode standard HTML entities.
		content := htmlUnescape(sub[1])

		// If encoder + local PlantUML server URL are set, render as <img>.
		if r.encoder != nil && baseURL != "" {
			encoded, err := r.encoder.Encode(content)
			if err == nil {
				imgURL := PlantUMLImgURL(baseURL, encoded)
				return fmt.Sprintf(`<img src="%s" alt="PlantUML diagram" class="plantuml-diagram"/>`, template.HTMLEscapeString(imgURL))
			}
		}

		// Convert PlantUML to Mermaid for client-side rendering (fully local).
		if mermaidSrc := PlantUMLToMermaid(content); mermaidSrc != "" {
			return fmt.Sprintf(`<pre class="mermaid">%s</pre>`, template.HTMLEscapeString(mermaidSrc))
		}

		// Fallback: render as styled code block.
		return fmt.Sprintf(`<div class="plantuml-block"><div class="plantuml-label">PlantUML</div><pre class="plantuml-src">%s</pre></div>`, template.HTMLEscapeString(content))
	})
}

// htmlUnescape reverses the minimal HTML escaping goldmark applies to code block content.
func htmlUnescape(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	return s
}

// replaceMermaidBlocks converts goldmark-rendered mermaid code blocks into
// <pre class="mermaid"> elements for client-side rendering by mermaid.js.
func replaceMermaidBlocks(output string) string {
	return mermaidBlockRe.ReplaceAllStringFunc(output, func(match string) string {
		sub := mermaidBlockRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		content := htmlUnescape(sub[1])
		return fmt.Sprintf(`<pre class="mermaid">%s</pre>`, template.HTMLEscapeString(content))
	})
}

// replaceHighlightMarks converts ==text== syntax into <mark>text</mark>.
func replaceHighlightMarks(output string) string {
	return highlightRe.ReplaceAllString(output, "<mark>$1</mark>")
}
