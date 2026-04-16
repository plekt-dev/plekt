package loader_test

import (
	"encoding/json"
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
)

// TestFrontendAssets_JSONRoundTrip verifies that FrontendAssets marshals and
// unmarshals correctly, preserving both js_file and css_file fields.
func TestFrontendAssets_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := loader.FrontendAssets{
		JSFile:  "app.js",
		CSSFile: "app.css",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var got loader.FrontendAssets
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if got.JSFile != original.JSFile {
		t.Errorf("JSFile = %q, want %q", got.JSFile, original.JSFile)
	}
	if got.CSSFile != original.CSSFile {
		t.Errorf("CSSFile = %q, want %q", got.CSSFile, original.CSSFile)
	}
}

// TestFrontendAssets_OmitEmptyFields verifies that empty fields are omitted
// from the marshalled JSON output.
func TestFrontendAssets_OmitEmptyFields(t *testing.T) {
	t.Parallel()

	fa := loader.FrontendAssets{}
	data, err := json.Marshal(fa)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Both fields have omitempty: the result should be an empty object.
	if string(data) != "{}" {
		t.Errorf("expected empty JSON object, got %s", string(data))
	}
}

// TestFrontendAssets_OnlyJSFile verifies that only the js_file key appears
// when CSSFile is empty.
func TestFrontendAssets_OnlyJSFile(t *testing.T) {
	t.Parallel()

	fa := loader.FrontendAssets{JSFile: "plugin.js"}
	data, err := json.Marshal(fa)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal to map error: %v", err)
	}

	if v, ok := m["js_file"]; !ok || v != "plugin.js" {
		t.Errorf("js_file = %q (present=%v), want %q", v, ok, "plugin.js")
	}
	if _, ok := m["css_file"]; ok {
		t.Error("expected css_file to be absent when CSSFile is empty")
	}
}

// TestFrontendAssets_OnlyCSSFile verifies that only the css_file key appears
// when JSFile is empty.
func TestFrontendAssets_OnlyCSSFile(t *testing.T) {
	t.Parallel()

	fa := loader.FrontendAssets{CSSFile: "style.css"}
	data, err := json.Marshal(fa)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal to map error: %v", err)
	}

	if _, ok := m["js_file"]; ok {
		t.Error("expected js_file to be absent when JSFile is empty")
	}
	if v, ok := m["css_file"]; !ok || v != "style.css" {
		t.Errorf("css_file = %q (present=%v), want %q", v, ok, "style.css")
	}
}

// TestPageDescriptor_NilFrontend_OmitEmpty verifies that when Frontend is nil
// the JSON output does not include a "frontend" key.
func TestPageDescriptor_NilFrontend_OmitEmpty(t *testing.T) {
	t.Parallel()

	pd := loader.PageDescriptor{
		ID:           "tasks",
		Title:        "Tasks",
		Icon:         "check",
		DataFunction: "get_tasks",
		PageType:     "list",
		Frontend:     nil,
	}

	data, err := json.Marshal(pd)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal to map error: %v", err)
	}

	if _, ok := m["frontend"]; ok {
		t.Error("expected 'frontend' key to be absent when Frontend is nil")
	}
}

// TestPageDescriptor_WithFrontend_RoundTrip verifies that a PageDescriptor
// with a non-nil Frontend marshals and unmarshals correctly.
func TestPageDescriptor_WithFrontend_RoundTrip(t *testing.T) {
	t.Parallel()

	original := loader.PageDescriptor{
		ID:           "kanban",
		Title:        "Kanban Board",
		Icon:         "columns",
		DataFunction: "get_kanban_data",
		PageType:     "kanban",
		Frontend: &loader.FrontendAssets{
			JSFile:  "kanban.js",
			CSSFile: "kanban.css",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var got loader.PageDescriptor
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if got.ID != original.ID {
		t.Errorf("ID = %q, want %q", got.ID, original.ID)
	}
	if got.Title != original.Title {
		t.Errorf("Title = %q, want %q", got.Title, original.Title)
	}
	if got.DataFunction != original.DataFunction {
		t.Errorf("DataFunction = %q, want %q", got.DataFunction, original.DataFunction)
	}
	if got.PageType != original.PageType {
		t.Errorf("PageType = %q, want %q", got.PageType, original.PageType)
	}
	if got.Frontend == nil {
		t.Fatal("Frontend is nil after unmarshal, want non-nil")
	}
	if got.Frontend.JSFile != original.Frontend.JSFile {
		t.Errorf("Frontend.JSFile = %q, want %q", got.Frontend.JSFile, original.Frontend.JSFile)
	}
	if got.Frontend.CSSFile != original.Frontend.CSSFile {
		t.Errorf("Frontend.CSSFile = %q, want %q", got.Frontend.CSSFile, original.Frontend.CSSFile)
	}
}

// TestPageDescriptor_FrontendInManifest verifies the full JSON round-trip path
// via Manifest.UI.Pages when frontend assets are present.
func TestPageDescriptor_FrontendInManifest(t *testing.T) {
	t.Parallel()

	raw := `{
		"name": "tasks",
		"version": "1.0.0",
		"ui": {
			"pages": [
				{
					"id": "kanban",
					"title": "Kanban",
					"icon": "columns",
					"data_function": "get_kanban",
					"page_type": "kanban",
					"frontend": {
						"js_file": "kanban.js",
						"css_file": "kanban.css"
					}
				},
				{
					"id": "list",
					"title": "List",
					"data_function": "get_list",
					"page_type": "list"
				}
			]
		}
	}`

	var m loader.Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(m.UI.Pages) != 2 {
		t.Fatalf("Pages count = %d, want 2", len(m.UI.Pages))
	}

	kanban := m.UI.Pages[0]
	if kanban.Frontend == nil {
		t.Fatal("kanban page Frontend is nil, want non-nil")
	}
	if kanban.Frontend.JSFile != "kanban.js" {
		t.Errorf("JSFile = %q, want kanban.js", kanban.Frontend.JSFile)
	}
	if kanban.Frontend.CSSFile != "kanban.css" {
		t.Errorf("CSSFile = %q, want kanban.css", kanban.Frontend.CSSFile)
	}

	list := m.UI.Pages[1]
	if list.Frontend != nil {
		t.Errorf("list page Frontend = %+v, want nil", list.Frontend)
	}
}
