package loader

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Package validation limits.
const (
	maxExtractedSize = 100 << 20 // 100 MB total extracted size
	maxFileCount     = 1000      // maximum files in archive
)

// Sentinel errors for package operations.
var (
	ErrPackageMissingManifest = errors.New("package missing manifest.json")
	ErrPackageMissingWASM     = errors.New("package missing plugin.wasm")
	ErrPackageMissingMCP      = errors.New("package missing mcp.yaml")
	ErrPackagePathTraversal   = errors.New("package contains path traversal")
	ErrPackageTooLarge        = errors.New("package exceeds 100MB extracted size limit")
	ErrPackageTooManyFiles    = errors.New("package exceeds 1000 file limit")
	ErrPackageManifestInvalid = errors.New("package manifest.json invalid")
	ErrPackageAlreadyExists   = errors.New("plugin directory already exists")
)

// PackageInfo holds metadata extracted from a .mcpkg archive without full unpacking.
type PackageInfo struct {
	Name    string
	Version string
}

// UnpackPlugin extracts a .mcpkg archive (tar.gz) into destDir/{plugin-name}/.
// It validates the archive structure:
//   - manifest.json must exist and be valid JSON with "name" and "version" fields
//   - plugin.wasm must exist
//   - mcp.yaml must exist
//   - No path traversal (no ".." or absolute paths in archive entries)
//   - Creates destDir/{name}/ directory and extracts all files into it
//
// Returns error if validation fails or extraction fails.
func UnpackPlugin(mcpkgPath, destDir string) (PackageInfo, error) {
	// First pass: validate without writing anything.
	info, err := ValidatePackage(mcpkgPath)
	if err != nil {
		return PackageInfo{}, err
	}

	// Check destination does not already exist.
	pluginDir := filepath.Join(destDir, info.Name)
	if _, err := os.Stat(pluginDir); err == nil {
		return PackageInfo{}, fmt.Errorf("%w: %s", ErrPackageAlreadyExists, pluginDir)
	}

	// Second pass: extract files.
	f, err := os.Open(mcpkgPath)
	if err != nil {
		return PackageInfo{}, fmt.Errorf("open package: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return PackageInfo{}, fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var totalSize int64

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return PackageInfo{}, fmt.Errorf("read tar entry: %w", err)
		}

		if err := validateTarPath(hdr.Name); err != nil {
			return PackageInfo{}, err
		}
		clean := filepath.Clean(hdr.Name)

		target := filepath.Join(pluginDir, clean)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return PackageInfo{}, fmt.Errorf("mkdir %s: %w", clean, err)
			}
		case tar.TypeReg:
			totalSize += hdr.Size
			if totalSize > maxExtractedSize {
				return PackageInfo{}, ErrPackageTooLarge
			}

			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return PackageInfo{}, fmt.Errorf("mkdir parent %s: %w", clean, err)
			}

			if err := writeFile(target, tr, hdr.Size); err != nil {
				return PackageInfo{}, fmt.Errorf("write %s: %w", clean, err)
			}
		}
	}

	return info, nil
}

// ValidatePackage reads a .mcpkg archive and validates its structure
// without extracting files to disk. Returns PackageInfo on success.
func ValidatePackage(mcpkgPath string) (PackageInfo, error) {
	f, err := os.Open(mcpkgPath)
	if err != nil {
		return PackageInfo{}, fmt.Errorf("open package: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return PackageInfo{}, fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	var (
		hasManifest bool
		hasWASM     bool
		hasMCP      bool
		info        PackageInfo
		totalSize   int64
		fileCount   int
	)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return PackageInfo{}, fmt.Errorf("read tar entry: %w", err)
		}

		if err := validateTarPath(hdr.Name); err != nil {
			return PackageInfo{}, err
		}
		clean := filepath.Clean(hdr.Name)

		if hdr.Typeflag == tar.TypeReg {
			fileCount++
			if fileCount > maxFileCount {
				return PackageInfo{}, ErrPackageTooManyFiles
			}

			totalSize += hdr.Size
			if totalSize > maxExtractedSize {
				return PackageInfo{}, ErrPackageTooLarge
			}
		}

		switch clean {
		case "manifest.json":
			hasManifest = true
			data, err := io.ReadAll(io.LimitReader(tr, hdr.Size+1))
			if err != nil {
				return PackageInfo{}, fmt.Errorf("read manifest.json: %w", err)
			}
			info, err = parseManifestInfo(data)
			if err != nil {
				return PackageInfo{}, err
			}
		case "plugin.wasm":
			hasWASM = true
		case "mcp.yaml":
			hasMCP = true
		}
	}

	if !hasManifest {
		return PackageInfo{}, ErrPackageMissingManifest
	}
	if !hasWASM {
		return PackageInfo{}, ErrPackageMissingWASM
	}
	if !hasMCP {
		return PackageInfo{}, ErrPackageMissingMCP
	}

	return info, nil
}

// validateTarPath rejects paths with traversal or absolute components.
// It checks the raw tar header name (which uses forward slashes) and the
// OS-cleaned path to catch absolute paths on all platforms.
func validateTarPath(raw string) error {
	// Tar paths use forward slashes. An absolute path starts with "/" regardless of OS.
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) || filepath.IsAbs(raw) {
		return fmt.Errorf("%w: absolute path %q", ErrPackagePathTraversal, raw)
	}
	clean := filepath.Clean(raw)
	if filepath.IsAbs(clean) {
		return fmt.Errorf("%w: absolute path %q", ErrPackagePathTraversal, raw)
	}
	// Check each component for ".." traversal. Split on both separators for safety.
	for _, part := range strings.FieldsFunc(clean, func(r rune) bool { return r == '/' || r == filepath.Separator }) {
		if part == ".." {
			return fmt.Errorf("%w: %q", ErrPackagePathTraversal, raw)
		}
	}
	return nil
}

// parseManifestInfo extracts name and version from manifest.json bytes.
func parseManifestInfo(data []byte) (PackageInfo, error) {
	var m struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return PackageInfo{}, fmt.Errorf("%w: %v", ErrPackageManifestInvalid, err)
	}
	if m.Name == "" {
		return PackageInfo{}, fmt.Errorf("%w: missing name", ErrPackageManifestInvalid)
	}
	if m.Version == "" {
		return PackageInfo{}, fmt.Errorf("%w: missing version", ErrPackageManifestInvalid)
	}
	return PackageInfo{Name: m.Name, Version: m.Version}, nil
}

// writeFile writes a tar entry to disk with 0644 permissions, enforcing a size limit.
func writeFile(path string, r io.Reader, size int64) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	// LimitReader prevents reading more than declared size (decompression bomb guard).
	if _, err := io.Copy(out, io.LimitReader(r, size)); err != nil {
		return err
	}
	return nil
}
