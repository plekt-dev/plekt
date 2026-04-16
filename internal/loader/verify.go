package loader

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"regexp"
)

// signatureBlockRe matches the `signature:` line and the lines that follow
// until the next top-level YAML key or end-of-file.
// It replaces the entire signature block with `signature: {}`.
var signatureBlockRe = regexp.MustCompile(`(?m)^signature:.*(?:\n(?:[ \t]+.*)?)*`)

// canonicalizeMCPYAML replaces the `signature:` block in raw YAML with
// `signature: {}` so that the signed content is deterministic.
func canonicalizeMCPYAML(raw []byte) []byte {
	return signatureBlockRe.ReplaceAll(raw, []byte("signature: {}"))
}

// VerifyMCPSignature verifies the Ed25519 signature embedded in mcp.yaml
// against the plugin's expected public key from the registry. Revoked
// keys are rejected before the signature check. Pass revokedKeys=nil to
// skip the revocation check (tests only).
func VerifyMCPSignature(
	expectedPubKeyHex string,
	mcpPubKeyHex string,
	mcpSignatureHex string,
	rawMCPYAML []byte,
	revokedKeys map[string]bool,
) error {
	if expectedPubKeyHex == "" {
		return fmt.Errorf("%w: registry has no public_key for this plugin", ErrUnsignedPlugin)
	}
	if mcpPubKeyHex == "" || mcpSignatureHex == "" {
		return fmt.Errorf("%w: mcp.yaml signature block is empty", ErrUnsignedPlugin)
	}

	if revokedKeys != nil && revokedKeys[expectedPubKeyHex] {
		return fmt.Errorf("%w: %s", ErrKeyRevoked, expectedPubKeyHex)
	}

	if mcpPubKeyHex != expectedPubKeyHex {
		return fmt.Errorf("%w: registry says %s, mcp.yaml signed by %s",
			ErrPublicKeyMismatch, expectedPubKeyHex, mcpPubKeyHex)
	}

	pubBytes, err := hex.DecodeString(expectedPubKeyHex)
	if err != nil {
		return fmt.Errorf("%w: public key hex decode: %v", ErrSignatureInvalid, err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes, got %d",
			ErrSignatureInvalid, ed25519.PublicKeySize, len(pubBytes))
	}

	sigBytes, err := hex.DecodeString(mcpSignatureHex)
	if err != nil {
		return fmt.Errorf("%w: signature hex decode: %v", ErrSignatureInvalid, err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature must be %d bytes, got %d",
			ErrSignatureInvalid, ed25519.SignatureSize, len(sigBytes))
	}

	canonical := canonicalizeMCPYAML(rawMCPYAML)

	if !ed25519.Verify(ed25519.PublicKey(pubBytes), canonical, sigBytes) {
		return fmt.Errorf("%w: signature does not match content", ErrSignatureInvalid)
	}
	return nil
}
