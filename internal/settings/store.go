package settings

import (
	"context"
	"errors"
)

// ErrSettingsUnavailable is returned when the settings store cannot complete an operation.
var ErrSettingsUnavailable = errors.New("settings store unavailable")

// ErrSettingNotFound is returned when a requested setting key does not exist.
var ErrSettingNotFound = errors.New("setting not found")

// Settings holds all global application configuration values.
type Settings struct {
	AdminEmail          string
	AllowPluginInstall  bool
	SessionTTLMinutes   int
	RegistrationEnabled bool
	PasswordMinLength   int
}

// SettingsStore persists and retrieves application settings.
type SettingsStore interface {
	Load(ctx context.Context) (Settings, error)
	Save(ctx context.Context, s Settings) error
	// GetRaw retrieves a raw key-value setting. Returns ErrSettingNotFound
	// if the key does not exist.
	GetRaw(ctx context.Context, key string) (string, error)
	// SetRaw persists a raw key-value setting (upsert).
	SetRaw(ctx context.Context, key, value string) error
	// DeleteRaw removes a raw key-value setting.
	DeleteRaw(ctx context.Context, key string) error
	Close() error
}
