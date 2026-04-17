// Package version exposes the Plekt core version and semver helpers.
package version

// Version is the Plekt core version (bare semver, no "v" prefix).
// Override at build time:
//
//	go build -ldflags "-X github.com/plekt-dev/plekt/internal/version.Version=1.2.3"
var Version = "1.1.0"
