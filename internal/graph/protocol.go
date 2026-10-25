package graph

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"log/slog"
	"net/http"
	"ops-platform/gen/internalgraph"
	"ops-platform/internal/auth"
	"ops-platform/internal/authz"
	platformcontract "ops-platform/internal/contract"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
	"slices"
	"sort"
	"strings"
	"time"
)

func ScopeDigest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type Authorization struct{ Pool persistence.TxBeginner }

// Effective rebuilds the caller's current scope. When the context carries a
// persisted task authority (a native assistant job acting without a browser
// session) the task authority is re-proven first; otherwise the interactive /
// legacy behaviour is unchanged. Callers keep comparing the returned digest, so a
// task can never widen authority silently.
func (a Authorization) Effective(ctx context.Context, tenant, subject, cluster string) (Scope, error) {
	if jobID, ok := TaskAuthorityFromContext(ctx); ok {
		return a.effectiveTask(ctx, tenant, subject, cluster, jobID)
	}
	return a.effectiveLocal(ctx, tenant, subject, cluster)
}

func (a Authorization) effectiveLocal(ctx context.Context, tenant, subject, cluster string) (Scope, error) {
	tid, err := uuid.Parse(tenant)
	if err != nil {
		return Scope{}, ErrScope
	}
	var out Scope
	err = persistence.WithTenantTx(ctx, a.Pool, tid, func(tx pgx.Tx) error {
		out, err = EffectiveTx(ctx, tx, tenant, subject, cluster, false)
		return err
	})
	return out, err
}

// effectiveTask re-reads live authority for a persisted native task. It refuses
// audit-domain tasks (they are not operator business scope) and any task whose
// re-proof does not reproduce its frozen grant/execution digests.
//
// The live projection returned here is the account's complete grant for the
// cluster, which is what admission froze as the grant snapshot. A task's execution
// scope is normally narrower, so comparing the live grant against the frozen
// execution scope would reject every healthy task; the two frozen values are
// therefore proven separately: the live grant must reproduce the frozen grant
// digest, and it must still cover the frozen execution scope.
func (a Authorization) effectiveTask(ctx context.Context, tenant, subject, cluster string, jobID uuid.UUID) (Scope, error) {
	tid, err := uuid.Parse(tenant)
	if err != nil || cluster == "" {
		return Scope{}, ErrScope
	}
	var out Scope
	err = persistence.WithTenantTx(ctx, a.Pool, tid, func(tx pgx.Tx) error {
		// Local authorization reads the persisted facts and then rebuilds live
		// authority; it must not compare against a signed claim scope, because
		// this call site has no signed envelope at all.
		resolution, err := resolveTaskAuthorityTx(ctx, tx, tid, jobID)
		if err != nil {
			return err
		}
		if resolution.Domain != "operational" || resolution.Subject != subject {
			return ErrScope
		}
		current, err := EffectivePersistedNativeTx(ctx, tx, tenant, subject, cluster,
			resolution.UserID, resolution.AuthorizationVersion)
		if err != nil {
			return err
		}
		if err := taskAuthorityHolds(current, resolution); err != nil {
			return err
		}
		out = current
		return nil
	})
	if err != nil {
		return Scope{}, err
	}
	return out, nil
}

// taskAuthorityHolds compares the live projection of a persisted native task with
// its frozen facts. The live projection is the complete account grant, so it must
// reproduce the frozen grant snapshot digest; the (normally narrower) frozen
// execution scope must remain inside that live grant. It is a pure function so
// both the local pre-check and the Worker envelope share exactly this rule and it
// stays testable without a database.
func taskAuthorityHolds(current Scope, resolution NativeResolution) error {
	if resolution.GrantDigest == "" || ScopeDigest(current) != resolution.GrantDigest {
		return ErrScope
	}
	if resolution.EffectiveScope == nil || !coversScope(current, *resolution.EffectiveScope) {
		return ErrScope
	}
	return nil
}

// EffectiveTx reloads authority using the caller's transaction. Fenced reads
// hold row share locks until the consuming business mutation commits.
func EffectiveTx(ctx context.Context, tx pgx.Tx, tenant, subject, cluster string, fenced bool) (Scope, error) {
	native, isNative := auth.NativeSessionFromContext(ctx)
	if !isNative {
		return EffectiveLegacyTx(ctx, tx, tenant, subject, cluster, fenced)
	}
	out := Scope{Tenant: tenant, Cluster: cluster, Namespaces: []string{}, Resources: []string{}}
	tid, err := uuid.Parse(tenant)
	if err != nil || subject == "" {
		return out, ErrScope
	}
	if native.Session.TenantID != tid || native.Session.Subject != subject {
		return out, ErrScope
	}
	authorized, err := authz.NativeRoleAuthorized(ctx, tx, auth.RequestContext{TenantID: tid, Subject: subject}, "operator")
	if err != nil || !authorized {
		return out, ErrScope
	}
	var clusterID uuid.UUID
	var clusterRevision, tenantRevision int64
	if err := tx.QueryRow(ctx, `SELECT c.cluster_id,c.revision,t.revision FROM platform.cluster_registrations c JOIN platform.tenants t USING(tenant_id) WHERE c.tenant_id=$1 AND c.cluster_uid=$2 AND c.status='active' AND t.status='active'`, tid, cluster).Scan(&clusterID, &clusterRevision, &tenantRevision); err != nil {
		return out, ErrScope
	}
	versions := []any{clusterRevision, tenantRevision, "native", native.Session.UserID.String(), native.Session.AuthorizationVersion}
	grants := make([]NativeGrant, 0, len(native.Session.Grants))
	for _, grant := range native.Session.Grants {
		if grant.Role != "operator" {
			continue
		}
		grants = append(grants, NativeGrant{BindingID: grant.BindingID, AuthorizationVersion: grant.AuthorizationVersion, ClusterUIDs: grant.ClusterUIDs, Namespaces: grant.Namespaces})
	}
	if out, versions, err = applyNativeGrants(out, cluster, grants, versions); err != nil {
		return out, err
	}
	return finishScopeTx(ctx, tx, tid, clusterID, cluster, out, versions)
}

// EffectiveLegacyTx re-proves authority from platform.role_bindings only. It is
// the explicit re-proof for a decision whose persisted source is legacy, so a
// native session can never lend its grants to a legacy job and a legacy request
// can never borrow native authority.
func EffectiveLegacyTx(ctx context.Context, tx pgx.Tx, tenant, subject, cluster string, fenced bool) (Scope, error) {
	out := Scope{Tenant: tenant, Cluster: cluster, Namespaces: []string{}, Resources: []string{}}
	tid, err := uuid.Parse(tenant)
	if err != nil || subject == "" {
		return out, ErrScope
	}
	if fenced {
		var locked bool
		if err := tx.QueryRow(ctx, `SELECT platform.sp06_lock_authority($1,$2,$3)`, tid, subject, cluster).Scan(&locked); err != nil || !locked {
			return out, ErrScope
		}
	}
	var clusterID uuid.UUID
	var clusterRevision, tenantRevision int64
	if err := tx.QueryRow(ctx, `SELECT c.cluster_id,c.revision,t.revision FROM platform.cluster_registrations c JOIN platform.tenants t USING(tenant_id) WHERE c.tenant_id=$1 AND c.cluster_uid=$2 AND c.status='active' AND t.status='active'`, tid, cluster).Scan(&clusterID, &clusterRevision, &tenantRevision); err != nil {
		return out, ErrScope
	}
	versions := []any{clusterRevision, tenantRevision}
	rows, err := tx.Query(ctx, `SELECT binding_id,revision,cluster_scopes,namespace_scopes FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2 AND status='active' AND role_name='operator' ORDER BY binding_id`, tid, subject)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var revision int64
		var clustersRaw, namespacesRaw []byte
		if err := rows.Scan(&id, &revision, &clustersRaw, &namespacesRaw); err != nil {
			return out, err
		}
		var clusters []uuid.UUID
		var namespaces []auth.NamespaceScope
		if json.Unmarshal(clustersRaw, &clusters) != nil || json.Unmarshal(namespacesRaw, &namespaces) != nil {
			return out, ErrScope
		}
		versions = append(versions, id.String(), revision, clusters, namespaces)
		if slices.Contains(clusters, clusterID) {
			out.ClusterScoped = true
		}
		for _, ns := range namespaces {
			if ns.ClusterID == clusterID && !slices.Contains(out.Namespaces, ns.Namespace) {
				out.Namespaces = append(out.Namespaces, ns.Namespace)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	return finishScopeTx(ctx, tx, tid, clusterID, cluster, out, versions)
}

// NativeGrant is one persisted operator grant row. A Worker re-proves a job's
// frozen scope from these rows instead of a browser session.
type NativeGrant struct {
	BindingID            uuid.UUID
	AuthorizationVersion int
	ClusterUIDs          []string
	Namespaces           []string
}

// applyNativeGrants is the single implementation of "what a native operator
// grant covers for one cluster". EffectiveTx (browser session) and
// EffectiveNativeTx (persisted job re-proof) share it, so both produce the same
// scope and the same authorization revision vector.
func applyNativeGrants(out Scope, cluster string, grants []NativeGrant, versions []any) (Scope, []any, error) {
	for _, grant := range grants {
		if grant.BindingID == uuid.Nil || grant.AuthorizationVersion < 1 {
			return out, versions, ErrScope
		}
		if !slices.Contains(grant.ClusterUIDs, cluster) {
			continue
		}
		versions = append(versions, grant.BindingID.String(), grant.AuthorizationVersion, grant.ClusterUIDs, grant.Namespaces)
		if len(grant.Namespaces) == 0 {
			out.ClusterScoped = true
			continue
		}
		if len(grant.ClusterUIDs) != 1 {
			return out, versions, ErrScope
		}
		for _, namespace := range grant.Namespaces {
			if !slices.Contains(out.Namespaces, namespace) {
				out.Namespaces = append(out.Namespaces, namespace)
			}
		}
	}
	return out, versions, nil
}

// EffectiveNativeTx rebuilds the exact scope of a job admitted by a native
// operator without a browser session. The caller must have already proven the
// persisted user/version; this function re-reads the cluster/tenant/source
// revisions so any change invalidates the frozen digest.
func EffectiveNativeTx(ctx context.Context, tx pgx.Tx, tenant, subject, cluster string, userID uuid.UUID, authorizationVersion int, grants []NativeGrant) (Scope, error) {
	out := Scope{Tenant: tenant, Cluster: cluster, Namespaces: []string{}, Resources: []string{}}
	tid, err := uuid.Parse(tenant)
	if err != nil || subject == "" || tx == nil || userID == uuid.Nil || authorizationVersion < 1 || cluster == "" {
		return out, ErrScope
	}
	var clusterID uuid.UUID
	var clusterRevision, tenantRevision int64
	if err := tx.QueryRow(ctx, `SELECT c.cluster_id,c.revision,t.revision FROM platform.cluster_registrations c JOIN platform.tenants t USING(tenant_id) WHERE c.tenant_id=$1 AND c.cluster_uid=$2 AND c.status='active' AND t.status='active'`, tid, cluster).Scan(&clusterID, &clusterRevision, &tenantRevision); err != nil {
		return out, ErrScope
	}
	versions := []any{clusterRevision, tenantRevision, "native", userID.String(), authorizationVersion}
	if out, versions, err = applyNativeGrants(out, cluster, grants, versions); err != nil {
		return out, err
	}
	return finishScopeTx(ctx, tx, tid, clusterID, cluster, out, versions)
}

// EffectivePersistedNativeTx re-proves a persisted decision (a Worker job or a
// command execution that outlived its browser session) from the account and the
// live operator grants. The projection locks the account and grant rows in the
// caller's transaction, so an administrative revocation waits until that
// transaction commits instead of slipping between the check and the business
// write. A version mismatch refuses the decision; it never falls back to legacy.
func EffectivePersistedNativeTx(ctx context.Context, tx pgx.Tx, tenant, subject, cluster string, userID uuid.UUID, authorizationVersion int) (Scope, error) {
	out := Scope{Tenant: tenant, Cluster: cluster, Namespaces: []string{}, Resources: []string{}}
	tid, err := uuid.Parse(tenant)
	if err != nil || subject == "" || cluster == "" || tx == nil || userID == uuid.Nil || authorizationVersion < 1 {
		return out, ErrScope
	}
	rows, err := tx.Query(ctx, `SELECT authorization_version,binding_id,scope FROM platform.native_operator_scope_shared($1,$2)`, tid, userID)
	if err != nil {
		return out, ErrScope
	}
	defer rows.Close()
	grants := []NativeGrant{}
	for rows.Next() {
		var version int
		var binding uuid.UUID
		var scopeRaw []byte
		if err := rows.Scan(&version, &binding, &scopeRaw); err != nil {
			return out, ErrScope
		}
		if version != authorizationVersion {
			// Revoked, re-granted, disabled or re-versioned authority can never
			// revive a decision frozen at an earlier version.
			return out, ErrScope
		}
		var scope struct {
			ClusterUIDs []string `json:"clusterUids"`
			Namespaces  []string `json:"namespaces"`
		}
		if json.Unmarshal(scopeRaw, &scope) != nil {
			return out, ErrScope
		}
		grants = append(grants, NativeGrant{BindingID: binding, AuthorizationVersion: version, ClusterUIDs: scope.ClusterUIDs, Namespaces: scope.Namespaces})
	}
	if err := rows.Err(); err != nil || len(grants) == 0 {
		return out, ErrScope
	}
	return EffectiveNativeTx(ctx, tx, tenant, subject, cluster, userID, authorizationVersion, grants)
}

func finishScopeTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, clusterID uuid.UUID, cluster string, out Scope, versions []any) (Scope, error) {
	if !out.ClusterScoped && len(out.Namespaces) == 0 {
		return out, ErrScope
	}
	sort.Strings(out.Namespaces)
	sourceRows, err := tx.Query(ctx, `SELECT source_id,revision,status,source_type,backend_logical_id,data_scope_mapping FROM platform.source_registrations WHERE tenant_id=$1 AND (cluster_id=$2 OR data_scope_mapping->'scopes'->'cluster' ? $3) ORDER BY source_id`, tenant, clusterID, cluster)
	if err != nil {
		return out, err
	}
	defer sourceRows.Close()
	for sourceRows.Next() {
		var id, status, sourceType, backend string
		var revision int64
		var mapping []byte
		if err := sourceRows.Scan(&id, &revision, &status, &sourceType, &backend, &mapping); err != nil {
			return out, err
		}
		versions = append(versions, id, revision, status, sourceType, backend, json.RawMessage(mapping))
	}
	if err := sourceRows.Err(); err != nil {
		return out, err
	}
	out.AuthorizationRevision = ScopeDigest(versions)
	return out, nil
}

// Explicit internal-context sources. A token must say which identity system
// proved the caller; there is no implicit fallback and no inference from a
// subject prefix.
const (
	SourceLegacy            = "legacy"
	SourceNativeInteractive = "native_interactive"
	SourceNativeTask        = "native_task"
)

// NativeProof is the only native material a signed internal context carries: a
// reference to live persisted state, never a cookie, password, role, reusable
// credential or scope assertion. The Worker resolves it and re-proves the scope
// from the account, the live grants and (for a task) the persisted job authority.
type NativeProof struct {
	SessionID string `json:"sessionId,omitempty"`
	JobID     string `json:"jobId,omitempty"`
}

func (p *NativeProof) validateFor(source string) bool {
	switch source {
	case SourceLegacy:
		return p == nil
	case SourceNativeInteractive:
		if p == nil || p.JobID != "" {
			return false
		}
		_, err := uuid.Parse(p.SessionID)
		return err == nil
	case SourceNativeTask:
		if p == nil || p.SessionID != "" {
			return false
		}
		_, err := uuid.Parse(p.JobID)
		return err == nil
	default:
		return false
	}
}

type Claims struct {
	GrantSnapshotDigest string       `json:"grantSnapshotDigest,omitempty"`
	Source              string       `json:"source"`
	Native              *NativeProof `json:"nativeProof,omitempty"`
	Audience            string       `json:"audience"`
	Subject             string       `json:"subject"`
	Scope               Scope        `json:"scope"`
	Deadline            time.Time    `json:"deadline"`
	QueryDigest         string       `json:"queryDigest"`
}

// NativeResolution is what the Worker re-derived from persisted state. No field
// comes from the signed envelope.
type NativeResolution struct {
	UserID               uuid.UUID
	Subject              string
	AuthorizationVersion int
	Domain               string
	// Task-only frozen facts, re-read from the persisted job authority.
	TaskKind        string
	TaskState       string
	GrantSnapshot   *Scope
	EffectiveScope  *Scope
	GrantDigest     string
	EffectiveDigest string
}

type taskAuthorityKey struct{}

// WithTaskAuthority marks a context whose calls run for a persisted assistant
// task rather than a browser session. Only the API attaches this, and only from a
// job whose persisted authority is native; the Worker still re-proves everything.
func WithTaskAuthority(ctx context.Context, jobID uuid.UUID) context.Context {
	if ctx == nil || jobID == uuid.Nil {
		return ctx
	}
	return context.WithValue(ctx, taskAuthorityKey{}, jobID)
}

// TaskAuthorityFromContext reports the persisted task this context acts for.
func TaskAuthorityFromContext(ctx context.Context) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	jobID, ok := ctx.Value(taskAuthorityKey{}).(uuid.UUID)
	return jobID, ok && jobID != uuid.Nil
}

func SignContext(key ed25519.PrivateKey, claims Claims) (string, error) {
	if len(key) != ed25519.PrivateKeySize {
		return "", ErrScope
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw)), nil
}
func VerifyContext(key ed25519.PublicKey, token, audience string, payload []byte) (Claims, error) {
	var claims Claims
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 32<<10 || len(key) != ed25519.PublicKeySize {
		return claims, ErrScope
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, ErrScope
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !ed25519.Verify(key, raw, sig) {
		return claims, ErrScope
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&claims) != nil || !claims.Native.validateFor(claims.Source) {
		return Claims{}, ErrScope
	}
	now := time.Now()
	if claims.Audience != audience || claims.Subject == "" || !now.Before(claims.Deadline) || claims.Deadline.After(now.Add(30*time.Second)) || claims.QueryDigest != ScopeDigest(json.RawMessage(payload)) {
		return claims, ErrScope
	}
	return claims, nil
}

// ResolveNativeClaimTx turns a signed native reference into persisted facts under
// the caller's transaction. The signed envelope contributes only the reference;
// the account, session versions, subject and authorization version all come from
// the database, so a tampered or incomplete proof can only fail closed.
func ResolveNativeClaimTx(ctx context.Context, tx pgx.Tx, claims Claims) (NativeResolution, error) {
	var out NativeResolution
	if tx == nil || claims.Native == nil || !claims.Native.validateFor(claims.Source) {
		return out, ErrScope
	}
	tenant, err := uuid.Parse(claims.Scope.Tenant)
	if err != nil || claims.Subject == "" {
		return out, ErrScope
	}
	switch claims.Source {
	case SourceNativeInteractive:
		sessionID, err := uuid.Parse(claims.Native.SessionID)
		if err != nil {
			return out, ErrScope
		}
		if err := tx.QueryRow(ctx, `SELECT out_user_id, out_subject, out_authorization_version
			FROM identity.native_internal_session_shared($1,$2)`, tenant, sessionID).
			Scan(&out.UserID, &out.Subject, &out.AuthorizationVersion); err != nil {
			return NativeResolution{}, ErrScope
		}
		if out.Subject != claims.Subject || out.AuthorizationVersion < 1 {
			return NativeResolution{}, ErrScope
		}
		out.Domain = "interactive"
		return out, nil
	case SourceNativeTask:
		jobID, err := uuid.Parse(claims.Native.JobID)
		if err != nil {
			return out, ErrScope
		}
		resolved, err := resolveTaskAuthorityTx(ctx, tx, tenant, jobID)
		if err != nil {
			return NativeResolution{}, err
		}
		if resolved.Subject != claims.Subject {
			return NativeResolution{}, ErrScope
		}
		// The signed context must be bound to this task's frozen authority; an
		// unknown or incomplete binding fails closed instead of degrading to
		// legacy. The request scope must stay inside the task's frozen execution
		// scope, not merely inside the wider account grant.
		if claims.GrantSnapshotDigest == "" {
			if ScopeDigest(claims.Scope) != resolved.EffectiveDigest {
				return NativeResolution{}, ErrScope
			}
		} else if claims.GrantSnapshotDigest != resolved.GrantDigest ||
			!coversScope(*resolved.EffectiveScope, claims.Scope) {
			return NativeResolution{}, ErrScope
		}
		return resolved, nil
	default:
		return out, ErrScope
	}
}

// resolveTaskAuthorityTx re-reads and validates the persisted task authority
// without looking at any signed claim. Both the local (API pre-check) and the
// external (worker envelope) paths start here, so a single set of rules decides
// what a trusted persisted task is. Both persisted shapes are supported: a v3
// assistant task (conversation/turn binding) and a v2 Incident investigation job
// (frozen provenance columns added by migration 68). The schema version is read
// from the row, never inferred from the subject or the caller.
func resolveTaskAuthorityTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, jobID uuid.UUID) (NativeResolution, error) {
	var out NativeResolution
	if tx == nil || jobID == uuid.Nil || tenantID == uuid.Nil {
		return out, ErrScope
	}
	var schemaVersion string
	if err := tx.QueryRow(ctx, `SELECT schema_version FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2 AND schema_version IN('investigation-job/v2','investigation-job/v3')`, tenantID, jobID).Scan(&schemaVersion); err != nil {
		return NativeResolution{}, ErrScope
	}
	// Fixed whitelist: no caller input selects the projection.
	projection := `platform.native_internal_task_shared($1,$2)`
	if schemaVersion == "investigation-job/v2" {
		projection = `platform.native_incident_task_shared($1,$2)`
	}
	var (
		effectiveScope, grantSnapshot []byte
		inputDigest, contextDigest    string
		expiresAt                     time.Time
	)
	// The function locks account -> grants -> job and re-proves the whole
	// persisted task authority (lifecycle, source, version, policy, account
	// subject binding, frozen grant/execution digests, conversation/turn
	// ownership and input/context binding).
	if err := tx.QueryRow(ctx, `SELECT out_user_id, out_subject, out_authorization_version,
			out_domain, out_task_kind, out_effective_scope, out_grant_snapshot,
			out_grant_digest, out_effective_digest, out_input_digest, out_context_digest,
			out_state, out_expires_at
		FROM `+projection, tenantID, jobID).
		Scan(&out.UserID, &out.Subject, &out.AuthorizationVersion, &out.Domain, &out.TaskKind,
			&effectiveScope, &grantSnapshot, &out.GrantDigest, &out.EffectiveDigest,
			&inputDigest, &contextDigest, &out.TaskState, &expiresAt); err != nil {
		return NativeResolution{}, ErrScope
	}
	_ = inputDigest
	_ = contextDigest
	var effective, grant Scope
	if json.Unmarshal(effectiveScope, &effective) != nil || json.Unmarshal(grantSnapshot, &grant) != nil {
		return NativeResolution{}, ErrScope
	}
	if out.Subject == "" || out.AuthorizationVersion < 1 ||
		(out.Domain != "operational" && out.Domain != "audit") ||
		!time.Now().Before(expiresAt) ||
		!validTaskKind(out.TaskKind) ||
		ScopeDigest(effective) != out.EffectiveDigest ||
		ScopeDigest(grant) != out.GrantDigest ||
		!coversScope(grant, effective) {
		return NativeResolution{}, ErrScope
	}
	out.GrantSnapshot = &grant
	out.EffectiveScope = &effective
	return out, nil
}

// validTaskKind mirrors investigation.AssistantTask.Valid for the task kinds a
// persisted v3 task may carry.
func validTaskKind(kind string) bool {
	switch kind {
	case "data_query", "incident_followup", "resource_troubleshooting", "fault_chain":
		return true
	}
	return false
}

// ScopeCovers is the revision-free containment rule used across identity
// sources (the same rule as investigation.ScopeCoversMappedOwner): an empty
// resource list on the grant means "all resources", a restricted grant cannot
// cover an unrestricted (empty) request, and namespaces must be contained. It is
// exported for business-transaction authorization whose frozen scope was
// produced by another identity source and carries an incomparable revision.
func ScopeCovers(grant, scope Scope) bool {
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
		for _, resource := range scope.Resources {
			if !slices.Contains(grant.Resources, resource) {
				return false
			}
		}
	}
	return true
}

func coversScope(grant, scope Scope) bool { return ScopeCovers(grant, scope) }

// RequireNativeRoleTx re-proves one native role at the account's current
// authorization version. It is the Worker-side counterpart of the browser
// authorization projection and never consults the legacy binding table.
func RequireNativeRoleTx(ctx context.Context, tx pgx.Tx, claims Claims, role string) error {
	if role != "operator" && role != "platform_admin" {
		return ErrScope
	}
	resolution, err := ResolveNativeClaimTx(ctx, tx, claims)
	if err != nil || resolution.Subject != claims.Subject {
		return ErrScope
	}
	// A persisted task only proves the role of its own authorization domain: an
	// operational task can never borrow platform administration, and an audit task
	// never grants operator business scope.
	if claims.Source == SourceNativeTask {
		if (role == "operator" && resolution.Domain != "operational") ||
			(role == "platform_admin" && resolution.Domain != "audit") {
			return ErrScope
		}
	}
	tenant, err := uuid.Parse(claims.Scope.Tenant)
	if err != nil {
		return ErrScope
	}
	var version int
	var bindings []uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT authorization_version,binding_ids
		FROM platform.native_authority_shared($1,$2,$3)`,
		tenant, resolution.UserID, role).Scan(&version, &bindings); err != nil {
		return ErrScope
	}
	if version != resolution.AuthorizationVersion || len(bindings) == 0 {
		return ErrScope
	}
	return nil
}

// EffectiveClaim rebuilds operator scope for one signed internal request. The
// legacy source keeps the frozen role_bindings behaviour; every native source is
// re-proven from persisted account/grants and must reproduce the signed scope (or
// be a subset of the signed grant snapshot). Native never falls back to legacy.
func (a Authorization) EffectiveClaim(ctx context.Context, claims Claims) (Scope, error) {
	tenant, err := uuid.Parse(claims.Scope.Tenant)
	if err != nil || claims.Scope.Cluster == "" {
		return Scope{}, ErrScope
	}
	if claims.Source == SourceLegacy {
		if claims.Native != nil {
			return Scope{}, ErrScope
		}
		return a.Effective(ctx, claims.Scope.Tenant, claims.Subject, claims.Scope.Cluster)
	}
	if claims.Source != SourceNativeInteractive && claims.Source != SourceNativeTask {
		return Scope{}, ErrScope
	}
	var out Scope
	err = persistence.WithTenantTx(ctx, a.Pool, tenant, func(tx pgx.Tx) error {
		resolution, err := ResolveNativeClaimTx(ctx, tx, claims)
		if err != nil {
			return err
		}
		if resolution.Subject != claims.Subject {
			return ErrScope
		}
		// An audit-domain task proves platform administration, not operator
		// business scope; the graph/evidence/analysis Workers never accept it.
		if claims.Source == SourceNativeTask && resolution.Domain != "operational" {
			return ErrScope
		}
		current, err := EffectivePersistedNativeTx(ctx, tx, claims.Scope.Tenant, claims.Subject,
			claims.Scope.Cluster, resolution.UserID, resolution.AuthorizationVersion)
		if err != nil {
			return err
		}
		if !claims.AllowsCurrent(current) {
			return ErrScope
		}
		out = current
		return nil
	})
	if err != nil {
		return Scope{}, err
	}
	return out, nil
}

type InternalHandler struct {
	SourceAuthorities []SourceAuthority
	Graph             *Graph
	Lease             *Lease
	Key               ed25519.PublicKey
	Authorization     Authorization
	Trust             auth.WorkloadTrust
	ValidateSources   func(context.Context) error
	Metrics           *observability.Metrics
	Logger            *slog.Logger
}

// Frozen collection authority is process configuration, not a caller grant.
// It accompanies RCA inputs and optional qualified observation metadata.
type SourceAuthority struct {
	SourceRegistrationID string `json:"sourceRegistrationId"`
	Revision             int64  `json:"revision"`
	ScopeDigest          string `json:"scopeDigest"`
}

func (h InternalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"code": code})
	}
	if r.Method != "POST" || r.URL.Path != "/internal/v1/graph:query" {
		fail(404, "NOT_FOUND")
		return
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		fail(403, "FORBIDDEN")
		return
	}
	if _, err := auth.VerifyWorkload(auth.WithWorkloadTrust(r.Context(), h.Trust), r.TLS.PeerCertificates[0]); err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	claims, err := VerifyContext(h.Key, r.Header.Get("X-Graph-Context"), "platform-graph-query", raw)
	if err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	// Consume the independently generated internal contract before projection.
	var contract internalgraph.GraphQuery
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&contract) != nil || platformcontract.Validate("https://ops.local/schemas/internal-graph-query/v1", raw) != nil {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	var q Query
	if json.Unmarshal(raw, &q) != nil || ScopeDigest(q.Scope) != ScopeDigest(claims.Scope) {
		fail(403, "FORBIDDEN")
		return
	}
	current, err := h.Authorization.EffectiveClaim(r.Context(), claims)
	if err != nil || !claims.AllowsCurrent(current) {
		fail(409, "STALE_CONTEXT")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), claims.Deadline)
	defer cancel()
	unavailable := func(stage string) {
		if h.Logger != nil {
			h.Logger.WarnContext(ctx, "Graph request unavailable", "stage", stage)
		}
		fail(503, "GRAPH_NOT_READY")
	}
	if h.Lease == nil || h.Lease.Confirm(ctx, q.ExpectedOwnerEpoch) != nil {
		unavailable("lease_before_query")
		return
	}
	if h.ValidateSources != nil && h.ValidateSources(ctx) != nil {
		fail(503, "SOURCE_SCOPE_UNVERIFIED")
		return
	}
	result, err := h.Graph.Query(ctx, q)
	if err != nil {
		switch {
		case errors.Is(err, ErrScope):
			fail(403, "FORBIDDEN")
		case errors.Is(err, ErrStale):
			fail(409, "STALE_CONTEXT")
		default:
			stage := "graph_query_error"
			if errors.Is(err, ErrNotReady) {
				stage = h.Graph.availabilityFailure(q.ExpectedOwnerEpoch)
			} else if errors.Is(err, context.DeadlineExceeded) {
				stage = "query_deadline"
			} else if err.Error() == "INVALID_ARGUMENT" {
				stage = "query_argument"
			}
			unavailable(stage)
		}
		return
	}
	current, err = h.Authorization.EffectiveClaim(ctx, claims)
	if err != nil || !claims.AllowsCurrent(current) {
		fail(409, "STALE_CONTEXT")
		return
	}
	if h.Lease.Confirm(ctx, q.ExpectedOwnerEpoch) != nil {
		unavailable("lease_after_query")
		return
	}
	if h.ValidateSources != nil && h.ValidateSources(ctx) != nil {
		fail(503, "SOURCE_SCOPE_UNVERIFIED")
		return
	}
	// Current nonpaged reads may refresh a changed snapshot after the remote
	// fences. Query and encoding share the local revision lock, without holding
	// it across native Lease, source authorization or socket operations.
	var encoded []byte
	err = h.Graph.QueryReadResponse(ctx, q, func(current Result) error {
		used := map[string]bool{}
		for _, node := range current.Nodes {
			for _, id := range node.SourceRefs {
				used[id] = true
			}
		}
		for _, edge := range current.Edges {
			used[edge.Provenance.SourceRegistrationID] = true
		}
		for _, source := range h.SourceAuthorities {
			if used[source.SourceRegistrationID] {
				current.ObservationSources = append(current.ObservationSources, source)
			}
		}
		slices.SortFunc(current.ObservationSources, func(a, b SourceAuthority) int { return strings.Compare(a.SourceRegistrationID, b.SourceRegistrationID) })
		result = current
		var encodeErr error
		encoded, encodeErr = json.Marshal(current)
		if encodeErr != nil {
			return encodeErr
		}
		return platformcontract.Validate("https://ops.local/schemas/resource-graph/v2", encoded)
	})
	if err != nil {
		if errors.Is(err, ErrStale) {
			fail(409, "STALE_CONTEXT")
		} else if errors.Is(err, ErrScope) {
			fail(403, "FORBIDDEN")
		} else {
			unavailable("response_fence")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	h.Metrics.ObserveQuery("graph", result.Partial || len(result.DegradedSources) > 0, result.Freshness)
	_, _ = w.Write(encoded)
}

// AllowsCurrent preserves v1 full-grant equality. A signed narrowed query must
// bind its complete grant revision separately and remain a subset of that grant.
func (c Claims) AllowsCurrent(current Scope) bool {
	if c.GrantSnapshotDigest == "" {
		return ScopeDigest(current) == ScopeDigest(c.Scope)
	}
	return ScopeDigest(current) == c.GrantSnapshotDigest && ContainsScope(current, c.Scope)
}
func ContainsScope(grant, scope Scope) bool {
	if grant.Tenant != scope.Tenant || grant.Cluster != scope.Cluster || grant.AuthorizationRevision != scope.AuthorizationRevision || (scope.ClusterScoped && !grant.ClusterScoped) {
		return false
	}
	for _, ns := range scope.Namespaces {
		if !slices.Contains(grant.Namespaces, ns) {
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
