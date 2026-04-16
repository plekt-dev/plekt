// Package firstrun centralises all first-run detection and setup logic:
// setup-token generation/validation, default settings, and the startup banner.
package firstrun

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/plekt-dev/plekt/internal/settings"
)

// UserCounter is the minimal interface for checking whether any users exist.
type UserCounter interface {
	CountUsers(ctx context.Context) (int, error)
}

// RawSettingsStore is the subset of settings.SettingsStore needed for
// setup-token storage. Using a narrow interface avoids pulling in the
// full settings package.
type RawSettingsStore interface {
	GetRaw(ctx context.Context, key string) (string, error)
	SetRaw(ctx context.Context, key, value string) error
	DeleteRaw(ctx context.Context, key string) error
}

const setupTokenHashKey = "setup_token_hash"

// Detect returns true when no users exist (first-run scenario).
func Detect(ctx context.Context, counter UserCounter) bool {
	count, err := counter.CountUsers(ctx)
	return err == nil && count == 0
}

// SetupToken holds the generated token and its SHA-256 hash.
type SetupToken struct {
	Plain string // 64-char hex string (32 random bytes)
	Hash  string // SHA-256 of Plain, hex-encoded
}

// GenerateSetupToken creates a cryptographically random setup token.
func GenerateSetupToken() (SetupToken, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return SetupToken{}, fmt.Errorf("generate setup token: %w", err)
	}
	plain := hex.EncodeToString(tokenBytes)
	hash := sha256.Sum256([]byte(plain))
	return SetupToken{
		Plain: plain,
		Hash:  hex.EncodeToString(hash[:]),
	}, nil
}

// StoreTokenHash persists the token hash so it can be validated on registration.
func StoreTokenHash(ctx context.Context, store RawSettingsStore, hash string) error {
	return store.SetRaw(ctx, setupTokenHashKey, hash)
}

// ValidateToken checks the submitted token against the stored hash.
// Returns nil if valid. Skips validation when no hash is stored (backward-compat).
func ValidateToken(ctx context.Context, store RawSettingsStore, submitted string) error {
	if store == nil {
		return nil
	}
	storedHash, err := store.GetRaw(ctx, setupTokenHashKey)
	if errors.Is(err, settings.ErrSettingNotFound) {
		// No hash stored: skip validation (pre-upgrade installs).
		return nil
	}
	if err != nil {
		// DB error: fail closed for security. Do not bypass validation.
		slog.Error("setup token validation: failed to read token hash", "error", err)
		return fmt.Errorf("setup token validation unavailable: %w", err)
	}
	if submitted == "" {
		return errors.New("setup token required")
	}
	hash := sha256.Sum256([]byte(submitted))
	submittedHash := hex.EncodeToString(hash[:])
	if subtle.ConstantTimeCompare([]byte(submittedHash), []byte(storedHash)) != 1 {
		return errors.New("invalid setup token")
	}
	return nil
}

// DeleteToken removes the stored token hash after successful first-user registration.
func DeleteToken(ctx context.Context, store RawSettingsStore) {
	if store == nil {
		return
	}
	if err := store.DeleteRaw(ctx, setupTokenHashKey); err != nil {
		slog.Warn("first-run: failed to delete setup token hash", "error", err)
	}
}

// PrintSetupBanner prints the setup token in a visible framed banner.
func PrintSetupBanner(token string) {
	const w = 64 // inner visible width between "  *  " and "  *"
	border := "  " + strings.Repeat("*", w+6)
	empty := bannerLine("", w)

	fmt.Println()
	fmt.Println(border)
	fmt.Println(bannerLine("FIRST-RUN SETUP TOKEN", w))
	fmt.Println(border)
	fmt.Println(empty)
	fmt.Println(bannerLine(token, w))
	fmt.Println(empty)
	fmt.Println(bannerLine("Use this token to register the first admin account.", w))
	fmt.Println(empty)
	fmt.Println(border)
	fmt.Println()
}

func bannerLine(content string, width int) string {
	pad := width - len(content)
	if pad < 0 {
		pad = 0
	}
	return "  *  " + content + strings.Repeat(" ", pad) + "  *"
}
