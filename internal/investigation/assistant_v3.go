package investigation

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/authz"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"slices"
	"sort"
)

// Authorization sources for audit-domain work. The source is persisted with the
// job, so a Worker recheck can never silently downgrade a native grant to the
// legacy binding table (or the reverse).
const (
	AuditSourceLegacy = "legacy"
	AuditSourceNative = "native"
)

type AssistantTask struct {
	ExecutionMode           string      `json:"executionMode,omitempty"`
	FaultChainRequestDigest string      `json:"faultChainRequestDigest,omitempty"`
	TaskKind                string      `json:"taskKind"`
	AuthorizationDomain     string      `json:"authorizationDomain"`
	ConversationID          uuid.UUID   `json:"conversationId"`
	TurnID                  uuid.UUID   `json:"turnId"`
	InputDigest             string      `json:"inputDigest"`
	ContextDigest           string      `json:"contextDigest"`
	GrantSnapshot           graph.Scope `json:"grantSnapshot"`
	GrantSnapshotDigest     string      `json:"grantSnapshotDigest"`
	DatasetCatalogVersion   string      `json:"datasetCatalogVersion"`
	DatasetCatalogDigest    string      `json:"datasetCatalogDigest"`
	CatalogVersion          string      `json:"catalogVersion"`
	PromptVersion           string      `json:"promptVersion"`
	ModelProfileVersion     string      `json:"modelProfileVersion"`
	ResultSchemaVersion     string      `json:"resultSchemaVersion"`
	// AuthorizationSource records which identity system proved this job's audit
	// authority. Empty is only valid for rows written before this field existed;
	// those rows were necessarily legacy, because the native path rejected them
	// outright. New jobs always persist an explicit source.
	AuthorizationSource  string    `json:"authorizationSource,omitempty"`
	AuthorizationUserID  uuid.UUID `json:"authorizationUserId,omitempty"`
	AuthorizationVersion int       `json:"authorizationVersion,omitempty"`
}

func (a *AssistantTask) Valid(j Job) bool {
	if a == nil || a.ConversationID == uuid.Nil || a.TurnID == uuid.Nil || a.InputDigest == "" || a.ContextDigest == "" || !slices.Contains([]string{"assistant-tools/v1", "assistant-tools/v2"}, a.CatalogVersion) || a.PromptVersion != "assistant/v1" || a.ModelProfileVersion == "" || a.DatasetCatalogVersion != "analysis-catalog/v1" || a.DatasetCatalogDigest == "" || a.ResultSchemaVersion != "assistant-answer/v1" || a.GrantSnapshotDigest != graph.ScopeDigest(a.GrantSnapshot) {
		return false
	}
	if (a.ExecutionMode != "" || a.FaultChainRequestDigest != "") && (a.ExecutionMode != "evidence_only" || a.FaultChainRequestDigest == "" || a.AuthorizationDomain != "operational" || !slices.Contains([]string{"resource_troubleshooting", "incident_followup"}, a.TaskKind)) {
		return false
	}
	if a.AuthorizationDomain == "audit" {
		if !a.validAuthority() {
			return false
		}
		return a.TaskKind == "data_query" && j.IncidentID == uuid.Nil && j.TriggerRevision == 0 && j.Scope.Cluster == "" && a.GrantSnapshot.Tenant == j.TenantID.String() && graph.ScopeDigest(j.Scope) == a.GrantSnapshotDigest && !j.Scope.ClusterScoped && len(j.Scope.Resources) == 0 && len(j.Scope.Namespaces) == 0
	}
	if a.AuthorizationDomain != "operational" || !a.validAuthority() || !ScopeSubset(a.GrantSnapshot, j.Scope) {
		return false
	}
	return (a.TaskKind == "incident_followup" && j.IncidentID != uuid.Nil && j.TriggerRevision > 0) || (slices.Contains([]string{"data_query", "resource_troubleshooting"}, a.TaskKind) && j.IncidentID == uuid.Nil && j.TriggerRevision == 0)
}
func ScopeSubset(grant, scope graph.Scope) bool { return graph.ContainsScope(grant, scope) }

// HistoricalRead binds the frozen owner of a business row with the identity
// reading it now. It is built only after an explicit, reviewed subject mapping
// was proven in the same transaction. It never rewrites the frozen subject,
// grant snapshot or digest of the historical row.
type HistoricalRead struct {
	OwnerSubject   string
	CurrentSubject string
	Mapped         bool
	MappingID      uuid.UUID
	EvidenceDigest string
}

// ScopeCoversMappedOwner compares a current native grant with an execution
// scope frozen under another identity source. The authorization revision is
// not comparable across identity systems, so it cannot be part of the
// comparison; tenant, cluster, cluster-scoped flag, namespaces and resources
// must still be contained. The frozen snapshot itself remains unchanged and is
// still validated against the frozen job by AssistantTask.Valid.
func ScopeCoversMappedOwner(grant, scope graph.Scope) bool {
	if grant.Tenant != scope.Tenant || grant.Cluster != scope.Cluster || (scope.ClusterScoped && !grant.ClusterScoped) {
		return false
	}
	for _, namespace := range scope.Namespaces {
		if !slices.Contains(grant.Namespaces, namespace) {
			return false
		}
	}
	if len(grant.Resources) > 0 {
		if len(scope.Resources) == 0 {
			return false
		}
		for _, id := range scope.Resources {
			if !slices.Contains(grant.Resources, id) {
				return false
			}
		}
	}
	return true
}

// validAuthority rejects a job whose identity source is unknown, or a native job
// that lost the user/version its digest was computed from. An empty source is
// accepted only as the historical legacy encoding; every new job, audit or
// operational, persists an explicit source.
func (a *AssistantTask) validAuthority() bool {
	switch a.AuthorizationSource {
	case AuditSourceNative:
		return a.AuthorizationUserID != uuid.Nil && a.AuthorizationVersion >= 1
	case AuditSourceLegacy:
		return a.AuthorizationUserID == uuid.Nil && a.AuthorizationVersion == 0
	case "":
		return a.AuthorizationUserID == uuid.Nil && a.AuthorizationVersion == 0
	default:
		return false
	}
}

// AuditAuthorityRef identifies which identity system proved a job's audit
// authority and at which version. It is persisted with the job.
type AuditAuthorityRef struct {
	Source               string
	UserID               uuid.UUID
	AuthorizationVersion int
}

// AuditAuthorityFromContext returns the source that the current request proves
// its audit authority with. A native session is never described as legacy.
func AuditAuthorityFromContext(ctx context.Context) AuditAuthorityRef {
	ref := auth.AuthorityFromContext(ctx)
	return AuditAuthorityRef{Source: ref.Source, UserID: ref.UserID, AuthorizationVersion: ref.AuthorizationVersion}
}

func emptyAuditScope(tenant uuid.UUID) graph.Scope {
	return graph.Scope{Tenant: tenant.String(), Namespaces: []string{}, Resources: []string{}}
}

// AuditScopeTx is independent of Graph/operator scope and locks current admin
// grants. The decision is dispatched on the trusted identity source first: a
// native request never reaches the legacy binding fence (that fence only knows
// platform.role_bindings and would deny every native administrator before the
// native branch could run).
func AuditScopeTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string) (graph.Scope, error) {
	out := emptyAuditScope(tenant)
	if tx == nil || tenant == uuid.Nil || subject == "" {
		return out, ErrDenied
	}
	ref := AuditAuthorityFromContext(ctx)
	if ref.Source == AuditSourceNative {
		// Request path: prove the live session first, then the grant set. The
		// session-bound check pins the session and grant rows for this
		// transaction, so a revocation that commits first is refused here.
		native, ok := auth.NativeSessionFromContext(ctx)
		if !ok || native.Session.UserID != ref.UserID ||
			native.Session.AuthorizationVersion != ref.AuthorizationVersion {
			return out, ErrDenied
		}
		allowed, err := authz.NativeRoleAuthorized(ctx, tx, auth.RequestContext{
			TenantID: tenant, Subject: subject,
		}, "platform_admin")
		if err != nil || !allowed {
			return out, ErrDenied
		}
		return nativeAuditScope(ctx, tx, tenant, subject, ref, out)
	}
	return legacyAuditScope(ctx, tx, tenant, subject, out)
}

// AuditScopeForJobTx re-proves a queued job's authority without a browser
// session. The persisted source decides the branch: a native job cannot degrade
// to the legacy binding table, and a legacy job cannot borrow a native grant.
func AuditScopeForJobTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string, ref AuditAuthorityRef) (graph.Scope, error) {
	out := emptyAuditScope(tenant)
	if tx == nil || tenant == uuid.Nil || subject == "" {
		return out, ErrDenied
	}
	switch ref.Source {
	case AuditSourceNative:
		return nativeAuditScope(ctx, tx, tenant, subject, ref, out)
	case AuditSourceLegacy, "":
		// Rows written before the source field existed could only be legacy:
		// the native branch was unreachable then, so no native job exists.
		return legacyAuditScope(ctx, tx, tenant, subject, out)
	default:
		return out, ErrDenied
	}
}

func nativeAuditScope(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string, ref AuditAuthorityRef, out graph.Scope) (graph.Scope, error) {
	return nativeRoleScopeForJobTx(ctx, tx, tenant, subject, ref, "platform_admin", "")
}

// NativeOperatorScopeForJobTx re-proves an operational job admitted by a native
// operator without a browser session. It reads the live grant rows and refuses
// when the account is disabled, the grants were revoked or re-granted, or the
// account authorization version moved; the rebuilt scope must also reproduce the
// frozen grant digest exactly. Legacy rows never reach this function.
func NativeOperatorScopeForJobTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string, ref AuditAuthorityRef, cluster string) (graph.Scope, error) {
	return nativeRoleScopeForJobTx(ctx, tx, tenant, subject, ref, "operator", cluster)
}

func nativeRoleScopeForJobTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string, ref AuditAuthorityRef, role, cluster string) (graph.Scope, error) {
	out := emptyAuditScope(tenant)
	if tx == nil || tenant == uuid.Nil || subject == "" || ref.UserID == uuid.Nil || ref.AuthorizationVersion < 1 {
		return out, ErrDenied
	}
	if role == "platform_admin" {
		// Locked projection: same account->grant order as the administrative write,
		// so a concurrent revocation waits until this transaction commits.
		var version int
		var bindings []uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT authorization_version,binding_ids FROM platform.native_authority_shared($1,$2,$3)`, tenant, ref.UserID, role).Scan(&version, &bindings); err != nil {
			return out, ErrDenied
		}
		if version != ref.AuthorizationVersion || len(bindings) == 0 {
			return out, ErrDenied
		}
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT revision FROM platform.tenants WHERE tenant_id=$1 AND status='active'`, tenant).Scan(&revision); err != nil {
			return out, ErrDenied
		}
		out.AuthorizationRevision = NativeAuditRevision(revision, subject, ref.UserID, version, bindings)
		return out, nil
	}
	return graph.EffectivePersistedNativeTx(ctx, tx, tenant.String(), subject, cluster, ref.UserID, ref.AuthorizationVersion)
}

// NativeAuditRevision is the audit authorization digest for a native subject.
// It binds the real native authorization basis: tenant revision, subject,
// account user, current authorization version and the active grant rows.
// Revoking and re-granting a role increments the version (and changes the grant
// rows), so the digest changes and the stored job binding no longer matches.
func NativeAuditRevision(tenantRevision int64, subject string, userID uuid.UUID, authorizationVersion int, bindingIDs []uuid.UUID) string {
	bindings := make([]string, 0, len(bindingIDs))
	for _, bindingID := range bindingIDs {
		bindings = append(bindings, bindingID.String())
	}
	sort.Strings(bindings)
	return graph.ScopeDigest([]any{
		"audit-native/v1", tenantRevision, subject, userID.String(),
		authorizationVersion, bindings,
	})
}

func legacyAuditScope(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string, out graph.Scope) (graph.Scope, error) {
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT platform.assistant_lock_audit_authority($1,$2)`, tenant, subject).Scan(&locked); err != nil || !locked {
		return out, ErrDenied
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM platform.tenants WHERE tenant_id=$1 AND status='active'`, tenant).Scan(&revision); err != nil {
		return out, ErrDenied
	}
	rows, err := tx.Query(ctx, `SELECT binding_id,revision FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2 AND role_name='platform_admin' AND status='active' ORDER BY binding_id`, tenant, subject)
	if err != nil {
		return out, err
	}
	versions := []any{revision}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var rev int64
		if err := rows.Scan(&id, &rev); err != nil {
			return out, err
		}
		versions = append(versions, id.String(), rev)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(versions) == 1 {
		return out, ErrDenied
	}
	out.AuthorizationRevision = graph.ScopeDigest(versions)
	return out, nil
}
func AuditScope(ctx context.Context, pool persistence.TxBeginner, tenant uuid.UUID, subject string) (graph.Scope, error) {
	var out graph.Scope
	err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error { var err error; out, err = AuditScopeTx(ctx, tx, tenant, subject); return err })
	return out, err
}
func currentAssistant(ctx context.Context, tx pgx.Tx, j Job) error {
	return currentAssistantAs(ctx, tx, j, HistoricalRead{OwnerSubject: j.Subject, CurrentSubject: j.Subject})
}

// currentAssistantAs re-proves a job's authority inside the caller's
// transaction. A mapped historical owner is re-authorized with the reading
// account's current native authority only; the frozen legacy grant is kept as
// evidence and never compared as if it were a native grant.
func currentAssistantAs(ctx context.Context, tx pgx.Tx, j Job, read HistoricalRead) error {
	if !j.Assistant.Valid(j) || graph.ScopeDigest(j.Scope) != j.EffectiveScopeDigest {
		return ErrDenied
	}
	if read.Mapped {
		if read.OwnerSubject != j.Subject || read.CurrentSubject == "" || read.CurrentSubject == j.Subject || read.MappingID == uuid.Nil {
			return ErrDenied
		}
		if err := currentMappedAuthority(ctx, tx, j, read.CurrentSubject); err != nil {
			return ErrDenied
		}
	} else {
		var grant graph.Scope
		var err error
		authority := AuditAuthorityRef{
			Source:               j.Assistant.AuthorizationSource,
			UserID:               j.Assistant.AuthorizationUserID,
			AuthorizationVersion: j.Assistant.AuthorizationVersion,
		}
		switch {
		case j.Assistant.AuthorizationDomain == "audit":
			// Recheck with the source recorded at admission. A Worker has no browser
			// session, and must not guess its way into the legacy binding table.
			grant, err = AuditScopeForJobTx(ctx, tx, j.TenantID, j.Subject, authority)
		case authority.Source == AuditSourceNative:
			// A business job admitted by a native operator is re-proved from the
			// persisted account/version and the live grant rows, never from a
			// browser session and never by falling back to legacy bindings.
			grant, err = NativeOperatorScopeForJobTx(ctx, tx, j.TenantID, j.Subject, authority, j.Scope.Cluster)
		default:
			// Rows written before the source field existed could only be legacy.
			grant, err = graph.EffectiveTx(ctx, tx, j.TenantID.String(), j.Subject, j.Scope.Cluster, true)
		}
		if err != nil || graph.ScopeDigest(grant) != j.Assistant.GrantSnapshotDigest {
			return ErrDenied
		}
	}
	var owner, digest, contextDigest string
	if err := tx.QueryRow(ctx, `SELECT c.owner_subject,t.input_digest,t.context_digest FROM investigation.assistant_turns t JOIN investigation.assistant_conversations c USING(tenant_id,conversation_id) WHERE t.tenant_id=$1 AND t.turn_id=$2 AND t.job_id=$3 AND c.conversation_id=$4`, j.TenantID, j.Assistant.TurnID, j.JobID, j.Assistant.ConversationID).Scan(&owner, &digest, &contextDigest); err != nil || owner != j.Subject || digest != j.Assistant.InputDigest || contextDigest != j.Assistant.ContextDigest {
		return ErrDenied
	}
	vid, err := uuid.Parse(j.PolicyVersion)
	if err != nil {
		return ErrDenied
	}
	// The existing definer fences tenant policy for audit with an empty cluster;
	// it grants no operator or cluster authority.
	var valid bool
	if tx.QueryRow(ctx, `SELECT platform.sp06_lock_policy($1,$2,$3,$4)`, j.TenantID, vid, j.Scope.Cluster, j.Scope.Namespaces).Scan(&valid) != nil || !valid {
		return ErrDenied
	}
	return nil
}

// currentMappedAuthority re-proves a reading account's *current* authority for
// history frozen under a pre-native owner. It requires a live native session
// with its issued versions, and either the current platform administrator role
// (audit domain) or a current operator grant that still covers the frozen
// cluster/namespace/resource scope (operational domain). The frozen snapshot is
// validated against the frozen job separately, so a stored grant is never
// rewritten into a native one.
func currentMappedAuthority(ctx context.Context, tx pgx.Tx, j Job, currentSubject string) error {
	native, ok := auth.NativeSessionFromContext(ctx)
	if !ok || native.Session.TenantID != j.TenantID || native.Session.Subject != currentSubject {
		return ErrDenied
	}
	if graph.ScopeDigest(j.Assistant.GrantSnapshot) != j.Assistant.GrantSnapshotDigest || !ScopeCoversMappedOwner(j.Assistant.GrantSnapshot, j.Scope) {
		return ErrDenied
	}
	switch j.Assistant.AuthorizationDomain {
	case "audit":
		allowed, err := authz.NativeRoleAuthorized(ctx, tx, auth.RequestContext{TenantID: j.TenantID, Subject: currentSubject}, "platform_admin")
		if err != nil || !allowed {
			return ErrDenied
		}
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT revision FROM platform.tenants WHERE tenant_id=$1 AND status='active'`, j.TenantID).Scan(&revision); err != nil {
			return ErrDenied
		}
		return nil
	case "operational":
		grant, err := graph.EffectiveTx(ctx, tx, j.TenantID.String(), currentSubject, j.Scope.Cluster, true)
		if err != nil || !ScopeCoversMappedOwner(grant, j.Scope) {
			return ErrDenied
		}
		return nil
	default:
		return ErrDenied
	}
}

// CreateAssistantJobTx shares the caller's short transaction with Message/Turn.
// Deferrable FKs permit the Job and Turn to be inserted atomically in either order.
func CreateAssistantJobTx(ctx context.Context, tx pgx.Tx, j Job) error {
	if j.SchemaVersion != "investigation-job/v3" || !j.Assistant.Valid(j) || !j.Budget.Valid() || j.JobID == uuid.Nil {
		return ErrInvalid
	}
	var incident, revision any
	if j.IncidentID != uuid.Nil {
		incident = j.IncidentID
		revision = j.TriggerRevision
	}
	_, err := tx.Exec(ctx, `INSERT INTO investigation.jobs(tenant_id,job_id,incident_id,trigger_revision,state,schema_version,requesting_subject,trigger_kind,policy_version,effective_scope,effective_scope_digest,tool_catalog_digest,budget,budget_reserved,budget_consumed,expires_at,assistant_binding) VALUES($1,$2,$3,$4,'queued','investigation-job/v3',$5,'manual',$6,$7,$8,$9,$10,$11,$11,$12,$13)`, j.TenantID, j.JobID, incident, revision, j.Subject, j.PolicyVersion, raw(j.Scope), j.EffectiveScopeDigest, j.ToolCatalogDigest, raw(j.Budget), raw(Usage{}), j.ExpiresAt, raw(j.Assistant))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO investigation.worker_queue(tenant_id,job_id) VALUES($1,$2)`, j.TenantID, j.JobID)
	return err
}
func (r Repository) GetAssistantInput(ctx context.Context, l Lease) (json.RawMessage, error) {
	var out json.RawMessage
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if j.Assistant == nil {
			return ErrInvalid
		}
		if err := tx.QueryRow(ctx, `SELECT input FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2 AND input_digest=$3`, j.TenantID, j.Assistant.TurnID, j.Assistant.InputDigest).Scan(&out); err != nil {
			return err
		}
		if err := ValidateAssistantHistoryTx(ctx, tx, j, out); err != nil {
			return err
		}
		return chargeRead(ctx, tx, j, "assistant-context", int64(len(out)))
	})
	return out, err
}
func (r Repository) completeAssistant(ctx context.Context, l Lease, b []byte) error {
	return r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if j.Assistant == nil || j.Reserved != (Usage{}) {
			return ErrLease
		}
		validated, err := ValidateAssistantAnswerTx(ctx, tx, j, b)
		if err != nil {
			return err
		}
		var answer struct {
			Partial bool `json:"partial"`
		}
		json.Unmarshal(validated, &answer)
		state := "succeeded"
		if answer.Partial {
			state = "partial"
		}
		if _, err := tx.Exec(ctx, `UPDATE investigation.jobs SET state=$3,result=$4 WHERE tenant_id=$1 AND job_id=$2`, j.TenantID, j.JobID, state, validated); err != nil {
			return err
		}
		return event(ctx, tx, j, "result", map[string]any{"state": state, "result": json.RawMessage(validated)})
	})
}

// historicalReadGrant carries an already-proven mapped read to the nested
// read-only helpers that re-check the Job. The type and its key are unexported,
// so only AuthorizeHistoricalReadTx can create one; every use is re-proved
// against the live native session and the account's current authority.
type historicalReadGrant struct {
	tenant uuid.UUID
	job    uuid.UUID
	read   HistoricalRead
}

type historicalReadGrantKey struct{}

// AuthorizeHistoricalReadTx proves the historical read once and returns a
// context carrying that grant so nested read-only checks (fault-chain context,
// source steps, formal references) stay authorized. The frozen Job, Subject and
// digests are untouched: the grant only names the reading account, and
// CheckAssistantJobTx re-runs currentAssistantAs for the exact same Job.
func AuthorizeHistoricalReadTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, read HistoricalRead, conversation, turn uuid.UUID, scope graph.Scope) (context.Context, Job, error) {
	j, err := AssistantReadJobTxAs(ctx, tx, tenant, read, conversation, turn, scope)
	if err != nil {
		return ctx, Job{}, err
	}
	if read.Mapped {
		ctx = context.WithValue(ctx, historicalReadGrantKey{}, historicalReadGrant{tenant: tenant, job: j.JobID, read: read})
	}
	return ctx, j, nil
}

// CheckAssistantJobTx re-proves Job authority. A mapped read grant can only
// extend an already authorized read to its own Job: it never matches another
// tenant/Job, never applies to an owner-exact read and cannot be constructed by
// a Worker or an HTTP write path (neither attaches one, and the mapped branch
// still requires a live native session plus current cluster/namespace scope).
func CheckAssistantJobTx(ctx context.Context, tx pgx.Tx, j Job) error {
	if grant, ok := ctx.Value(historicalReadGrantKey{}).(historicalReadGrant); ok {
		if !grant.read.Mapped || grant.tenant != j.TenantID || grant.job != j.JobID || grant.read.OwnerSubject != j.Subject {
			return ErrDenied
		}
		return currentAssistantAs(ctx, tx, j, grant.read)
	}
	return currentAssistant(ctx, tx, j)
}
func AuthorizeAssistantResultTx(ctx context.Context, tx pgx.Tx, j Job, result []byte) error {
	return authorizeResult(ctx, tx, j, result)
}

// AssistantReadJobTx restores the exact admitted policy and data ceiling for
// history reads. A conversation scope alone cannot stand in for a Job budget.
func AssistantReadJobTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string, conversation, turn uuid.UUID, scope graph.Scope) (Job, error) {
	return AssistantReadJobTxAs(ctx, tx, tenant, HistoricalRead{OwnerSubject: subject, CurrentSubject: subject}, conversation, turn, scope)
}

// AssistantReadJobTxAs restores the exact admitted job for a read whose owner
// may be an explicitly mapped historical subject. The frozen owner, job id,
// input/context digests and policy version are still required to match; only
// the identity that must currently hold authority is the reading account.
func AssistantReadJobTxAs(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, read HistoricalRead, conversation, turn uuid.UUID, scope graph.Scope) (Job, error) {
	var id uuid.UUID
	if tx.QueryRow(ctx, `SELECT job_id FROM investigation.assistant_turns WHERE tenant_id=$1 AND conversation_id=$2 AND turn_id=$3`, tenant, conversation, turn).Scan(&id) != nil {
		return Job{}, ErrDenied
	}
	j, err := load(ctx, tx, tenant, id, false)
	if err != nil || j.Assistant == nil || read.OwnerSubject == "" || j.Subject != read.OwnerSubject || j.Assistant.ConversationID != conversation || !ScopeSubset(scope, j.Scope) {
		return Job{}, ErrDenied
	}
	if err = currentAssistantAs(ctx, tx, j, read); err != nil {
		return Job{}, err
	}
	return j, nil
}

func AssistantAnswerRetainedTx(ctx context.Context, tx pgx.Tx, tenant, conversation, turn uuid.UUID) error {
	var retained bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.assistant_messages WHERE tenant_id=$1 AND conversation_id=$2 AND turn_id=$3 AND role='assistant' AND (retain_until>clock_timestamp() OR legal_hold))`, tenant, conversation, turn).Scan(&retained); err != nil {
		return err
	}
	if !retained {
		return ErrResultExpired
	}
	return nil
}
