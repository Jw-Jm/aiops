package redfish

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"ops-platform/internal/evidence"
	"ops-platform/internal/resource"
	"slices"
	"time"
)

// Adapter never accepts commands, Redfish actions, OData paths or credentials
// from a query. Those belong to the authenticated immutable source binding.
type Adapter struct {
	Mode      string
	Binding   evidence.Binding
	Config    Config
	Authorize evidence.Authorizer
}

// ValidateScope rejects dimensions that the read-only BMC protocol cannot
// enforce. One immutable BMC binding belongs to exactly one hardware scope;
// declarations of account or label isolation are not a source-side proof.
func (a *Adapter) ValidateScope() error {
	m := a.Binding.ScopeMapping
	if m.NativeTenant != "" || len(m.RequiredLabels) != 0 || len(m.Scopes) != 1 || len(m.Scopes["cluster"]) != 1 || m.Scopes["cluster"][0] != a.Config.Scope || a.Config.Tenant != a.Binding.Tenant || a.Config.SourceID != a.Binding.SourceID {
		return evidence.ErrScopeUnverified
	}
	return nil
}

func (a *Adapter) Collect(ctx context.Context) (Inventory, error) {
	if err := a.ValidateScope(); err != nil {
		return Inventory{}, err
	}
	if err := a.Authorize(ctx, a.Binding); err != nil {
		return Inventory{}, err
	}
	return Collect(ctx, a.Config)
}

func (a *Adapter) Name() string { return "redfish" }
func (a *Adapter) Capabilities(ctx context.Context) (evidence.CapabilitySet, error) {
	if err := a.ValidateScope(); err != nil {
		return evidence.CapabilitySet{"inventory": "disabled/unverified", "write": "disabled"}, err
	}
	if err := a.Authorize(ctx, a.Binding); err != nil {
		return evidence.CapabilitySet{"inventory": "disabled/unverified", "write": "disabled"}, err
	}
	state := "read_only"
	if a.Mode == "fixture_only" {
		state = "fixture_only"
	}
	return evidence.CapabilitySet{"inventory": state, "write": "disabled"}, nil
}
func (a *Adapter) Query(ctx context.Context, q evidence.Query) (evidence.Result, error) {
	out := evidence.Result{SchemaVersion: "evidence-result/v2", Evidence: []evidence.Evidence{}, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}}
	if err := a.ValidateScope(); err != nil {
		return out, err
	}
	id, err := resource.ParseCanonicalID(q.ResourceCanonicalID)
	if err != nil || id.Domain != "hardware" || id.Tenant != a.Binding.Tenant || !q.Scope.Allows(q.ResourceCanonicalID, "") || q.Namespace != "" || !slices.Contains(a.Binding.ScopeMapping.Scopes["cluster"], id.Scope) || a.Config.Scope != id.Scope || a.Config.Tenant != a.Binding.Tenant || a.Config.SourceID != a.Binding.SourceID {
		return out, evidence.ErrScopeUnverified
	}
	if q.Template != "hardware-inventory/v1" && q.Template != "hardware-health/v1" {
		return out, evidence.ErrArgument
	}
	if q.Limit < 1 || q.Limit > 200 || q.From.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > time.Hour || q.To.After(time.Now().Add(time.Minute)) {
		return out, evidence.ErrBudget
	}
	bound, _ := json.Marshal(struct {
		Binding evidence.Binding
		Query   evidence.Query
		Scope   any
	}{a.Binding, q, q.Scope})
	out.QueryHash = evidence.Digest(bound)
	if q.To.Before(time.Now().Add(-time.Minute)) {
		out.Freshness = "unavailable"
		out.Partial = true
		out.DegradedSources = []string{"redfish"}
		out.Warnings = append(out.Warnings, "historical_inventory_requires_archive")
		return out, nil
	}
	if err := a.Authorize(ctx, a.Binding); err != nil {
		return out, err
	}
	deadline, cancel := context.WithTimeout(ctx, q.TimeBudget())
	defer cancel()
	inventory, err := a.Collect(deadline)
	if err != nil {
		return out, err
	}
	if a.Mode == "fixture_only" {
		out.Warnings = append(out.Warnings, "fixture_only")
	}
	out.Partial = inventory.Partial
	out.DegradedSources = inventory.DegradedSources
	out.Warnings = append(out.Warnings, inventory.Warnings...)
	entities := []resource.Entity{}
	for _, entity := range inventory.Entities {
		canonical, err := resource.ParseCanonicalID(entity.CanonicalID)
		if err == nil && (canonical.StableID == id.StableID || slices.Contains([]string{canonical.StableID}, id.StableID)) && q.Scope.Allows(entity.CanonicalID, "") {
			entities = append(entities, entity)
		}
	}
	// Components are selected only under the immutable parent UUID.
	for _, entity := range inventory.Entities {
		canonical, err := resource.ParseCanonicalID(entity.CanonicalID)
		if err == nil && canonical.StableID != id.StableID && len(canonical.StableID) > len(id.StableID) && canonical.StableID[:len(id.StableID)+1] == id.StableID+"/" && q.Scope.Allows(entity.CanonicalID, "") {
			entities = append(entities, entity)
		}
	}
	if len(entities) == 0 {
		out.Partial = true
		out.Freshness = "unavailable"
		out.Warnings = append(out.Warnings, "inventory_unknown")
		out.DegradedSources = []string{"redfish"}
		return out, nil
	}
	if len(entities) > q.Limit {
		entities = entities[:q.Limit]
		out.Partial = true
		out.Warnings = append(out.Warnings, "row_budget_exhausted")
	}
	health := "unknown"
	for _, entity := range entities {
		if entity.CanonicalID == q.ResourceCanonicalID {
			if h, ok := entity.Attributes["health"].(string); ok {
				health = h
			}
		}
	}
	data, _ := json.Marshal(map[string]any{"entities": entities, "health": health})
	if len(data) > q.ByteBudget() {
		return out, evidence.ErrBudget
	}
	if err := a.Authorize(ctx, a.Binding); err != nil {
		return evidence.Result{}, err
	}
	observed := time.Now().UTC()
	out.Evidence = []evidence.Evidence{{SourceScopeDigest: evidence.BindingScopeDigest(a.Binding), SchemaVersion: "evidence/v2", EvidenceID: uuid.Must(uuid.NewV7()).String(), TenantID: a.Binding.Tenant, ResourceCanonicalID: q.ResourceCanonicalID, Type: "hardware", DataClass: "D1", SourceRegistrationID: a.Binding.SourceID, SourceRevision: a.Binding.Revision, SourceSystem: "redfish", BackendLogicalID: a.Binding.BackendLogicalID, QueryTemplateVersion: q.Template, QueryHash: out.QueryHash, EffectiveScope: q.Scope, EvaluatedAt: observed, ObservedFrom: observed, ObservedTo: observed, SourceRetentionUntil: observed, TimeReliable: false, ReplayState: "archive_pending", ContentDigest: evidence.Digest(data), DerivationEvidenceRefs: []string{}, IndependenceGroup: a.Binding.SourceID, Data: data}}
	return out, nil
}
