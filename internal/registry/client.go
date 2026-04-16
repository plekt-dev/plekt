package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/plekt-dev/plekt/internal/version"
)

var (
	// ErrPluginNotFound is returned when a plugin name is not present in the registry.
	ErrPluginNotFound = errors.New("plugin not found in registry")
	// ErrChecksumMismatch is returned when a downloaded file's SHA256 does not match the registry.
	ErrChecksumMismatch = errors.New("downloaded file checksum does not match registry")
	// ErrRegistryFetch is returned when the registry.json cannot be retrieved or parsed.
	ErrRegistryFetch = errors.New("failed to fetch registry")
	// ErrDownloadFailed is returned when a plugin package download fails.
	ErrDownloadFailed = errors.New("failed to download plugin")
	// ErrNoCompatibleVersion is returned when no version of a plugin is compatible with the running core.
	ErrNoCompatibleVersion = errors.New("no compatible plugin version found")
)

const (
	defaultCacheTTL = 1 * time.Hour
	defaultTimeout  = 30 * time.Second
	maxDownloadSize = 100 * 1024 * 1024 // 100 MB
)

var defaultUserAgent = "Plekt/" + version.Version

// RegistryClient fetches plugin metadata and downloads plugin packages.
type RegistryClient interface {
	// FetchRegistry downloads and parses the registry.json.
	FetchRegistry(ctx context.Context) (Registry, error)

	// FetchRevokedKeys downloads and parses the revoked-keys.json sibling of
	// registry.json. Returns an empty RevokedKeys (Revoked: nil) without an
	// error when the file is absent (HTTP 404): revocation is opt-in and a
	// missing file means "no keys revoked yet". Other HTTP / parse failures
	// surface as ErrRegistryFetch so callers can decide to fail-closed.
	FetchRevokedKeys(ctx context.Context) (RevokedKeys, error)

	// DownloadPlugin downloads a .mcpkg file to destPath and verifies its SHA256 checksum.
	DownloadPlugin(ctx context.Context, pv PluginVersion, destPath string) error

	// FindPlugin looks up a plugin by name in the registry.
	// Returns the plugin entry (with all versions) or ErrPluginNotFound.
	FindPlugin(ctx context.Context, name string) (RegistryPlugin, error)

	// FindCompatibleVersion looks up a plugin by name and returns the newest
	// version compatible with the running core. Returns ErrNoCompatibleVersion
	// if no version satisfies the core constraint.
	FindCompatibleVersion(ctx context.Context, name string) (RegistryPlugin, PluginVersion, error)

	// CheckUpdates compares installed plugin versions against the registry
	// and returns available updates.
	CheckUpdates(ctx context.Context, installed map[string]string) ([]UpdateInfo, error)
}

// ClientOption configures an httpRegistryClient.
type ClientOption func(*httpRegistryClient)

// WithHTTPClient sets a custom HTTP client (useful for testing).
func WithHTTPClient(c *http.Client) ClientOption {
	return func(h *httpRegistryClient) {
		h.httpClient = c
	}
}

// WithCacheTTL sets the cache duration for registry.json.
func WithCacheTTL(d time.Duration) ClientOption {
	return func(h *httpRegistryClient) {
		h.cacheTTL = d
	}
}

// WithUserAgent sets a custom User-Agent header.
func WithUserAgent(ua string) ClientOption {
	return func(h *httpRegistryClient) {
		h.userAgent = ua
	}
}

type registryCache struct {
	registry  Registry
	fetchedAt time.Time
}

type httpRegistryClient struct {
	registryURL string
	httpClient  *http.Client
	cacheTTL    time.Duration
	userAgent   string

	mu    sync.Mutex
	cache *registryCache
}

// NewHTTPRegistryClient creates a new registry client that fetches from the given URL.
func NewHTTPRegistryClient(registryURL string, opts ...ClientOption) RegistryClient {
	c := &httpRegistryClient{
		registryURL: registryURL,
		httpClient:  &http.Client{Timeout: defaultTimeout},
		cacheTTL:    defaultCacheTTL,
		userAgent:   defaultUserAgent,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *httpRegistryClient) FetchRegistry(ctx context.Context) (Registry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cache != nil && time.Since(c.cache.fetchedAt) < c.cacheTTL {
		return c.cache.registry, nil
	}

	reg, err := c.fetchFromRemote(ctx)
	if err != nil {
		return Registry{}, err
	}

	c.cache = &registryCache{
		registry:  reg,
		fetchedAt: time.Now(),
	}
	return reg, nil
}

func (c *httpRegistryClient) fetchFromRemote(ctx context.Context) (Registry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.registryURL, nil)
	if err != nil {
		return Registry{}, fmt.Errorf("%w: %v", ErrRegistryFetch, err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Registry{}, fmt.Errorf("%w: %v", ErrRegistryFetch, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Registry{}, fmt.Errorf("%w: HTTP %d", ErrRegistryFetch, resp.StatusCode)
	}

	var reg Registry
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return Registry{}, fmt.Errorf("%w: %v", ErrRegistryFetch, err)
	}
	return reg, nil
}

// revokedKeysURL derives the revoked-keys.json URL from the registry URL by
// replacing the final path segment. e.g. ".../registry.json" → ".../revoked-keys.json".
// If the URL is malformed or does not have a path component to replace, the
// fallback is to append "revoked-keys.json" relative to the URL.
func revokedKeysURL(registryURL string) (string, error) {
	u, err := url.Parse(registryURL)
	if err != nil {
		return "", err
	}
	dir := path.Dir(u.Path)
	if dir == "." || dir == "/" {
		u.Path = path.Join(dir, "revoked-keys.json")
	} else {
		u.Path = dir + "/revoked-keys.json"
	}
	return u.String(), nil
}

func (c *httpRegistryClient) FetchRevokedKeys(ctx context.Context) (RevokedKeys, error) {
	revURL, err := revokedKeysURL(c.registryURL)
	if err != nil {
		return RevokedKeys{}, fmt.Errorf("%w: derive revoked-keys URL: %v", ErrRegistryFetch, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, revURL, nil)
	if err != nil {
		return RevokedKeys{}, fmt.Errorf("%w: %v", ErrRegistryFetch, err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return RevokedKeys{}, fmt.Errorf("%w: %v", ErrRegistryFetch, err)
	}
	defer resp.Body.Close()

	// Missing revoked-keys.json is normal: treat as "no revocations".
	if resp.StatusCode == http.StatusNotFound {
		return RevokedKeys{Version: 1}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return RevokedKeys{}, fmt.Errorf("%w: HTTP %d", ErrRegistryFetch, resp.StatusCode)
	}

	var rk RevokedKeys
	if err := json.NewDecoder(resp.Body).Decode(&rk); err != nil {
		return RevokedKeys{}, fmt.Errorf("%w: parse revoked-keys.json: %v", ErrRegistryFetch, err)
	}
	return rk, nil
}

func (c *httpRegistryClient) DownloadPlugin(ctx context.Context, pv PluginVersion, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pv.DownloadURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrDownloadFailed, resp.StatusCode)
	}

	// Stream to a temp file in the same directory so rename is atomic.
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, "mcpkg-download-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpPath) // no-op if already renamed
	}()

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxDownloadSize+1)
	written, err := io.Copy(tmp, io.TeeReader(limited, hasher))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}
	if written > maxDownloadSize {
		return fmt.Errorf("%w: file exceeds %d byte limit", ErrDownloadFailed, maxDownloadSize)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if got != pv.ChecksumSHA256 {
		return fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, pv.ChecksumSHA256, got)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}
	return nil
}

func (c *httpRegistryClient) FindPlugin(ctx context.Context, name string) (RegistryPlugin, error) {
	reg, err := c.FetchRegistry(ctx)
	if err != nil {
		return RegistryPlugin{}, err
	}
	for _, p := range reg.Plugins {
		if p.Name == name {
			return p, nil
		}
	}
	return RegistryPlugin{}, ErrPluginNotFound
}

func (c *httpRegistryClient) FindCompatibleVersion(ctx context.Context, name string) (RegistryPlugin, PluginVersion, error) {
	plugin, err := c.FindPlugin(ctx, name)
	if err != nil {
		return RegistryPlugin{}, PluginVersion{}, err
	}
	for _, v := range plugin.Versions {
		ok, err := version.AtLeast(version.Version, v.MinCoreVersion)
		if err != nil {
			continue
		}
		if ok {
			return plugin, v, nil
		}
	}
	return RegistryPlugin{}, PluginVersion{}, fmt.Errorf("%w: %s (core %s)", ErrNoCompatibleVersion, name, version.Version)
}

func (c *httpRegistryClient) CheckUpdates(ctx context.Context, installed map[string]string) ([]UpdateInfo, error) {
	reg, err := c.FetchRegistry(ctx)
	if err != nil {
		return nil, err
	}
	var updates []UpdateInfo
	for _, p := range reg.Plugins {
		if len(p.Versions) == 0 {
			continue
		}
		currentVer, ok := installed[p.Name]
		if !ok {
			continue
		}
		latest := p.Versions[0] // newest first
		if latest.Version == currentVer {
			continue
		}
		info := UpdateInfo{
			Name:           p.Name,
			CurrentVersion: currentVer,
			LatestVersion:  latest.Version,
			DownloadURL:    latest.DownloadURL,
			ChecksumSHA256: latest.ChecksumSHA256,
		}
		if latest.MinCoreVersion != "" {
			ok, _ := version.AtLeast(version.Version, latest.MinCoreVersion)
			if !ok {
				info.RequiresCoreUpdate = true
				info.RequiredCoreVersion = latest.MinCoreVersion
			}
		}
		updates = append(updates, info)
	}
	return updates, nil
}
