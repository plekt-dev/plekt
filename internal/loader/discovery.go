package loader

import "time"

// DiscoveredPlugin represents a plugin folder found on disk during a scan.
type DiscoveredPlugin struct {
	Dir                  string
	Name                 string
	Version              string
	Description          string
	OptionalDependencies FlexDeps
	ManifestValid        bool
	ScannedAt            time.Time
}
