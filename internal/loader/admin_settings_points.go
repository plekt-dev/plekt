package loader

// Extension point IDs for the /admin/settings page.
// Plugins register extensions against these points to inject configuration
// sections into the global admin settings UI.
const (
	ExtPointAdminSettingsGeneral      = "core.admin.settings.general"
	ExtPointAdminSettingsIntegrations = "core.admin.settings.integrations"
	ExtPointAdminSettingsAdvanced     = "core.admin.settings.advanced"
)

// AdminSettingsExtensionPointOrder returns the canonical display order of the
// admin settings extension points. Sections registered at points listed earlier
// are rendered before sections registered at later points.
// A fresh slice is returned each call to prevent callers from mutating the order.
func AdminSettingsExtensionPointOrder() []string {
	return []string{
		ExtPointAdminSettingsGeneral,
		ExtPointAdminSettingsIntegrations,
		ExtPointAdminSettingsAdvanced,
	}
}
