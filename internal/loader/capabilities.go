// Package loader: capability derivation.
//
// CapabilityID / Capability / PermissionDeriver form the permissions model
// shown to the operator at plugin install time and on the per-plugin
// permissions settings page. The derivation is a pure function over the
// already-parsed Manifest: no IO, no WASM load.
package loader

import (
	"fmt"
	"sort"
	"strings"
)

// CapabilityID identifies a category of plugin permission.
type CapabilityID string

const (
	CapGlobalFrontend         CapabilityID = "global_frontend"
	CapNetwork                CapabilityID = "network"
	CapEventPublish           CapabilityID = "event_publish"
	CapEventSubscribe         CapabilityID = "event_subscribe"
	CapMCPTool                CapabilityID = "mcp_tool"
	CapDBSchema               CapabilityID = "db_schema"
	CapCronJobs               CapabilityID = "cron_jobs"
	CapConfigRead             CapabilityID = "config_read"
	CapAdminSettingsExtension CapabilityID = "admin_settings_extension"
	CapRequiredPlugins        CapabilityID = "required_plugins"
	CapOptionalPlugins        CapabilityID = "optional_plugins"
)

// CapabilitySeverity grades how dangerous a capability is for the operator.
type CapabilitySeverity string

const (
	SeverityLow    CapabilitySeverity = "low"
	SeverityMedium CapabilitySeverity = "medium"
	SeverityHigh   CapabilitySeverity = "high"
)

// Capability is a single permission entry presented to the operator.
type Capability struct {
	ID          CapabilityID
	Severity    CapabilitySeverity
	Title       string
	Description string
	Details     []string
}

// PluginPermissions aggregates all capabilities a plugin requests.
type PluginPermissions struct {
	PluginName   string
	Capabilities []Capability
}

// PermissionDeriver produces a PluginPermissions summary from a Manifest.
// Implementations must be pure: same Manifest in → same PluginPermissions out.
type PermissionDeriver interface {
	Derive(m Manifest) PluginPermissions
}

// defaultPermissionDeriver emits a Capability for each populated manifest
// section. Plugins without Network/GlobalFrontend simply have no entry for
// that capability: existing plugins need zero manifest changes.
type defaultPermissionDeriver struct{}

// NewDefaultPermissionDeriver returns the built-in PermissionDeriver.
func NewDefaultPermissionDeriver() PermissionDeriver {
	return defaultPermissionDeriver{}
}

// Derive inspects m and returns a PluginPermissions value. See package doc.
func (defaultPermissionDeriver) Derive(m Manifest) PluginPermissions {
	caps := make([]Capability, 0, 8)

	if m.UI.GlobalFrontend != nil && m.UI.GlobalFrontend.JSFile != "" {
		details := []string{"js: " + m.UI.GlobalFrontend.JSFile}
		if m.UI.GlobalFrontend.CSSFile != "" {
			details = append(details, "css: "+m.UI.GlobalFrontend.CSSFile)
		}
		caps = append(caps, Capability{
			ID:          CapGlobalFrontend,
			Severity:    SeverityHigh,
			Title:       "Global frontend script",
			Description: "Injects JavaScript into every page of the Web UI.",
			Details:     details,
		})
	}

	if len(m.Network.RequestedHosts) > 0 {
		details := make([]string, 0, len(m.Network.RequestedHosts))
		for _, h := range m.Network.RequestedHosts {
			details = append(details, h+" [requested]")
		}
		caps = append(caps, Capability{
			ID:          CapNetwork,
			Severity:    SeverityHigh,
			Title:       "Outbound network access",
			Description: "Plugin requests permission to reach external hosts.",
			Details:     details,
		})
	}

	if len(m.Events.Emits) > 0 {
		caps = append(caps, Capability{
			ID:          CapEventPublish,
			Severity:    SeverityMedium,
			Title:       "Publish events",
			Description: "Plugin emits events on the internal bus.",
			Details:     append([]string(nil), m.Events.Emits...),
		})
	}

	if len(m.Events.Subscribes) > 0 {
		caps = append(caps, Capability{
			ID:          CapEventSubscribe,
			Severity:    SeverityLow,
			Title:       "Subscribe to events",
			Description: "Plugin receives events from the internal bus.",
			Details:     append([]string(nil), m.Events.Subscribes...),
		})
	}

	// Collect extension points targeting core.admin.settings.*.
	var adminSettingsPoints []string
	for _, ext := range m.UI.Extensions {
		if strings.HasPrefix(ext.Point, "core.admin.settings") {
			adminSettingsPoints = append(adminSettingsPoints, ext.Point)
		}
	}
	if len(adminSettingsPoints) > 0 {
		caps = append(caps, Capability{
			ID:          CapAdminSettingsExtension,
			Severity:    SeverityMedium,
			Title:       "Admin settings sections",
			Description: "Plugin renders configuration sections inside /admin/settings.",
			Details:     adminSettingsPoints,
		})
	}

	if len(m.MCP.Tools) > 0 {
		names := make([]string, 0, len(m.MCP.Tools))
		for _, t := range m.MCP.Tools {
			names = append(names, t.Name)
		}
		caps = append(caps, Capability{
			ID:          CapMCPTool,
			Severity:    SeverityLow,
			Title:       "MCP tools",
			Description: "Plugin exposes tools to MCP clients (agents).",
			Details:     names,
		})
	}

	if len(m.Dashboard.Widgets) > 0 {
		names := make([]string, 0, len(m.Dashboard.Widgets))
		for _, w := range m.Dashboard.Widgets {
			names = append(names, w.ID)
		}
		caps = append(caps, Capability{
			ID:          CapDBSchema,
			Severity:    SeverityLow,
			Title:       "Dashboard widgets",
			Description: "Plugin contributes dashboard widgets.",
			Details:     names,
		})
	}

	// Required plugin dependencies.
	if len(m.Dependencies) > 0 {
		details := make([]string, 0, len(m.Dependencies))
		for dep, constraint := range m.Dependencies {
			entry := dep
			if constraint != "" {
				entry += " " + constraint
			}
			details = append(details, entry)
		}
		sort.Strings(details)
		caps = append(caps, Capability{
			ID:          CapRequiredPlugins,
			Severity:    SeverityMedium,
			Title:       "Required plugins",
			Description: "Plugin will not load without these dependencies.",
			Details:     details,
		})
	}

	// Optional plugin dependencies.
	if len(m.OptionalDependencies) > 0 {
		details := make([]string, 0, len(m.OptionalDependencies))
		for dep, constraint := range m.OptionalDependencies {
			entry := dep
			if constraint != "" {
				entry += " " + constraint
			}
			details = append(details, entry)
		}
		sort.Strings(details)
		caps = append(caps, Capability{
			ID:          CapOptionalPlugins,
			Severity:    SeverityLow,
			Title:       "Optional plugins",
			Description: "Plugin works without these, but enables extra features when present.",
			Details:     details,
		})
	}

	return PluginPermissions{PluginName: m.Name, Capabilities: caps}
}

// DeriveWithGrants returns the same PluginPermissions as Derive but annotates
// the CapNetwork Details with the live grant state of each host:
//
//   - "[requested]"       : only in the manifest, not yet granted
//   - "[granted]"         : in grants with source="install"
//   - "[granted:operator]": in grants with source="operator"
//
// Hosts present in grants but absent from the manifest are appended at the
// end annotated with "[granted:operator]" (or their actual source).
//
// If the manifest has no network block but grants exist, a CapNetwork entry
// is still emitted so the operator sees the active grants.
func DeriveWithGrants(m Manifest, grants []HostGrant) PluginPermissions {
	perms := NewDefaultPermissionDeriver().Derive(m)

	grantBySrc := make(map[string]string, len(grants))
	for _, g := range grants {
		grantBySrc[g.Host] = g.Source
	}

	annotate := func(host string) string {
		src, ok := grantBySrc[host]
		if !ok {
			return host + " [requested]"
		}
		if src == "operator" {
			return host + " [granted:operator]"
		}
		return host + " [granted]"
	}

	// Track which hosts from grants are already covered by manifest.
	inManifest := make(map[string]struct{}, len(m.Network.RequestedHosts))
	for _, h := range m.Network.RequestedHosts {
		inManifest[h] = struct{}{}
	}

	// Extra hosts: in grants but not in manifest.
	var extras []string
	for _, g := range grants {
		if _, ok := inManifest[g.Host]; !ok {
			extras = append(extras, g.Host)
		}
	}
	sort.Strings(extras)

	// Find or build the network capability and rewrite its details.
	netIdx := -1
	for i, c := range perms.Capabilities {
		if c.ID == CapNetwork {
			netIdx = i
			break
		}
	}

	if netIdx == -1 && len(extras) == 0 {
		return perms
	}

	details := make([]string, 0, len(m.Network.RequestedHosts)+len(extras))
	for _, h := range m.Network.RequestedHosts {
		details = append(details, annotate(h))
	}
	for _, h := range extras {
		src := grantBySrc[h]
		if src == "install" {
			details = append(details, h+" [granted]")
		} else {
			details = append(details, fmt.Sprintf("%s [granted:%s]", h, src))
		}
	}

	netCap := Capability{
		ID:          CapNetwork,
		Severity:    SeverityHigh,
		Title:       "Outbound network access",
		Description: "Plugin requests permission to reach external hosts.",
		Details:     details,
	}
	if netIdx == -1 {
		perms.Capabilities = append(perms.Capabilities, netCap)
	} else {
		perms.Capabilities[netIdx] = netCap
	}
	return perms
}
