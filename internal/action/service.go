package action

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	platformcrypto "ops-platform/internal/crypto"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"ops-platform/internal/policy"
	"ops-platform/internal/resource"
	"slices"
	"time"
)

type PolicyEvaluator interface {
	Evaluate(context.Context, policy.PolicyInput) (policy.PolicyDecision, error)
}
type Service struct {
	AdmitHost   func(context.Context, HostOnboarding) error
	Credentials CredentialProvider
	Pool        persistence.TxBeginner
	Protector   platformcrypto.Protector
	Policy      PolicyEvaluator
	Trust       configregistry.SignatureVerifier
	// AdmitProfile verifies admitted material, installed target permissions and
	// network/credential boundaries. Missing verification keeps publication closed.
	AdmitProfile func(context.Context, Profile) error
}

func (s Service) with(ctx context.Context, a auth.RequestContext, f func(pgx.Tx) error) error {
	if s.Pool == nil || a.Subject == "" || a.TenantID == uuid.Nil || !slices.Contains(a.Roles, auth.Operator) || !time.Now().Before(a.TokenExpiresAt) {
		return ErrDenied
	}
	return persistence.WithTenantTx(ctx, s.Pool, a.TenantID, f)
}
func profileTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, version int) (Profile, uuid.UUID, error) {
	var b []byte
	var vid uuid.UUID
	err := tx.QueryRow(ctx, `SELECT profile_version_id,content FROM action.lock_profile($1,$2,$3)`, tenant, id.String(), version).Scan(&vid, &b)
	if err != nil {
		return Profile{}, vid, err
	}
	p, err := DecodeProfile(b)
	return p, vid, err
}
func (s Service) authorize(ctx context.Context, tx pgx.Tx, a auth.RequestContext, b Binding, p Profile, touch bool) (policy.PolicyDecision, error) {
	if a.TenantID != b.TenantID || a.Subject != b.Subject || !p.Allows(b.Target, b.Options) || p.ID != b.ProfileID || p.Version != b.ProfileVersion || p.ClusterUID != b.ClusterUID || s.Policy == nil {
		return policy.PolicyDecision{}, ErrDenied
	}
	effective, err := graph.EffectiveTx(ctx, tx, a.TenantID.String(), a.Subject, b.ClusterUID, true)
	ns := ""
	if b.Namespace != nil {
		ns = *b.Namespace
	}
	if err != nil || !effective.Allows(b.Target, ns) || (p.HighPrivilege() && !effective.ClusterScoped) || (p.Type == "k8s_namespace" && p.Namespace != ns) {
		return policy.PolicyDecision{}, ErrDenied
	}
	if err := hostAdmittedTx(ctx, tx, a.TenantID, p); err != nil {
		return policy.PolicyDecision{}, err
	}
	// Inventory identity and incident ownership are server facts, never caller claims.
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT action.lock_target($1,$2,$3)`, a.TenantID, b.Target, b.ClusterUID).Scan(&locked); err != nil || !locked {
		return policy.PolicyDecision{}, ErrDenied
	}
	var raw []byte
	var namespace string
	var clusterID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT e.metadata,e.namespace,e.cluster_id FROM platform.resource_entities e JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE e.tenant_id=$1 AND e.canonical_id=$2 AND e.deleted_at IS NULL AND c.cluster_uid=$3 AND c.status='active'`, a.TenantID, b.Target, b.ClusterUID).Scan(&raw, &namespace, &clusterID)
	var identity resource.Resolution
	if err != nil || json.Unmarshal(raw, &identity) != nil || targetIdentity(identity) != b.TargetUID || namespace != ns {
		return policy.PolicyDecision{}, ErrDenied
	}
	var exists bool
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM incident.records i JOIN incident.finding_links l USING(tenant_id,incident_id) JOIN finding.records f USING(tenant_id,finding_id) WHERE i.tenant_id=$1 AND i.incident_id=$2 AND f.cluster_uid=$3)`, a.TenantID, b.IncidentID, b.ClusterUID).Scan(&exists) != nil || !exists {
		return policy.PolicyDecision{}, ErrDenied
	}
	step := false
	if touch && p.HighPrivilege() {
		if _, err = auth.TouchCurrentStepUpSession(ctx, tx, a, []string{auth.StepUpACRLevel2}); err != nil {
			return policy.PolicyDecision{}, err
		}
		step = true
	}
	scope := configregistry.Scope{Type: configregistry.ScopeCluster, ClusterID: clusterID}
	if ns != "" {
		scope.Type = configregistry.ScopeNamespace
		scope.Namespace = ns
	}
	risk := ClassifyRiskBinding(b)
	decision, err := s.Policy.Evaluate(ctx, policy.PolicyInput{Request: a, PrincipalType: policy.PrincipalOperator, RequestType: policy.RequestAction, Scope: scope, Risk: policy.Risk(risk), RiskAcknowledged: touch, ClusterLevel: p.Type == "k8s_cluster", RootLevel: p.Type == "ssh_root", StepUpVerified: step, ActualCommandDigest: b.CommandDigest, ConfirmedCommandDigest: b.CommandDigest})
	if err != nil && !(!touch && errors.Is(err, policy.ErrPolicyDenied) && decision.PolicyVersion != "") {
		return decision, err
	}
	if touch && !decision.Allow {
		return decision, ErrDenied
	}
	var policyLocked bool
	namespaces := []string{}
	if ns != "" {
		namespaces = append(namespaces, ns)
	}
	if err = tx.QueryRow(ctx, `SELECT platform.sp06_lock_policy($1,$2,$3,$4)`, a.TenantID, decision.PolicyVersion, b.ClusterUID, namespaces).Scan(&policyLocked); err != nil || !policyLocked {
		return decision, ErrDenied
	}
	if b.PolicyVersion != "" && decision.PolicyVersion != b.PolicyVersion {
		return decision, ErrConflict
	}
	return decision, nil
}

// Risk is recomputed from the immutable server assessment in prepare; the
// conservative default prevents absence of a risk record reducing authority.
func ClassifyRiskBinding(Binding) string { return "high" }
func (s Service) bind(ctx context.Context, tx pgx.Tx, a auth.RequestContext, request CommandRequest) (Binding, Profile, uuid.UUID, error) {
	if ValidateCommand(request.ActualCommand, request.Shell) != nil || request.IncidentID == uuid.Nil {
		return Binding{}, Profile{}, uuid.Nil, ErrInvalid
	}
	p, vid, err := profileTx(ctx, tx, a.TenantID, request.ProfileID, request.ProfileVersion)
	if err != nil {
		return Binding{}, p, vid, err
	}
	var raw []byte
	var ns, cluster string
	err = tx.QueryRow(ctx, `SELECT e.metadata,e.namespace,c.cluster_uid FROM platform.resource_entities e JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE e.tenant_id=$1 AND e.canonical_id=$2 AND e.deleted_at IS NULL`, a.TenantID, request.Target).Scan(&raw, &ns, &cluster)
	var identity resource.Resolution
	if err != nil || json.Unmarshal(raw, &identity) != nil || targetIdentity(identity) == "" {
		return Binding{}, p, vid, ErrDenied
	}
	var namespace *string
	if ns != "" {
		namespace = &ns
	}
	b := Binding{DigestVersion: "execution-request/v1", TenantID: a.TenantID, Subject: a.Subject, IncidentID: request.IncidentID, ActionPlanID: request.ActionPlanID, Target: request.Target, TargetUID: targetIdentity(identity), ClusterUID: cluster, Namespace: namespace, Shell: request.Shell, CommandDigest: Digest([]byte(request.ActualCommand)), ProfileID: p.ID, ProfileVersion: p.Version, Options: request.Options, RiskVersion: RiskVersion}
	if request.ActionPlanID != nil {
		var target, incident string
		err = tx.QueryRow(ctx, `SELECT content->>'targetCanonicalId',incident_id::text FROM action.plans WHERE tenant_id=$1 AND action_plan_id=$2 FOR SHARE`, a.TenantID, *request.ActionPlanID).Scan(&target, &incident)
		if err != nil || target != b.Target || incident != b.IncidentID.String() {
			return b, p, vid, ErrDenied
		}
	}
	return b, p, vid, nil
}
func (s Service) AssessRisk(ctx context.Context, a auth.RequestContext, request CommandRequest, key string) (Assessment, error) {
	var out Assessment
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		b, p, _, err := s.bind(ctx, tx, a, request)
		if err != nil {
			return err
		}
		decision, err := s.authorize(ctx, tx, a, b, p, false)
		if err != nil {
			return err
		}
		b.PolicyVersion = decision.PolicyVersion
		d, err := persistence.Begin(ctx, tx, persistence.Scope{TenantID: a.TenantID, Subject: a.Subject, Operation: "assess-command-risk"}, key, persistence.Digest(b.Digest()))
		if err != nil {
			return err
		}
		if d.Kind == persistence.DecisionReplay {
			return json.Unmarshal(d.Response.Body, &out)
		}
		if d.Kind != persistence.DecisionProceed {
			return ErrConflict
		}
		out = Assessment{ID: uuid.Must(uuid.NewV7()), Binding: b, RequestDigest: b.Digest(), Risk: Classify(request.ActualCommand)}
		out.Risk.RequiresStepUp = p.HighPrivilege() || decision.RequiresStepUp
		// The conservative OPA risk is surfaced, never hidden by a lower AST score.
		if decision.Risk == policy.RiskHigh {
			out.Risk.OperationRisk = "high"
		}
		// Evaluate the database clock once: two volatile defaults can otherwise
		// exceed the exact five-minute upper bound by a few microseconds.
		err = tx.QueryRow(ctx, `WITH expiry AS MATERIALIZED (SELECT clock_timestamp() AS created_at) INSERT INTO action.assessments(tenant_id,assessment_id,subject,binding,request_digest,risk,created_at,expires_at) SELECT $1,$2,$3,$4,$5,$6,created_at,created_at+interval '5 minutes' FROM expiry RETURNING created_at,expires_at`, a.TenantID, out.ID, a.Subject, Canonical(b), out.RequestDigest, Canonical(out.Risk)).Scan(&out.CreatedAt, &out.ExpiresAt)
		if err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, a, out.ID, "command.risk_assessed", map[string]any{"requestDigest": out.RequestDigest}); err != nil {
			return err
		}
		return complete(ctx, tx, d, out, 201)
	})
	return out, err
}
func assessmentTx(ctx context.Context, tx pgx.Tx, a auth.RequestContext, id uuid.UUID) (Assessment, error) {
	var out Assessment
	var b, r []byte
	out.ID = id
	err := tx.QueryRow(ctx, `SELECT binding,request_digest,risk,created_at,expires_at FROM action.assessments WHERE tenant_id=$1 AND assessment_id=$2 AND subject=$3`, a.TenantID, id, a.Subject).Scan(&b, &out.RequestDigest, &r, &out.CreatedAt, &out.ExpiresAt)
	if err != nil {
		return out, err
	}
	if json.Unmarshal(b, &out.Binding) != nil || json.Unmarshal(r, &out.Risk) != nil {
		return out, ErrInvalid
	}
	return out, nil
}
func (s Service) AcknowledgeRisk(ctx context.Context, a auth.RequestContext, id uuid.UUID, digest, key string) (Acknowledgement, error) {
	var out Acknowledgement
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		assessment, err := assessmentTx(ctx, tx, a, id)
		if err != nil {
			return err
		}
		if digest != assessment.RequestDigest {
			return ErrAcknowledgement
		}
		p, _, err := profileTx(ctx, tx, a.TenantID, assessment.Binding.ProfileID, assessment.Binding.ProfileVersion)
		if err != nil {
			return err
		}
		if _, err = s.authorize(ctx, tx, a, assessment.Binding, p, true); err != nil {
			return err
		}
		d, err := persistence.Begin(ctx, tx, persistence.Scope{TenantID: a.TenantID, Subject: a.Subject, Operation: "acknowledge-command-risk"}, key, persistence.Digest(Digest(Canonical([]any{id, digest}))))
		if err != nil {
			return err
		}
		if d.Kind == persistence.DecisionReplay {
			return json.Unmarshal(d.Response.Body, &out)
		}
		if d.Kind != persistence.DecisionProceed {
			return ErrConflict
		}
		out = Acknowledgement{ID: uuid.Must(uuid.NewV7()), AssessmentID: id, RequestDigest: digest}
		err = tx.QueryRow(ctx, `INSERT INTO action.risk_acknowledgements(tenant_id,acknowledgement_id,subject,target_canonical_id,shell,command_digest,risk_level,policy_version_id,confirmed_at,expires_at,assessment_id,binding,request_digest) SELECT $1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp(),clock_timestamp()+interval '5 minutes',$9,$10,$11 WHERE EXISTS(SELECT 1 FROM action.assessments WHERE tenant_id=$1 AND assessment_id=$9 AND expires_at>clock_timestamp()) RETURNING expires_at`, a.TenantID, out.ID, a.Subject, assessment.Binding.Target, assessment.Binding.Shell, assessment.Binding.CommandDigest, assessment.Risk.OperationRisk, assessment.Binding.PolicyVersion, id, Canonical(assessment.Binding), digest).Scan(&out.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAcknowledgement
		}
		if err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, a, out.ID, "command.risk_acknowledged", map[string]any{"assessmentId": id, "requestDigest": digest}); err != nil {
			return err
		}
		return complete(ctx, tx, d, out, 201)
	})
	return out, err
}
func (s Service) PrepareCommandExecution(ctx context.Context, a auth.RequestContext, request CommandRequest, ackID uuid.UUID, key string) (Execution, error) {
	var out Execution
	err := s.with(ctx, a, func(tx pgx.Tx) error {
		if s.Protector == nil {
			return ErrDenied
		}
		var ackBinding []byte
		var assessmentID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT binding,assessment_id FROM action.risk_acknowledgements WHERE tenant_id=$1 AND acknowledgement_id=$2 AND subject=$3 FOR UPDATE`, a.TenantID, ackID, a.Subject).Scan(&ackBinding, &assessmentID)
		if err != nil {
			return ErrAcknowledgement
		}
		var confirmed Binding
		if json.Unmarshal(ackBinding, &confirmed) != nil {
			return ErrAcknowledgement
		}
		if request.OmitIncident {
			request.IncidentID = confirmed.IncidentID
		}
		if request.OmitVersion {
			request.ProfileVersion = confirmed.ProfileVersion
		}
		if request.OmitOptions {
			request.Options = confirmed.Options
		}
		b, p, vid, err := s.bind(ctx, tx, a, request)
		if err != nil {
			return err
		}
		b.PolicyVersion = confirmed.PolicyVersion
		if b.Digest() != confirmed.Digest() {
			return ErrAcknowledgement
		}
		decision, err := s.authorize(ctx, tx, a, b, p, true)
		if err != nil {
			return err
		}
		d, err := persistence.Begin(ctx, tx, persistence.Scope{TenantID: a.TenantID, Subject: a.Subject, Operation: "prepare-command-execution", NoRedispatch: true}, key, persistence.Digest(Digest(Canonical(struct {
			Binding         string    `json:"bindingDigest"`
			Acknowledgement uuid.UUID `json:"acknowledgementId"`
		}{b.Digest(), ackID}))))
		if err != nil {
			return err
		}
		if d.Kind == persistence.DecisionReplay {
			return json.Unmarshal(d.Response.Body, &out)
		}
		if d.Kind != persistence.DecisionProceed {
			return ErrConflict
		}
		tag, err := tx.Exec(ctx, `UPDATE action.risk_acknowledgements SET consumed_at=clock_timestamp() WHERE tenant_id=$1 AND acknowledgement_id=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp() AND request_digest=$3`, a.TenantID, ackID, b.Digest())
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrAcknowledgement
		}
		out = Execution{Iteration: 1, AckID: ackID, PolicyID: decision.DecisionID, ID: uuid.Must(uuid.NewV7()), Binding: b, RequestDigest: b.Digest(), State: "prepared", SuggestionMatch: "not_applicable", ComparatorVersion: ComparatorVersion, PostCheck: "inconclusive"}
		if b.ActionPlanID != nil {
			var suggestion string
			if err = tx.QueryRow(ctx, `SELECT content->>'suggestedCommand',(content->>'iteration')::int FROM action.plans WHERE tenant_id=$1 AND action_plan_id=$2`, a.TenantID, *b.ActionPlanID).Scan(&suggestion, &out.Iteration); err != nil {
				return err
			}
			out.SuggestionMatch = SuggestionMatch(&suggestion, request.ActualCommand)
			digest := Digest([]byte(suggestion))
			out.SuggestedDigest = &digest
		}
		envelope, err := s.Protector.Seal(ctx, a.TenantID, out.ID, []byte(request.ActualCommand))
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO action.executions(tenant_id,execution_id,subject,target_canonical_id,shell,command_digest,risk_acknowledgement_id,execution_profile_version_id,idempotency_key_digest,state,binding,request_digest,command_envelope,suggestion_match,comparator_version,policy_decision_id,actor_context,dispatch_deadline) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'prepared',$10,$11,$12,$13,$14,$15,$16,LEAST($17,(SELECT expires_at FROM action.risk_acknowledgements WHERE tenant_id=$1 AND acknowledgement_id=$7))) RETURNING created_at`, a.TenantID, out.ID, a.Subject, b.Target, b.Shell, b.CommandDigest, ackID, vid, persistence.HashIdempotencyKey(key), Canonical(b), b.Digest(), Canonical(envelope), out.SuggestionMatch, ComparatorVersion, decision.DecisionID, Canonical(a), a.TokenExpiresAt).Scan(&out.CreatedAt)
		if err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, a, out.ID, "command.prepared", map[string]any{"requestDigest": b.Digest(), "commandDigest": b.CommandDigest, "ciphertextRef": out.ID, "acknowledgementId": ackID, "suggestionMatch": out.SuggestionMatch, "comparatorVersion": ComparatorVersion, "policyDecisionId": decision.DecisionID}); err != nil {
			return err
		}
		if err = event(ctx, tx, a.TenantID, out.ID, "state", map[string]any{"state": "prepared"}); err != nil {
			return err
		}
		return complete(ctx, tx, d, out, 202)
	})
	return out, err
}
func complete(ctx context.Context, tx pgx.Tx, d persistence.Decision, v any, status int) error {
	return persistence.Complete(ctx, tx, d.Lease, persistence.StoredResponse{Status: status, ContentType: "application/json", Body: Canonical(v)})
}
func appendAudit(ctx context.Context, tx pgx.Tx, a auth.RequestContext, id uuid.UUID, kind string, payload map[string]any) error {
	_, err := audit.Append(ctx, tx, audit.Entry{TenantID: a.TenantID, RecordID: uuid.Must(uuid.NewV7()), EntityID: id, EntityKind: "command", Subject: a.Subject, EventType: kind, Payload: payload})
	return err
}
func event(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, kind string, payload any) error {
	var seq int64
	if err := tx.QueryRow(ctx, `UPDATE action.executions SET event_seq=event_seq+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2 RETURNING event_seq`, tenant, id).Scan(&seq); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO action.events(tenant_id,execution_id,event_seq,event_type,payload) VALUES($1,$2,$3,$4,$5)`, tenant, id, seq, kind, Canonical(payload))
	return err
}

func targetIdentity(r resource.Resolution) string {
	if r.SourceValues["uid"] != "" {
		return r.SourceValues["uid"]
	}
	return resource.NormalizeHardwareUUID(r.SourceValues["uuid"])
}
