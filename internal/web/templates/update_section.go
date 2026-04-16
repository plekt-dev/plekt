package templates

// CoreUpdateData is the view model for the core update section on the settings page.
type CoreUpdateData struct {
	CurrentVersion string
	LatestVersion  string
	ReleaseNotes   string
	ReleasedAt     string
	Status         string
	Error          string
	IsDocker       bool
	CSRFToken      string
}
