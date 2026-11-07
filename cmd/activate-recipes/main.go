package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"

	"github.com/google/uuid"
	"ops-platform/internal/bundle"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/rca"
)

// Signs the built-in recipes with the deployment registry key and prints one
// JSON line per recipe for the HTTP activation driver.
func main() {
	keyRaw, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	if len(keyRaw) != ed25519.PrivateKeySize {
		panic("registry key must be a 64-byte ed25519 private key")
	}
	key := ed25519.PrivateKey(keyRaw)
	tenant := uuid.MustParse(os.Args[2])
	names := []string{"dimm-failure", "pvc-csi-failure", "node-failure", "pod-image-pull-failure", "service-no-endpoints", "pod-crashloop-failure", "pod-oom-failure", "pod-probe-failure", "deployment-rollout-stuck", "pod-scheduling-failure"}
	out := json.NewEncoder(os.Stdout)
	for _, name := range names {
		recipe, err := rca.Builtin(name)
		if err != nil {
			panic(name + ": " + err.Error())
		}
		raw, _ := json.Marshal(recipe)
		message, _, err := configregistry.SigningPayload(tenant, configregistry.KindRecipe, name, json.RawMessage(raw))
		if err != nil {
			panic(name + ": " + err.Error())
		}
		if _, err := bundle.CanonicalizeJSON(raw); err != nil {
			panic(name + ": " + err.Error())
		}
		if err := out.Encode(map[string]any{"name": name, "content": json.RawMessage(raw), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, message))}); err != nil {
			panic(err)
		}
	}
}
