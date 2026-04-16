package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Semver holds parsed major.minor.patch components.
type Semver struct {
	Major, Minor, Patch int
}

// Parse splits a bare semver string ("1.2.3") into components.
// Pre-release suffixes and "v" prefixes are rejected.
func Parse(s string) (Semver, error) {
	if s == "" {
		return Semver{}, fmt.Errorf("empty version string")
	}
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("invalid semver %q: expected major.minor.patch", s)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Semver{}, fmt.Errorf("invalid semver %q: bad major: %w", s, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Semver{}, fmt.Errorf("invalid semver %q: bad minor: %w", s, err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return Semver{}, fmt.Errorf("invalid semver %q: bad patch: %w", s, err)
	}
	if major < 0 || minor < 0 || patch < 0 {
		return Semver{}, fmt.Errorf("invalid semver %q: negative component", s)
	}
	return Semver{major, minor, patch}, nil
}

// Compare returns -1, 0, or +1 comparing a to b.
func Compare(a, b string) (int, error) {
	sa, err := Parse(a)
	if err != nil {
		return 0, err
	}
	sb, err := Parse(b)
	if err != nil {
		return 0, err
	}
	return sa.Compare(sb), nil
}

// Compare returns -1, 0, or +1.
func (a Semver) Compare(b Semver) int {
	if a.Major != b.Major {
		return cmp(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return cmp(a.Minor, b.Minor)
	}
	return cmp(a.Patch, b.Patch)
}

// AtLeast returns true if version satisfies the constraint.
// Constraint formats: ">=1.2.3", "1.2.3" (bare = minimum), "" (any).
func AtLeast(version, constraint string) (bool, error) {
	if constraint == "" {
		return true, nil
	}
	minVer := strings.TrimPrefix(constraint, ">=")
	r, err := Compare(version, minVer)
	if err != nil {
		return false, err
	}
	return r >= 0, nil
}

func cmp(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
