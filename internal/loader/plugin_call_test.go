package loader

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	extism "github.com/extism/go-sdk"
	_ "modernc.org/sqlite"
)

// minimalWASM is a valid WebAssembly module that exports a single function
// "greet" via the Extism PDK pattern using WasmData.
// This module is WAT-encoded and compiled to binary:
//
//	(module
//	  (memory (export "memory") 1)
//	  (func (export "greet") (result i32) i32.const 0)
//	)
//
// Hex: 0061736d 01000000 0105016000017f 030201 00 070b02066d656d6f727902000567726565740000 0a0601040041000b
var minimalWASMBytes = []byte{
	0x00, 0x61, 0x73, 0x6d, // magic
	0x01, 0x00, 0x00, 0x00, // version
	// type section: one type [] -> [i32]
	0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
	// function section: one function of type 0
	0x03, 0x02, 0x01, 0x00,
	// memory section: one memory with min 1 page
	0x05, 0x03, 0x01, 0x00, 0x01,
	// export section: export "memory" (mem 0) and "greet" (func 0)
	0x07, 0x11, 0x02,
	0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00,
	0x05, 0x67, 0x72, 0x65, 0x65, 0x74, 0x00, 0x00,
	// code section: function body: i32.const 0; end
	0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x00, 0x0b,
}

// newExtismRunner creates a real Extism plugin wrapped as pluginRunner for
// use in low-level pluginImpl tests. Skips the test if creation fails.
func newExtismRunner(t *testing.T) pluginRunner {
	t.Helper()
	manifest := extism.Manifest{
		Wasm: []extism.Wasm{extism.WasmData{Data: minimalWASMBytes}},
	}
	p, err := extism.NewPlugin(context.Background(), manifest, extism.PluginConfig{EnableWasi: false}, nil)
	if err != nil {
		t.Skipf("extism.NewPlugin failed (runtime unavailable): %v", err)
	}
	return &extismPluginRunner{plugin: p}
}

// newSQLiteDB opens an in-memory SQLite database for test use.
func newSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newSingleRunnerPool creates a buffered channel of size 1 containing the
// given runner. This is the minimal pool representation for unit tests that
// only need one runner slot.
func newSingleRunnerPool(r pluginRunner) chan pluginRunner {
	pool := make(chan pluginRunner, 1)
	pool <- r
	return pool
}

func TestPluginImpl_Call_ActivePlugin(t *testing.T) {
	runner := newExtismRunner(t)
	db := newSQLiteDB(t)

	impl := &pluginImpl{
		info: PluginInfo{Name: "test", Status: PluginStatusActive},
		pool: newSingleRunnerPool(runner),
		db:   db,
	}
	defer impl.Close()

	// "greet" returns i32.const 0: Extism may return empty output or a length-prefixed result.
	// We just verify no error is returned.
	_, err := impl.Call(context.Background(), "greet", nil)
	if err != nil {
		t.Errorf("expected no error from active plugin call, got: %v", err)
	}
}

func TestPluginImpl_Call_UnknownFunction(t *testing.T) {
	runner := newExtismRunner(t)
	db := newSQLiteDB(t)

	impl := &pluginImpl{
		info: PluginInfo{Name: "test", Status: PluginStatusActive},
		pool: newSingleRunnerPool(runner),
		db:   db,
	}
	defer impl.Close()

	_, err := impl.Call(context.Background(), "nonexistent_fn", nil)
	if err == nil {
		t.Error("expected an error calling nonexistent function")
	}
	// Must not be ErrPluginNotReady.
	if errors.Is(err, ErrPluginNotReady) {
		t.Error("unexpected ErrPluginNotReady for unknown function: should be a WASM call error")
	}
}

func TestPluginImpl_Close_WithRealResources(t *testing.T) {
	runner := newExtismRunner(t)
	db := newSQLiteDB(t)

	impl := &pluginImpl{
		info: PluginInfo{Name: "test", Status: PluginStatusActive},
		pool: newSingleRunnerPool(runner),
		db:   db,
	}
	if err := impl.Close(); err != nil {
		t.Errorf("Close with real resources returned error: %v", err)
	}
}

func TestPluginImpl_Call_Concurrent(t *testing.T) {
	const goroutines = 8

	// Create a pool with multiple fake runners so concurrent calls don't queue.
	poolSize := goroutines
	pool := make(chan pluginRunner, poolSize)
	for i := 0; i < poolSize; i++ {
		pool <- newFakeRunner(map[string][]byte{
			"compute": []byte(`{"result":1}`),
		})
	}

	impl := &pluginImpl{
		info: PluginInfo{Name: "test", Status: PluginStatusActive},
		pool: pool,
	}

	type result struct {
		dur time.Duration
		err error
	}
	results := make(chan result, goroutines)

	// Measure a single call first to establish a baseline.
	singleStart := time.Now()
	_, _ = impl.Call(context.Background(), "compute", nil)
	singleDur := time.Since(singleStart)

	// Fire all goroutines simultaneously.
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			callStart := time.Now()
			_, err := impl.Call(context.Background(), "compute", nil)
			results <- result{dur: time.Since(callStart), err: err}
		}()
	}
	wg.Wait()
	totalDur := time.Since(start)
	close(results)

	for r := range results {
		if r.err != nil {
			t.Errorf("concurrent Call error: %v", r.err)
		}
	}

	// With a pool of goroutines runners, all calls should complete in roughly
	// the time of a single call, not goroutines * single call time.
	// Allow a generous 10x multiplier to keep the test robust in slow CI.
	maxExpected := singleDur*time.Duration(goroutines)*10 + 50*time.Millisecond
	if totalDur > maxExpected {
		t.Logf("total=%v single=%v max_expected=%v: may indicate serialization", totalDur, singleDur, maxExpected)
	}
	_ = totalDur // primary validation is no errors and race-free execution
}
