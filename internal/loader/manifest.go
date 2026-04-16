package loader

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FlexDeps accepts both map ({"name": ">=1.0"}) and array (["name"]) formats
// for dependency fields. Array entries get an empty constraint (any version).
type FlexDeps map[string]string

func (f *FlexDeps) UnmarshalJSON(data []byte) error {
	// Try map first.
	var m map[string]string
	if err := json.Unmarshal(data, &m); err == nil {
		*f = m
		return nil
	}
	// Fall back to array.
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return fmt.Errorf("optional_dependencies must be a map or array: %w", err)
	}
	result := make(map[string]string, len(arr))
	for _, name := range arr {
		result[name] = ""
	}
	*f = result
	return nil
}

// manifestDeps is a lightweight struct for reading only the dependency fields
// of manifest.json during bootstrap. It avoids loading the full Manifest.
type manifestDeps struct {
	Name                 string            `json:"name"`
	MinCoreVersion       string            `json:"min_core_version,omitempty"`
	Dependencies         map[string]string `json:"dependencies,omitempty"`
	OptionalDependencies FlexDeps          `json:"optional_dependencies,omitempty"`
}

// readManifestDeps reads only the name and dependency fields from
// {dir}/manifest.json. No validation, no signature verification, no WASM.
func readManifestDeps(dir string) (manifestDeps, error) {
	path := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return manifestDeps{}, fmt.Errorf("read manifest deps %q: %w", path, err)
	}
	var md manifestDeps
	if err := json.Unmarshal(data, &md); err != nil {
		return manifestDeps{}, fmt.Errorf("parse manifest deps %q: %w", path, err)
	}
	return md, nil
}

// Manifest is the parsed content of a plugin's manifest.json.
type Manifest struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Description    string `json:"description"`
	Author         string `json:"author"`
	License        string `json:"license"`
	MinCoreVersion string `json:"min_core_version,omitempty"`
	// Dependencies maps plugin names to version constraints.
	// Each listed plugin MUST be loaded (with a compatible version) before this
	// plugin can load. Constraint format: ">=0.1.0" or "" (any version).
	Dependencies map[string]string `json:"dependencies,omitempty"`
	// OptionalDependencies maps plugin names to version constraints.
	// The plugin loads and works normally without them; features are conditionally
	// enabled at runtime via mc_config::get("__available_plugins").
	OptionalDependencies FlexDeps             `json:"optional_dependencies,omitempty"`
	Events               EventsDeclaration    `json:"events"`
	MCP                  MCPDefinition        `json:"mcp"`
	Dashboard            DashboardDeclaration `json:"dashboard"`
	UI                   UIDeclaration        `json:"ui"`
	Network              NetworkDeclaration   `json:"network,omitempty"`
}

// NetworkDeclaration lists outbound hosts a plugin requests access to.
// RequestedHosts is a hint shown to the operator in the install permissions
// modal; it is NOT an authorization. The source of truth for granted hosts is
// the core plugin_host_grants store, owned exclusively by the operator.
type NetworkDeclaration struct {
	RequestedHosts []string `json:"requested_hosts,omitempty"`
}

// UIDeclaration lists pages a plugin exposes in the sidebar navigation
// and extensions it registers into other plugins' extension points.
type UIDeclaration struct {
	Pages          []PageDescriptor      `json:"pages"`
	Extensions     []ExtensionDescriptor `json:"extensions,omitempty"`
	GlobalFrontend *FrontendAssets       `json:"global_frontend,omitempty"`
}

// FrontendAssets declares optional plugin-owned JS and CSS files.
// File names are relative to the plugin's frontend/ directory.
type FrontendAssets struct {
	JSFile  string `json:"js_file,omitempty"`
	CSSFile string `json:"css_file,omitempty"`
}

// PageDescriptor describes a single plugin page accessible from the sidebar.
// Pages without NavParent appear as top-level items in the global sidebar.
// Pages with NavParent appear as sub-items inside the parent page (e.g. project sidebar).
// Format: "{plugin_name}:{page_id}" (e.g. "projects-plugin:projects").
type PageDescriptor struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Icon            string          `json:"icon"`
	DataFunction    string          `json:"data_function"`
	PageType        string          `json:"page_type"`
	NavParent       string          `json:"nav_parent,omitempty"`
	NavOrder        int             `json:"nav_order,omitempty"`
	ExtensionPoints []string        `json:"extension_points,omitempty"`
	Frontend        *FrontendAssets `json:"frontend,omitempty"`
}

// ExtensionDescriptor describes an extension a plugin registers into another plugin's slot.
type ExtensionDescriptor struct {
	Point        string `json:"point"`         // extension point ID (e.g. "task-card-badge")
	TargetPlugin string `json:"target_plugin"` // which plugin owns the point
	DataFunction string `json:"data_function"` // WASM function to call for extension data
	Type         string `json:"type"`          // "badge", "section", "action"
}

// DashboardDeclaration lists widgets a plugin exposes for the dashboard.
type DashboardDeclaration struct {
	Widgets []WidgetDescriptor `json:"widgets"`
}

// WidgetDescriptor describes a single dashboard widget exposed by a plugin.
type WidgetDescriptor struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	DataFunction   string `json:"data_function"`
	RefreshSeconds int    `json:"refresh_seconds"`
	Width          string `json:"width"`
	LinkTemplate   string `json:"link_template"`
}

// EventsDeclaration lists events the plugin emits and subscribes to.
type EventsDeclaration struct {
	Emits      []string `json:"emits"`
	Subscribes []string `json:"subscribes"`
}

// MCPDefinition is the MCP section of a manifest.
type MCPDefinition struct {
	Tools     []MCPTool     `json:"tools"`
	Resources []MCPResource `json:"resources"`
}

// MCPTool describes one MCP tool exposed by the plugin.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// MCPResource describes one MCP resource exposed by the plugin.
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mime_type,omitempty"`
}

// MCPSignature holds the Ed25519 public key and signature for mcp.yaml verification.
type MCPSignature struct {
	PublicKey string `yaml:"public_key"`
	Signature string `yaml:"signature"`
}

// ErrInvalidEventName is returned when an event name is empty or whitespace-only.
var ErrInvalidEventName = errors.New("invalid event name")

// ValidateManifestEvents checks that all event names in decl are non-empty
// and non-whitespace-only strings.
// Returns ErrInvalidEventName wrapping the offending position on first failure.
func ValidateManifestEvents(decl EventsDeclaration) error {
	for i, name := range decl.Emits {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: emits[%d] is empty", ErrInvalidEventName, i)
		}
	}
	for i, name := range decl.Subscribes {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: subscribes[%d] is empty", ErrInvalidEventName, i)
		}
	}
	return nil
}
