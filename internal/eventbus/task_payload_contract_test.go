package eventbus

import (
	"encoding/json"
	"testing"
)

// TestTaskPayloadJSONContract verifies that the typed payload structs produce
// exactly the JSON field names that the WASM plugin emits via map[string]any.
// The plugin emits events as map[string]any (unavoidable at the WASM boundary),
// so the JSON keys in the map must match the struct tags exactly.
func TestTaskPayloadJSONContract(t *testing.T) {
	t.Run("TaskCreatedPayload marshal keys", func(t *testing.T) {
		p := TaskCreatedPayload{
			TaskID:    1,
			Title:     "t",
			Status:    "pending",
			Priority:  3,
			CreatedAt: "2026-03-21T00:00:00Z",
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal to map: %v", err)
		}
		for _, key := range []string{"task_id", "title", "status", "priority", "created_at"} {
			if _, ok := m[key]; !ok {
				t.Errorf("missing key %q in marshalled TaskCreatedPayload", key)
			}
		}
		if len(m) != 5 {
			t.Errorf("expected 5 keys, got %d: %v", len(m), m)
		}
	})

	t.Run("TaskCreatedPayload reverse: map to struct", func(t *testing.T) {
		m := map[string]any{
			"task_id":    float64(1),
			"title":      "t",
			"status":     "pending",
			"priority":   float64(3),
			"created_at": "2026-03-21T00:00:00Z",
		}
		b, _ := json.Marshal(m)
		var p TaskCreatedPayload
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if p.TaskID != 1 {
			t.Errorf("TaskID: got %d want 1", p.TaskID)
		}
		if p.Title != "t" {
			t.Errorf("Title: got %q want 't'", p.Title)
		}
		if p.Status != "pending" {
			t.Errorf("Status: got %q want 'pending'", p.Status)
		}
		if p.Priority != 3 {
			t.Errorf("Priority: got %d want 3", p.Priority)
		}
		if p.CreatedAt != "2026-03-21T00:00:00Z" {
			t.Errorf("CreatedAt: got %q want '2026-03-21T00:00:00Z'", p.CreatedAt)
		}
	})

	t.Run("TaskUpdatedPayload marshal keys", func(t *testing.T) {
		p := TaskUpdatedPayload{
			TaskID:         2,
			PreviousStatus: "pending",
			NewStatus:      "done",
			UpdatedAt:      "2026-03-21T00:00:00Z",
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal to map: %v", err)
		}
		for _, key := range []string{"task_id", "previous_status", "new_status", "updated_at"} {
			if _, ok := m[key]; !ok {
				t.Errorf("missing key %q in marshalled TaskUpdatedPayload", key)
			}
		}
	})

	t.Run("TaskUpdatedPayload reverse: map to struct", func(t *testing.T) {
		m := map[string]any{
			"task_id":         float64(2),
			"previous_status": "pending",
			"new_status":      "done",
			"updated_at":      "2026-03-21T00:00:00Z",
		}
		b, _ := json.Marshal(m)
		var p TaskUpdatedPayload
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if p.TaskID != 2 {
			t.Errorf("TaskID: got %d want 2", p.TaskID)
		}
		if p.PreviousStatus != "pending" {
			t.Errorf("PreviousStatus: got %q want 'pending'", p.PreviousStatus)
		}
		if p.NewStatus != "done" {
			t.Errorf("NewStatus: got %q want 'done'", p.NewStatus)
		}
		if p.UpdatedAt != "2026-03-21T00:00:00Z" {
			t.Errorf("UpdatedAt: got %q want '2026-03-21T00:00:00Z'", p.UpdatedAt)
		}
	})

	t.Run("TaskDeletedPayload marshal keys", func(t *testing.T) {
		p := TaskDeletedPayload{
			TaskID:    3,
			DeletedAt: "2026-03-21T00:00:00Z",
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal to map: %v", err)
		}
		for _, key := range []string{"task_id", "deleted_at"} {
			if _, ok := m[key]; !ok {
				t.Errorf("missing key %q in marshalled TaskDeletedPayload", key)
			}
		}
		if len(m) != 2 {
			t.Errorf("expected 2 keys, got %d: %v", len(m), m)
		}
	})

	t.Run("TaskDeletedPayload reverse: map to struct", func(t *testing.T) {
		m := map[string]any{
			"task_id":    float64(3),
			"deleted_at": "2026-03-21T00:00:00Z",
		}
		b, _ := json.Marshal(m)
		var p TaskDeletedPayload
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if p.TaskID != 3 {
			t.Errorf("TaskID: got %d want 3", p.TaskID)
		}
		if p.DeletedAt != "2026-03-21T00:00:00Z" {
			t.Errorf("DeletedAt: got %q want '2026-03-21T00:00:00Z'", p.DeletedAt)
		}
	})

	t.Run("TaskCompletedPayload marshal keys", func(t *testing.T) {
		p := TaskCompletedPayload{
			TaskID:      4,
			Title:       "t",
			CompletedAt: "2026-03-21T00:00:00Z",
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal to map: %v", err)
		}
		for _, key := range []string{"task_id", "title", "completed_at"} {
			if _, ok := m[key]; !ok {
				t.Errorf("missing key %q in marshalled TaskCompletedPayload", key)
			}
		}
		if len(m) != 3 {
			t.Errorf("expected 3 keys, got %d: %v", len(m), m)
		}
	})

	t.Run("TaskCompletedPayload reverse: map to struct", func(t *testing.T) {
		m := map[string]any{
			"task_id":      float64(4),
			"title":        "t",
			"completed_at": "2026-03-21T00:00:00Z",
		}
		b, _ := json.Marshal(m)
		var p TaskCompletedPayload
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if p.TaskID != 4 {
			t.Errorf("TaskID: got %d want 4", p.TaskID)
		}
		if p.Title != "t" {
			t.Errorf("Title: got %q want 't'", p.Title)
		}
		if p.CompletedAt != "2026-03-21T00:00:00Z" {
			t.Errorf("CompletedAt: got %q want '2026-03-21T00:00:00Z'", p.CompletedAt)
		}
	})
}

// TestTaskUpdatedPayloadOmitsEmptyPreviousStatus verifies that PreviousStatus
// is omitted from JSON when empty (omitempty tag), matching the plugin's
// map[string]any emission which only includes keys with values.
func TestTaskUpdatedPayloadOmitsEmptyPreviousStatus(t *testing.T) {
	p := TaskUpdatedPayload{
		TaskID:    5,
		NewStatus: "pending",
		UpdatedAt: "2026-03-21T00:00:00Z",
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["previous_status"]; ok {
		t.Errorf("previous_status should be omitted when empty, but it was present")
	}
}
