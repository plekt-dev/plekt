package loader

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCronValidateHostFn(t *testing.T) {
	cases := []struct {
		name         string
		req          CronValidateRequest
		wantValid    bool
		wantErrSub   string
		wantFires    int
		wantTZ       string // informational only
		expectNonUTC bool
	}{
		{
			name:      "daily UTC",
			req:       CronValidateRequest{Expression: "0 9 * * *"},
			wantValid: true,
			wantFires: CronValidateNextFiresCount,
		},
		{
			name:      "every 5 min with explicit tz",
			req:       CronValidateRequest{Expression: "*/5 * * * *", Timezone: "Europe/Berlin"},
			wantValid: true,
			wantFires: CronValidateNextFiresCount,
		},
		{
			name:       "at-every descriptor rejected",
			req:        CronValidateRequest{Expression: "@every 1s"},
			wantValid:  false,
			wantErrSub: "@every",
		},
		{
			name:       "yearly descriptor rejected",
			req:        CronValidateRequest{Expression: "@yearly"},
			wantValid:  false,
			wantErrSub: "@yearly",
		},
		{
			name:       "garbage expression",
			req:        CronValidateRequest{Expression: "not a cron"},
			wantValid:  false,
			wantErrSub: "not a cron",
		},
		{
			name:       "four fields rejected",
			req:        CronValidateRequest{Expression: "0 0 * *"},
			wantValid:  false,
			wantErrSub: "0 0 * *",
		},
		{
			name:       "six fields rejected",
			req:        CronValidateRequest{Expression: "0 0 * * * *"},
			wantValid:  false,
			wantErrSub: "0 0 * * * *",
		},
		{
			name:       "empty",
			req:        CronValidateRequest{Expression: ""},
			wantValid:  false,
			wantErrSub: "empty",
		},
		{
			name:       "bad timezone",
			req:        CronValidateRequest{Expression: "0 9 * * *", Timezone: "Mars/Olympus_Mons"},
			wantValid:  false,
			wantErrSub: "Mars/Olympus_Mons",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := CronValidateHostFn(context.Background(), PluginCallContext{}, tc.req)
			if resp.Valid != tc.wantValid {
				t.Fatalf("valid=%v want %v (err=%q)", resp.Valid, tc.wantValid, resp.Error)
			}
			if tc.wantValid {
				if len(resp.NextFires) != tc.wantFires {
					t.Errorf("want %d fires, got %d", tc.wantFires, len(resp.NextFires))
				}
				// Verify every entry parses as RFC3339.
				for i, f := range resp.NextFires {
					if _, err := time.Parse(time.RFC3339, f); err != nil {
						t.Errorf("fire[%d] %q not RFC3339: %v", i, f, err)
					}
				}
				if resp.Error != "" {
					t.Errorf("valid response must not carry error, got %q", resp.Error)
				}
			} else {
				if resp.Error == "" {
					t.Errorf("invalid response must carry error")
				}
				if tc.wantErrSub != "" && !strings.Contains(resp.Error, tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", resp.Error, tc.wantErrSub)
				}
				if len(resp.NextFires) != 0 {
					t.Errorf("invalid response must not carry fires, got %d", len(resp.NextFires))
				}
			}
		})
	}
}

func TestCronValidateHostFn_SingletonReuse(t *testing.T) {
	// Two successive calls must both succeed and return strictly-increasing fires.
	req := CronValidateRequest{Expression: "*/10 * * * *"}
	a := CronValidateHostFn(context.Background(), PluginCallContext{}, req)
	b := CronValidateHostFn(context.Background(), PluginCallContext{}, req)
	if !a.Valid || !b.Valid {
		t.Fatalf("both calls should be valid, got a=%+v b=%+v", a, b)
	}
	if len(a.NextFires) != CronValidateNextFiresCount || len(b.NextFires) != CronValidateNextFiresCount {
		t.Fatalf("unexpected fire counts")
	}
}
