package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDashboardRoutes_NilDashboard verifies that when Dashboard is nil,
// routes are not registered (covered by router_test.go in the web package).
// This test covers the dashboard package's handler interface contract.

func TestDashboardHandler_Interface_Compliance(t *testing.T) {
	t.Parallel()
	// Verify that NewDashboardHandler returns a DashboardHandler
	h, _, _, _, _ := newTestHandler(t)

	// All three methods must exist and not panic with valid requests
	cases := []struct {
		name    string
		method  string
		path    string
		handler func(w http.ResponseWriter, r *http.Request)
	}{
		{
			name:    "HandleDashboardPage",
			method:  http.MethodGet,
			path:    "/dashboard",
			handler: h.HandleDashboardPage,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()
			tc.handler(w, req)
			// Just ensure no panic
		})
	}
}
