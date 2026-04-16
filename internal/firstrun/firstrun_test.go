package firstrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/plekt-dev/plekt/internal/settings"
)

// --- stubs ---

type stubCounter struct {
	count int
	err   error
}

func (s *stubCounter) CountUsers(_ context.Context) (int, error) {
	return s.count, s.err
}

type stubRawStore struct {
	data   map[string]string
	getErr error // if set, GetRaw returns this for all keys
}

func newStubRawStore() *stubRawStore {
	return &stubRawStore{data: make(map[string]string)}
}

func (s *stubRawStore) GetRaw(_ context.Context, key string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	v, ok := s.data[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return v, nil
}

func (s *stubRawStore) SetRaw(_ context.Context, key, value string) error {
	s.data[key] = value
	return nil
}

func (s *stubRawStore) DeleteRaw(_ context.Context, key string) error {
	delete(s.data, key)
	return nil
}

// --- tests ---

func TestDetect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		count int
		err   error
		want  bool
	}{
		{"no users", 0, nil, true},
		{"has users", 3, nil, false},
		{"db error", 0, errors.New("db"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(context.Background(), &stubCounter{count: tc.count, err: tc.err})
			if got != tc.want {
				t.Errorf("Detect = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGenerateSetupToken(t *testing.T) {
	t.Parallel()
	token, err := GenerateSetupToken()
	if err != nil {
		t.Fatalf("GenerateSetupToken: %v", err)
	}
	if len(token.Plain) != 64 {
		t.Errorf("Plain length = %d, want 64", len(token.Plain))
	}
	// Verify hash matches SHA-256 of plain.
	hash := sha256.Sum256([]byte(token.Plain))
	wantHash := hex.EncodeToString(hash[:])
	if token.Hash != wantHash {
		t.Errorf("Hash mismatch: got %s, want %s", token.Hash, wantHash)
	}
}

func TestValidateToken(t *testing.T) {
	t.Parallel()
	store := newStubRawStore()

	// No hash stored: validation skipped.
	if err := ValidateToken(context.Background(), store, "anything"); err != nil {
		t.Errorf("no hash stored: got error %v, want nil", err)
	}

	// Store a hash.
	token, _ := GenerateSetupToken()
	_ = StoreTokenHash(context.Background(), store, token.Hash)

	// Correct token.
	if err := ValidateToken(context.Background(), store, token.Plain); err != nil {
		t.Errorf("correct token: got error %v, want nil", err)
	}

	// Wrong token.
	if err := ValidateToken(context.Background(), store, "wrong"); err == nil {
		t.Error("wrong token: got nil, want error")
	}

	// Empty token.
	if err := ValidateToken(context.Background(), store, ""); err == nil {
		t.Error("empty token: got nil, want error")
	}

	// Nil store.
	if err := ValidateToken(context.Background(), nil, "anything"); err != nil {
		t.Errorf("nil store: got error %v, want nil", err)
	}

	// DB error: must NOT bypass validation (fail closed).
	dbErrStore := newStubRawStore()
	dbErrStore.getErr = errors.New("database locked")
	if err := ValidateToken(context.Background(), dbErrStore, "anything"); err == nil {
		t.Error("DB error: got nil, want error (fail closed)")
	}
}

func TestDeleteToken(t *testing.T) {
	t.Parallel()
	store := newStubRawStore()
	_ = store.SetRaw(context.Background(), setupTokenHashKey, "hash")

	DeleteToken(context.Background(), store)

	if _, err := store.GetRaw(context.Background(), setupTokenHashKey); err == nil {
		t.Error("expected key to be deleted")
	}
}
