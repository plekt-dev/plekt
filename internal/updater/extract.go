package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// coreBinaryName returns the expected filename of the Plekt core binary
// inside a release archive for the current platform ("plekt-core.exe" on
// Windows, "plekt-core" elsewhere).
func coreBinaryName() string {
	if runtime.GOOS == "windows" {
		return "plekt-core.exe"
	}
	return "plekt-core"
}

// ErrCoreBinaryNotFound is returned when a release archive does not contain
// the expected plekt-core(.exe) entry.
var ErrCoreBinaryNotFound = errors.New("plekt-core binary not found inside archive")

// extractCoreBinary extracts the plekt-core binary from a tar.gz or zip
// archive (dispatched by magic bytes) and writes it to destPath. A file
// that is neither is treated as a raw binary and renamed into place.
func extractCoreBinary(archivePath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	var magic [4]byte
	n, _ := io.ReadFull(f, magic[:])
	_ = f.Close()

	switch {
	case n >= 2 && bytes.Equal(magic[:2], []byte{0x1f, 0x8b}):
		return extractFromTarGz(archivePath, coreBinaryName(), destPath)
	case n >= 4 && bytes.Equal(magic[:4], []byte{0x50, 0x4b, 0x03, 0x04}):
		return extractFromZip(archivePath, coreBinaryName(), destPath)
	default:
		return os.Rename(archivePath, destPath)
	}
}

func extractFromTarGz(archivePath, memberName, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		// Match by basename so archives prefixed with a top-level dir still work.
		if filepath.Base(hdr.Name) != memberName {
			continue
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return fmt.Errorf("archive entry %q is not a regular file (typeflag %d)", hdr.Name, hdr.Typeflag)
		}
		return writeExtracted(tr, destPath)
	}
	return fmt.Errorf("%w: %q", ErrCoreBinaryNotFound, memberName)
}

func extractFromZip(archivePath, memberName, destPath string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if filepath.Base(f.Name) != memberName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %q: %w", f.Name, err)
		}
		err = writeExtracted(rc, destPath)
		_ = rc.Close()
		return err
	}
	return fmt.Errorf("%w: %q", ErrCoreBinaryNotFound, memberName)
}

func writeExtracted(src io.Reader, destPath string) error {
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".plekt-extract-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup of temp on failure.
	defer func() {
		if _, err := os.Stat(tmpPath); err == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
