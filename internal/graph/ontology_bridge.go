package graph

import (
	"github.com/aalpar/ariadne"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/resource"
	api "ops-platform/internal/upstream/ontology/api"
	"ops-platform/internal/upstream/ontology/model"
	"slices"
	"strings"
	"time"
)

func modelID(raw string) model.CanonicalID { return model.CanonicalID(raw) }

type ontologyReader struct {
	owner        *Graph
	snapshot     *snapshot
	scope        Scope
	now          time.Time
	scopeLimited bool
	kinds        []string
	queryKind    string
	direction    string
}

func (r *ontologyReader) GetNode(id model.CanonicalID) (model.Node, bool) {
	ref, ok := r.snapshot.identities[id.String()]
	if !ok {
		return model.Node{}, false
	}
	o, ok := r.snapshot.graph.Get(ref)
	if !ok {
		return model.Node{}, false
	}
	if !r.scope.Allows(id.String(), o.GetNamespace()) {
		r.scopeLimited = true
		return model.Node{}, false
	}
	kind := model.NodeKind(o.GetKind())
	switch o.GetKind() {
	case "PersistentVolumeClaim":
		kind = model.NodeKindPVC
	case "PersistentVolume":
		kind = model.NodeKindPV
	case "Deployment", "ReplicaSet", "StatefulSet", "DaemonSet", "Job":
		kind = model.NodeKindWorkload
	}
	attrs := map[string]any{}
	if status, ok := o.Object["status"]; ok {
		if o.GetAnnotations()["ops.internal/canonical-id"] != "" {
			attrs, _ = status.(map[string]any)
		} else {
			attrs["status"] = status
		}
	}
	return model.Node{ID: id, Kind: kind, SourceKind: o.GetKind(), Name: o.GetName(), Namespace: o.GetNamespace(), Attributes: attrs}, true
}
func (r *ontologyReader) ListNodes() []model.Node {
	out := []model.Node{}
	for id := range r.snapshot.identities {
		if n, ok := r.GetNode(modelID(id)); ok {
			out = append(out, n)
		}
	}
	return out
}
func (r *ontologyReader) Neighbors(id model.CanonicalID) []model.Edge {
	ref, ok := r.snapshot.identities[id.String()]
	if !ok {
		return nil
	}
	edges := []ariadne.Edge{}
	if r.queryKind != "impact" && r.direction != "in" {
		edges = append(edges, r.snapshot.graph.DependenciesOf(ref)...)
	}
	if r.queryKind != "dependencies" && r.direction != "out" {
		edges = append(edges, r.snapshot.graph.DependentsOf(ref)...)
	}
	out := []model.Edge{}
	for _, edge := range edges {
		from, fok := r.snapshot.graph.Get(edge.From)
		to, tok := r.snapshot.graph.Get(edge.To)
		if !fok || !tok {
			continue
		}
		// Pinned resolvers locate references by GVK/namespace/name. The platform
		// identity projection fences references that also declare an immutable UID,
		// so an Event/claim cannot attach to a same-name replacement object.
		if from.GetKind() == "Event" {
			uid, _, _ := unstructured.NestedString(from.Object, "involvedObject", "uid")
			if uid == "" || uid != string(to.GetUID()) {
				continue
			}
		}
		if from.GetKind() == "PersistentVolume" && to.GetKind() == "PersistentVolumeClaim" {
			uid, _, _ := unstructured.NestedString(from.Object, "spec", "claimRef", "uid")
			if uid != "" && uid != string(to.GetUID()) {
				continue
			}
		}

		fid, _ := r.owner.id(from)
		tid, _ := r.owner.id(to)
		if _, ok := r.GetNode(modelID(fid.String())); !ok {
			continue
		}
		if _, ok := r.GetNode(modelID(tid.String())); !ok {
			continue
		}
		kind := model.EdgeKindRelatedTo
		source := model.EdgeSourceTypeExplicitRef
		state := model.EdgeStateAsserted
		if edge.Type == ariadne.EdgeLabelSelector {
			kind = model.EdgeKindSelectsPod
			source = model.EdgeSourceTypeSelectorMatch
			state = model.EdgeStateInferred
		} else {
			for _, owner := range from.GetOwnerReferences() {
				if owner.Name == to.GetName() && owner.Kind == to.GetKind() {
					if owner.UID != to.GetUID() {
						kind = ""
					} else {
						kind = model.EdgeKindManagedBy
						source = model.EdgeSourceTypeOwnerReference
						state = model.EdgeStateInferred
					}
					break
				}
			}
			if kind == model.EdgeKindRelatedTo {
				field := edge.Field
				switch {
				case from.GetKind() == "Pod" && to.GetKind() == "Node":
					kind = model.EdgeKindScheduledOn
					source = model.EdgeSourceTypeObserved
					state = model.EdgeStateObserved
				case from.GetKind() == "Pod" && to.GetKind() == "PersistentVolumeClaim":
					kind = model.EdgeKindMountsPVC
				case from.GetKind() == "PersistentVolumeClaim" && to.GetKind() == "PersistentVolume":
					kind = model.EdgeKindBoundToPV
				case to.GetKind() == "StorageClass":
					kind = model.EdgeKindUsesStorageClass
				case to.GetKind() == "CSIDriver" || strings.Contains(field, "csi.driver"):
					kind = model.EdgeKindProvisionedByCSIDriver
				}
			}
		}
		if kind == "" || (len(r.kinds) > 0 && !slices.Contains(r.kinds, string(kind))) {
			continue
		}
		out = append(out, model.Edge{From: modelID(fid.String()), To: modelID(tid.String()), Kind: kind, Provenance: model.EdgeProvenance{SourceType: source, State: state, Resolver: edge.Resolver}})
	}
	for _, edge := range r.owner.overlays {
		if (r.queryKind == "impact" || r.direction == "in") && edge.To != id.String() {
			continue
		}
		if (r.queryKind == "dependencies" || r.direction == "out") && edge.From != id.String() {
			continue
		}
		if edge.From != id.String() && edge.To != id.String() {
			continue
		}
		if edge.TTLSeconds > 0 && !r.now.Before(edge.ObservedAt.Add(time.Duration(edge.TTLSeconds)*time.Second)) {
			continue
		}
		if _, ok := r.GetNode(modelID(edge.From)); !ok {
			continue
		}
		if _, ok := r.GetNode(modelID(edge.To)); !ok {
			continue
		}
		if len(r.kinds) > 0 && !slices.Contains(r.kinds, edge.Kind) {
			continue
		}
		out = append(out, model.Edge{From: modelID(edge.From), To: modelID(edge.To), Kind: model.EdgeKind(edge.Kind), Provenance: model.EdgeProvenance{SourceType: model.EdgeSourceTypeObserved, State: model.EdgeStateObserved, Resolver: edge.Provenance.RuleVersion}})
	}
	return out
}
func (r *ontologyReader) projectEdge(e api.DiagnosticEdge) resource.Relation {
	observed := time.Time{}
	sourceID := ""
	if ref, ok := r.snapshot.identities[e.From]; ok {
		if obj, found := r.snapshot.graph.Get(ref); found {
			observed, _ = time.Parse(time.RFC3339Nano, obj.GetAnnotations()["ops.internal/observed-at"])
			sourceID = obj.GetAnnotations()["ops.internal/source-id"]
		}
	}
	for _, o := range r.owner.overlays {
		if o.From == e.From && o.To == e.To && o.Kind == string(e.Kind) {
			return o
		}
	}
	return resource.Relation{From: e.From, To: e.To, Kind: string(e.Kind), Provenance: resource.Provenance{SourceRegistrationID: sourceID, RuleVersion: "ariadne/fa2232ce45c4c869e47ddae1c57e8886830cfb6c/" + e.Provenance.Resolver + "/identity-projection-v1", ObservedAt: observed}, Confidence: edgeConfidence(e), ObservedAt: observed, ValidFrom: observed}
}

func edgeConfidence(e api.DiagnosticEdge) float64 {
	if string(e.Provenance.SourceType) == string(model.EdgeSourceTypeSelectorMatch) {
		return 0.95
	}
	return 1
}

func (g *Graph) projectEntity(id string, attributes map[string]any) resource.Entity {
	ref := g.current.identities[id]
	o, _ := g.current.graph.Get(ref)
	observed, _ := time.Parse(time.RFC3339Nano, o.GetAnnotations()["ops.internal/observed-at"])
	first, _ := time.Parse(time.RFC3339Nano, o.GetAnnotations()["ops.internal/first-observed-at"])
	sources := []string{}
	if source := o.GetAnnotations()["ops.internal/source-id"]; source != "" {
		sources = append(sources, source)
	}
	canonical, _ := resource.ParseCanonicalID(id)
	if canonical.Domain == "hardware" && g.degraded[o.GetAnnotations()["ops.internal/source-id"]] != "" && g.degraded[o.GetAnnotations()["ops.internal/source-id"]] != "hardware_fixture_only" {
		safe := map[string]any{}
		for k, v := range attributes {
			safe[k] = v
		}
		safe["health"] = "unknown"
		attributes = safe
	}
	name := o.GetName()
	if display := o.GetAnnotations()["ops.internal/display-name"]; display != "" {
		name = display
	}
	return resource.Entity{CanonicalID: id, Kind: canonical.Kind, Name: name, Namespace: o.GetNamespace(), Labels: o.GetLabels(), Attributes: attributes, SourceRefs: sources, FirstObservedAt: first, UpdatedAt: observed}
}
