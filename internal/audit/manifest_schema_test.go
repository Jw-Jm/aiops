package audit

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"ops-platform/internal/archive"
)

func TestSignedManifestAndProofMatchVersionedSchemas(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	for _, file := range []string{"audit-segment-v2.schema.json", "audit-segment-proof-v1.schema.json"} {
		encoded, err := os.ReadFile("../../api/schemas/" + file)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(encoded, &document); err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource(document["$id"].(string), document); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := compiler.Compile("https://ops.local/schemas/audit-segment-proof/v1")
	if err != nil {
		t.Fatal(err)
	}
	tenant, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	segment := segmentDescriptor{FormatVersion: "audit-segment/v2", TenantID: tenant, ID: id, First: 1, Last: 1, Count: 1, Root: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	ref := archive.ObjectRef{TenantID: tenant, ObjectID: id, Category: "audit-segment", Key: "opaque-key", Digest: segment.Root, Size: 10, ContentType: "application/json", RetainUntil: time.Now().UTC().Add(time.Hour)}
	manifest, err := manifestBytes(segment, ref)
	if err != nil {
		t.Fatal(err)
	}
	proof, _ := json.Marshal(archivedProof{manifest, "vault:v1:AAAA", "1"})
	var instance any
	if json.Unmarshal(proof, &instance) != nil {
		t.Fatal("invalid proof JSON")
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatal(err)
	}
}
