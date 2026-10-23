package integration

import (
	"encoding/json"
	"net/http"
	"ops-platform/internal/bundle"
	"ops-platform/internal/identity"
	"testing"
	"time"
)

// Uses the real foundation router, native session, two factors and database.
// Creating a draft does not assert publication, signature admission or activation.
func mfaAdministrativeDraftProof(t *testing.T, client mfaClient, session *mfaSession, secret string) {
	t.Helper()
	path := "/api/v1/admin/registry-drafts"
	body := map[string]any{"kind": "policy", "logicalName": "native-stepup-draft", "content": map[string]any{"draftOnly": true}}
	raw, _ := json.Marshal(body)
	canonical, err := bundle.CanonicalizeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	target := map[string]any{"kind": "administrative_write", "write": map[string]any{"method": "POST", "path": path, "bodyDigest": "sha256:" + identity.DigestHex(identity.DigestSHA256(canonical))}}
	status, challenge, message := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{"operation": "registry_administration", "target": target, "expectedSessionVersion": session.sessionVersion}, session)
	if status != 201 {
		t.Fatalf("proposed draft challenge=%d %s", status, message)
	}
	id := mfaData(t, challenge)["challengeId"].(string)
	for i, method := range []string{"password", "totp"} {
		credential := "mfa-rotated-password"
		if method == "totp" {
			credential = mfaCodeAt(t, secret, identity.TOTPStep(time.Now().UTC())+1)
		}
		status, proof, message := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+id+":verify", map[string]any{"method": method, "credential": credential, "expectedChallengeRevision": i + 1}, session)
		if status != 200 || (method == "totp" && mfaData(t, proof)["verified"] != true) {
			t.Fatalf("draft proof %s=%d %s", method, status, message)
		}
	}
	session.stepUpChallenge = id
	defer func() { session.stepUpChallenge = "" }()
	changed := map[string]any{"kind": "policy", "logicalName": "changed-draft", "content": map[string]any{"draftOnly": true}}
	if status, _, _ := client.do(t, http.MethodPost, path, changed, session); status != 403 {
		t.Fatalf("changed draft borrowed proof=%d", status)
	}
	if status, _, _ := client.do(t, http.MethodPost, "/api/v1/admin/registry-activations", body, session); status != 403 {
		t.Fatalf("different path borrowed proof=%d", status)
	}
	status, created, message := client.do(t, http.MethodPost, path, body, session)
	if status != 201 {
		t.Fatalf("exact draft creation=%d %s", status, message)
	}
	idDraft := mfaData(t, created)["draftId"].(string)
	status, loaded, message := client.do(t, http.MethodGet, path+"/"+idDraft, nil, session)
	if status != 200 || mfaData(t, loaded)["revision"] != float64(1) || mfaData(t, loaded)["logicalName"] != "native-stepup-draft" {
		t.Fatalf("persisted draft=%d %s", status, message)
	}
}
