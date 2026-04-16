package loader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/plekt-dev/plekt/internal/db"
	"github.com/plekt-dev/plekt/internal/eventbus"
	"github.com/plekt-dev/plekt/internal/scheduler"
)

// DBQueryParams is the input payload for the mc_db.query host function.
// SQL must use parameterized queries; Args contains the positional arguments.
type DBQueryParams struct {
	SQL  string `json:"sql"`
	Args []any  `json:"args,omitempty"`
}

// DBQueryResult is the output of the mc_db.query host function.
type DBQueryResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// DBExecParams is the input payload for the mc_db.exec host function.
type DBExecParams struct {
	SQL  string `json:"sql"`
	Args []any  `json:"args,omitempty"`
}

// DBExecResult is the output of the mc_db.exec host function.
type DBExecResult struct {
	RowsAffected int64 `json:"rows_affected"`
	LastInsertID int64 `json:"last_insert_id"`
}

// EventEmitParams is the input payload for the mc_event.emit host function.
type EventEmitParams struct {
	EventName string `json:"event_name"`
	Payload   any    `json:"payload,omitempty"`
}

// DBQueryHostFn implements the mc_db::query host function.
// ctx must carry a PluginCallContext with a non-nil DB field.
func DBQueryHostFn(ctx context.Context, pcc PluginCallContext, params DBQueryParams) (DBQueryResult, error) {
	if pcc.DB == nil {
		return DBQueryResult{}, fmt.Errorf("no database for plugin %q", pcc.PluginName)
	}
	mgr := db.NewDBManager(pcc.DB, pcc.PluginName)
	result, err := mgr.Query(ctx, params.SQL, params.Args)
	if err != nil {
		return DBQueryResult{}, err
	}
	return DBQueryResult{
		Columns: result.Columns,
		Rows:    result.Rows,
	}, nil
}

// DBExecHostFn implements the mc_db::exec host function.
// ctx must carry a PluginCallContext with a non-nil DB field.
func DBExecHostFn(ctx context.Context, pcc PluginCallContext, params DBExecParams) (DBExecResult, error) {
	if pcc.DB == nil {
		return DBExecResult{}, fmt.Errorf("no database for plugin %q", pcc.PluginName)
	}
	mgr := db.NewDBManager(pcc.DB, pcc.PluginName)
	result, err := mgr.Exec(ctx, params.SQL, params.Args)
	if err != nil {
		return DBExecResult{}, err
	}
	return DBExecResult{
		RowsAffected: result.RowsAffected,
		LastInsertID: result.LastInsertID,
	}, nil
}

// ConfigGetParams is the input payload for the mc_config.get host function.
type ConfigGetParams struct {
	Key string `json:"key"`
}

// ConfigGetResult is the output of the mc_config.get host function.
type ConfigGetResult struct {
	Value any `json:"value"`
}

// ConfigGetHostFn implements the mc_config::get host function.
// Supported keys:
//   - "__available_plugins": returns []string of currently loaded plugin names.
//   - "__plugin_name": returns the calling plugin's own name.
func ConfigGetHostFn(_ context.Context, pcc PluginCallContext, params ConfigGetParams) ConfigGetResult {
	switch params.Key {
	case "__available_plugins":
		if pcc.LoadedPlugins != nil {
			return ConfigGetResult{Value: pcc.LoadedPlugins()}
		}
		return ConfigGetResult{Value: []string{}}
	case "__plugin_name":
		return ConfigGetResult{Value: pcc.PluginName}
	default:
		return ConfigGetResult{Value: nil}
	}
}

// CronValidateRequest is the input payload for the mc_cron::validate host function.
// Expression is a classic 5-field cron expression; Timezone is IANA (empty == UTC).
type CronValidateRequest struct {
	Expression string `json:"expression"`
	Timezone   string `json:"timezone,omitempty"`
}

// CronValidateResponse is the output of the mc_cron::validate host function.
// Valid is true iff Expression parses cleanly in Timezone; on success NextFires
// contains up to CronValidateNextFiresCount upcoming fire times as RFC3339 strings
// in the requested Timezone (with offset). Returning local-time RFC3339 lets the
// WASM plugin parse the wall-clock hour/date directly from string positions
// without needing the time zone DB inside the WASM sandbox: the scheduler-plugin
// week/month grids slot fires by these local-hour values, so a UTC return would
// shift every job by the timezone offset (e.g. 19:00 Berlin → 17:00 slot).
// On failure Error holds a sanitized human-readable reason.
type CronValidateResponse struct {
	Valid     bool     `json:"valid"`
	Error     string   `json:"error,omitempty"`
	NextFires []string `json:"next_fires,omitempty"`
}

// CronValidateNextFiresCount is the fixed number of upcoming fires returned by
// mc_cron::validate. Hardcoded per the Phase B contract.
const CronValidateNextFiresCount = 5

// cronValidatorSingleton is lazily constructed on first use. The underlying
// robfig/cron/v3 parser is stateless and safe for concurrent use, so one
// instance for the entire process is sufficient.
var (
	cronValidatorOnce     sync.Once
	cronValidatorInstance scheduler.CronValidator
)

func sharedCronValidator() scheduler.CronValidator {
	cronValidatorOnce.Do(func() {
		cronValidatorInstance = scheduler.NewCronValidator()
	})
	return cronValidatorInstance
}

// CronValidateHostFn implements the mc_cron::validate host function.
//
// Stateless: needs neither a DB nor an event bus. The PluginCallContext is
// accepted for signature uniformity with the other host functions; its fields
// are not read. Never returns a Go error: validation failures are reported
// through the Valid / Error fields of the response so WASM plugins can render
// the reason to the user without having to distinguish transport errors.
func CronValidateHostFn(_ context.Context, _ PluginCallContext, req CronValidateRequest) CronValidateResponse {
	fires, err := sharedCronValidator().Validate(req.Expression, req.Timezone, CronValidateNextFiresCount)
	if err != nil {
		return CronValidateResponse{Valid: false, Error: err.Error()}
	}
	// Resolve the requested timezone (empty == UTC) so we can format every
	// fire in local wall-clock with the correct offset suffix. The validator
	// already accepted the same string, so LoadLocation is guaranteed to
	// succeed here: fall back to UTC defensively just in case.
	loc, locErr := time.LoadLocation(req.Timezone)
	if locErr != nil || req.Timezone == "" {
		loc = time.UTC
	}
	out := make([]string, 0, len(fires))
	for _, f := range fires {
		out = append(out, f.In(loc).Format(time.RFC3339))
	}
	return CronValidateResponse{Valid: true, NextFires: out}
}

// Sentinel errors for EventEmitHostFn.
var (
	ErrEventNotDeclared    = errors.New("event not declared in manifest")
	ErrEventBusUnavailable = errors.New("event bus unavailable")
	ErrSystemEventBlocked  = errors.New("plugins cannot emit system events")
	ErrPayloadTooLarge     = errors.New("event payload exceeds maximum size")
)

// systemEventPrefixes lists event name prefixes reserved for the core system.
// Plugins cannot emit or subscribe to events matching these prefixes.
var systemEventPrefixes = []string{
	"plugin.", "token.", "mcp.", "web.", "auth.",
	"core.", "dashboard.", "agent.",
}

// maxEventPayloadSize is the maximum size of a serialized event payload (64 KB).
const maxEventPayloadSize = 65536

// isSystemEvent reports whether eventName starts with a reserved system prefix.
func isSystemEvent(eventName string) bool {
	for _, prefix := range systemEventPrefixes {
		if strings.HasPrefix(eventName, prefix) {
			return true
		}
	}
	return false
}

// EventEmitHostFn implements the mc_event::emit host function.
func EventEmitHostFn(ctx context.Context, pcc PluginCallContext, params EventEmitParams) error {
	if pcc.Bus == nil {
		return ErrEventBusUnavailable
	}
	// Block system event prefixes.
	if isSystemEvent(params.EventName) {
		return fmt.Errorf("%w: %q", ErrSystemEventBlocked, params.EventName)
	}
	// Enforce payload size limit.
	if params.Payload != nil {
		payloadBytes, err := json.Marshal(params.Payload)
		if err != nil {
			return fmt.Errorf("event payload marshal: %w", err)
		}
		if len(payloadBytes) > maxEventPayloadSize {
			return fmt.Errorf("%w: %d bytes (max %d)", ErrPayloadTooLarge, len(payloadBytes), maxEventPayloadSize)
		}
	}
	// Validate event name is declared in manifest.
	allowed := false
	for _, name := range pcc.AllowedEmits {
		if name == params.EventName {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("%w: %q", ErrEventNotDeclared, params.EventName)
	}
	pcc.Bus.Emit(ctx, eventbus.Event{
		Name:         params.EventName,
		SourcePlugin: pcc.PluginName,
		Payload:      params.Payload,
	})
	return nil
}
