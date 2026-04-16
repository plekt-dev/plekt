package loader_test

import (
	"encoding/json"
	"testing"

	"github.com/plekt-dev/plekt/internal/loader"
)

func TestManifest_DashboardDeclaration_Roundtrip(t *testing.T) {
	t.Parallel()

	raw := `{
		"name": "tasks",
		"version": "1.0.0",
		"dashboard": {
			"widgets": [
				{
					"id": "task-count",
					"title": "Task Count",
					"description": "Number of open tasks",
					"data_function": "get_task_count",
					"refresh_seconds": 30,
					"width": "half"
				}
			]
		}
	}`

	var m loader.Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(m.Dashboard.Widgets) != 1 {
		t.Fatalf("Widgets count = %d, want 1", len(m.Dashboard.Widgets))
	}
	w := m.Dashboard.Widgets[0]
	if w.ID != "task-count" {
		t.Errorf("ID = %q, want task-count", w.ID)
	}
	if w.Title != "Task Count" {
		t.Errorf("Title = %q, want Task Count", w.Title)
	}
	if w.DataFunction != "get_task_count" {
		t.Errorf("DataFunction = %q, want get_task_count", w.DataFunction)
	}
	if w.RefreshSeconds != 30 {
		t.Errorf("RefreshSeconds = %d, want 30", w.RefreshSeconds)
	}
	if w.Width != "half" {
		t.Errorf("Width = %q, want half", w.Width)
	}
}

func TestManifest_NoDashboardField_DefaultsToEmpty(t *testing.T) {
	t.Parallel()

	raw := `{"name": "tasks", "version": "1.0.0"}`

	var m loader.Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(m.Dashboard.Widgets) != 0 {
		t.Errorf("expected empty widgets, got %d", len(m.Dashboard.Widgets))
	}
}

func TestManifest_MultipleWidgets(t *testing.T) {
	t.Parallel()

	raw := `{
		"name": "plug",
		"dashboard": {
			"widgets": [
				{"id": "w1", "data_function": "fn1"},
				{"id": "w2", "data_function": "fn2"}
			]
		}
	}`

	var m loader.Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(m.Dashboard.Widgets) != 2 {
		t.Fatalf("Widgets count = %d, want 2", len(m.Dashboard.Widgets))
	}
}
