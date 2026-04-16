package web

import (
	"net/http"
	"net/http/pprof"
	"os"
)

// registerPprofRoutes wires net/http/pprof endpoints under /debug/pprof.
// Only registers when MC_PPROF=1 is set in the environment so production
// deployments are unaffected. Use to debug deadlocks and goroutine leaks:
//
//	MC_PPROF=1 ./mc.exe
//	curl http://localhost:8080/debug/pprof/goroutine?debug=2
//
// All routes return 404 with the env var unset.
func registerPprofRoutes(mux *http.ServeMux) {
	if os.Getenv("MC_PPROF") != "1" {
		return
	}
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	// Specific handlers for the well-known profiles. The Index handler
	// dispatches to these but mux pattern matching needs them registered.
	mux.Handle("GET /debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("GET /debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("GET /debug/pprof/allocs", pprof.Handler("allocs"))
	mux.Handle("GET /debug/pprof/block", pprof.Handler("block"))
	mux.Handle("GET /debug/pprof/mutex", pprof.Handler("mutex"))
	mux.Handle("GET /debug/pprof/threadcreate", pprof.Handler("threadcreate"))
}
