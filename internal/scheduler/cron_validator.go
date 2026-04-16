package scheduler

import (
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// CronValidator validates cron expressions and computes upcoming fire times.
//
// We deliberately restrict to classic 5-field POSIX cron (minute hour dom mon dow).
// Descriptors like @every / @yearly are rejected because the underlying parser
// is constructed without the Descriptor flag, and 6-field "with seconds" forms
// are rejected because the Second flag is also omitted. Either form yields a
// parse error, which we surface verbatim to the caller.
type CronValidator interface {
	// Validate parses expr in the given IANA timezone (empty == UTC) and returns
	// up to count next fire times after time.Now() in UTC. count <= 0 means
	// "validate only, return empty slice".
	Validate(expr, timezone string, count int) ([]time.Time, error)

	// NextAfter returns the first fire time strictly after t.
	NextAfter(expr, timezone string, t time.Time) (time.Time, error)
}

// stdCronValidator is the default implementation backed by robfig/cron/v3.
type stdCronValidator struct {
	parser cron.Parser
}

// NewCronValidator returns a CronValidator restricted to 5-field expressions.
func NewCronValidator() CronValidator {
	return &stdCronValidator{
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

// loadLocation resolves an IANA timezone name. An empty string maps to UTC.
func loadLocation(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return loc, nil
}

func (v *stdCronValidator) parse(expr, timezone string) (cron.Schedule, *time.Location, error) {
	if expr == "" {
		return nil, nil, errors.New("cron expression is empty")
	}
	loc, err := loadLocation(timezone)
	if err != nil {
		return nil, nil, err
	}
	sched, err := v.parser.Parse(expr)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return sched, loc, nil
}

func (v *stdCronValidator) Validate(expr, timezone string, count int) ([]time.Time, error) {
	sched, loc, err := v.parse(expr, timezone)
	if err != nil {
		return nil, err
	}
	if count <= 0 {
		return []time.Time{}, nil
	}
	out := make([]time.Time, 0, count)
	cursor := time.Now().In(loc)
	for i := 0; i < count; i++ {
		next := sched.Next(cursor)
		if next.IsZero() {
			break
		}
		out = append(out, next.UTC())
		cursor = next
	}
	return out, nil
}

func (v *stdCronValidator) NextAfter(expr, timezone string, t time.Time) (time.Time, error) {
	sched, loc, err := v.parse(expr, timezone)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(t.In(loc)).UTC(), nil
}
