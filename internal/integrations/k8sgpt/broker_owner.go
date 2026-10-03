package k8sgpt

import (
	"context"
	"encoding/json"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type brokerParent struct {
	Object unstructured.Unstructured
	Depth  int
}

// Native GetParent in the locked Pod/Node/PVC analyzers follows ownerRefs. These
// are single-object reads, not a general application discovery API. Only an
// already admitted object's exact UID reference may grant the next bounded GET.
func (b *ReadBroker) serveOwner(w http.ResponseWriter, r *http.Request, deny func(int)) bool {
	prefix := "/apis/apps/v1/namespaces/" + b.Namespace + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	kinds := map[string]string{"replicasets": "ReplicaSet", "deployments": "Deployment", "statefulsets": "StatefulSet", "daemonsets": "DaemonSet"}
	kind := kinds[parts[0]]
	if len(parts) != 2 || kind == "" || parts[1] == "" || r.URL.RawPath != "" || len(r.URL.Query()) != 0 || url.PathEscape(parts[1]) != parts[1] {
		deny(403)
		return true
	}
	b.mu.Lock()
	known := []brokerParent{}
	for _, o := range b.objects {
		known = append(known, brokerParent{Object: o})
	}
	for _, o := range b.parents {
		known = append(known, o)
	}
	uid := ""
	depth := 0
	ambiguous := false
	for _, o := range known {
		if o.Object.GetNamespace() != b.Namespace || o.Depth >= 2 {
			continue
		}
		for _, owner := range o.Object.GetOwnerReferences() {
			if owner.APIVersion == "apps/v1" && owner.Kind == kind && owner.Name == parts[1] && owner.UID != "" {
				if uid != "" && uid != string(owner.UID) {
					ambiguous = true
				}
				uid = string(owner.UID)
				if depth == 0 || o.Depth+1 < depth {
					depth = o.Depth + 1
				}
			}
		}
	}
	budget := len(b.parents) >= 100
	b.mu.Unlock()
	if uid == "" || ambiguous || budget {
		deny(403)
		return true
	}
	if b.Authorize(r.Context()) != nil {
		deny(403)
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	response, err := b.Client.Do(ctx, "GET", r.URL.Path, nil)
	if err != nil {
		deny(503)
		return true
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		deny(503)
		return true
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	var object unstructured.Unstructured
	if err != nil || len(raw) > 128<<10 || json.Unmarshal(raw, &object.Object) != nil || object.GetAPIVersion() != "apps/v1" || object.GetKind() != kind || object.GetNamespace() != b.Namespace || object.GetName() != parts[1] || string(object.GetUID()) != uid {
		deny(503)
		return true
	}
	for key, value := range b.RequiredLabels {
		if object.GetLabels()[key] != value {
			deny(503)
			return true
		}
	}
	if b.Authorize(ctx) != nil {
		deny(403)
		return true
	}
	// GetParent uses metadata only. Workload templates, env, commands and provider
	// configuration are never passed through these supplemental parent routes.
	safe := unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": kind}}
	safe.SetName(object.GetName())
	safe.SetNamespace(object.GetNamespace())
	safe.SetUID(object.GetUID())
	safe.SetOwnerReferences(object.GetOwnerReferences())
	b.mu.Lock()
	if b.parents == nil {
		b.parents = map[string]brokerParent{}
	}
	b.parents[r.URL.Path] = brokerParent{Object: safe, Depth: depth}
	b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(safe.Object)
	return true
}
