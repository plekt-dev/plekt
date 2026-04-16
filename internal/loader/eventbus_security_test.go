package loader

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEventEmitHostFn_SystemEventBlocked(t *testing.T) {
	cases := []struct {
		name      string
		eventName string
		blocked   bool
	}{
		{"task.created allowed", "task.created", false},
		{"my.custom.event allowed", "my.custom.event", false},
		{"plugin.loaded blocked", "plugin.loaded", true},
		{"web.auth.login blocked", "web.auth.login", true},
		{"auth.login blocked", "auth.login", true},
		{"token.rotated blocked", "token.rotated", true},
		{"mcp.request blocked", "mcp.request", true},
		{"core.shutdown blocked", "core.shutdown", true},
		{"dashboard.refresh blocked", "dashboard.refresh", true},
		{"agent.run blocked", "agent.run", true},
		{"scheduler.tick allowed", "scheduler.tick", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := &fakeEventBus{}
			pcc := PluginCallContext{
				PluginName:   "testplugin",
				Bus:          bus,
				AllowedEmits: []string{tc.eventName},
			}
			params := EventEmitParams{EventName: tc.eventName}
			err := EventEmitHostFn(context.Background(), pcc, params)

			if tc.blocked {
				if !errors.Is(err, ErrSystemEventBlocked) {
					t.Errorf("expected ErrSystemEventBlocked for %q, got %v", tc.eventName, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error for %q, got %v", tc.eventName, err)
				}
			}
		})
	}
}

func TestEventEmitHostFn_PayloadTooLarge(t *testing.T) {
	bus := &fakeEventBus{}
	pcc := PluginCallContext{
		PluginName:   "testplugin",
		Bus:          bus,
		AllowedEmits: []string{"task.created"},
	}
	// Create a payload larger than 64 KB.
	bigPayload := strings.Repeat("x", 70000)
	params := EventEmitParams{
		EventName: "task.created",
		Payload:   bigPayload,
	}
	err := EventEmitHostFn(context.Background(), pcc, params)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestEventEmitHostFn_PayloadWithinLimit(t *testing.T) {
	bus := &fakeEventBus{}
	pcc := PluginCallContext{
		PluginName:   "testplugin",
		Bus:          bus,
		AllowedEmits: []string{"task.created"},
	}
	params := EventEmitParams{
		EventName: "task.created",
		Payload:   map[string]string{"key": "value"},
	}
	err := EventEmitHostFn(context.Background(), pcc, params)
	if err != nil {
		t.Errorf("expected no error for small payload, got %v", err)
	}
	emitted := bus.emitted()
	if len(emitted) != 1 {
		t.Errorf("expected 1 emitted event, got %d", len(emitted))
	}
}

func TestIsSystemEvent(t *testing.T) {
	cases := []struct {
		name     string
		event    string
		isSystem bool
	}{
		{"plugin prefix", "plugin.loaded", true},
		{"token prefix", "token.rotated", true},
		{"mcp prefix", "mcp.call", true},
		{"web prefix", "web.request", true},
		{"auth prefix", "auth.login", true},
		{"core prefix", "core.init", true},
		{"dashboard prefix", "dashboard.refresh", true},
		{"agent prefix", "agent.run", true},
		{"task not system", "task.created", false},
		{"notes not system", "notes.updated", false},
		{"custom not system", "my.custom", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isSystemEvent(tc.event)
			if got != tc.isSystem {
				t.Errorf("isSystemEvent(%q) = %v, want %v", tc.event, got, tc.isSystem)
			}
		})
	}
}
