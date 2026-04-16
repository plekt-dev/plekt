package i18n

import (
	"sync"
	"testing"
)

// TestNewLocalizer_ConcurrentInitRace is a regression test for a data race
// where NewLocalizer read the package-level `bundle` variable without
// synchronization while Init() wrote to it inside sync.Once.Do.
//
// Run with:  go test -race -run TestNewLocalizer_ConcurrentInitRace ./internal/i18n/...
//
// Before the fix the race detector reported a write/read pair on `bundle`
// in i18n.go (NewLocalizer reading bundle vs Init writing it).
func TestNewLocalizer_ConcurrentInitRace(t *testing.T) {
	const goroutines = 64

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loc := NewLocalizer("en")
			if loc == nil {
				t.Error("NewLocalizer returned nil")
			}
		}()
	}
	wg.Wait()
}

// TestTranslateAll_ConcurrentWithLoad exercises TranslateAll alongside
// LoadPluginMessages to catch races on allMessageIDs.
func TestTranslateAll_ConcurrentWithLoad(t *testing.T) {
	const readers = 16
	const writers = 4

	pluginJSON := []byte(`{"plugin.test.hello": "Hello"}`)

	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = TranslateAll("en")
		}()
	}
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = LoadPluginMessages("en", pluginJSON)
		}()
	}
	wg.Wait()
}
