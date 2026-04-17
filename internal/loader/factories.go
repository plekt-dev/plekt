package loader

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	extism "github.com/extism/go-sdk"
	_ "modernc.org/sqlite" // CGO-free SQLite driver; registers the "sqlite" driver name.

	"github.com/plekt-dev/plekt/internal/db"
)

// ---------------------------------------------------------------------------
// extismPluginFactory: real WASM factory backed by the Extism SDK.
// ---------------------------------------------------------------------------

// extismPluginFactory implements pluginFactory using extism.NewPlugin.
type extismPluginFactory struct {
	memoryLimitPages uint32
}

// newExtismPluginFactory returns a pluginFactory backed by the Extism runtime.
func newExtismPluginFactory(memoryLimitPages uint32) pluginFactory {
	return &extismPluginFactory{memoryLimitPages: memoryLimitPages}
}

func (f *extismPluginFactory) New(wasmPath string, _ []HostFunction, memoryLimitPages uint32, pcc PluginCallContext, allowedHosts []string) (pluginRunner, error) {
	pages := memoryLimitPages
	if pages == 0 {
		pages = f.memoryLimitPages
	}
	// AllowedHosts are sourced from the core plugin_host_grants store; an empty
	// or nil slice means default-deny (no outbound network for the plugin).
	var hosts []string
	if len(allowedHosts) > 0 {
		hosts = append(hosts, allowedHosts...)
	}
	manifest := extism.Manifest{
		Wasm: []extism.Wasm{extism.WasmFile{Path: wasmPath}},
		Memory: &extism.ManifestMemory{
			MaxPages: pages,
			// 200 MB cap on HTTP response bodies from pdk.NewHTTPRequest. Default
			// is 50 MB; voice-plugin multipart uploads to Whisper/OpenAI can push
			// past the limit because MaxBytesReader here also accounts for the
			// full request body echo path inside wazero's host HTTP.
			MaxHttpResponseBytes: 200 * 1024 * 1024,
		},
		// No AllowedPaths: plugins have no filesystem access.
		// AllowedHosts come from the operator-controlled plugin_host_grants store.
		AllowedHosts: hosts,
	}
	cfg := extism.PluginConfig{
		EnableWasi: true, // Go-compiled WASM imports wasi_snapshot_preview1 (e.g. proc_exit).
	}

	hostFns := buildExtismHostFunctions(pcc)

	p, err := extism.NewPlugin(context.Background(), manifest, cfg, hostFns)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWASMInit, err)
	}

	// Go wasip1 plugins are compiled with -buildmode=c-shared, producing a
	// reactor module that exports _initialize. Extism auto-detects _initialize
	// (in runtime.go/reactorModule) and calls it before the first function.
	return &extismPluginRunner{plugin: p}, nil
}

// buildExtismHostFunctions creates Extism host function callbacks that delegate
// to the Go-level host function implementations (DBQueryHostFn, DBExecHostFn,
// EventEmitHostFn) with the given PluginCallContext captured in closures.
func buildExtismHostFunctions(pcc PluginCallContext) []extism.HostFunction {
	dbQuery := extism.NewHostFunctionWithStack(
		"query",
		func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
			// Read JSON input from WASM memory.
			offset := stack[0]
			inputBytes, err := p.ReadBytes(offset)
			if err != nil {
				writeHostError(p, stack, fmt.Sprintf("read input: %v", err))
				return
			}

			var params DBQueryParams
			if err := json.Unmarshal(inputBytes, &params); err != nil {
				writeHostError(p, stack, fmt.Sprintf("unmarshal input: %v", err))
				return
			}

			result, err := DBQueryHostFn(ctx, pcc, params)
			if err != nil {
				writeHostError(p, stack, fmt.Sprintf("db query: %v", err))
				return
			}

			writeHostResult(p, stack, result)
		},
		[]extism.ValueType{extism.ValueTypeI64},
		[]extism.ValueType{extism.ValueTypeI64},
	)
	dbQuery.SetNamespace("mc_db")

	dbExec := extism.NewHostFunctionWithStack(
		"exec",
		func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
			offset := stack[0]
			inputBytes, err := p.ReadBytes(offset)
			if err != nil {
				writeHostError(p, stack, fmt.Sprintf("read input: %v", err))
				return
			}

			var params DBExecParams
			if err := json.Unmarshal(inputBytes, &params); err != nil {
				writeHostError(p, stack, fmt.Sprintf("unmarshal input: %v", err))
				return
			}

			result, err := DBExecHostFn(ctx, pcc, params)
			if err != nil {
				writeHostError(p, stack, fmt.Sprintf("db exec: %v", err))
				return
			}

			writeHostResult(p, stack, result)
		},
		[]extism.ValueType{extism.ValueTypeI64},
		[]extism.ValueType{extism.ValueTypeI64},
	)
	dbExec.SetNamespace("mc_db")

	eventEmit := extism.NewHostFunctionWithStack(
		"emit",
		func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
			offset := stack[0]
			inputBytes, err := p.ReadBytes(offset)
			if err != nil {
				writeHostError(p, stack, fmt.Sprintf("read input: %v", err))
				return
			}

			var params EventEmitParams
			if err := json.Unmarshal(inputBytes, &params); err != nil {
				writeHostError(p, stack, fmt.Sprintf("unmarshal input: %v", err))
				return
			}

			if err := EventEmitHostFn(ctx, pcc, params); err != nil {
				writeHostError(p, stack, fmt.Sprintf("event emit: %v", err))
				return
			}

			// Return empty success JSON.
			writeHostResult(p, stack, map[string]string{"status": "ok"})
		},
		[]extism.ValueType{extism.ValueTypeI64},
		[]extism.ValueType{extism.ValueTypeI64},
	)
	eventEmit.SetNamespace("mc_event")

	timeNow := extism.NewHostFunctionWithStack(
		"now",
		func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
			now := time.Now().UTC().Format(time.RFC3339)
			writeHostResult(p, stack, map[string]string{"now": now})
		},
		[]extism.ValueType{extism.ValueTypeI64},
		[]extism.ValueType{extism.ValueTypeI64},
	)
	timeNow.SetNamespace("mc_time")

	configGet := extism.NewHostFunctionWithStack(
		"get",
		func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
			offset := stack[0]
			inputBytes, err := p.ReadBytes(offset)
			if err != nil {
				writeHostError(p, stack, fmt.Sprintf("read input: %v", err))
				return
			}

			var params ConfigGetParams
			if err := json.Unmarshal(inputBytes, &params); err != nil {
				writeHostError(p, stack, fmt.Sprintf("unmarshal input: %v", err))
				return
			}

			result := ConfigGetHostFn(ctx, pcc, params)
			writeHostResult(p, stack, result)
		},
		[]extism.ValueType{extism.ValueTypeI64},
		[]extism.ValueType{extism.ValueTypeI64},
	)
	configGet.SetNamespace("mc_config")

	cronValidate := extism.NewHostFunctionWithStack(
		"validate",
		func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
			offset := stack[0]
			inputBytes, err := p.ReadBytes(offset)
			if err != nil {
				writeHostError(p, stack, fmt.Sprintf("read input: %v", err))
				return
			}
			var params CronValidateRequest
			if err := json.Unmarshal(inputBytes, &params); err != nil {
				writeHostError(p, stack, fmt.Sprintf("unmarshal input: %v", err))
				return
			}
			// Stateless: CronValidateHostFn never returns a Go error: it reports
			// validation failures via the response struct itself.
			result := CronValidateHostFn(ctx, pcc, params)
			writeHostResult(p, stack, result)
		},
		[]extism.ValueType{extism.ValueTypeI64},
		[]extism.ValueType{extism.ValueTypeI64},
	)
	cronValidate.SetNamespace("mc_cron")

	return []extism.HostFunction{dbQuery, dbExec, eventEmit, timeNow, configGet, cronValidate}
}

// writeHostResult marshals result to JSON and writes it to WASM memory.
// Updates stack[0] with the output offset.
func writeHostResult(p *extism.CurrentPlugin, stack []uint64, result any) {
	out, err := json.Marshal(result)
	if err != nil {
		writeHostError(p, stack, fmt.Sprintf("marshal result: %v", err))
		return
	}
	outMem, err := p.WriteBytes(out)
	if err != nil {
		stack[0] = 0
		return
	}
	stack[0] = outMem
}

// writeHostError writes a JSON error object to WASM memory.
// The error message is sanitized: never contains raw SQL or secrets.
func writeHostError(p *extism.CurrentPlugin, stack []uint64, msg string) {
	errJSON, _ := json.Marshal(map[string]string{"error": msg})
	outMem, err := p.WriteBytes(errJSON)
	if err != nil {
		stack[0] = 0
		return
	}
	stack[0] = outMem
}

// extismPluginRunner wraps *extism.Plugin and implements pluginRunner.
type extismPluginRunner struct {
	plugin *extism.Plugin
}

func (r *extismPluginRunner) CallFunc(name string, input []byte) ([]byte, error) {
	rc, out, err := r.plugin.Call(name, input)
	if err != nil {
		return nil, err
	}
	if rc != 0 {
		// WASM function returned a non-zero exit code indicating an error.
		// The output buffer contains the error message.
		return nil, fmt.Errorf("%s", string(out))
	}
	return out, nil
}

func (r *extismPluginRunner) Close() error {
	return r.plugin.Close(context.Background())
}

// ---------------------------------------------------------------------------
// sqliteDBFactory: real DB factory using modernc.org/sqlite (no CGO).
// ---------------------------------------------------------------------------

// sqliteDBFactory implements dbFactory using the modernc sqlite driver.
type sqliteDBFactory struct{}

// Open opens a per-plugin SQLite database with foreign-key enforcement, WAL
// journal mode, and a busy timeout.
//
// SQLite has foreign keys DISABLED by default on every connection for backward
// compatibility. Without `PRAGMA foreign_keys = ON` the FK clauses declared in
// plugin schema.yaml files (via ColumnSchema.References) are accepted at DDL
// time but never enforced at runtime.
//
// WAL mode is required because plugin handlers can fire multiple concurrent
// writes (e.g. tasks-plugin update_board_column during a drag-and-drop column
// reorder sends N parallel UPDATEs). The default rollback journal takes an
// exclusive write lock on the whole DB file, causing SQLITE_BUSY on the 2nd+
// concurrent exec. WAL allows a single writer + concurrent readers and, with
// busy_timeout, lets contending writers wait instead of failing immediately.
//
// We rely on modernc.org/sqlite's `_pragma=` query-parameter form so every
// pragma is applied to every connection the *sql.DB pool hands out.
func (f *sqliteDBFactory) Open(dataSourceName string) (*sql.DB, error) {
	return sql.Open("sqlite", db.WithPluginPragmas(dataSourceName))
}
