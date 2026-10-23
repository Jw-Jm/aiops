package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
)

// NativeCommandStepUpTarget binds the actual command bytes through the server's
// immutable risk assessment digest, its policy/profile versions and target UID.
func NativeCommandStepUpTarget(b Binding, assessmentID uuid.UUID) map[string]any {
	return map[string]any{"kind": "k8s_object", "canonicalId": b.Target, "uid": b.TargetUID, "clusterUid": b.ClusterUID, "namespace": b.Namespace, "revision": b.TargetRevision, "command": map[string]any{"assessmentId": assessmentID, "riskAssessmentVersion": b.RiskVersion, "executionRequestDigest": b.Digest(), "policyVersion": b.PolicyVersion, "incidentId": b.IncidentID, "executionProfileId": b.ProfileID, "executionProfileVersion": b.ProfileVersion, "actualCommandDigest": b.CommandDigest}}
}

func ResolveNativeCommandStepUp(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, target, command json.RawMessage) (map[string]any, error) {
	var reference struct {
		AssessmentID uuid.UUID `json:"assessmentId"`
	}
	if json.Unmarshal(command, &reference) != nil || reference.AssessmentID == uuid.Nil {
		return nil, ErrInvalid
	}
	assessment, err := assessmentTx(ctx, tx, actor, reference.AssessmentID)
	if err != nil {
		return nil, err
	}
	if !time.Now().Before(assessment.ExpiresAt) || assessment.Binding.TargetRevision < 1 {
		return nil, ErrConflict
	}
	b := assessment.Binding
	scope, err := graph.EffectiveTx(ctx, tx, actor.TenantID.String(), actor.Subject, b.ClusterUID, true)
	namespace := ""
	if b.Namespace != nil {
		namespace = *b.Namespace
	}
	if err != nil || !scope.Allows(b.Target, namespace) {
		return nil, ErrDenied
	}
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT action.lock_target($1,$2,$3)`, actor.TenantID, b.Target, b.ClusterUID).Scan(&locked); err != nil {
		return nil, err
	}
	if !locked {
		return nil, ErrDenied
	}
	var revision int
	if err := tx.QueryRow(ctx, `SELECT resource_revision FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, actor.TenantID, b.Target).Scan(&revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrConflict
		}
		return nil, err
	}
	if revision != b.TargetRevision {
		return nil, ErrConflict
	}
	expected := NativeCommandStepUpTarget(b, reference.AssessmentID)
	var provided map[string]any
	if json.Unmarshal(target, &provided) != nil || provided == nil {
		return nil, ErrInvalid
	}
	var binding any
	if json.Unmarshal(command, &binding) != nil {
		return nil, ErrInvalid
	}
	provided["command"] = binding
	if !bytes.Equal(Canonical(provided), Canonical(expected)) {
		return nil, ErrAckScopeMismatch
	}
	return expected, nil
}
