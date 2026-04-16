package scheduler

import (
	"strings"
	"testing"
	"time"
)

func TestCronValidator_ValidExpressions(t *testing.T) {
	v := NewCronValidator()
	cases := []struct {
		name string
		expr string
		tz   string
	}{
		{"daily 9am", "0 9 * * *", ""},
		{"every 5 min", "*/5 * * * *", ""},
		{"new year", "0 0 1 1 *", ""},
		{"weekdays 9am", "0 9 * * 1-5", "Europe/Berlin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fires, err := v.Validate(tc.expr, tc.tz, 3)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(fires) != 3 {
				t.Fatalf("want 3 fires, got %d", len(fires))
			}
			for i := 1; i < len(fires); i++ {
				if !fires[i].After(fires[i-1]) {
					t.Errorf("fires not strictly increasing: %v then %v", fires[i-1], fires[i])
				}
			}
		})
	}
}

func TestCronValidator_InvalidExpressions(t *testing.T) {
	v := NewCronValidator()
	cases := []struct {
		name string
		expr string
		tz   string
	}{
		{"every descriptor", "@every 1s", ""},
		{"yearly descriptor", "@yearly", ""},
		{"out of range", "99 99 99 99 99", ""},
		{"garbage", "not a cron", ""},
		{"four fields", "0 0 * *", ""},
		{"six fields", "0 0 * * * *", ""},
		{"empty", "", ""},
		{"bad timezone", "0 9 * * *", "Mars/Olympus_Mons"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Validate(tc.expr, tc.tz, 1)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestCronValidator_TimezoneDST(t *testing.T) {
	v := NewCronValidator()
	// In 2026, Europe/Berlin DST starts on Sunday March 29 at 02:00 -> 03:00.
	// "0 9 * * *" must still fire at 09:00 local on March 29: i.e. 07:00 UTC
	// (because Berlin is already CEST/UTC+2 at 09:00 local).
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	startLocal := time.Date(2026, 3, 28, 12, 0, 0, 0, loc)
	next, err := v.NextAfter("0 9 * * *", "Europe/Berlin", startLocal)
	if err != nil {
		t.Fatalf("NextAfter: %v", err)
	}
	want := time.Date(2026, 3, 29, 7, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("DST: got %v, want %v", next, want)
	}
}

func TestCronValidator_ParseErrorMentionsExpr(t *testing.T) {
	v := NewCronValidator()
	_, err := v.Validate("@every 1s", "", 1)
	if err == nil || !strings.Contains(err.Error(), "@every") {
		t.Fatalf("error should mention the bad expression, got %v", err)
	}
}
