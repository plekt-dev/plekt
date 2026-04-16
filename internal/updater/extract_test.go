package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// helper: build a tar.gz archive in-memory with the given members.
func makeTarGz(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for name, data := range members {
		hdr := &tar.Header{Name: name, Size: int64(len(data)), Mode: 0o755, Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// TestExtractCoreBinary_TarGz exercises the .tar.gz branch.
func TestExtractCoreBinary_TarGz(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz archives target non-Windows platforms")
	}
	coreBytes := []byte("fake-plekt-core-elf")
	archive := makeTarGz(t, map[string][]byte{
		"plekt-core":    coreBytes,
		"plekt-updater": []byte("fake-updater"),
	})
	archivePath := writeTemp(t, "plekt_1.0.0_linux_amd64.tar.gz", archive)
	destPath := filepath.Join(filepath.Dir(archivePath), "plekt-core.new")

	if err := extractCoreBinary(archivePath, destPath); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, coreBytes) {
		t.Errorf("dest content mismatch: got %q, want %q", got, coreBytes)
	}
}

// TestExtractCoreBinary_Zip exercises the .zip branch.
func TestExtractCoreBinary_Zip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("zip archives target Windows: extract looks for plekt-core.exe")
	}
	coreBytes := []byte("fake-plekt-core-pe")
	archive := makeZip(t, map[string][]byte{
		"plekt-core.exe":    coreBytes,
		"plekt-updater.exe": []byte("fake-updater"),
	})
	archivePath := writeTemp(t, "plekt_1.0.0_windows_amd64.zip", archive)
	destPath := filepath.Join(filepath.Dir(archivePath), "plekt-core.new")

	if err := extractCoreBinary(archivePath, destPath); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, coreBytes) {
		t.Errorf("dest content mismatch")
	}
}

// TestExtractCoreBinary_RawBinary covers the legacy/raw-binary fallback
// when the URL doesn't end with a known archive suffix.
func TestExtractCoreBinary_RawBinary(t *testing.T) {
	coreBytes := []byte("raw-binary")
	archivePath := writeTemp(t, "plekt-core-linux-amd64", coreBytes)
	destPath := filepath.Join(filepath.Dir(archivePath), "plekt-core.new")

	if err := extractCoreBinary(archivePath, destPath); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, coreBytes) {
		t.Errorf("dest content mismatch")
	}
}

// TestExtractCoreBinary_MissingMember verifies that a tar.gz that lacks
// plekt-core returns ErrCoreBinaryNotFound.
func TestExtractCoreBinary_MissingMember(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz test")
	}
	archive := makeTarGz(t, map[string][]byte{
		"README.md": []byte("no binary here"),
	})
	archivePath := writeTemp(t, "plekt_1.0.0_linux_amd64.tar.gz", archive)
	destPath := filepath.Join(filepath.Dir(archivePath), "plekt-core.new")

	err := extractCoreBinary(archivePath, destPath)
	if !errors.Is(err, ErrCoreBinaryNotFound) {
		t.Errorf("expected ErrCoreBinaryNotFound, got %v", err)
	}
}

// TestExtractCoreBinary_NestedDir ensures entries prefixed with a release
// directory ("plekt-1.0.0/plekt-core") are still matched by basename.
func TestExtractCoreBinary_NestedDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz test")
	}
	coreBytes := []byte("nested-core")
	archive := makeTarGz(t, map[string][]byte{
		"plekt-1.0.0/plekt-core": coreBytes,
	})
	archivePath := writeTemp(t, "plekt_1.0.0_linux_amd64.tar.gz", archive)
	destPath := filepath.Join(filepath.Dir(archivePath), "plekt-core.new")

	if err := extractCoreBinary(archivePath, destPath); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got, coreBytes) {
		t.Errorf("dest content mismatch")
	}
}

// TestExtractCoreBinary_DispatchesByMagic verifies the critical property
// that format detection is by content (magic bytes), not filename. In the
// real Apply() flow the archive is downloaded to `<exe>.download`
// : a filename that has no zip/tar.gz suffix: so suffix-based dispatch
// would silently fall into the raw-binary branch and rename the archive
// into place as if it were an executable (bricking the install on
// restart). This test pins that behaviour.
func TestExtractCoreBinary_DispatchesByMagic(t *testing.T) {
	coreBytes := []byte("content-via-magic")

	if runtime.GOOS == "windows" {
		archive := makeZip(t, map[string][]byte{"plekt-core.exe": coreBytes})
		// Deliberately misleading filename: no .zip suffix.
		archivePath := writeTemp(t, "plekt-core.exe.download", archive)
		destPath := filepath.Join(filepath.Dir(archivePath), "plekt-core.new")
		if err := extractCoreBinary(archivePath, destPath); err != nil {
			t.Fatalf("extract: %v", err)
		}
		got, _ := os.ReadFile(destPath)
		if !bytes.Equal(got, coreBytes) {
			t.Errorf("magic-dispatched zip extract mismatch")
		}
		return
	}
	archive := makeTarGz(t, map[string][]byte{"plekt-core": coreBytes})
	archivePath := writeTemp(t, "plekt-core.download", archive)
	destPath := filepath.Join(filepath.Dir(archivePath), "plekt-core.new")
	if err := extractCoreBinary(archivePath, destPath); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, _ := os.ReadFile(destPath)
	if !bytes.Equal(got, coreBytes) {
		t.Errorf("magic-dispatched tar.gz extract mismatch")
	}
}
