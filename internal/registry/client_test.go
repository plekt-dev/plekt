package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func sampleRegistry() Registry {
	return Registry{
		Version:   2,
		UpdatedAt: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		Plugins: []RegistryPlugin{
			{
				Name:        "tasks",
				Description: "Task management plugin",
				Author:      "plekt",
				License:     "MIT",
				Category:    "productivity",
				Tags:        []string{"tasks", "project"},
				Versions: []PluginVersion{
					{
						Version:        "1.2.0",
						DownloadURL:    "", // set per test
						ChecksumSHA256: "", // set per test
						SizeBytes:      4096,
						MinCoreVersion: "0.1.0",
						UpdatedAt:      time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
					},
					{
						Version:        "1.0.0",
						DownloadURL:    "",
						ChecksumSHA256: "",
						SizeBytes:      3000,
						MinCoreVersion: "0.1.0",
						UpdatedAt:      time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
					},
				},
			},
			{
				Name:        "notes",
				Description: "Note taking plugin",
				Author:      "plekt",
				License:     "MIT",
				Versions: []PluginVersion{
					{
						Version:        "0.9.0",
						MinCoreVersion: "0.1.0",
					},
				},
			},
		},
	}
}

func serveRegistry(t *testing.T, reg Registry) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(reg); err != nil {
			t.Fatalf("failed to encode registry: %v", err)
		}
	}))
}

func TestFetchRegistry_HappyPath(t *testing.T) {
	reg := sampleRegistry()
	srv := serveRegistry(t, reg)
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	got, err := client.FetchRegistry(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
	}
	if len(got.Plugins) != 2 {
		t.Errorf("plugins count = %d, want 2", len(got.Plugins))
	}
	if got.Plugins[0].Name != "tasks" {
		t.Errorf("first plugin name = %q, want %q", got.Plugins[0].Name, "tasks")
	}
	if len(got.Plugins[0].Versions) != 2 {
		t.Errorf("tasks versions count = %d, want 2", len(got.Plugins[0].Versions))
	}
}

func TestFetchRegistry_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	_, err := client.FetchRegistry(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errorIs(err, ErrRegistryFetch) {
		t.Errorf("error = %v, want ErrRegistryFetch", err)
	}
}

func TestFetchRegistry_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	_, err := client.FetchRegistry(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errorIs(err, ErrRegistryFetch) {
		t.Errorf("error = %v, want ErrRegistryFetch", err)
	}
}

func TestFetchRegistry_Caching(t *testing.T) {
	var hits atomic.Int64
	reg := sampleRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		json.NewEncoder(w).Encode(reg)
	}))
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL, WithCacheTTL(1*time.Hour))

	_, err := client.FetchRegistry(context.Background())
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	_, err = client.FetchRegistry(context.Background())
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("HTTP hits = %d, want 1 (cache should prevent second request)", got)
	}
}

func TestFetchRegistry_CacheExpired(t *testing.T) {
	var hits atomic.Int64
	reg := sampleRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		json.NewEncoder(w).Encode(reg)
	}))
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL, WithCacheTTL(0))

	_, err := client.FetchRegistry(context.Background())
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	_, err = client.FetchRegistry(context.Background())
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	if got := hits.Load(); got != 2 {
		t.Errorf("HTTP hits = %d, want 2 (expired cache should refetch)", got)
	}
}

func TestDownloadPlugin_HappyPath(t *testing.T) {
	payload := []byte("fake-plugin-binary-content")
	checksum := sha256sum(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	pv := PluginVersion{
		Version:        "1.2.0",
		DownloadURL:    srv.URL + "/tasks-1.2.0.mcpkg",
		ChecksumSHA256: checksum,
	}

	dest := filepath.Join(t.TempDir(), "tasks-1.2.0.mcpkg")
	client := NewHTTPRegistryClient("unused")

	err := client.DownloadPlugin(context.Background(), pv, dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("failed to read dest: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("file content mismatch")
	}
}

func TestDownloadPlugin_ChecksumMismatch(t *testing.T) {
	payload := []byte("fake-plugin-binary-content")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	pv := PluginVersion{
		Version:        "1.0.0",
		DownloadURL:    srv.URL + "/tasks.mcpkg",
		ChecksumSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
	}

	dest := filepath.Join(t.TempDir(), "tasks.mcpkg")
	client := NewHTTPRegistryClient("unused")

	err := client.DownloadPlugin(context.Background(), pv, dest)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errorIs(err, ErrChecksumMismatch) {
		t.Errorf("error = %v, want ErrChecksumMismatch", err)
	}

	if _, err := os.Stat(dest); err == nil {
		t.Error("dest file should not exist after checksum mismatch")
	}
}

func TestDownloadPlugin_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	pv := PluginVersion{
		Version:     "1.0.0",
		DownloadURL: srv.URL + "/tasks.mcpkg",
	}

	dest := filepath.Join(t.TempDir(), "tasks.mcpkg")
	client := NewHTTPRegistryClient("unused")

	err := client.DownloadPlugin(context.Background(), pv, dest)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errorIs(err, ErrDownloadFailed) {
		t.Errorf("error = %v, want ErrDownloadFailed", err)
	}
}

func TestFindPlugin_Found(t *testing.T) {
	reg := sampleRegistry()
	srv := serveRegistry(t, reg)
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	got, err := client.FindPlugin(context.Background(), "tasks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "tasks" {
		t.Errorf("name = %q, want %q", got.Name, "tasks")
	}
	if len(got.Versions) != 2 {
		t.Errorf("versions = %d, want 2", len(got.Versions))
	}
}

func TestFindPlugin_NotFound(t *testing.T) {
	reg := sampleRegistry()
	srv := serveRegistry(t, reg)
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	_, err := client.FindPlugin(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errorIs(err, ErrPluginNotFound) {
		t.Errorf("error = %v, want ErrPluginNotFound", err)
	}
}

func TestFindCompatibleVersion_Compatible(t *testing.T) {
	reg := sampleRegistry()
	srv := serveRegistry(t, reg)
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	plugin, pv, err := client.FindCompatibleVersion(context.Background(), "tasks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plugin.Name != "tasks" {
		t.Errorf("plugin name = %q, want %q", plugin.Name, "tasks")
	}
	if pv.Version != "1.2.0" {
		t.Errorf("version = %q, want %q (should pick newest compatible)", pv.Version, "1.2.0")
	}
}

func TestFindCompatibleVersion_NoCompatible(t *testing.T) {
	reg := sampleRegistry()
	// Set all versions to require a very high core version.
	for i := range reg.Plugins[0].Versions {
		reg.Plugins[0].Versions[i].MinCoreVersion = "99.0.0"
	}
	srv := serveRegistry(t, reg)
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	_, _, err := client.FindCompatibleVersion(context.Background(), "tasks")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errorIs(err, ErrNoCompatibleVersion) {
		t.Errorf("error = %v, want ErrNoCompatibleVersion", err)
	}
}

func TestFindCompatibleVersion_FallbackToOlder(t *testing.T) {
	reg := sampleRegistry()
	// Latest requires high core, older version is compatible.
	reg.Plugins[0].Versions[0].MinCoreVersion = "99.0.0"
	reg.Plugins[0].Versions[1].MinCoreVersion = "0.1.0"
	srv := serveRegistry(t, reg)
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	_, pv, err := client.FindCompatibleVersion(context.Background(), "tasks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pv.Version != "1.0.0" {
		t.Errorf("version = %q, want %q (should fall back to older compatible)", pv.Version, "1.0.0")
	}
}

func TestCheckUpdates_NewerAvailable(t *testing.T) {
	reg := sampleRegistry()
	srv := serveRegistry(t, reg)
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	installed := map[string]string{
		"tasks": "1.0.0", // registry has 1.2.0
	}

	updates, err := client.CheckUpdates(context.Background(), installed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates count = %d, want 1", len(updates))
	}
	if updates[0].CurrentVersion != "1.0.0" {
		t.Errorf("current = %q, want %q", updates[0].CurrentVersion, "1.0.0")
	}
	if updates[0].LatestVersion != "1.2.0" {
		t.Errorf("latest = %q, want %q", updates[0].LatestVersion, "1.2.0")
	}
	if updates[0].RequiresCoreUpdate {
		t.Error("should not require core update for min_core_version 0.1.0")
	}
}

func TestCheckUpdates_RequiresCoreUpdate(t *testing.T) {
	reg := sampleRegistry()
	reg.Plugins[0].Versions[0].MinCoreVersion = "99.0.0"
	srv := serveRegistry(t, reg)
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	installed := map[string]string{
		"tasks": "1.0.0",
	}

	updates, err := client.CheckUpdates(context.Background(), installed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates count = %d, want 1", len(updates))
	}
	if !updates[0].RequiresCoreUpdate {
		t.Error("should require core update")
	}
	if updates[0].RequiredCoreVersion != "99.0.0" {
		t.Errorf("required core = %q, want %q", updates[0].RequiredCoreVersion, "99.0.0")
	}
}

func TestCheckUpdates_UpToDate(t *testing.T) {
	reg := sampleRegistry()
	srv := serveRegistry(t, reg)
	defer srv.Close()

	client := NewHTTPRegistryClient(srv.URL)
	installed := map[string]string{
		"tasks": "1.2.0",
		"notes": "0.9.0",
	}

	updates, err := client.CheckUpdates(context.Background(), installed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("updates count = %d, want 0", len(updates))
	}
}

// --- helpers ---

func sha256sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func errorIs(err, target error) bool {
	return err != nil && (err == target || errors.Is(err, target))
}
