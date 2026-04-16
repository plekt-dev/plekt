package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/plekt-dev/plekt/internal/config"
	"github.com/plekt-dev/plekt/internal/scheduler"
)

// lazyPluginBridge is a scheduler.PluginBridge that resolves the
// scheduler-plugin's *sql.DB on every call. This decouples the webhook
// dispatcher's lifecycle from the scheduler engine's plugin.loaded /
// plugin.unloaded cycle: the dispatcher can be constructed at startup before
// the plugin is loaded and will simply return ErrSchedulerPluginUnavailable
// for any operation until the plugin is present.
//
// Resolving on every call is cheap because *sql.DB is a connection pool, not
// a connection. We pay one map lookup inside the loader's PluginManager.
type lazyPluginBridge struct {
	resolve func(name string) (*sql.DB, error)
}

// errSchedulerUnavailable is returned by every method when the scheduler
// plugin is not currently loaded.
var errSchedulerUnavailable = errors.New("scheduler plugin not loaded")

func newLazyPluginBridge(resolve func(name string) (*sql.DB, error)) *lazyPluginBridge {
	return &lazyPluginBridge{resolve: resolve}
}

// inner builds a fresh sqlite-backed PluginBridge for the current scheduler
// plugin DB, or returns an error if the plugin is not loaded.
func (b *lazyPluginBridge) inner() (scheduler.PluginBridge, error) {
	db, err := b.resolve(scheduler.PluginName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errSchedulerUnavailable, err)
	}
	return scheduler.NewSQLiteBridge(db), nil
}

func (b *lazyPluginBridge) LoadEnabledJobs(ctx context.Context) ([]scheduler.JobRecord, error) {
	inner, err := b.inner()
	if err != nil {
		return nil, err
	}
	return inner.LoadEnabledJobs(ctx)
}

func (b *lazyPluginBridge) LoadJob(ctx context.Context, jobID int64) (scheduler.JobRecord, error) {
	inner, err := b.inner()
	if err != nil {
		return scheduler.JobRecord{}, err
	}
	return inner.LoadJob(ctx, jobID)
}

func (b *lazyPluginBridge) InsertJobRun(ctx context.Context, rec scheduler.JobRunRecord) (int64, error) {
	inner, err := b.inner()
	if err != nil {
		return 0, err
	}
	return inner.InsertJobRun(ctx, rec)
}

func (b *lazyPluginBridge) UpdateJobRun(ctx context.Context, runID int64, status scheduler.RunStatus, errMsg *string, durationMs int64) error {
	inner, err := b.inner()
	if err != nil {
		return err
	}
	return inner.UpdateJobRun(ctx, runID, status, errMsg, durationMs)
}

func (b *lazyPluginBridge) UpdateJobLastRun(ctx context.Context, jobID int64, runAt string, status scheduler.RunStatus, errMsg *string, durationMs int64) error {
	inner, err := b.inner()
	if err != nil {
		return err
	}
	return inner.UpdateJobLastRun(ctx, jobID, runAt, status, errMsg, durationMs)
}

func (b *lazyPluginBridge) UpdateJobNextFire(ctx context.Context, jobID int64, nextFireAt *string) error {
	inner, err := b.inner()
	if err != nil {
		return err
	}
	return inner.UpdateJobNextFire(ctx, jobID, nextFireAt)
}

func (b *lazyPluginBridge) PromoteRunToActive(ctx context.Context, runID int64, triggeredAt string) error {
	inner, err := b.inner()
	if err != nil {
		return err
	}
	return inner.PromoteRunToActive(ctx, runID, triggeredAt)
}

func (b *lazyPluginBridge) GetJobRun(ctx context.Context, runID int64) (scheduler.JobRunRecord, error) {
	inner, err := b.inner()
	if err != nil {
		return scheduler.JobRunRecord{}, err
	}
	return inner.GetJobRun(ctx, runID)
}

func (b *lazyPluginBridge) UpdateJobRunOutput(ctx context.Context, runID int64, output string) error {
	inner, err := b.inner()
	if err != nil {
		return err
	}
	return inner.UpdateJobRunOutput(ctx, runID, output)
}

func (b *lazyPluginBridge) UpdateJobRunDispatchStatus(ctx context.Context, runID int64, status scheduler.DispatchStatus) error {
	inner, err := b.inner()
	if err != nil {
		return err
	}
	return inner.UpdateJobRunDispatchStatus(ctx, runID, status)
}

// webhookCallbackBaseURL derives the public base URL the webhook dispatcher
// puts in callback_url. Prefer an explicit cfg.PublicBaseURL if present;
// otherwise derive from the bind address.
func webhookCallbackBaseURL(cfg config.Config) string {
	if cfg.Server.PublicBaseURL != "" {
		return strings.TrimRight(cfg.Server.PublicBaseURL, "/")
	}
	addr := cfg.Server.Addr
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}
	slog.Warn("public_base_url not configured, webhook callbacks will use localhost: set server.public_base_url in config for production",
		"derived_url", "http://"+addr)
	return "http://" + addr
}
