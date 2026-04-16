// Package webhooks implements outbound webhook delivery for scheduled job
// firings. The dispatcher subscribes to scheduler.job.fired events, looks up
// the agent's webhook configuration, and POSTs an HMAC-signed payload to the
// receiver. The receiver may either return the LLM output inline (sync mode)
// or acknowledge the request and call back later via /api/runs/{id}/result
// (async mode).
//
// Plekt deliberately does NOT call any LLM API directly. The receiver
// (typically a small relay binary the operator runs alongside core) decides
// what to invoke: claude code, Ollama, LiteLLM, n8n, etc. This keeps the core
// LLM-agnostic and offloads cost, model choice, and provider lock-in to the
// operator.
package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// SignatureHeader is the HTTP header that carries the HMAC signature on both
// outbound webhook requests and inbound /api/runs/{id}/result callbacks.
const SignatureHeader = "X-MC-Signature"

// SignaturePrefix is the algorithm tag prepended to the hex digest, e.g.
// "sha256=ab12...". Receivers split on '=' to extract the digest. The prefix
// future-proofs the protocol if we ever need to rotate to a stronger MAC.
const SignaturePrefix = "sha256="

// Sign returns "sha256=<hex>" of HMAC-SHA256(secret, body). Constant-time
// verification is the caller's responsibility: use Verify rather than
// comparing strings directly.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return SignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether sig matches the HMAC of body under secret using a
// constant-time comparison. Tolerates both prefixed ("sha256=...") and bare
// hex digest forms so receivers can be lenient.
func Verify(secret string, body []byte, sig string) bool {
	if sig == "" {
		return false
	}
	expected := Sign(secret, body)
	// Strip prefix from incoming sig if present so the comparison is uniform.
	if len(sig) >= len(SignaturePrefix) && sig[:len(SignaturePrefix)] == SignaturePrefix {
		// Compare full prefixed strings.
		return hmac.Equal([]byte(sig), []byte(expected))
	}
	// Bare hex form: compare just the digests.
	return hmac.Equal([]byte(sig), []byte(expected[len(SignaturePrefix):]))
}
