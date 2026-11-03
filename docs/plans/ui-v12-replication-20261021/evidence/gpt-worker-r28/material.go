//go:build ignore

// Prepare the task's own policy signing material; publication uses native HTTP proof.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/modelprofile"
	"ops-platform/policies"
	"os"
	"path/filepath"
)

func must(err error) {
	if err != nil {
		panic("task policy material failed; private inputs omitted")
	}
}
func save(path string, b []byte, mode os.FileMode) {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	must(e)
	_, e = f.Write(b)
	must(e)
	must(f.Close())
}
func main() {
	root := "/data/ops-v12-native-visual-r24-20261026"
	dir := root + "/r28-worker"
	keyPath := dir + "/private/policy-key"
	if len(os.Args) != 2 {
		panic("prepare or sign required")
	}
	if os.Args[1] == "scope" {
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		must(e)
		save(dir+"/private/scope-key", key, 0600)
		save(dir+"/scope-public-key", pub, 0644)
		return
	}
	if os.Args[1] == "reference" {
		b, e := os.ReadFile(dir + "/model-profile.json")
		must(e)
		var p modelprofile.Profile
		must(json.Unmarshal(b, &p))
		if !p.Valid(":8083") {
			panic("profile invalid")
		}
		fmt.Println(p.Reference())
		return
	}
	if os.Args[1] == "prepare" {
		must(os.Mkdir(filepath.Dir(keyPath), 0700))
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		must(e)
		save(keyPath, key, 0600)
		var trust map[string]string
		b, e := os.ReadFile(root + "/registry-trust.json")
		must(e)
		must(json.Unmarshal(b, &trust))
		trust["r28-task-policy"] = base64.StdEncoding.EncodeToString(pub)
		b, e = json.Marshal(trust)
		must(e)
		save(dir+"/registry-trust.json", b, 0644)
		modules := []map[string]string{}
		for _, id := range []string{"tool/v1/tool.rego", "action/v1/action.rego"} {
			raw, e := policies.FS.ReadFile(id)
			must(e)
			modules = append(modules, map[string]string{"id": id, "content": string(raw)})
		}
		b, e = json.Marshal(map[string]any{"kind": "policy", "logicalName": "r28-read-only", "content": map[string]any{"schemaVersion": "policy-registry/v1", "name": "r28-read-only", "modules": modules}})
		must(e)
		save(dir+"/policy-draft-body.json", b, 0644)
		return
	}
	if os.Args[1] != "sign" {
		panic("unsupported mode")
	}
	raw, e := os.ReadFile(dir + "/policy-draft.json")
	must(e)
	var reply struct {
		Data configregistry.Draft `json:"data"`
	}
	must(json.Unmarshal(raw, &reply))
	key, e := os.ReadFile(keyPath)
	must(e)
	if len(key) != ed25519.PrivateKeySize {
		panic("signing key unavailable")
	}
	payload, _, e := configregistry.SigningPayload(reply.Data.TenantID, reply.Data.Kind, reply.Data.LogicalName, reply.Data.Content)
	must(e)
	b, e := json.Marshal(map[string]any{"expectedRevision": reply.Data.Revision, "signerKeyId": "r28-task-policy", "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	must(e)
	save(dir+"/policy-publish-body.json", b, 0644)
}
