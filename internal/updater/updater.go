// Package updater implements the Plekt core auto-updater. It periodically
// checks the plugin registry for a newer core binary, downloads it, and
// spawns the plekt-updater helper to replace the running executable.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/registry"
	"github.com/plekt-dev/plekt/internal/version"
)

// Sentinel errors returned by Updater methods.
var (
	ErrNoUpdate      = errors.New("no update available")
	ErrDocker        = errors.New("cannot self-update inside a Docker container")
	ErrUnsupportedOS = errors.New("no binary available for this OS/arch")
)

// UpdateStatus represents the current phase of the updater state machine.
type UpdateStatus int

const (
	StatusIdle        UpdateStatus = iota
	StatusChecking                 // actively querying the registry
	StatusAvailable                // newer version detected
	StatusDownloading              // binary download in progress
	StatusReady                    // updater spawned, process will be killed soon
	StatusFailed                   // last operation failed
)

// String returns a human-readable label for the status.
func (s UpdateStatus) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusChecking:
		return "checking"
	case StatusAvailable:
		return "available"
	case StatusDownloading:
		return "downloading"
	case StatusReady:
		return "ready"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// UpdateState is a snapshot of the updater's current state, safe to read
// concurrently from any goroutine.
type UpdateState struct {
	Status         UpdateStatus
	CurrentVersion string
	LatestVersion  string
	ReleaseNotes   string
	ReleasedAt     time.Time
	DownloadURL    string
	Error          string
	CheckedAt      time.Time
	IsDocker       bool
}

// Config holds the dependencies and tunables for an Updater instance.
type Config struct {
	RegistryClient registry.RegistryClient
	Bus            eventbus.EventBus
	CheckInterval  time.Duration // 0 = manual only
	DataDir        string
}

// Updater manages core binary update checks and application.
type Updater struct {
	regClient registry.RegistryClient
	bus       eventbus.EventBus
	interval  time.Duration
	dataDir   string
	isDocker  bool

	mu    sync.Mutex
	state atomic.Value // stores UpdateState

	stopOnce sync.Once
	stopCh   chan struct{}
}

// New creates a new Updater with the given configuration.
func New(cfg Config) *Updater {
	u := &Updater{
		regClient: cfg.RegistryClient,
		bus:       cfg.Bus,
		interval:  cfg.CheckInterval,
		dataDir:   cfg.DataDir,
		isDocker:  IsRunningInDocker(),
		stopCh:    make(chan struct{}),
	}
	u.state.Store(UpdateState{
		Status:         StatusIdle,
		CurrentVersion: version.Version,
		IsDocker:       u.isDocker,
	})
	return u
}

// Start begins the background update-check loop. It runs an immediate check
// on first invocation and then repeats at cfg.CheckInterval. If interval is 0,
// no background loop is started and checks are manual-only via CheckNow.
func (u *Updater) Start(ctx context.Context) {
	if _, err := u.CheckNow(ctx); err != nil {
		slog.Warn("updater: initial check failed", "error", err)
	}

	if u.interval <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(u.interval)
		defer ticker.Stop()
		for {
			select {
			case <-u.stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := u.CheckNow(ctx); err != nil {
					slog.Warn("updater: periodic check failed", "error", err)
				}
			}
		}
	}()
}

// Stop signals the background loop to exit.
func (u *Updater) Stop() {
	u.stopOnce.Do(func() {
		close(u.stopCh)
	})
}

// State returns a snapshot of the current updater state.
func (u *Updater) State() UpdateState {
	return u.state.Load().(UpdateState)
}

func (u *Updater) setState(s UpdateState) {
	s.CurrentVersion = version.Version
	s.IsDocker = u.isDocker
	u.state.Store(s)
}

// CheckNow performs an immediate update check against the registry.
func (u *Updater) CheckNow(ctx context.Context) (UpdateState, error) {
	u.setState(UpdateState{Status: StatusChecking, CheckedAt: time.Now().UTC()})

	reg, err := u.regClient.FetchRegistry(ctx)
	if err != nil {
		st := UpdateState{Status: StatusFailed, Error: err.Error(), CheckedAt: time.Now().UTC()}
		u.setState(st)
		u.emitFailed("check", err)
		return u.State(), fmt.Errorf("fetch registry: %w", err)
	}

	if reg.Core == nil {
		st := UpdateState{Status: StatusIdle, CheckedAt: time.Now().UTC()}
		u.setState(st)
		return u.State(), nil
	}

	cmp, cmpErr := version.Compare(reg.Core.Version, version.Version)
	if cmpErr != nil {
		st := UpdateState{Status: StatusFailed, Error: cmpErr.Error(), CheckedAt: time.Now().UTC()}
		u.setState(st)
		u.emitFailed("check", cmpErr)
		return u.State(), fmt.Errorf("version compare: %w", cmpErr)
	}
	if cmp <= 0 {
		st := UpdateState{Status: StatusIdle, CheckedAt: time.Now().UTC()}
		u.setState(st)
		return u.State(), nil
	}

	binary, found := findBinary(reg.Core.Binaries, runtime.GOOS, runtime.GOARCH)
	if !found {
		st := UpdateState{Status: StatusFailed, Error: ErrUnsupportedOS.Error(), CheckedAt: time.Now().UTC()}
		u.setState(st)
		u.emitFailed("check", ErrUnsupportedOS)
		return u.State(), ErrUnsupportedOS
	}

	st := UpdateState{
		Status:        StatusAvailable,
		LatestVersion: reg.Core.Version,
		ReleaseNotes:  reg.Core.ReleaseNotes,
		ReleasedAt:    reg.Core.ReleasedAt,
		DownloadURL:   binary.DownloadURL,
		CheckedAt:     time.Now().UTC(),
	}
	u.setState(st)

	if u.bus != nil {
		u.bus.Emit(ctx, eventbus.Event{
			Name: eventbus.EventCoreUpdateAvailable,
			Payload: eventbus.CoreUpdateAvailablePayload{
				CurrentVersion: version.Version,
				LatestVersion:  reg.Core.Version,
				ReleaseNotes:   reg.Core.ReleaseNotes,
				ReleasedAt:     reg.Core.ReleasedAt,
				DetectedAt:     time.Now().UTC(),
			},
		})
	}

	return u.State(), nil
}

// Apply downloads the new binary, verifies its checksum, then spawns
// plekt-updater which will kill this process, swap binaries, and restart.
func (u *Updater) Apply(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.isDocker {
		return ErrDocker
	}

	st := u.State()
	if st.Status != StatusAvailable {
		return ErrNoUpdate
	}

	u.setState(UpdateState{
		Status:        StatusDownloading,
		LatestVersion: st.LatestVersion,
		ReleaseNotes:  st.ReleaseNotes,
		ReleasedAt:    st.ReleasedAt,
		DownloadURL:   st.DownloadURL,
		CheckedAt:     st.CheckedAt,
	})

	// Resolve current executable path.
	exePath, err := os.Executable()
	if err != nil {
		u.failApply(st, fmt.Errorf("os.Executable: %w", err))
		return err
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		u.failApply(st, fmt.Errorf("eval symlinks: %w", err))
		return err
	}

	newPath := exePath + ".new"
	archivePath := exePath + ".download"
	checksum, err := u.downloadBinary(ctx, st.DownloadURL, archivePath)
	if err != nil {
		_ = os.Remove(archivePath)
		u.failApply(st, fmt.Errorf("download: %w", err))
		return err
	}

	reg, err := u.regClient.FetchRegistry(ctx)
	if err != nil {
		_ = os.Remove(archivePath)
		u.failApply(st, fmt.Errorf("re-fetch registry: %w", err))
		return err
	}
	var expectedChecksum string
	if reg.Core != nil {
		if bin, ok := findBinary(reg.Core.Binaries, runtime.GOOS, runtime.GOARCH); ok {
			expectedChecksum = bin.ChecksumSHA256
		}
	}
	if expectedChecksum != "" && checksum != expectedChecksum {
		_ = os.Remove(archivePath)
		checksumErr := fmt.Errorf("checksum mismatch: got %s, expected %s", checksum, expectedChecksum)
		u.failApply(st, checksumErr)
		return checksumErr
	}

	// plekt-updater inside the archive is ignored: replacing the running
	// updater mid-swap is unsafe.
	if err := extractCoreBinary(archivePath, newPath); err != nil {
		_ = os.Remove(archivePath)
		_ = os.Remove(newPath)
		u.failApply(st, fmt.Errorf("extract core binary: %w", err))
		return err
	}
	_ = os.Remove(archivePath)

	_ = os.Chmod(newPath, 0o755)

	updaterName := "plekt-updater"
	if runtime.GOOS == "windows" {
		updaterName = "plekt-updater.exe"
	}
	updaterPath := filepath.Join(filepath.Dir(exePath), updaterName)
	if _, err := os.Stat(updaterPath); err != nil {
		_ = os.Remove(newPath)
		u.failApply(st, fmt.Errorf("plekt-updater not found at %s", updaterPath))
		return fmt.Errorf("plekt-updater not found: %w", err)
	}

	// Build args for plekt-updater.
	cwd, _ := os.Getwd()
	pid := os.Getpid()
	updaterArgs := []string{
		"--pid", strconv.Itoa(pid),
		"--target", exePath,
		"--new", newPath,
		"--cwd", cwd,
	}
	originalArgs := collectOriginalArgs()
	if len(originalArgs) > 0 {
		updaterArgs = append(updaterArgs, "--args", strings.Join(originalArgs, "\x00"))
	}

	slog.Info("updater: spawning plekt-updater",
		"updater", updaterPath,
		"pid", pid,
		"target", exePath,
	)
	cmd := exec.Command(updaterPath, updaterArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = os.Remove(newPath)
		u.failApply(st, fmt.Errorf("spawn updater: %w", err))
		return err
	}

	u.setState(UpdateState{
		Status:        StatusReady,
		LatestVersion: st.LatestVersion,
		ReleaseNotes:  st.ReleaseNotes,
		ReleasedAt:    st.ReleasedAt,
		CheckedAt:     st.CheckedAt,
	})

	if u.bus != nil {
		u.bus.Emit(ctx, eventbus.Event{
			Name: eventbus.EventCoreUpdateApplied,
			Payload: eventbus.CoreUpdateAppliedPayload{
				PreviousVersion: version.Version,
				NewVersion:      st.LatestVersion,
				AppliedAt:       time.Now().UTC(),
			},
		})
	}

	// The updater will kill us: nothing more to do.
	return nil
}

// collectOriginalArgs returns os.Args[1:] for passing to the restarted process.
func collectOriginalArgs() []string {
	if len(os.Args) <= 1 {
		return nil
	}
	return os.Args[1:]
}

func (u *Updater) failApply(prev UpdateState, err error) {
	u.setState(UpdateState{
		Status:        StatusFailed,
		LatestVersion: prev.LatestVersion,
		Error:         err.Error(),
		CheckedAt:     prev.CheckedAt,
	})
	u.emitFailed("apply", err)
}

func (u *Updater) emitFailed(op string, err error) {
	if u.bus == nil {
		return
	}
	u.bus.Emit(context.Background(), eventbus.Event{
		Name: eventbus.EventCoreUpdateFailed,
		Payload: eventbus.CoreUpdateFailedPayload{
			Operation:  op,
			Error:      err.Error(),
			OccurredAt: time.Now().UTC(),
		},
	})
}

func (u *Updater) downloadBinary(ctx context.Context, url, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func findBinary(binaries []registry.CoreBinary, goos, goarch string) (registry.CoreBinary, bool) {
	for _, b := range binaries {
		if b.OS == goos && b.Arch == goarch {
			return b, true
		}
	}
	return registry.CoreBinary{}, false
}

// CleanupOldBinary removes the .old binary left behind by a previous update.
// Should be called at startup.
func CleanupOldBinary() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return err
	}
	oldPath := exePath + ".old"
	if _, err := os.Stat(oldPath); err != nil {
		return nil
	}
	return os.Remove(oldPath)
}
