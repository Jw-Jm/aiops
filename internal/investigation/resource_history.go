package investigation

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
)

type ResourceObservation struct {
	ObservationID   uuid.UUID             `json:"observationId"`
	CanonicalID     string                `json:"canonicalId"`
	Namespace       string                `json:"namespace"`
	Source          graph.SourceAuthority `json:"source"`
	ResourceVersion string                `json:"resourceVersion"`
	ObservedAt      time.Time             `json:"observedAt"`
	EventAt         *time.Time            `json:"eventAt,omitempty"`
	Coverage        string                `json:"coverage"`
	Projection      map[string]any        `json:"projection"`
}

type ResourceCoverage struct {
	SourceID        string     `json:"sourceId"`
	Revision        int64      `json:"revision"`
	GVR             string     `json:"gvr"`
	ObservedAt      time.Time  `json:"observedAt"`
	LastListAt      *time.Time `json:"lastListAt"`
	WatchConnected  bool       `json:"watchConnected"`
	WatchContinuous bool       `json:"watchContinuous"`
	HasError        bool       `json:"hasError"`
}
type ResourceHistory struct {
	CoverageTransitions []ResourceCoverage       `json:"coverageTransitions"`
	Relations           []resource.Relation      `json:"relations"`
	CanonicalID         string                   `json:"canonicalId"`
	Window              analytics.QueryTimeRange `json:"window"`
	Records             []ResourceObservation    `json:"records"`
	Events              []ResourceObservation    `json:"events"`
	MissingEvidence     []string                 `json:"missingEvidence"`
	Truncated           bool                     `json:"truncated"`
}

// Sources are checked on every read, including committed history reads. A
// revision/namespace change cannot silently relabel an earlier observation.
func CheckObservationSourceTx(ctx context.Context, tx pgx.Tx, j Job, o ResourceObservation) error {
	if !j.Scope.Allows(o.CanonicalID, o.Namespace) || !slices.Contains(j.Budget.AllowedDataClasses, "D0") {
		return ErrDenied
	}
	var valid bool
	if tx.QueryRow(ctx, `SELECT platform.sp05_lock_evidence_source($1,$2,$3)`, j.TenantID, o.Source.SourceRegistrationID, o.Source.Revision).Scan(&valid) != nil || !valid {
		return ErrDenied
	}
	var raw []byte
	var typ, backend string
	if tx.QueryRow(ctx, `SELECT source_type,backend_logical_id,data_scope_mapping FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND revision=$3 AND status='active'`, j.TenantID, o.Source.SourceRegistrationID, o.Source.Revision).Scan(&typ, &backend, &raw) != nil {
		return ErrDenied
	}
	var mapping datascope.Mapping
	if json.Unmarshal(raw, &mapping) != nil {
		return ErrDenied
	}
	b := evidence.Binding{Tenant: j.TenantID.String(), SourceID: o.Source.SourceRegistrationID, SourceType: typ, BackendLogicalID: backend, Revision: o.Source.Revision, ScopeMapping: mapping}
	if evidence.BindingScopeDigest(b) != o.Source.ScopeDigest {
		return ErrDenied
	}
	return nil
}

func ReadResourceHistoryTx(ctx context.Context, tx pgx.Tx, j Job, id string, w analytics.QueryTimeRange) (ResourceHistory, error) {
	out := ResourceHistory{CanonicalID: id, Window: w, CoverageTransitions: []ResourceCoverage{}, Relations: []resource.Relation{}, Records: []ResourceObservation{}, Events: []ResourceObservation{}, MissingEvidence: []string{"history_observations_only_not_complete_coverage"}}
	var namespace string
	if CheckAssistantJobTx(ctx, tx, j) != nil || tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, id).Scan(&namespace) != nil || !j.Scope.Allows(id, namespace) {
		return out, ErrDenied
	}
	// Event attribution uses the immutable involved UID, never just a name. Event
	// clocks remain separate from ingestion; records without a clock stay gaps.
	rows, err := tx.Query(ctx, `SELECT observation_id,canonical_id,namespace,source_id::text,source_revision,source_scope_digest,resource_version,observed_at,event_at,coverage,projection FROM platform.resource_observations WHERE tenant_id=$1 AND ((canonical_id=$2 AND observed_at >= $3 AND observed_at < $4) OR ((projection->'involvedObject'->>'uid'=$5 OR projection->'regarding'->>'uid'=$5) AND namespace=$6 AND event_at >= $3 AND event_at < $4)) ORDER BY observed_at,observation_id LIMIT 101`, j.TenantID, id, w.From, w.To, canonicalUID(id), namespace)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	items := []ResourceObservation{}
	bytes := 0
	for rows.Next() {
		var o ResourceObservation
		var raw []byte
		if err := rows.Scan(&o.ObservationID, &o.CanonicalID, &o.Namespace, &o.Source.SourceRegistrationID, &o.Source.Revision, &o.Source.ScopeDigest, &o.ResourceVersion, &o.ObservedAt, &o.EventAt, &o.Coverage, &raw); err != nil {
			return out, err
		}
		// Events need not be selected as investigation targets. Only the attributed
		// UID is disclosed; the event source and original namespace remain checked.
		if len(items) == 100 || bytes+len(raw) > 40<<10 {
			out.Truncated = true
			break
		}
		if json.Unmarshal(raw, &o.Projection) != nil {
			return out, ErrInvalid
		}
		bytes += len(raw)
		items = append(items, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, o := range items {
		check := o
		check.CanonicalID = id
		if err := CheckObservationSourceTx(ctx, tx, j, check); err != nil {
			return out, err
		}
		if o.CanonicalID == id {
			var paths []resource.Relation
			raw, _ := json.Marshal(o.Projection["relations"])
			if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &paths) != nil {
				return out, ErrInvalid
			}
			safe := []resource.Relation{}
			for _, r := range paths {
				var ns string
				if r.From != id || r.Provenance.SourceRegistrationID != o.Source.SourceRegistrationID {
					continue
				}
				if tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, r.To).Scan(&ns) != nil || !j.Scope.Allows(r.To, ns) {
					continue
				}
				safe = append(safe, r)
				out.Relations = append(out.Relations, r)
			}
			o.Projection["relations"] = safe
			out.Records = append(out.Records, o)
		} else {
			out.Events = append(out.Events, o)
		}
	}
	sources := []string{}
	for _, o := range items {
		if !slices.Contains(sources, o.Source.SourceRegistrationID) {
			sources = append(sources, o.Source.SourceRegistrationID)
		}
	}
	if len(sources) > 0 {
		rows, err := tx.Query(ctx, `SELECT source_id::text,source_revision,gvr,observed_at,last_list_at,watch_connected,watch_continuous,has_error FROM platform.resource_observation_coverage WHERE tenant_id=$1 AND source_id::text=ANY($2) AND observed_at >= $3 AND observed_at < $4 ORDER BY observed_at,gvr LIMIT 101`, j.TenantID, sources, w.From, w.To)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var c ResourceCoverage
			if err := rows.Scan(&c.SourceID, &c.Revision, &c.GVR, &c.ObservedAt, &c.LastListAt, &c.WatchConnected, &c.WatchContinuous, &c.HasError); err != nil {
				rows.Close()
				return out, err
			}
			if len(out.CoverageTransitions) == 100 {
				out.Truncated = true
				break
			}
			out.CoverageTransitions = append(out.CoverageTransitions, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	if len(out.Records) == 0 {
		out.MissingEvidence = append(out.MissingEvidence, "resource_history_missing")
	}
	if len(out.Events) == 0 {
		out.MissingEvidence = append(out.MissingEvidence, "native_event_history_missing")
	}
	if out.Truncated {
		out.MissingEvidence = append(out.MissingEvidence, "history_limit_reached")
	}
	return out, nil
}

func canonicalUID(id string) string { p, _ := resource.ParseCanonicalID(id); return p.StableID }

func authorizeResourceToolTx(ctx context.Context, tx pgx.Tx, j Job, tool string, data json.RawMessage) error {
	switch tool {
	case "get_dependencies", "get_impact_scope", "get_resource_context", "get_resource_health":
		var snapshot graph.Result
		if json.Unmarshal(data, &snapshot) != nil {
			return ErrInvalid
		}
		if len(snapshot.ObservationSources) > 0 && rca.CheckGraphSources(ctx, tx, j.TenantID, snapshot.ObservationSources, snapshot) != nil {
			return ErrDenied
		}
		for _, node := range snapshot.Nodes {
			if !j.Scope.Allows(node.CanonicalID, node.Namespace) {
				return ErrDenied
			}
		}
	case "resource_history":
		var h ResourceHistory
		if json.Unmarshal(data, &h) != nil {
			return ErrInvalid
		}
		for _, o := range append(h.Records, h.Events...) {
			o.CanonicalID = h.CanonicalID
			if err := CheckObservationSourceTx(ctx, tx, j, o); err != nil {
				return err
			}
		}
		for _, r := range h.Relations {
			for _, id := range []string{r.From, r.To} {
				var ns string
				if tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, id).Scan(&ns) != nil || !j.Scope.Allows(id, ns) {
					return ErrDenied
				}
			}
		}
	case "resource_changes", "resource_findings":
		var d struct {
			CanonicalID string                  `json:"canonicalId"`
			Sources     []graph.SourceAuthority `json:"sources"`
		}
		if json.Unmarshal(data, &d) != nil {
			return ErrInvalid
		}
		var ns string
		if tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, d.CanonicalID).Scan(&ns) != nil || !j.Scope.Allows(d.CanonicalID, ns) {
			return ErrDenied
		}
		for _, s := range d.Sources {
			if err := CheckObservationSourceTx(ctx, tx, j, ResourceObservation{CanonicalID: d.CanonicalID, Namespace: ns, Source: s}); err != nil {
				return err
			}
		}
	}
	return nil
}
