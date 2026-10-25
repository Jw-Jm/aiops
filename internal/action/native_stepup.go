package action

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/identity"
)

// CommandStepUpOperation is the only operation a command step-up proof may be
// minted for. A proof for another operation is never accepted by the command
// boundary and is never re-proved as one.
const CommandStepUpOperation = "command_execution"

// commandStepUpContext binds the exact server-derived command target to the native
// challenge the browser verified. Acknowledge and prepare must derive the same
// intent: the client supplies only the challenge reference, never the target.
func commandStepUpContext(ctx context.Context, assessmentID uuid.UUID, b Binding) context.Context {
	native, ok := auth.NativeSessionFromContext(ctx)
	if !ok {
		return ctx
	}
	target, _ := json.Marshal(NativeCommandStepUpTarget(b, assessmentID))
	return auth.WithNativeStepUpIntent(ctx, auth.NativeStepUpIntent{ChallengeID: native.StepUpChallengeID, Operation: CommandStepUpOperation, Target: target})
}

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

// reproveFrozenStepUp re-proves, in the caller's transaction, the native step-up
// proof a high-privilege execution was prepared with. The Worker has no session and
// a native account never has a Keycloak SID/ACR, so the proof is re-checked from
// the persisted reference: same account/session/operation, the exact frozen binding
// digest, an unrevoked and unexpired proof, and the account/session still at the
// versions and authorization version the proof was issued for.
func (s Service) reproveFrozenStepUp(ctx context.Context, tx pgx.Tx, e Execution) (auth.NativeProof, error) {
	f := e.stepUp
	if f == nil || f.Operation != CommandStepUpOperation || f.SessionID == uuid.Nil || f.ProofID == uuid.Nil ||
		len(f.BindingDigest) != sha256.Size || f.ExpiresAt.IsZero() || !time.Now().Before(f.ExpiresAt) {
		return auth.NativeProof{}, ErrDenied
	}
	native, err := e.Authority.nativeForRecheck()
	if err != nil || !native {
		return auth.NativeProof{}, ErrDenied
	}
	var credentialVersion, sessionVersion, authorizationVersion int
	if err = tx.QueryRow(ctx, `SELECT out_issued_credential_version,out_issued_session_version,out_issued_authorization_version FROM identity.native_command_proof_recheck($1,$2,$3,$4,$5,$6,$7)`,
		e.Binding.TenantID, e.Authority.UserID, f.SessionID, f.ProofID, f.Operation, f.BindingDigest, e.Authority.AuthorizationVersion).Scan(&credentialVersion, &sessionVersion, &authorizationVersion); err != nil {
		return auth.NativeProof{}, ErrDenied
	}
	// The frozen digest must be the digest of this exact command under the proof's
	// own session and versions. A proof of another command, session or version can
	// therefore never be presented for this execution.
	assessmentID, err := assessmentIDTx(ctx, tx, e)
	if err != nil {
		return auth.NativeProof{}, ErrDenied
	}
	target, err := json.Marshal(NativeCommandStepUpTarget(e.Binding, assessmentID))
	if err != nil {
		return auth.NativeProof{}, ErrDenied
	}
	want := identity.StepUpBindingDigest(identity.SessionRef(f.SessionID), f.Operation, target, sessionVersion, credentialVersion, authorizationVersion)
	if !identity.ConstantTimeDigest(f.BindingDigest, want) {
		return auth.NativeProof{}, ErrDenied
	}
	return auth.NativeProof{SessionID: f.SessionID, Ref: f.ProofID, Operation: f.Operation, BindingDigest: f.BindingDigest, ExpiresAt: f.ExpiresAt}, nil
}

// assessmentIDTx reads the assessment the acknowledgement was issued for. The
// proof binding names that assessment, so the re-derivation above is exact.
func assessmentIDTx(ctx context.Context, tx pgx.Tx, e Execution) (uuid.UUID, error) {
	var assessmentID uuid.UUID
	if e.AckID == uuid.Nil {
		return uuid.Nil, ErrDenied
	}
	if err := tx.QueryRow(ctx, `SELECT assessment_id FROM action.risk_acknowledgements WHERE tenant_id=$1 AND acknowledgement_id=$2`, e.Binding.TenantID, e.AckID).Scan(&assessmentID); err != nil {
		return uuid.Nil, err
	}
	if assessmentID == uuid.Nil {
		return uuid.Nil, ErrDenied
	}
	return assessmentID, nil
}

// verifiedStepUpProof accepts only a re-proved proof for the command operation
// that is still inside its own expiry. A marker without a complete binding
// digest or for another operation is refused, so a proof can never be reused
// across operations or after its window closed.
func verifiedStepUpProof(proof auth.NativeProof, now time.Time) error {
	if proof.Operation != CommandStepUpOperation || len(proof.BindingDigest) != sha256.Size {
		return auth.ErrStepUpInvalid
	}
	if proof.ExpiresAt.IsZero() || !now.Before(proof.ExpiresAt) {
		return auth.ErrStepUpInvalid
	}
	return nil
}
