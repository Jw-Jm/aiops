package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/uuid"
	"ops-platform/internal/bundle"
	"ops-platform/internal/configregistry"
)

// Signs a policy bundle with the deployment registry key; prints base64 signature.
func main() {
	keyRaw, err := os.ReadFile(os.Args[1])
	if err != nil || len(keyRaw) != ed25519.PrivateKeySize {
		panic("registry key must be a 64-byte ed25519 private key")
	}
	key := ed25519.PrivateKey(keyRaw)
	tenant := uuid.MustParse(os.Args[2])
	raw := json.RawMessage(os.Args[4])
	message, _, err := configregistry.SigningPayload(tenant, configregistry.KindPolicy, os.Args[3], raw)
	if err != nil {
		panic(err)
	}
	if _, err := bundle.CanonicalizeJSON(raw); err != nil {
		panic(err)
	}
	fmt.Println(base64.StdEncoding.EncodeToString(ed25519.Sign(key, message)))
}
