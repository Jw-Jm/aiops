package graph

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aalpar/ariadne"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	api "ops-platform/internal/upstream/ontology/api"
	"ops-platform/internal/upstream/ontology/service/diagnostic"
	"slices"
	"sort"
	"sync"
	"time"
)

var ErrNotReady = errors.New("GRAPH_NOT_READY")
var ErrScope = errors.New("FORBIDDEN")
var ErrStale = errors.New("STALE_CONTEXT")

type Revision struct {
	OwnerEpoch      int64 `json:"ownerEpoch"`
	GraphGeneration int64 `json:"graphGeneration"`
}
type Scope struct {
	Tenant                string   `json:"tenantId"`
	Cluster               string   `json:"clusterUid"`
	Namespaces            []string `json:"namespaces"`
	Resources             []string `json:"resources"`
	ClusterScoped         bool     `json:"clusterScoped"`
	AuthorizationRevision string   `json:"authorizationRevision"`
}

func (s Scope) MarshalJSON() ([]byte, error) {
	type plain Scope
	if s.Namespaces == nil {
		s.Namespaces = []string{}
	}
	if s.Resources == nil {
		s.Resources = []string{}
	}
	return json.Marshal(plain(s))
}

func (s Scope) Allows(id, ns string) bool {
	parsed, err := resource.ParseCanonicalID(id)
	if err != nil || parsed.Tenant != s.Tenant || parsed.Scope != s.Cluster {
		return false
	}
	if len(s.Resources) > 0 && !slices.Contains(s.Resources, id) {
		return false
	}
	if ns == "" {
		return s.ClusterScoped
	}
	return slices.Contains(s.Namespaces, ns)
}

type Query struct {
	Direction          string    `json:"direction,omitempty"`
	Name               string    `json:"name,omitempty"`
	Health             string    `json:"health,omitempty"`
	Label              string    `json:"label,omitempty"`
	Sort               string    `json:"sort,omitempty"`
	Order              string    `json:"order,omitempty"`
	QueryKind          string    `json:"queryKind"`
	CanonicalID        string    `json:"canonicalId"`
	ExpectedOwnerEpoch int64     `json:"expectedOwnerEpoch"`
	MaxDepth           int       `json:"maxDepth"`
	MaxNodes           int       `json:"maxNodes"`
	MaxEdges           int       `json:"maxEdges"`
	Scope              Scope     `json:"scope"`
	RelationKinds      []string  `json:"relationKinds"`
	CursorRevision     *Revision `json:"cursorRevision,omitempty"`
	AfterCanonicalID   string    `json:"afterCanonicalId,omitempty"`
	Kind               string    `json:"kind,omitempty"`
	Namespace          string    `json:"namespace,omitempty"`
}
type Result struct {
	DirectlyAffected   []string                 `json:"directlyAffected"`
	IndirectlyAffected []string                 `json:"indirectlyAffected"`
	DependencyOnly     []string                 `json:"dependencyOnly"`
	SchemaVersion      string                   `json:"schemaVersion"`
	GraphRevision      Revision                 `json:"graphRevision"`
	OwnerInstance      string                   `json:"ownerInstance"`
	CollectedAt        time.Time                `json:"collectedAt"`
	Freshness          string                   `json:"freshness"`
	Partial            bool                     `json:"partial"`
	Warnings           []string                 `json:"warnings"`
	DegradedSources    []string                 `json:"degradedSources"`
	Nodes              []resource.Entity        `json:"nodes"`
	Edges              []resource.Relation      `json:"edges"`
	Budgets            api.DiagnosticBudget     `json:"budgets"`
	RankedEvidence     []api.RankedEvidence     `json:"rankedEvidence"`
	Conflicts          []api.DiagnosticConflict `json:"conflicts"`
	NextCanonicalID    string                   `json:"nextCanonicalId,omitempty"`
}
type snapshot struct {
	graph      *ariadne.Graph
	identities map[string]ariadne.ObjectRef
	revision   Revision
}
type Graph struct {
	now                       func() time.Time
	mu                        sync.RWMutex
	buildGate                 chan struct{}
	rebuilding                bool
	tenant, cluster, instance string
	current                   *snapshot
	states                    map[string]kubernetes.GVRState
	required                  []kubernetes.GVR
	epoch                     int64
	deadline                  time.Time
	overlays                  []resource.Relation
	degraded                  map[string]string
}

func New(tenant, cluster, instance string, required []kubernetes.GVR) *Graph {
	return NewWithClock(tenant, cluster, instance, required, time.Now)
}

// NewWithClock binds a trusted process clock at construction. Production always
// uses New; no request, configuration file or environment can override this clock.
func NewWithClock(tenant, cluster, instance string, required []kubernetes.GVR, clock func() time.Time) *Graph {
	if clock == nil {
		panic("graph clock is required")
	}
	return &Graph{now: clock, buildGate: make(chan struct{}, 1), tenant: tenant, cluster: cluster, instance: instance, required: slices.Clone(required), states: map[string]kubernetes.GVRState{}, degraded: map[string]string{}}
}
func (g *Graph) SetOwner(epoch int64, deadline time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if epoch < g.epoch {
		return
	}
	if epoch != g.epoch && g.current != nil {
		g.current = &snapshot{graph: g.current.graph, identities: g.current.identities, revision: Revision{epoch, 1}}
	}
	g.epoch = epoch
	g.deadline = deadline
}
func (g *Graph) InvalidateOwner()  { g.mu.Lock(); defer g.mu.Unlock(); g.deadline = time.Time{} }
func (g *Graph) OwnerEpoch() int64 { g.mu.RLock(); defer g.mu.RUnlock(); return g.epoch }

// UpdateSourceState changes observation health without replacing a published
// resource set. A failed metadata write is not an empty successful List.
func (g *Graph) UpdateSourceState(epoch int64, gvr kubernetes.GVR, state kubernetes.GVRState) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if epoch != g.epoch {
		return ErrStale
	}
	g.states[gvr.Key()] = state
	return nil
}
func (g *Graph) Qualified(now time.Time) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.ready(now)
}
func (g *Graph) ready(now time.Time) bool {
	if g.rebuilding {
		return false
	}
	for _, gvr := range g.required {
		s, ok := g.states[gvr.Key()]
		if !ok || s.LastListCompletedAt.IsZero() || !s.WatchConnected || !s.WatchContinuous || s.ProjectionQueueLag > time.Second*60 || s.LastConnectivityProbeAt.IsZero() || now.Sub(s.LastConnectivityProbeAt) > 45*time.Second || s.LastError != "" {
			return false
		}
	}
	return len(g.required) > 0
}

// Replacement is built off the published snapshot, then swapped atomically.
// Ariadne is the only node/edge store. The sidecar contains only ID→ObjectRef
// identity projection, no entities, edges or adjacency information.
func (g *Graph) Replace(ctx context.Context, epoch int64, change kubernetes.Snapshot) error {
	if change.ObservationOnly {
		return g.UpdateSourceState(epoch, change.GVR, change.State)
	}
	select {
	case g.buildGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-g.buildGate }()
	g.mu.Lock()
	if epoch != g.epoch {
		g.mu.Unlock()
		return ErrStale
	}
	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		return err
	}
	if change.State.LastListCompletedAt.IsZero() {
		g.states[change.GVR.Key()] = change.State
		g.mu.Unlock()
		return nil
	}
	base := g.current
	g.rebuilding = true
	g.mu.Unlock()
	published := false
	defer func() {
		g.mu.Lock()
		g.rebuilding = false
		if !published && epoch == g.epoch {
			failed := change.State
			failed.LastError = "generation_rebuild_failed"
			g.states[change.GVR.Key()] = failed
		}
		g.mu.Unlock()
	}()
	// List/Watch connectivity observations do not mutate facts. Reuse the
	// immutable Ariadne generation when its exact UID/RV set is unchanged.
	if base != nil {
		existing := map[string]string{}
		for _, ref := range base.graph.Nodes() {
			if o, ok := base.graph.Get(ref); ok && o.GetAnnotations()["ops.internal/gvr"] == change.GVR.Key() {
				existing[string(o.GetUID())] = o.GetResourceVersion() + "/" + o.GetAnnotations()["ops.internal/source-id"]
			}
		}
		same := len(existing) == len(change.Objects)
		seen := map[string]bool{}
		for _, o := range change.Objects {
			uid := string(o.GetUID())
			if uid == "" || seen[uid] || existing[uid] != o.GetResourceVersion()+"/"+o.GetAnnotations()["ops.internal/source-id"] {
				same = false
			}
			seen[uid] = true
		}
		if same {
			g.mu.Lock()
			if epoch != g.epoch {
				g.mu.Unlock()
				return ErrStale
			}
			g.states[change.GVR.Key()] = change.State
			published = true
			g.mu.Unlock()
			return nil
		}
	}
	objects := make([]unstructured.Unstructured, 0, len(change.Objects))
	if base != nil {
		for _, ref := range base.graph.Nodes() {
			o, ok := base.graph.Get(ref)
			if ok && o.GetAnnotations()["ops.internal/gvr"] != change.GVR.Key() {
				objects = append(objects, *o.DeepCopy())
			}
		}
	}
	for _, raw := range change.Objects {
		o := raw.DeepCopy()
		annotations := o.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations["ops.internal/gvr"] = change.GVR.Key()
		if annotations["ops.internal/observed-at"] == "" {
			annotations["ops.internal/observed-at"] = change.State.LastListCompletedAt.UTC().Format(time.RFC3339Nano)
		}
		if annotations["ops.internal/first-observed-at"] == "" {
			annotations["ops.internal/first-observed-at"] = annotations["ops.internal/observed-at"]
			if base != nil {
				if old, ok := base.graph.Get(ariadne.RefFromUnstructured(o)); ok && old.GetUID() == o.GetUID() && old.GetAnnotations()["ops.internal/first-observed-at"] != "" {
					annotations["ops.internal/first-observed-at"] = old.GetAnnotations()["ops.internal/first-observed-at"]
				}
			}
		}
		o.SetAnnotations(annotations)
		objects = append(objects, *o)
	}
	next := &snapshot{graph: ariadne.New(ariadne.WithResolver(ariadne.NewStructuralResolver()), ariadne.WithResolver(ariadne.NewSelectorResolver()), ariadne.WithResolver(ariadne.NewEventResolver())), identities: map[string]ariadne.ObjectRef{}, revision: Revision{epoch, 1}}
	if base != nil && base.revision.OwnerEpoch == epoch {
		next.revision.GraphGeneration = base.revision.GraphGeneration + 1
	}
	for i := range objects {
		raw := &objects[i]
		id, err := g.id(raw)
		if err != nil {
			return err
		}
		if _, exists := next.identities[id.String()]; exists {
			return errors.New("conflicting immutable identities")
		}
		next.identities[id.String()] = ariadne.RefFromUnstructured(raw)
	}
	next.graph.Load(objects)
	if err := ctx.Err(); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if epoch != g.epoch {
		return ErrStale
	}
	// Overlay expiry may advance the revision while the immutable graph is
	// rebuilt. Preserve that advancement, and fence any graph replacement.
	if g.current != nil {
		if base == nil || g.current.graph != base.graph {
			return ErrStale
		}
		next.revision.GraphGeneration = g.current.revision.GraphGeneration + 1
	}
	g.current = next
	g.states[change.GVR.Key()] = change.State
	published = true
	return nil
}
func (g *Graph) id(o *unstructured.Unstructured) (resource.CanonicalID, error) {
	if raw := o.GetAnnotations()["ops.internal/canonical-id"]; raw != "" {
		id, err := resource.ParseCanonicalID(raw)
		if err != nil || id.Domain != "hardware" || id.Tenant != g.tenant || id.Scope != g.cluster || id.Kind != o.GetKind() || id.StableID != string(o.GetUID()) {
			return resource.CanonicalID{}, ErrScope
		}
		return id, nil
	}

	group := o.GroupVersionKind().Group
	if group == "" {
		group = "core"
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: g.tenant, Scope: g.cluster, APIGroup: group, Kind: o.GetKind(), StableID: string(o.GetUID())}
	return id, resource.ValidateCanonicalID(id)
}
func (g *Graph) Query(ctx context.Context, q Query) (Result, error) {
	g.expireExternal(g.now())
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.queryLocked(ctx, q)
}

// QueryReadResponse builds and encodes one current immutable snapshot after
// the caller's external authorization/Lease checks. The callback must be local,
// bounded serialization only: no database, remote I/O or response writing.
// Paged queries still require their exact cursor revision; changing facts can
// never be combined across snapshots or pass an old owner epoch.
func (g *Graph) QueryReadResponse(ctx context.Context, q Query, encode func(Result) error) error {
	if encode == nil {
		return ErrNotReady
	}
	g.expireExternal(g.now())
	g.mu.RLock()
	defer g.mu.RUnlock()
	result, err := g.queryLocked(ctx, q)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = encode(result); err != nil {
		return err
	}
	return ctx.Err()
}

func (g *Graph) queryLocked(ctx context.Context, q Query) (Result, error) {
	now := g.now()
	if q.QueryKind != "" && !slices.Contains([]string{"entity", "neighbors", "impact", "dependencies", "diagnostic", "list"}, q.QueryKind) {
		return Result{}, errors.New("INVALID_ARGUMENT")
	}
	if g.current == nil || g.epoch != q.ExpectedOwnerEpoch || !now.Before(g.deadline) {
		return Result{}, ErrNotReady
	}
	if q.Scope.Tenant != g.tenant || q.Scope.Cluster != g.cluster {
		return Result{}, ErrScope
	}
	if q.Direction != "" && !slices.Contains([]string{"in", "out", "both"}, q.Direction) {
		return Result{}, errors.New("INVALID_ARGUMENT")
	}
	if q.MaxDepth < 1 || q.MaxDepth > 2 || q.MaxNodes < 1 || q.MaxNodes > 200 || q.MaxEdges < 1 || q.MaxEdges > 400 {
		return Result{}, errors.New("INVALID_ARGUMENT")
	}
	if err := validateList(q); err != nil {
		return Result{}, err
	}
	revision := Revision{g.epoch, g.current.revision.GraphGeneration}
	if q.CursorRevision != nil && *q.CursorRevision != revision {
		return Result{}, ErrStale
	}
	reader := &ontologyReader{owner: g, snapshot: g.current, scope: q.Scope, now: now, kinds: q.RelationKinds, queryKind: q.QueryKind, direction: q.Direction}
	if q.QueryKind == "list" {
		out := Result{SchemaVersion: "resource-graph/v2", GraphRevision: revision, OwnerInstance: g.instance, CollectedAt: now, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}, Nodes: []resource.Entity{}, Edges: []resource.Relation{}, RankedEvidence: []api.RankedEvidence{}, Conflicts: []api.DiagnosticConflict{}}

		candidates := []resource.Entity{}
		for id := range g.current.identities {
			n, ok := reader.GetNode(modelID(id))
			if !ok {
				continue
			}
			entity := g.projectEntity(id, n.Attributes)
			if listMatches(q, entity) {
				candidates = append(candidates, entity)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			left, right := listKey(q, candidates[i]), listKey(q, candidates[j])
			if left == right {
				left, right = candidates[i].CanonicalID, candidates[j].CanonicalID
			}
			if q.Order == "desc" {
				return left > right
			}
			return left < right
		})
		offset := 0
		if q.AfterCanonicalID != "" {
			found := false
			for i, e := range candidates {
				if e.CanonicalID == q.AfterCanonicalID {
					offset = i + 1
					found = true
					break
				}
			}
			if !found {
				return Result{}, ErrStale
			}
		}
		for _, e := range candidates[offset:] {
			if len(out.Nodes) == q.MaxNodes {
				out.NextCanonicalID = out.Nodes[len(out.Nodes)-1].CanonicalID
				break
			}
			out.Nodes = append(out.Nodes, e)
		}
		if !g.ready(now) {
			out.Freshness = "stale"
			out.Partial = true
			out.Warnings = append(out.Warnings, "source_continuity_unverified")
			out.DegradedSources = append(out.DegradedSources, "kubernetes")
		}
		if reader.scopeLimited {
			out.Partial = true
			out.Warnings = append(out.Warnings, "scope_limited")
		}
		out.Budgets = api.DiagnosticBudget{MaxDepth: q.MaxDepth, StorageMaxDepth: q.MaxDepth, MaxNodes: q.MaxNodes, MaxEdges: q.MaxEdges, NodeCount: len(out.Nodes), EdgeCount: 0, Truncated: out.NextCanonicalID != ""}
		g.applySourceHealth(&out)
		return out, nil
	}
	node, found := reader.GetNode(modelID(q.CanonicalID))
	if !found {
		return Result{}, ErrScope
	}
	depth := q.MaxDepth
	if q.QueryKind == "entity" {
		depth = 1
	}
	upstream, err := diagnostic.NewService(reader).GetDiagnosticSubgraphContext(ctx, api.EntryRef{Kind: api.NodeKind(node.Kind), CanonicalID: q.CanonicalID}, api.ExpansionPolicy{MaxDepth: depth, StorageMaxDepth: depth, MaxNodes: q.MaxNodes, MaxEdges: q.MaxEdges, IncludeStorage: true, IncludeEvents: true, IncludeRBAC: true, ExpandTerminalNodes: true})
	if err != nil {
		return Result{}, err
	}
	var direct, indirect, dependencies []string
	if q.QueryKind == "impact" {
		direct, indirect, dependencies, err = impactProjection(ctx, reader, api.EntryRef{Kind: api.NodeKind(node.Kind), CanonicalID: q.CanonicalID}, api.ExpansionPolicy{MaxDepth: depth, StorageMaxDepth: depth, MaxNodes: q.MaxNodes, MaxEdges: q.MaxEdges, IncludeStorage: true, IncludeEvents: true, IncludeRBAC: true, ExpandTerminalNodes: true}, &upstream)
		if err != nil {
			return Result{}, err
		}
	}
	out := Result{SchemaVersion: "resource-graph/v2", GraphRevision: revision, OwnerInstance: g.instance, CollectedAt: now, Freshness: "fresh", Partial: upstream.Partial, Warnings: []string{}, DegradedSources: []string{}, Nodes: []resource.Entity{}, Edges: []resource.Relation{}, Budgets: upstream.Budgets}
	out.DirectlyAffected = direct
	out.IndirectlyAffected = indirect
	out.DependencyOnly = dependencies
	out.RankedEvidence = upstream.RankedEvidence
	out.Conflicts = upstream.Conflicts
	for _, n := range upstream.Nodes {
		if q.QueryKind == "entity" && n.CanonicalID != q.CanonicalID {
			continue
		}
		out.Nodes = append(out.Nodes, g.projectEntity(n.CanonicalID, n.Attributes))
	}
	for _, e := range upstream.Edges {
		if q.QueryKind == "entity" {
			continue
		}
		out.Edges = append(out.Edges, reader.projectEdge(e))
	}
	if !g.ready(now) {
		out.Freshness = "stale"
		out.Partial = true
		out.Warnings = append(out.Warnings, "source_continuity_unverified")
		out.DegradedSources = append(out.DegradedSources, "kubernetes")
	}
	if reader.scopeLimited {
		out.Partial = true
		out.Warnings = append(out.Warnings, "scope_limited")
	}
	if upstream.Partial {
		out.Warnings = append(out.Warnings, "query_budget_exhausted")
	}
	g.applySourceHealth(&out)
	return out, nil
}

func (g *Graph) SourceStates() map[string]kubernetes.GVRState {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := map[string]kubernetes.GVRState{}
	for key, state := range g.states {
		out[key] = state
	}
	return out
}
