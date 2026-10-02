package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"time"
)

// Hardware nodes reside in the same Ariadne generation as Kubernetes objects.
// This is a projection adapter, not a second store or traversal implementation.
func (g *Graph) ReplaceHardware(ctx context.Context, source string, entities []resource.Entity, observed time.Time) error {
	objects := []unstructured.Unstructured{}
	for _, e := range entities {
		id, err := resource.ParseCanonicalID(e.CanonicalID)
		if err != nil || id.Domain != "hardware" || id.Tenant != g.tenant || id.Scope != g.cluster || id.Kind != e.Kind {
			return ErrScope
		}
		digest := sha256.Sum256([]byte(e.CanonicalID))
		attributes, err := json.Marshal(e.Attributes)
		if err != nil {
			return err
		}
		// Unstructured requires JSON values, while selected hardware adapters
		// intentionally expose typed Metal3 models. Normalize at this boundary
		// before Ariadne copies them, and reject unsupported values explicitly.
		var projected map[string]any
		if err := json.Unmarshal(attributes, &projected); err != nil {
			return err
		}
		rv := sha256.Sum256(attributes)
		o := unstructured.Unstructured{Object: map[string]any{"apiVersion": "redfish/v1", "kind": id.Kind, "metadata": map[string]any{"name": "hardware-" + hex.EncodeToString(digest[:]), "uid": id.StableID, "resourceVersion": hex.EncodeToString(rv[:])}, "status": projected}}
		o.SetAnnotations(map[string]string{"ops.internal/canonical-id": e.CanonicalID, "ops.internal/source-id": source, "ops.internal/display-name": e.Name, "ops.internal/observed-at": e.UpdatedAt.UTC().Format(time.RFC3339Nano)})
		objects = append(objects, o)
	}
	return g.Replace(ctx, g.OwnerEpoch(), kubernetes.Snapshot{GVR: kubernetes.GVR{Group: "redfish", Version: "v1", Resource: source}, Objects: objects, State: kubernetes.GVRState{LastListCompletedAt: observed}})
}
