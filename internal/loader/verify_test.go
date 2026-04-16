package loader

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
)

// generateKeyPair produces a fresh Ed25519 key pair for test use.
func generateKeyPair(t *testing.T) (pubHex, privHex string, pub ed25519.PublicKey, priv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key pair: %v", err)
	}
	return hex.EncodeToString(pub), hex.EncodeToString(priv), pub, priv
}

// signYAML canonicalizes yaml and signs it with priv.
func signYAML(t *testing.T, yaml []byte, priv ed25519.PrivateKey) string {
	t.Helper()
	canonical := canonicalizeMCPYAML(yaml)
	sig := ed25519.Sign(priv, canonical)
	return hex.EncodeToString(sig)
}

var sampleMCPYAML = []byte(`tools:
  - name: list_tasks
    description: List all tasks
signature:
  public_key: placeholder
  signature: placeholder
`)

func TestVerifyMCPSignature_ValidSignature(t *testing.T) {
	pubHex, _, _, priv := generateKeyPair(t)
	sigHex := signYAML(t, sampleMCPYAML, priv)

	if err := VerifyMCPSignature(pubHex, pubHex, sigHex, sampleMCPYAML, nil); err != nil {
		t.Errorf("expected valid signature to pass, got: %v", err)
	}
}

func TestVerifyMCPSignature_InvalidSignature(t *testing.T) {
	pubHex, _, _, priv := generateKeyPair(t)
	sigHex := signYAML(t, sampleMCPYAML, priv)

	// Tamper the signature (flip the last byte).
	sigBytes, _ := hex.DecodeString(sigHex)
	sigBytes[len(sigBytes)-1] ^= 0xFF
	tamperedSigHex := hex.EncodeToString(sigBytes)

	err := VerifyMCPSignature(pubHex, pubHex, tamperedSigHex, sampleMCPYAML, nil)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("expected ErrSignatureInvalid, got: %v", err)
	}
}

func TestVerifyMCPSignature_TamperedYAML(t *testing.T) {
	pubHex, _, _, priv := generateKeyPair(t)
	sigHex := signYAML(t, sampleMCPYAML, priv)

	tampered := append(sampleMCPYAML, []byte("\nextra: injected")...)
	err := VerifyMCPSignature(pubHex, pubHex, sigHex, tampered, nil)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("expected ErrSignatureInvalid for tampered YAML, got: %v", err)
	}
}

func TestVerifyMCPSignature_MalformedPublicKeyHex(t *testing.T) {
	err := VerifyMCPSignature("not-hex!!", "not-hex!!", "aabbcc", sampleMCPYAML, nil)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("expected ErrSignatureInvalid for malformed pub key hex, got: %v", err)
	}
}

func TestVerifyMCPSignature_MalformedSignatureHex(t *testing.T) {
	pubHex, _, _, _ := generateKeyPair(t)
	err := VerifyMCPSignature(pubHex, pubHex, "zzzz", sampleMCPYAML, nil)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("expected ErrSignatureInvalid for malformed sig hex, got: %v", err)
	}
}

func TestVerifyMCPSignature_WrongPublicKeyLength(t *testing.T) {
	// 16 bytes instead of 32.
	shortKey := hex.EncodeToString(make([]byte, 16))
	validSig := hex.EncodeToString(make([]byte, 64))
	err := VerifyMCPSignature(shortKey, shortKey, validSig, sampleMCPYAML, nil)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("expected ErrSignatureInvalid for wrong key length, got: %v", err)
	}
}

func TestVerifyMCPSignature_WrongSignatureLength(t *testing.T) {
	pubHex, _, _, _ := generateKeyPair(t)
	// 16 bytes instead of 64.
	shortSig := hex.EncodeToString(make([]byte, 16))
	err := VerifyMCPSignature(pubHex, pubHex, shortSig, sampleMCPYAML, nil)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("expected ErrSignatureInvalid for wrong sig length, got: %v", err)
	}
}

// TestVerifyMCPSignature_PublicKeyMismatch: mcp.yaml signed with key X but
// registry says key Y. Must reject before ed25519.Verify is even attempted.
func TestVerifyMCPSignature_PublicKeyMismatch(t *testing.T) {
	registryPub, _, _, _ := generateKeyPair(t)
	mcpPub, _, _, mcpPriv := generateKeyPair(t)
	sigHex := signYAML(t, sampleMCPYAML, mcpPriv)

	err := VerifyMCPSignature(registryPub, mcpPub, sigHex, sampleMCPYAML, nil)
	if !errors.Is(err, ErrPublicKeyMismatch) {
		t.Errorf("expected ErrPublicKeyMismatch, got: %v", err)
	}
}

// TestVerifyMCPSignature_KeyRevoked: even a perfectly valid signature must
// be rejected when the registry's key is in the revocation list.
func TestVerifyMCPSignature_KeyRevoked(t *testing.T) {
	pubHex, _, _, priv := generateKeyPair(t)
	sigHex := signYAML(t, sampleMCPYAML, priv)
	revoked := map[string]bool{pubHex: true}

	err := VerifyMCPSignature(pubHex, pubHex, sigHex, sampleMCPYAML, revoked)
	if !errors.Is(err, ErrKeyRevoked) {
		t.Errorf("expected ErrKeyRevoked, got: %v", err)
	}
}

// TestVerifyMCPSignature_UnsignedExpected: empty expected pubkey must fail
// closed (no fallback to unsigned-plugin path inside VerifyMCPSignature).
func TestVerifyMCPSignature_UnsignedExpected(t *testing.T) {
	pubHex, _, _, priv := generateKeyPair(t)
	sigHex := signYAML(t, sampleMCPYAML, priv)

	err := VerifyMCPSignature("", pubHex, sigHex, sampleMCPYAML, nil)
	if !errors.Is(err, ErrUnsignedPlugin) {
		t.Errorf("expected ErrUnsignedPlugin for empty expected key, got: %v", err)
	}
}

// TestVerifyMCPSignature_UnsignedMCP: empty mcp.yaml signature must fail.
func TestVerifyMCPSignature_UnsignedMCP(t *testing.T) {
	pubHex, _, _, _ := generateKeyPair(t)

	err := VerifyMCPSignature(pubHex, "", "", sampleMCPYAML, nil)
	if !errors.Is(err, ErrUnsignedPlugin) {
		t.Errorf("expected ErrUnsignedPlugin for empty mcp signature, got: %v", err)
	}
}

func TestCanonicalizeMCPYAML_ReplacesSignatureBlock(t *testing.T) {
	input := []byte(`tools:
  - name: test
signature:
  public_key: abc
  signature: def
other: value
`)
	got := canonicalizeMCPYAML(input)
	// The signature block must be replaced.
	if string(got) == string(input) {
		t.Error("canonicalize did not change the YAML")
	}
	// "signature: {}" must appear.
	if !contains(got, []byte("signature: {}")) {
		t.Errorf("canonicalized YAML missing 'signature: {}': %s", got)
	}
}

func TestCanonicalizeMCPYAML_NoSignatureBlock(t *testing.T) {
	input := []byte(`tools:
  - name: test
`)
	got := canonicalizeMCPYAML(input)
	// No signature block → content unchanged.
	if string(got) != string(input) {
		t.Errorf("unexpected change when no signature block: %s", got)
	}
}

func TestVerifyMCPSignature_EmptyYAML(t *testing.T) {
	pubHex, _, _, priv := generateKeyPair(t)
	sigHex := signYAML(t, []byte{}, priv)

	if err := VerifyMCPSignature(pubHex, pubHex, sigHex, []byte{}, nil); err != nil {
		t.Errorf("empty YAML with valid sig should pass, got: %v", err)
	}
}

func contains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
