package dashboard

import (
	"fmt"
	"sync"
	"time"
)

// WidgetPlacement describes the position and visibility of a widget in a layout.
type WidgetPlacement struct {
	Key      WidgetKey `json:"key"`
	Position int       `json:"position"`
	Visible  bool      `json:"visible"`
}

// DashboardLayout holds the complete layout for a session.
type DashboardLayout struct {
	Placements []WidgetPlacement `json:"placements"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// DashboardLayoutStore persists and retrieves dashboard layouts per session.
type DashboardLayoutStore interface {
	// Load retrieves the layout for sessionID. Returns ErrLayoutNotFound if absent.
	Load(sessionID string) (DashboardLayout, error)
	// Save persists the layout for sessionID, overwriting any existing layout.
	Save(sessionID string, layout DashboardLayout) error
	// Close releases any resources held by the store.
	Close() error
}

// inMemoryDashboardLayoutStore is the default in-memory DashboardLayoutStore.
type inMemoryDashboardLayoutStore struct {
	mu      sync.RWMutex
	layouts map[string]DashboardLayout
}

// NewDashboardLayoutStore constructs an in-memory DashboardLayoutStore.
func NewDashboardLayoutStore() DashboardLayoutStore {
	return &inMemoryDashboardLayoutStore{
		layouts: make(map[string]DashboardLayout),
	}
}

// Load retrieves the layout for sessionID or returns ErrLayoutNotFound.
func (s *inMemoryDashboardLayoutStore) Load(sessionID string) (DashboardLayout, error) {
	s.mu.RLock()
	layout, ok := s.layouts[sessionID]
	s.mu.RUnlock()
	if !ok {
		return DashboardLayout{}, fmt.Errorf("%w: session %q", ErrLayoutNotFound, sessionID)
	}
	return layout, nil
}

// Save persists the layout for sessionID, overwriting any existing layout.
func (s *inMemoryDashboardLayoutStore) Save(sessionID string, layout DashboardLayout) error {
	s.mu.Lock()
	s.layouts[sessionID] = layout
	s.mu.Unlock()
	return nil
}

// Close is a no-op for the in-memory store.
func (s *inMemoryDashboardLayoutStore) Close() error {
	return nil
}
