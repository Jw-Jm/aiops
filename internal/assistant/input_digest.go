package assistant

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"ops-platform/internal/bundle"
	"ops-platform/internal/graph"
)

// JSONB reorders raw history objects, including freshly compacted answers.
// Bind their canonical content rather than their transient key order.
func InputDigest(input TurnInput) (string, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	canonical, err := bundle.CanonicalizeJSON(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func InputDigestMatches(input TurnInput, expected string) bool {
	digest, err := InputDigest(input)
	// Existing admitted jobs retain their original digest and authority binding.
	return err == nil && (digest == expected || graph.ScopeDigest(input) == expected)
}
