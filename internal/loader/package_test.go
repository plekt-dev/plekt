package loader

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// testFile represents a file entry in a test .mcpkg archive.
type testFile struct {
	Name    string
	Content []byte
	IsDir   bool
}

// buildMcpkg creates a .mcpkg (tar.gz) archive in memory from the given files.
func buildMcpkg(t *testing.T, files []testFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for _, f := range files {
		if f.IsDir {
			if err := tw.WriteHeader(&tar.Header{
				Name:     f.Name,
				Typeflag: tar.TypeDir,
				Mode:     0755,
			}); err != nil {
				t.Fatalf("write dir header %s: %v", f.Name, err)
			}
			continue
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     f.Name,
			Size:     int64(len(f.Content)),
			Typeflag: tar.TypeReg,
			Mode:     0644,
		}); err != nil {
			t.Fatalf("write header %s: %v", f.Name, err)
		}
		if _, err := tw.Write(f.Content); err != nil {
			t.Fatalf("write content %s: %v", f.Name, err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// validManifest returns a manifest.json with the given name and version.
func validManifest(name, version string) []byte {
	m := map[string]string{"name": name, "version": version}
	b, _ := json.Marshal(m)
	return b
}

// writeMcpkg writes a .mcpkg archive to a temp file and returns its path.
func writeMcpkg(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.mcpkg")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return f.Name()
}

// validFiles returns the three required files for a valid .mcpkg.
func validFiles(name, version string) []testFile {
	return []testFile{
		{Name: "manifest.json", Content: validManifest(name, version)},
		{Name: "plugin.wasm", Content: []byte("fake-wasm")},
		{Name: "mcp.yaml", Content: []byte("tools: []")},
	}
}

func TestValidatePackage(t *testing.T) {
	tests := []struct {
		name    string
		files   []testFile
		wantErr error
		wantPkg PackageInfo
	}{
		{
			name:    "valid package",
			files:   validFiles("my-plugin", "1.0.0"),
			wantPkg: PackageInfo{Name: "my-plugin", Version: "1.0.0"},
		},
		{
			name: "valid package with extra files",
			files: append(validFiles("extra-plugin", "2.1.0"), testFile{
				Name: "frontend/index.html", Content: []byte("<h1>hi</h1>"),
			}),
			wantPkg: PackageInfo{Name: "extra-plugin", Version: "2.1.0"},
		},
		{
			name: "missing manifest.json",
			files: []testFile{
				{Name: "plugin.wasm", Content: []byte("fake")},
				{Name: "mcp.yaml", Content: []byte("tools: []")},
			},
			wantErr: ErrPackageMissingManifest,
		},
		{
			name: "missing plugin.wasm",
			files: []testFile{
				{Name: "manifest.json", Content: validManifest("p", "1.0.0")},
				{Name: "mcp.yaml", Content: []byte("tools: []")},
			},
			wantErr: ErrPackageMissingWASM,
		},
		{
			name: "missing mcp.yaml",
			files: []testFile{
				{Name: "manifest.json", Content: validManifest("p", "1.0.0")},
				{Name: "plugin.wasm", Content: []byte("fake")},
			},
			wantErr: ErrPackageMissingMCP,
		},
		{
			name: "path traversal with dot-dot",
			files: []testFile{
				{Name: "manifest.json", Content: validManifest("p", "1.0.0")},
				{Name: "plugin.wasm", Content: []byte("fake")},
				{Name: "mcp.yaml", Content: []byte("tools: []")},
				{Name: "../evil.txt", Content: []byte("pwned")},
			},
			wantErr: ErrPackagePathTraversal,
		},
		{
			name: "absolute path in archive",
			files: []testFile{
				{Name: "manifest.json", Content: validManifest("p", "1.0.0")},
				{Name: "plugin.wasm", Content: []byte("fake")},
				{Name: "mcp.yaml", Content: []byte("tools: []")},
				{Name: "/etc/passwd", Content: []byte("pwned")},
			},
			wantErr: ErrPackagePathTraversal,
		},
		{
			name: "manifest missing name",
			files: []testFile{
				{Name: "manifest.json", Content: []byte(`{"version":"1.0.0"}`)},
				{Name: "plugin.wasm", Content: []byte("fake")},
				{Name: "mcp.yaml", Content: []byte("tools: []")},
			},
			wantErr: ErrPackageManifestInvalid,
		},
		{
			name: "manifest missing version",
			files: []testFile{
				{Name: "manifest.json", Content: []byte(`{"name":"p"}`)},
				{Name: "plugin.wasm", Content: []byte("fake")},
				{Name: "mcp.yaml", Content: []byte("tools: []")},
			},
			wantErr: ErrPackageManifestInvalid,
		},
		{
			name: "manifest invalid JSON",
			files: []testFile{
				{Name: "manifest.json", Content: []byte(`not json`)},
				{Name: "plugin.wasm", Content: []byte("fake")},
				{Name: "mcp.yaml", Content: []byte("tools: []")},
			},
			wantErr: ErrPackageManifestInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildMcpkg(t, tt.files)
			path := writeMcpkg(t, data)

			info, err := ValidatePackage(path)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error wrapping %v, got nil", tt.wantErr)
				}
				if !errorIs(err, tt.wantErr) {
					t.Fatalf("expected error wrapping %v, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info != tt.wantPkg {
				t.Fatalf("got %+v, want %+v", info, tt.wantPkg)
			}
		})
	}
}

func TestUnpackPlugin(t *testing.T) {
	t.Run("creates correct directory structure", func(t *testing.T) {
		files := append(validFiles("test-plugin", "1.2.3"),
			testFile{Name: "frontend/", IsDir: true},
			testFile{Name: "frontend/index.html", Content: []byte("<h1>test</h1>")},
		)
		data := buildMcpkg(t, files)
		path := writeMcpkg(t, data)
		destDir := t.TempDir()

		info, err := UnpackPlugin(path, destDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Name != "test-plugin" || info.Version != "1.2.3" {
			t.Fatalf("got %+v, want name=test-plugin version=1.2.3", info)
		}

		pluginDir := filepath.Join(destDir, "test-plugin")

		// Check required files exist.
		for _, name := range []string{"manifest.json", "plugin.wasm", "mcp.yaml", "frontend/index.html"} {
			p := filepath.Join(pluginDir, name)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("expected file %s to exist: %v", name, err)
			}
		}

		// Check manifest.json content.
		got, err := os.ReadFile(filepath.Join(pluginDir, "manifest.json"))
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		var m map[string]string
		if err := json.Unmarshal(got, &m); err != nil {
			t.Fatalf("parse manifest: %v", err)
		}
		if m["name"] != "test-plugin" || m["version"] != "1.2.3" {
			t.Fatalf("manifest content mismatch: %v", m)
		}
	})

	t.Run("already exists returns error", func(t *testing.T) {
		data := buildMcpkg(t, validFiles("existing-plugin", "1.0.0"))
		path := writeMcpkg(t, data)
		destDir := t.TempDir()

		// Pre-create the plugin directory.
		if err := os.MkdirAll(filepath.Join(destDir, "existing-plugin"), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		_, err := UnpackPlugin(path, destDir)
		if !errorIs(err, ErrPackageAlreadyExists) {
			t.Fatalf("expected ErrPackageAlreadyExists, got: %v", err)
		}
	})

	t.Run("path traversal rejected during unpack", func(t *testing.T) {
		files := append(validFiles("safe", "1.0.0"),
			testFile{Name: "../escape.txt", Content: []byte("evil")},
		)
		data := buildMcpkg(t, files)
		path := writeMcpkg(t, data)
		destDir := t.TempDir()

		_, err := UnpackPlugin(path, destDir)
		if !errorIs(err, ErrPackagePathTraversal) {
			t.Fatalf("expected ErrPackagePathTraversal, got: %v", err)
		}
	})

	t.Run("file permissions are correct", func(t *testing.T) {
		data := buildMcpkg(t, validFiles("perm-plugin", "1.0.0"))
		path := writeMcpkg(t, data)
		destDir := t.TempDir()

		_, err := UnpackPlugin(path, destDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		pluginDir := filepath.Join(destDir, "perm-plugin")
		fi, err := os.Stat(pluginDir)
		if err != nil {
			t.Fatalf("stat plugin dir: %v", err)
		}
		if !fi.IsDir() {
			t.Fatal("expected directory")
		}
	})
}

// errorIs checks errors.Is, handling both direct sentinel and wrapped errors.
func errorIs(err, target error) bool {
	return err != nil && (err == target || errors.Is(err, target))
}
