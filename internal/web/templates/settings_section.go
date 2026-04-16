package templates

// SettingsFieldType enumerates the supported field input types for plugin
// settings sections rendered inside /admin/settings.
type SettingsFieldType string

const (
	SettingsFieldTypeText     SettingsFieldType = "text"
	SettingsFieldTypePassword SettingsFieldType = "password"
	SettingsFieldTypeSelect   SettingsFieldType = "select"
	SettingsFieldTypeCheckbox SettingsFieldType = "checkbox"
	SettingsFieldTypeTextarea SettingsFieldType = "textarea"
	SettingsFieldTypeNumber   SettingsFieldType = "number"
	SettingsFieldTypeLink     SettingsFieldType = "link"
)

// SettingsFieldOption is one option in a select field.
type SettingsFieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// SettingsField describes one configurable field within a SettingsSection.
type SettingsField struct {
	Name        string                `json:"name"`
	Type        SettingsFieldType     `json:"type"` // one of SettingsFieldType constants
	Label       string                `json:"label"`
	Value       string                `json:"value"`
	Placeholder string                `json:"placeholder,omitempty"`
	HelpText    string                `json:"help_text,omitempty"`
	Options     []SettingsFieldOption `json:"options,omitempty"`
	Required    bool                  `json:"required,omitempty"`
	ReadOnly    bool                  `json:"read_only,omitempty"`
	WriteOnly   bool                  `json:"write_only,omitempty"`
}

// SettingsFooterLink is a hyperlink shown below a settings section (e.g.
// "View docs" or "Reset to defaults").
type SettingsFooterLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// SettingsSection is a structured block of configuration fields contributed by
// a plugin to /admin/settings. Plugins return this type from their
// data_function registered at a core.admin.settings.* extension point.
//
// SourcePlugin is populated by the settings handler: plugins do not need to
// set it. It is used by the template to build the correct POST URL without
// requiring an additional template argument.
type SettingsSection struct {
	Title        string               `json:"title"`
	Description  string               `json:"description,omitempty"`
	Fields       []SettingsField      `json:"fields"`
	SubmitAction string               `json:"submit_action"`
	FooterLinks  []SettingsFooterLink `json:"footer_links,omitempty"`
	SourcePlugin string               `json:"-"` // set by handler, not the plugin
}
