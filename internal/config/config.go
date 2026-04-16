package config

import (
	"fmt"
	"time"
)

// DefaultRegistryURL is the official Plekt plugin registry. Override via
// `registry.url` in config.yaml to point at a mirror.
const DefaultRegistryURL = "https://registry.plekt.dev/registry.json"

// DefaultConfig returns defaults used when config.yaml is absent.
func DefaultConfig() Config {
	return Config{
		PluginDir: "./plugins",
		DataDir:   "./data",
		Server: ServerConfig{
			Addr:         ":8080",
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Loader: LoaderConfig{
			MaxPlugins:           50,
			ReloadDrainTimeout:   5 * time.Second,
			WASMMemoryLimitPages: 512,
			AutoLoadOnStartup:    true,
		},
		Registry: RegistryConfig{
			URL:           DefaultRegistryURL,
			CheckInterval: 24 * time.Hour,
			DefaultPlugins: []string{
				"tasks-plugin",
				"notes-plugin",
				"projects-plugin",
				"pomodoro-plugin",
				"scheduler-plugin",
				"voice-plugin",
			},
		},
	}
}

// Validate checks configuration sanity. Zero-value fields are filled in
// by ApplyDefaults rather than flagged here.
func (c Config) Validate() error {
	if c.PluginDir == "" {
		return fmt.Errorf("plugin_dir must not be empty")
	}
	if c.Loader.MaxPlugins < 0 {
		return fmt.Errorf("loader.max_plugins must be >= 0, got %d", c.Loader.MaxPlugins)
	}
	return nil
}

// ApplyDefaults fills zero-value fields with sensible production defaults.
func (c *Config) ApplyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 30 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 30 * time.Second
	}
	if c.Server.LoginRateLimit.MaxAttempts == 0 && c.Server.LoginRateLimit.Window == 0 {
		c.Server.LoginRateLimit.MaxAttempts = 5
		c.Server.LoginRateLimit.Window = 15 * time.Minute
	}
	if c.Loader.MaxPlugins == 0 {
		c.Loader.MaxPlugins = 50
	}
	if c.Loader.WASMMemoryLimitPages == 0 {
		c.Loader.WASMMemoryLimitPages = 512
	}
	if c.Loader.ReloadDrainTimeout == 0 {
		c.Loader.ReloadDrainTimeout = 5 * time.Second
	}
	if c.Registry.CheckInterval == 0 {
		c.Registry.CheckInterval = 24 * time.Hour
	}
	if c.Registry.URL == "" {
		c.Registry.URL = DefaultRegistryURL
	}
}

// Config is the top-level application configuration.
type Config struct {
	PluginDir string         `yaml:"plugin_dir"`
	DataDir   string         `yaml:"data_dir"`
	Server    ServerConfig   `yaml:"server"`
	Loader    LoaderConfig   `yaml:"loader"`
	Registry  RegistryConfig `yaml:"registry"`
}

// RegistryConfig holds plugin registry settings.
type RegistryConfig struct {
	URL            string        `yaml:"url"`
	CheckInterval  time.Duration `yaml:"check_interval"`
	DefaultPlugins []string      `yaml:"default_plugins"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr         string        `yaml:"addr"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	// PublicBaseURL is the absolute URL prefix used for outbound webhook
	// callbacks. When empty, the dispatcher derives one from Addr.
	PublicBaseURL  string               `yaml:"public_base_url"`
	LoginRateLimit LoginRateLimitConfig `yaml:"login_rate_limit"`
}

// LoginRateLimitConfig caps login attempts per IP. Defaults:
// 5 attempts per 15 minutes. Env PLEKT_LOGIN_RATE_LIMIT_DISABLED=1 disables it.
type LoginRateLimitConfig struct {
	MaxAttempts int           `yaml:"max_attempts"`
	Window      time.Duration `yaml:"window"`
}

// LoaderConfig holds plugin loader settings.
type LoaderConfig struct {
	MaxPlugins           int           `yaml:"max_plugins"`
	ReloadDrainTimeout   time.Duration `yaml:"reload_drain_timeout"`
	WASMMemoryLimitPages uint32        `yaml:"wasm_memory_limit_pages"`
	AutoLoadOnStartup    bool          `yaml:"auto_load_on_startup"`
}
