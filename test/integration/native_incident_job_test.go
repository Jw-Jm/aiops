package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"ops-platform/internal/archive"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/incident"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/resource"
	"ops-platform/internal/source"
	"slices"
)

// nativeIncidentSeed is one real Finding/Incident family inside the native
// fixture tenant, together with the source binding its Evidence must reproduce.
type nativeIncidentSeed struct {
	IncidentID     uuid.UUID
	Revision       int64
	Canonical      string
	Namespace      string
	Findings       []uuid.UUID
	SourceID       uuid.UUID
	SourceRevision int64
	SourceBackend  string
	SourceType     string
	SourceMapping  datascope.Mapping
	RuleID         string
	RuleFamily     string
	Symptom        string
	Clock          time.Time
	Occurrences    []string
}

// newIncident seeds one Incident (two linked Findings of the same family) that the
// v2 investigation and the Incident mutation tests operate on.
func (f *nativeJobFixture) newIncident(t *testing.T) nativeIncidentSeed {
	t.Helper()
	return f.newIncidentFamily(t, "pod", "NotReady", "api-0")
}

// newIncidentFamily seeds a second, independent Incident family. Different rule
// families produce distinct fingerprints, so merge/split have two real Incidents.
func (f *nativeJobFixture) newIncidentFamily(t *testing.T, family, symptom, stableID string) nativeIncidentSeed {
	t.Helper()
	sourceID := uuid.Must(uuid.NewV7())
	backend := "native-incident-" + sourceID.String()[:8]
	mappingRaw, _ := json.Marshal(map[string]any{"scopes": map[string][]string{"cluster": {f.clusterUID}, "namespace": {"apps"}}})
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,allowed_schemas,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'kubernetes',$4,'openbao://native-incident/readonly',ARRAY['finding-envelope/v2'],$5,$6)`, f.tenantID, sourceID, f.clusterID, "native-incident-"+sourceID.String()[:8], backend, mappingRaw); err != nil {
		t.Fatalf("register incident source: %v", err)
	}
	b := source.BoundSourceContext{TenantID: f.tenantID, SourceID: sourceID, ClusterID: f.clusterID, ClusterUID: f.clusterUID, RegistrationRevision: 1, CredentialRevision: 1}
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: f.tenantID.String(), Scope: f.clusterUID, APIGroup: "core", Kind: "Pod", StableID: stableID}.String()
	clock := time.Now().UTC().Add(-10 * time.Minute)
	ids := []uuid.UUID{}
	seedOccurrences := []string{}
	ruleID := family + "-ready/v1"
	for index := 0; index < 2; index++ {
		event := fmt.Sprintf("%s-%d-%s", family, index, uuid.Must(uuid.NewV7()).String()[:8])
		occurrence := fmt.Sprintf("%s-occurrence-%d-%s", family, index, uuid.Must(uuid.NewV7()).String()[:8])
		envelope := finding.Envelope{
			SchemaVersion: "finding-envelope/v2", EventID: event, IdempotencyKey: event,
			ResourceCanonicalID: canonical, Namespace: "apps",
			RuleID: ruleID, RuleFamily: family, NormalizedSymptom: symptom,
			OccurrenceID: occurrence, StartsAt: clock, ObservedAt: clock, SourceSequence: int64(index + 1),
			TimeReliable: true, State: "firing", Severity: "warning",
			Payload: json.RawMessage(`{"ready":false}`), EvidenceRefs: []string{},
		}
		fd, _, err := (finding.Service{Pool: f.workerPool}).Ingest(f.ctx, b, envelope)
		if err != nil {
			t.Fatalf("ingest fixture finding: %v", err)
		}
		ids = append(ids, uuid.MustParse(fd.FindingID))
		seedOccurrences = append(seedOccurrences, occurrence)
	}
	if err := (finding.Service{Pool: f.workerPool}).RelayPass(f.ctx, f.tenantID, incident.Consume, 50); err != nil {
		t.Fatalf("relay fixture findings: %v", err)
	}
	seed := nativeIncidentSeed{Canonical: canonical, Namespace: "apps", SourceID: sourceID, SourceRevision: 1, SourceBackend: backend, SourceType: "kubernetes",
		SourceMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {f.clusterUID}, "namespace": {"apps"}}},
		RuleID:        ruleID, RuleFamily: family, Symptom: symptom, Clock: clock, Occurrences: seedOccurrences}
	if err := f.db.QueryRowContext(f.ctx, `SELECT r.incident_id,r.revision FROM incident.records r JOIN incident.finding_links l USING(tenant_id,incident_id) WHERE l.tenant_id=$1 AND l.finding_id=$2`, f.tenantID, ids[0]).Scan(&seed.IncidentID, &seed.Revision); err != nil {
		t.Fatalf("read fixture incident: %v", err)
	}
	rows, err := f.db.QueryContext(f.ctx, `SELECT finding_id FROM incident.finding_links WHERE tenant_id=$1 AND incident_id=$2 ORDER BY finding_id`, f.tenantID, seed.IncidentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		seed.Findings = append(seed.Findings, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return seed
}

// resolveFindings ingests a resolved sample for every linked Finding, so a later
// suppression can legitimately expire into closed.
func (f *nativeJobFixture) resolveFindings(t *testing.T, seed nativeIncidentSeed) {
	t.Helper()
	b := source.BoundSourceContext{TenantID: f.tenantID, SourceID: seed.SourceID, ClusterID: f.clusterID, ClusterUID: f.clusterUID, RegistrationRevision: seed.SourceRevision, CredentialRevision: 1}
	clock := seed.Clock.Add(time.Minute)
	for index, occurrence := range seed.Occurrences {
		event := fmt.Sprintf("resolved-%d-%s", index, uuid.Must(uuid.NewV7()).String()[:8])
		envelope := finding.Envelope{
			SchemaVersion: "finding-envelope/v2", EventID: event, IdempotencyKey: event,
			ResourceCanonicalID: seed.Canonical, Namespace: seed.Namespace,
			RuleID: seed.RuleID, RuleFamily: seed.RuleFamily, NormalizedSymptom: seed.Symptom,
			OccurrenceID: occurrence, StartsAt: seed.Clock, ObservedAt: clock, SourceSequence: int64(index + 10),
			TimeReliable: true, State: "resolved", Severity: "info",
			Payload: json.RawMessage(`{"ready":true}`), EvidenceRefs: []string{},
		}
		if _, _, err := (finding.Service{Pool: f.workerPool}).Ingest(f.ctx, b, envelope); err != nil {
			t.Fatalf("ingest resolved finding: %v", err)
		}
	}
	if err := (finding.Service{Pool: f.workerPool}).RelayPass(f.ctx, f.tenantID, incident.Consume, 50); err != nil {
		t.Fatalf("relay resolved findings: %v", err)
	}
}

// newEvidence inserts one retained Evidence row whose proven source binding is the
// one seeded with the Incident family.
func (f *nativeJobFixture) newEvidence(t *testing.T, seed nativeIncidentSeed, archived bool, evidenceType string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	data := []byte(`{"ready":false}`)
	item := evidence.Evidence{
		SchemaVersion: "evidence/v2", EvidenceID: id.String(), TenantID: f.tenantID.String(),
		ResourceCanonicalID: seed.Canonical, Type: evidenceType, DataClass: "D0",
		SourceRegistrationID: seed.SourceID.String(), SourceRevision: seed.SourceRevision,
		SourceSystem: seed.SourceType, BackendLogicalID: seed.SourceBackend,
		QueryTemplateVersion: "native-incident/v1", QueryHash: finding.Hash("native-incident-evidence"),
		EffectiveScope: graph.Scope{Tenant: f.tenantID.String(), Cluster: f.clusterUID, Namespaces: []string{seed.Namespace}, AuthorizationRevision: "fixture/v1"},
		EvaluatedAt:    now, ObservedFrom: now.Add(-2 * time.Minute), ObservedTo: now.Add(-time.Minute),
		SourceRetentionUntil: now.Add(-time.Hour), TimeReliable: true, ReplayState: "archive_pending",
		IndependenceGroup: "native-incident", DerivationEvidenceRefs: []string{},
		ContentDigest: evidence.Digest(data), Data: data,
	}
	item.SourceScopeDigest = evidence.BindingScopeDigest(evidence.Binding{Revision: seed.SourceRevision, BackendLogicalID: seed.SourceBackend, SourceType: seed.SourceType, ScopeMapping: seed.SourceMapping})
	raw, _ := json.Marshal(item)
	state := "archive_pending"
	if archived {
		state = "archived_verified"
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.evidence_metadata(tenant_id,evidence_id,source_id,canonical_id,namespace,metadata,content_digest,replay_state,retain_until) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, f.tenantID, id, seed.SourceID, seed.Canonical, seed.Namespace, raw, item.ContentDigest, state, now.Add(181*24*time.Hour)); err != nil {
		t.Fatalf("insert fixture evidence: %v", err)
	}
	// The incident Evidence page joins finding.evidence_refs, so the retained row
	// is linked to the linked Finding exactly like a collected fact.
	if len(seed.Findings) > 0 {
		if _, err := f.db.ExecContext(f.ctx, `INSERT INTO finding.evidence_refs(tenant_id,finding_id,evidence_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, f.tenantID, seed.Findings[0], id); err != nil {
			t.Fatalf("link fixture evidence: %v", err)
		}
	}
	if archived {
		objectID := uuid.Must(uuid.NewV7())
		ref := evidence.ArchiveRef{
			BackendLogicalID: seed.SourceBackend,
			Object: archive.ObjectRef{TenantID: f.tenantID, ObjectID: objectID, Category: "evidence", Key: id.String(),
				Digest: item.ContentDigest, Size: int64(len(data)), ContentType: "application/json",
				RetainUntil: now.Add(181 * 24 * time.Hour)},
			EncryptionKeyVersion: "fixture-key/v1",
			PlaintextDigest:      item.ContentDigest,
			CiphertextDigest:     evidence.Digest([]byte("ciphertext")),
		}
		objectRef, _ := json.Marshal(ref)
		if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.evidence_archive_intents(tenant_id,evidence_id,object_id,backend_logical_id,ciphertext_digest,encryption_key_version,object_ref,status,verified_at) VALUES($1,$2,$3,$4,$5,$6,$7,'verified',clock_timestamp())`, f.tenantID, id, objectID, ref.BackendLogicalID, ref.CiphertextDigest, ref.EncryptionKeyVersion, objectRef); err != nil {
			t.Fatalf("insert fixture archive intent: %v", err)
		}
	}
	return id
}

// newV2Job admits one investigation-job/v2 through the production repository with
// the native session context of the account, exactly like the HTTP create path.
func (f *nativeJobFixture) newV2Job(t *testing.T, account nativeJobAccount, incidentID uuid.UUID, revision int64, catalog string) investigation.Job {
	t.Helper()
	scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(account.ctx, f.tenantID.String(), account.subject, f.clusterUID)
	if err != nil {
		t.Fatalf("native operator scope: %v", err)
	}
	job, err := (investigation.Repository{Pool: f.apiPool}).CreateJob(account.ctx, investigation.CreateInvestigationRequest{
		TenantID: f.tenantID, IncidentID: incidentID, Subject: account.subject,
		TriggerRevision: revision, TriggerKind: "manual", PolicyVersion: f.policy,
		Scope: scope, ToolCatalogDigest: catalog, Budget: investigation.DefaultBudget(),
	})
	if err != nil {
		t.Fatalf("native v2 create job: %v", err)
	}
	return job
}

func (f *nativeJobFixture) jobAuthority(t *testing.T, jobID uuid.UUID) (string, uuid.UUID, int) {
	t.Helper()
	var source string
	var userID uuid.UUID
	var version int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COALESCE(authorization_source,''),COALESCE(authorization_user_id,'00000000-0000-0000-0000-000000000000'::uuid),COALESCE(authorization_version,0) FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, jobID).Scan(&source, &userID, &version); err != nil {
		t.Fatal(err)
	}
	return source, userID, version
}

// clearLegacyBindings proves the fixture really has no legacy mirror: the policy
// authoring binding is removed after activation, so every remaining decision must
// come from identity.role_grants.
func (f *nativeJobFixture) clearLegacyBindings(t *testing.T) {
	t.Helper()
	if _, err := f.db.ExecContext(f.ctx, `DELETE FROM platform.role_bindings WHERE tenant_id=$1`, f.tenantID); err != nil {
		t.Fatal(err)
	}
	var bindings int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM platform.role_bindings WHERE tenant_id=$1`, f.tenantID).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("legacy bindings = %d (%v)", bindings, err)
	}
}

// A native operator creates a v2 Incident investigation in the API. The queue is
// consumed by a Worker with no browser session, which renews its context, runs a
// real controlled SP05 read through the persisted native authority, settles the
// step and completes the Job. identity.role_grants is the only grant source and
// platform.role_bindings is empty for the whole chain.
func TestNativeIncidentV2JobWorkerChain(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	incidentID, revision, canonical := seed.IncidentID, seed.Revision, seed.Canonical
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)

	defs, err := tools.CatalogFor("investigation-job/v2", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	job := f.newV2Job(t, account, incidentID, revision, tools.Digest(defs))
	if job.JobID == uuid.Nil || job.Subject != account.subject || job.IncidentID != incidentID {
		t.Fatalf("created job = %+v", job)
	}
	if source, userID, version := f.jobAuthority(t, job.JobID); source != "native" || userID != account.userID || version != 1 {
		t.Fatalf("persisted provenance = %s/%s/%d", source, userID, version)
	}
	loaded, err := (investigation.Repository{Pool: f.apiPool}).Get(account.ctx, f.tenantID, job.JobID)
	if err != nil {
		t.Fatalf("owner read of the queued job: %v", err)
	}
	if loaded.AuthoritySource() != investigation.JobAuthorityNative {
		t.Fatalf("loaded authority source = %q", loaded.AuthoritySource())
	}
	if userID, version, ok := loaded.NativeAuthority(); !ok || userID != account.userID || version != 1 {
		t.Fatalf("loaded native authority = %s/%d/%v", userID, version, ok)
	}

	workerCtx := context.Background()
	workerJobs := investigation.Repository{Pool: f.workerPool, AssistantEnabled: true}
	lease, err := workerJobs.Claim(workerCtx, f.tenantID, "native-incident-worker", 30*time.Second)
	if err != nil {
		t.Fatalf("worker claim without a session: %v", err)
	}
	if lease.JobID != job.JobID {
		t.Fatalf("worker claimed %s want %s", lease.JobID, job.JobID)
	}
	if err = workerJobs.Heartbeat(workerCtx, lease, 30*time.Second); err != nil {
		t.Fatalf("worker heartbeat: %v", err)
	}

	// Context renewal signs the same frozen scope and registers it for the lease.
	signer := investigation.ContextSigner{Transit: newNativeTestTransit(t), Key: "native-incident-key"}
	names := tools.Names(defs)
	jobToken, err := signer.IssueRegistered(workerCtx, workerJobs, job, lease, "platform-job-api", nativeAssistantWorkload, names)
	if err != nil {
		t.Fatalf("renew job context: %v", err)
	}
	claims, err := signer.VerifyContext(workerCtx, jobToken, "platform-job-api", nativeAssistantWorkload)
	if err != nil {
		t.Fatalf("verify renewed context: %v", err)
	}
	if claims.Subject != account.subject || claims.Scope.Cluster != f.clusterUID || claims.SchemaVersion != "invocation-context/v2" {
		t.Fatalf("renewed claims = %+v", claims)
	}
	authorizedJob, toolLease, err := workerJobs.Authenticate(workerCtx, claims, job.ToolCatalogDigest)
	if err != nil {
		t.Fatalf("authenticate renewed context without a session: %v", err)
	}

	// A real controlled tool read through the SP05 handler. The nested handler has
	// no browser session, so the read only succeeds because the persisted v2 native
	// authority is propagated into the tool execution context.
	handlers := &httpapi.SP05Handlers{Pool: f.apiPool, Enabled: true}
	api := httpapi.InvestigationTools{SP04: &httpapi.SP04Handlers{Pool: f.apiPool}, SP05: handlers}
	allocated, err := workerJobs.AllocateTool(workerCtx, toolLease, "get_incident_context", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("allocate tool: %v", err)
	}
	call := investigation.Call{Claims: &claims, StepID: allocated.StepID, ContextID: claims.ContextID, JTI: allocated.JTI, Name: "get_incident_context", ArgsDigest: investigation.ArgumentsDigest(json.RawMessage(`{}`)), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 16384}}
	if _, err = workerJobs.BeginCall(workerCtx, toolLease, call); err != nil {
		t.Fatalf("begin tool call: %v", err)
	}
	result, err := api.Invoke(workerCtx, authorizedJob, "get_incident_context", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("controlled incident read: %v", err)
	}
	data, _ := json.Marshal(result.Data)
	var read struct {
		Incident struct {
			IncidentID          string `json:"incidentId"`
			ResourceCanonicalID string `json:"resourceCanonicalId"`
		} `json:"incident"`
		Findings struct {
			Items []struct {
				FindingID string `json:"findingId"`
			} `json:"items"`
		} `json:"findings"`
	}
	if json.Unmarshal(data, &read) != nil || read.Incident.IncidentID != incidentID.String() || read.Incident.ResourceCanonicalID != canonical || len(read.Findings.Items) != len(seed.Findings) {
		t.Fatalf("controlled read result = %s", data)
	}
	envelope, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := workerJobs.CompleteToolStep(workerCtx, toolLease, allocated.StepID, envelope, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(envelope))})
	if err != nil {
		t.Fatalf("complete tool step: %v", err)
	}
	if len(committed) == 0 {
		t.Fatal("tool result was not persisted")
	}

	// The Evidence tool reads the Incident's linked, retained Evidence through the
	// same persisted native authority and the real evidence repository.
	evidenceID := f.newEvidence(t, seed, false, "change")
	evidenceArgs, err := json.Marshal(map[string]any{"timeRange": map[string]time.Time{
		"from": time.Now().UTC().Add(-10 * time.Minute),
		"to":   time.Now().UTC().Add(time.Minute),
	}})
	if err != nil {
		t.Fatal(err)
	}
	allocatedChanges, err := workerJobs.AllocateTool(workerCtx, toolLease, "get_recent_changes", evidenceArgs)
	if err != nil {
		t.Fatalf("allocate evidence tool: %v", err)
	}
	call = investigation.Call{Claims: &claims, StepID: allocatedChanges.StepID, ContextID: claims.ContextID, JTI: allocatedChanges.JTI, Name: "get_recent_changes", ArgsDigest: investigation.ArgumentsDigest(evidenceArgs), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 16384}}
	if _, err = workerJobs.BeginCall(workerCtx, toolLease, call); err != nil {
		t.Fatalf("begin evidence call: %v", err)
	}
	changes, err := api.Invoke(workerCtx, authorizedJob, "get_recent_changes", evidenceArgs)
	if err != nil {
		t.Fatalf("controlled evidence read: %v", err)
	}
	if !slices.Contains(changes.EvidenceRefs, evidenceID.String()) {
		t.Fatalf("evidence read refs = %v want %s", changes.EvidenceRefs, evidenceID)
	}
	evidenceEnvelope, err := json.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workerJobs.CompleteToolStep(workerCtx, toolLease, allocatedChanges.StepID, evidenceEnvelope, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(evidenceEnvelope))}); err != nil {
		t.Fatalf("complete evidence step: %v", err)
	}

	proposal := []byte(`{"schemaVersion":"investigation-result/v1","status":"unresolved","summary":"fixture: native v2 incident investigation completed","evidenceRefs":[],"candidateUpdates":[],"actionPlans":[],"partial":true,"degradedSources":["fixture"]}`)
	if err = workerJobs.Complete(workerCtx, toolLease, proposal); err != nil {
		t.Fatalf("complete native v2 job: %v", err)
	}
	final, err := (investigation.Repository{Pool: f.apiPool}).Get(account.ctx, f.tenantID, job.JobID)
	if err != nil {
		t.Fatalf("owner read after completion: %v", err)
	}
	if final.State != "partial" || len(final.Result) == 0 {
		t.Fatalf("final job = %+v", final)
	}
	steps, err := (investigation.Repository{Pool: f.apiPool}).ReadSteps(account.ctx, f.tenantID, job.JobID, account.subject)
	if err != nil || len(steps) != 2 {
		t.Fatalf("owner step read = %+v (%v)", steps, err)
	}
	for _, step := range steps {
		if step.State != "succeeded" {
			t.Fatalf("persisted step = %+v", step)
		}
	}
	var grants, bindings int
	if err := f.db.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM identity.role_grants WHERE tenant_id=$1), (SELECT count(*) FROM platform.role_bindings WHERE tenant_id=$1)`, f.tenantID).Scan(&grants, &bindings); err != nil {
		t.Fatal(err)
	}
	if grants == 0 || bindings != 0 {
		t.Fatalf("authority source check: grants=%d legacyBindings=%d", grants, bindings)
	}
}

// A restarted Worker takes over an expired lease without re-admitting the Job, and
// the old lease can no longer publish.
func TestNativeIncidentV2JobWorkerTakeover(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	incidentID, revision := seed.IncidentID, seed.Revision
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)
	defs, err := tools.CatalogFor("investigation-job/v2", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	job := f.newV2Job(t, account, incidentID, revision, tools.Digest(defs))
	workerJobs := investigation.Repository{Pool: f.workerPool, AssistantEnabled: true}
	workerCtx := context.Background()
	first, err := workerJobs.Claim(workerCtx, f.tenantID, "native-incident-worker", time.Second)
	if err != nil || first.JobID != job.JobID {
		t.Fatalf("first claim = %+v (%v)", first, err)
	}
	time.Sleep(2 * time.Second)
	second, err := workerJobs.Claim(workerCtx, f.tenantID, "native-incident-worker-restarted", 30*time.Second)
	if err != nil {
		t.Fatalf("takeover claim: %v", err)
	}
	if second.JobID != job.JobID || second.Generation <= first.Generation {
		t.Fatalf("takeover lease = %+v (first %+v)", second, first)
	}
	if err = workerJobs.Heartbeat(workerCtx, first, 30*time.Second); !errors.Is(err, investigation.ErrLease) {
		t.Fatalf("expired lease heartbeat = %v", err)
	}
	if err = workerJobs.Heartbeat(workerCtx, second, 30*time.Second); err != nil {
		t.Fatalf("takeover heartbeat: %v", err)
	}
}

// Every revocation shape must fail the queued Job instead of reviving it, and a
// historical row without provenance is denied rather than silently treated as
// legacy.
func TestNativeIncidentV2JobAuthorityWithdrawal(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	incidentID, revision := seed.IncidentID, seed.Revision
	defs, err := tools.CatalogFor("investigation-job/v2", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	workerCtx := context.Background()
	workerJobs := investigation.Repository{Pool: f.workerPool, AssistantEnabled: true}
	denyClaim := func(label string) {
		t.Helper()
		if _, err := workerJobs.Claim(workerCtx, f.tenantID, "native-incident-worker", 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
			t.Fatalf("%s claim = %v", label, err)
		}
	}

	// Revocation after admission: the Job fails and is never claimable again.
	revoked := f.newAccount(t, `"apps"`)
	revokedJob := f.newV2Job(t, revoked, incidentID, revision, tools.Digest(defs))
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='integration revoke',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, revoked.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, revoked.userID); err != nil {
		t.Fatal(err)
	}
	denyClaim("revoked")
	if state, code := f.jobState(t, revokedJob.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("revoked job = %s/%s", state, code)
	}

	// Re-grant at a higher version must not resurrect the old Job.
	regranted := f.newAccount(t, `"apps"`)
	regrantedJob := f.newV2Job(t, regranted, incidentID, revision, tools.Digest(defs))
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='integration regrant' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, regranted.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, regranted.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,'operator',$4,2,'active')`, f.tenantID, regranted.userID, uuid.New(), `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["apps"]}`); err != nil {
		t.Fatal(err)
	}
	denyClaim("re-granted")
	if state, code := f.jobState(t, regrantedJob.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("re-granted job = %s/%s", state, code)
	}

	// Disabled account.
	disabled := f.newAccount(t, `"apps"`)
	disabledJob := f.newV2Job(t, disabled, incidentID, revision, tools.Digest(defs))
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET status='disabled',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, disabled.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, disabled.userID); err != nil {
		t.Fatal(err)
	}
	denyClaim("disabled")
	if state, code := f.jobState(t, disabledJob.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("disabled job = %s/%s", state, code)
	}

	// A narrowed grant no longer reproduces the frozen digest.
	narrowed := f.newAccount(t, `"apps"`)
	narrowedJob := f.newV2Job(t, narrowed, incidentID, revision, tools.Digest(defs))
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET scope=$3 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, narrowed.userID, `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["private"]}`); err != nil {
		t.Fatal(err)
	}
	denyClaim("narrowed")
	if state, code := f.jobState(t, narrowedJob.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("narrowed job = %s/%s", state, code)
	}

	// Cross-tenant claim never sees the job.
	otherTenant := uuid.Must(uuid.NewV7())
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'ninc-b','Native Incident B')`, otherTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := workerJobs.Claim(workerCtx, otherTenant, "native-incident-worker", 30*time.Second); !errors.Is(err, investigation.ErrNoJob) {
		t.Fatalf("cross-tenant claim = %v", err)
	}

	// Historical provenance: a row without source/user/version may only be read as
	// legacy. For a native subject that means denial, never a silent native-to-
	// legacy fallback.
	historical := f.newAccount(t, `"apps"`)
	historicalJob := f.newV2Job(t, historical, incidentID, revision, tools.Digest(defs))
	if _, err := f.db.ExecContext(f.ctx, `UPDATE investigation.jobs SET authorization_source=NULL,authorization_user_id=NULL,authorization_version=NULL WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, historicalJob.JobID); err != nil {
		t.Fatal(err)
	}
	denyClaim("historical row")

	// Source revision change invalidates the frozen digest even for a live account.
	sourceShift := f.newAccount(t, `"apps"`)
	sourceJob := f.newV2Job(t, sourceShift, incidentID, revision, tools.Digest(defs))
	if _, err := f.db.ExecContext(f.ctx, `UPDATE platform.source_registrations SET revision=revision+1 WHERE tenant_id=$1`, f.tenantID); err != nil {
		t.Fatal(err)
	}
	denyClaim("source revision")
	if state, code := f.jobState(t, sourceJob.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("source revision job = %s/%s", state, code)
	}
}

// A revocation committed after the lease was granted must stop the running Job at
// the next fence (heartbeat and completion), never letting it publish.
func TestNativeIncidentV2JobMidFlightRevocation(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	incidentID, revision := seed.IncidentID, seed.Revision
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)
	defs, err := tools.CatalogFor("investigation-job/v2", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	job := f.newV2Job(t, account, incidentID, revision, tools.Digest(defs))
	workerCtx := context.Background()
	workerJobs := investigation.Repository{Pool: f.workerPool, AssistantEnabled: true}
	lease, err := workerJobs.Claim(workerCtx, f.tenantID, "native-incident-worker", 30*time.Second)
	if err != nil || lease.JobID != job.JobID {
		t.Fatalf("claim = %+v (%v)", lease, err)
	}
	if err := workerJobs.Heartbeat(workerCtx, lease, 30*time.Second); err != nil {
		t.Fatalf("heartbeat before revocation: %v", err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='mid-flight' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if err := workerJobs.Heartbeat(workerCtx, lease, 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("heartbeat after revocation = %v", err)
	}
	proposal := []byte(`{"schemaVersion":"investigation-result/v1","status":"unresolved","summary":"must not publish","evidenceRefs":[],"candidateUpdates":[],"actionPlans":[],"partial":true,"degradedSources":[]}`)
	if err := workerJobs.Complete(workerCtx, lease, proposal); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("completion after revocation = %v", err)
	}
	state, _ := f.jobState(t, job.JobID)
	if state != "running" {
		t.Fatalf("revoked job state = %s", state)
	}
}
