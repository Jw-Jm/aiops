package investigation

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
)

type ResourceFieldChange struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}
type ResourceChange struct {
	Values                []ResourceFieldChange `json:"values"`
	ObservationID         uuid.UUID             `json:"observationId"`
	PreviousObservationID uuid.UUID             `json:"previousObservationId"`
	CanonicalID           string                `json:"canonicalId"`
	FromVersion           string                `json:"fromVersion"`
	ToVersion             string                `json:"toVersion"`
	ObservedAt            time.Time             `json:"observedAt"`
	Kind                  string                `json:"kind"`
	Fields                []string              `json:"fields"`
	TimeReliable          bool                  `json:"timeReliable"`
}

type ResourceChanges struct {
	Sources         []graph.SourceAuthority  `json:"sources"`
	CanonicalID     string                   `json:"canonicalId"`
	Window          analytics.QueryTimeRange `json:"window"`
	Changes         []ResourceChange         `json:"changes"`
	Executions      []map[string]any         `json:"executions"`
	MissingEvidence []string                 `json:"missingEvidence"`
	Truncated       bool                     `json:"truncated"`
}

// Only compare actual allowlisted fields. resourceVersion is an opaque version,
// observedAt is an observation clock, neither proves a deployment/event time.
func CompareResourceObservations(previous, current ResourceObservation) ResourceChange {
	out := ResourceChange{ObservationID: current.ObservationID, PreviousObservationID: previous.ObservationID, CanonicalID: current.CanonicalID, FromVersion: previous.ResourceVersion, ToVersion: current.ResourceVersion, ObservedAt: current.ObservedAt, Kind: "object_version_only", Fields: []string{}, Values: []ResourceFieldChange{}, TimeReliable: false}
	if previous.CanonicalID != current.CanonicalID || previous.Source != current.Source || !previous.ObservedAt.Before(current.ObservedAt) || current.Coverage != "continuous_watch" {
		out.Kind = "history_gap"
		return out
	}
	for _, path := range [][]string{{"spec", "containers"}, {"spec", "initContainers"}, {"spec", "template", "spec", "containers"}, {"spec", "template", "spec", "initContainers"}, {"spec", "nodeName"}, {"spec", "volumeName"}, {"spec", "storageClassName"}, {"spec", "replicas"}, {"spec", "unschedulable"}, {"spec", "volumes"}, {"spec", "claimRef"}, {"spec", "csi", "driver"}, {"owners"}} {
		before, _, _ := unstructured.NestedFieldCopy(previous.Projection, path...)
		after, _, _ := unstructured.NestedFieldCopy(current.Projection, path...)
		if !reflect.DeepEqual(before, after) {
			key := strings.Join(path, ".")
			out.Fields = append(out.Fields, key)
			out.Values = append(out.Values, ResourceFieldChange{key, before, after})
		}
	}
	if len(out.Fields) > 0 {
		out.Kind = "observed_field_change"
	}
	return out
}

func ReadResourceChangesTx(ctx context.Context, tx pgx.Tx, j Job, id string, w analytics.QueryTimeRange) (ResourceChanges, error) {
	out := ResourceChanges{CanonicalID: id, Window: w, Changes: []ResourceChange{}, Executions: []map[string]any{}, MissingEvidence: []string{"change_event_time_unverified", "execution_success_not_target_change"}}
	history, err := ReadResourceHistoryTx(ctx, tx, j, id, w)
	if err != nil {
		return out, err
	}
	out.Truncated = history.Truncated
	out.Sources = []graph.SourceAuthority{}
	for _, o := range history.Records {
		if !slices.Contains(out.Sources, o.Source) {
			out.Sources = append(out.Sources, o.Source)
		}
	}
	var prior ResourceObservation
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT observation_id,canonical_id,namespace,source_id::text,source_revision,source_scope_digest,resource_version,observed_at,coverage,projection FROM platform.resource_observations WHERE tenant_id=$1 AND canonical_id=$2 AND observed_at<$3 ORDER BY observed_at DESC,observation_id LIMIT 1`, j.TenantID, id, w.From).Scan(&prior.ObservationID, &prior.CanonicalID, &prior.Namespace, &prior.Source.SourceRegistrationID, &prior.Source.Revision, &prior.Source.ScopeDigest, &prior.ResourceVersion, &prior.ObservedAt, &prior.Coverage, &raw)
	if err != nil && err != pgx.ErrNoRows {
		return out, err
	}
	if err == nil {
		if !slices.Contains(out.Sources, prior.Source) {
			out.Sources = append(out.Sources, prior.Source)
		}
		if CheckObservationSourceTx(ctx, tx, j, prior) != nil || json.Unmarshal(raw, &prior.Projection) != nil {
			return out, ErrDenied
		}
	}
	for _, o := range history.Records {
		if prior.ObservationID == uuid.Nil {
			out.MissingEvidence = append(out.MissingEvidence, "previous_object_observation_missing")
		} else {
			var gap bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.resource_observation_coverage WHERE tenant_id=$1 AND source_id::text=$2 AND source_revision=$3 AND observed_at >= $4 AND observed_at <= $5 AND (has_error OR NOT watch_connected OR NOT watch_continuous))`, j.TenantID, o.Source.SourceRegistrationID, o.Source.Revision, prior.ObservedAt, o.ObservedAt).Scan(&gap); err != nil {
				return out, err
			}
			current := o
			if gap {
				current.Coverage = "coverage_gap"
			}
			out.Changes = append(out.Changes, CompareResourceObservations(prior, current))
		}
		prior = o
	}
	rows, err := tx.Query(ctx, `SELECT execution_id,state,created_at,completed_at,post_checked_at,post_check FROM action.executions WHERE tenant_id=$1 AND target_canonical_id=$2 AND created_at >= $3 AND created_at < $4 ORDER BY created_at,execution_id LIMIT 51`, j.TenantID, id, w.From, w.To)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(out.Executions) == 50 {
			out.Truncated = true
			break
		}
		var execution uuid.UUID
		var state, post string
		var created time.Time
		var completed, checked *time.Time
		if err := rows.Scan(&execution, &state, &created, &completed, &checked, &post); err != nil {
			return out, err
		}
		out.Executions = append(out.Executions, map[string]any{"executionId": execution, "state": state, "createdAt": created, "completedAt": completed, "postCheckedAt": checked, "postCheck": post, "targetChange": "unverified"})
	}
	return out, rows.Err()
}
