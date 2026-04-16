package registry

import "time"

// Registry represents the plugin registry (registry.json, format version 2).
//
// Trust model: each plugin entry carries its own Ed25519 public_key. The
// registry URL itself is the root of trust: anything served from it is
// considered authoritative for the listed plugins. There is no longer a
// global registry-level signing key.
type Registry struct {
	Version   int              `json:"version"`
	UpdatedAt time.Time        `json:"updated_at"`
	Plugins   []RegistryPlugin `json:"plugins"`
	Core      *CoreRelease     `json:"core,omitempty"`
}

// RegistryPlugin describes a plugin available in the registry.
// Plugin-level metadata (including the signing public_key) is shared across
// all versions of a single plugin.
type RegistryPlugin struct {
	Name        string   `json:"name"`
	Author      string   `json:"author"`
	License     string   `json:"license"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	// PublicKey is the Ed25519 hex-encoded public key used to sign every
	// release of this plugin. Per-repo signing model: each plugin has its
	// own keypair stored in the plugin repository's release secrets.
	PublicKey string `json:"public_key"`
	// Official is true when this plugin is published by the Plekt
	// maintainers themselves. UI renders an "Official" badge. The field is
	// optional; community plugins simply omit it (defaults to false).
	Official bool            `json:"official,omitempty"`
	Versions []PluginVersion `json:"versions"`
}

// RevokedKeys is the schema of revoked-keys.json: Ed25519 public keys that
// must NOT be trusted regardless of what registry.json claims. Used to
// invalidate compromised plugin signing keys without taking down the registry
// or the affected plugin entry. Applies equally to UAT and PROD.
type RevokedKeys struct {
	Version   int          `json:"version"`
	UpdatedAt time.Time    `json:"updated_at"`
	Revoked   []RevokedKey `json:"revoked"`
}

// RevokedKey is one entry in the revocation list.
type RevokedKey struct {
	PublicKey string    `json:"public_key"`
	Plugin    string    `json:"plugin"`
	RevokedAt time.Time `json:"revoked_at"`
	Reason    string    `json:"reason"`
}

// PluginVersion holds per-version metadata for a registry plugin.
// Versions are ordered newest-first in the registry JSON.
type PluginVersion struct {
	Version              string            `json:"version"`
	DownloadURL          string            `json:"download_url"`
	ChecksumSHA256       string            `json:"checksum_sha256"`
	SizeBytes            int64             `json:"size_bytes"`
	MinCoreVersion       string            `json:"min_core_version"`
	Dependencies         map[string]string `json:"dependencies,omitempty"`
	OptionalDependencies map[string]string `json:"optional_dependencies,omitempty"`
	UpdatedAt            time.Time         `json:"updated_at"`
}

// CoreRelease describes the latest core binary release in the registry.
type CoreRelease struct {
	Version      string       `json:"version"`
	ReleaseNotes string       `json:"release_notes"`
	ReleasedAt   time.Time    `json:"released_at"`
	Binaries     []CoreBinary `json:"binaries"`
}

// CoreBinary is a single downloadable binary for one OS/arch combination.
type CoreBinary struct {
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	DownloadURL    string `json:"download_url"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	SizeBytes      int64  `json:"size_bytes"`
}

// UpdateInfo describes an available update for an installed plugin.
type UpdateInfo struct {
	Name                string
	CurrentVersion      string
	LatestVersion       string
	DownloadURL         string
	ChecksumSHA256      string
	RequiresCoreUpdate  bool
	RequiredCoreVersion string
}
