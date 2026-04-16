package updater

import "os"

// IsRunningInDocker returns true when Plekt is running inside a Docker container.
// It checks the PLEKT_DOCKER environment variable first, then falls back to
// the presence of /.dockerenv which Docker creates in every container.
func IsRunningInDocker() bool {
	if os.Getenv("PLEKT_DOCKER") == "1" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}
