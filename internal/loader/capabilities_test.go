package loader

import (
	"testing"
	"time"
)

func TestDefaultPermissionDeriver_Derive(t *testing.T) {
	tests := []struct {
		name    string
		m       Manifest
		wantIDs []CapabilityID
	}{
		{
			name:    "empty manifest emits no capabilities",
			m:       Manifest{Name: "empty"},
			wantIDs: nil,
		},
		{
			name: "full manifest emits all capabilities",
			m: Manifest{
				Name: "full",
				Events: EventsDeclaration{
					Emits:      []string{"x.y"},
					Subscribes: []string{"a.b"},
				},
				MCP:                  MCPDefinition{Tools: []MCPTool{{Name: "t1"}}},
				Dashboard:            DashboardDeclaration{Widgets: []WidgetDescriptor{{ID: "w1"}}},
				UI:                   UIDeclaration{GlobalFrontend: &FrontendAssets{JSFile: "global.js", CSSFile: "global.css"}},
				Network:              NetworkDeclaration{RequestedHosts: []string{"api.example.com"}},
				Dependencies:         map[string]string{"base-plugin": ">=1.0.0"},
				OptionalDependencies: map[string]string{"ext-plugin": ">=0.2.0"},
			},
			wantIDs: []CapabilityID{
				CapGlobalFrontend, CapNetwork,
				CapEventPublish, CapEventSubscribe,
				CapMCPTool, CapDBSchema,
				CapRequiredPlugins, CapOptionalPlugins,
			},
		},
		{
			name: "required deps only",
			m: Manifest{
				Name:         "hard-deps",
				Dependencies: map[string]string{"projects-plugin": ">=0.1.0"},
			},
			wantIDs: []CapabilityID{CapRequiredPlugins},
		},
		{
			name: "optional deps only",
			m: Manifest{
				Name:                 "soft-deps",
				OptionalDependencies: map[string]string{"projects-plugin": ">=0.1.0"},
			},
			wantIDs: []CapabilityID{CapOptionalPlugins},
		},
		{
			name: "deps with empty constraint",
			m: Manifest{
				Name:         "any-version",
				Dependencies: map[string]string{"core-plugin": ""},
			},
			wantIDs: []CapabilityID{CapRequiredPlugins},
		},
		{
			name: "scheduler-plugin-like backward compat",
			m: Manifest{
				Name: "scheduler-plugin",
				Events: EventsDeclaration{
					Emits:      []string{"scheduler.job.ran"},
					Subscribes: []string{"plugin.loaded"},
				},
				MCP: MCPDefinition{Tools: []MCPTool{{Name: "scheduler.create_job"}}},
			},
			wantIDs: []CapabilityID{CapEventPublish, CapEventSubscribe, CapMCPTool},
		},
	}

	d := NewDefaultPermissionDeriver()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := d.Derive(tc.m)
			if got.PluginName != tc.m.Name {
				t.Errorf("PluginName = %q, want %q", got.PluginName, tc.m.Name)
			}
			if len(got.Capabilities) != len(tc.wantIDs) {
				t.Fatalf("got %d caps, want %d: %+v", len(got.Capabilities), len(tc.wantIDs), got.Capabilities)
			}
			for i, want := range tc.wantIDs {
				if got.Capabilities[i].ID != want {
					t.Errorf("cap[%d].ID = %s, want %s", i, got.Capabilities[i].ID, want)
				}
			}
		})
	}
}

func TestDeriveWithGrants_Annotation(t *testing.T) {
	m := Manifest{
		Name: "voice-plugin",
		Network: NetworkDeclaration{
			RequestedHosts: []string{"whisper:9000", "api.openai.com"},
		},
	}
	grants := []HostGrant{
		{PluginName: "voice-plugin", Host: "whisper:9000", Source: "install", GrantedAt: time.Now()},
		{PluginName: "voice-plugin", Host: "my-whisper.internal:9001", Source: "operator", GrantedAt: time.Now()},
	}

	perms := DeriveWithGrants(m, grants)

	var net *Capability
	for i := range perms.Capabilities {
		if perms.Capabilities[i].ID == CapNetwork {
			net = &perms.Capabilities[i]
		}
	}
	if net == nil {
		t.Fatal("expected CapNetwork")
	}

	wantDetails := map[string]bool{
		"whisper:9000 [granted]":                      false,
		"api.openai.com [requested]":                  false,
		"my-whisper.internal:9001 [granted:operator]": false,
	}
	for _, d := range net.Details {
		if _, ok := wantDetails[d]; ok {
			wantDetails[d] = true
		}
	}
	for k, seen := range wantDetails {
		if !seen {
			t.Errorf("missing detail %q, got %v", k, net.Details)
		}
	}
}

func TestPermissionDeriver_AdminSettingsExtension(t *testing.T) {
	d := NewDefaultPermissionDeriver()

	tests := []struct {
		name          string
		m             Manifest
		wantCap       bool
		wantDetails   []string
		notWantPoints []string
	}{
		{
			name:    "no extensions emits no admin settings cap",
			m:       Manifest{Name: "p"},
			wantCap: false,
		},
		{
			name: "extension with different target plugin does not emit cap",
			m: Manifest{
				Name: "p",
				UI: UIDeclaration{
					Extensions: []ExtensionDescriptor{
						{Point: "core.admin.settings.general", TargetPlugin: "other", DataFunction: "fn", Type: "section"},
					},
				},
			},
			// The cap is derived from manifest.UI.Extensions[].Point prefix only, not TargetPlugin.
			// The manifest describes what THIS plugin registers; TargetPlugin is metadata.
			// Per contract: scan m.UI.Extensions for ext.Point with prefix "core.admin.settings".
			wantCap:     true,
			wantDetails: []string{ExtPointAdminSettingsGeneral},
		},
		{
			name: "extension at general point emits cap with general detail",
			m: Manifest{
				Name: "p",
				UI: UIDeclaration{
					Extensions: []ExtensionDescriptor{
						{Point: ExtPointAdminSettingsGeneral, TargetPlugin: "core", DataFunction: "fn", Type: "section"},
					},
				},
			},
			wantCap:     true,
			wantDetails: []string{ExtPointAdminSettingsGeneral},
		},
		{
			name: "extension at integrations point emits cap with integrations detail",
			m: Manifest{
				Name: "p",
				UI: UIDeclaration{
					Extensions: []ExtensionDescriptor{
						{Point: ExtPointAdminSettingsIntegrations, TargetPlugin: "core", DataFunction: "fn", Type: "section"},
					},
				},
			},
			wantCap:     true,
			wantDetails: []string{ExtPointAdminSettingsIntegrations},
		},
		{
			name: "two points produce one cap with both details",
			m: Manifest{
				Name: "p",
				UI: UIDeclaration{
					Extensions: []ExtensionDescriptor{
						{Point: ExtPointAdminSettingsGeneral, TargetPlugin: "core", DataFunction: "fn1", Type: "section"},
						{Point: ExtPointAdminSettingsAdvanced, TargetPlugin: "core", DataFunction: "fn2", Type: "section"},
					},
				},
			},
			wantCap:     true,
			wantDetails: []string{ExtPointAdminSettingsGeneral, ExtPointAdminSettingsAdvanced},
		},
		{
			name: "non-prefix point does not emit admin settings cap",
			m: Manifest{
				Name: "p",
				UI: UIDeclaration{
					Extensions: []ExtensionDescriptor{
						{Point: "task-card-badge", TargetPlugin: "tasks", DataFunction: "fn", Type: "badge"},
					},
				},
			},
			wantCap: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			perms := d.Derive(tc.m)
			var found *Capability
			for i := range perms.Capabilities {
				if perms.Capabilities[i].ID == CapAdminSettingsExtension {
					found = &perms.Capabilities[i]
					break
				}
			}
			if tc.wantCap && found == nil {
				t.Fatalf("expected CapAdminSettingsExtension capability, got none. caps: %+v", perms.Capabilities)
			}
			if !tc.wantCap && found != nil {
				t.Fatalf("expected no CapAdminSettingsExtension capability, got: %+v", found)
			}
			if found == nil {
				return
			}
			if found.Severity != SeverityMedium {
				t.Errorf("Severity = %s, want %s", found.Severity, SeverityMedium)
			}
			detailSet := make(map[string]bool, len(found.Details))
			for _, d := range found.Details {
				detailSet[d] = true
			}
			for _, want := range tc.wantDetails {
				if !detailSet[want] {
					t.Errorf("missing detail %q in cap Details %v", want, found.Details)
				}
			}
		})
	}
}

func TestDeriveWithGrants_NoManifestNetworkButGrantsExist(t *testing.T) {
	m := Manifest{Name: "p"}
	grants := []HostGrant{{PluginName: "p", Host: "x.test:8080", Source: "operator"}}
	perms := DeriveWithGrants(m, grants)

	found := false
	for _, c := range perms.Capabilities {
		if c.ID == CapNetwork {
			found = true
			if len(c.Details) != 1 || c.Details[0] != "x.test:8080 [granted:operator]" {
				t.Errorf("unexpected details %v", c.Details)
			}
		}
	}
	if !found {
		t.Error("expected CapNetwork to be synthesized from grants")
	}
}
