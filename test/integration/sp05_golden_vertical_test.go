package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/app"
	"ops-platform/internal/archive"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/contract"
	protect "ops-platform/internal/crypto"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	kube "ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/integrations/redfish"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/policy"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"ops-platform/internal/resourcestore"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type goldenNativeSnapshot struct {
	GVR     kube.GVR         `json:"gvr"`
	Objects []map[string]any `json:"objects"`
}
type sp05GoldenInput struct {
	Scenario        string                     `json:"scenario"`
	Tenant          string                     `json:"tenant"`
	Cluster         string                     `json:"clusterUid"`
	Namespace       string                     `json:"namespace"`
	Clock           time.Time                  `json:"inputClock"`
	ConflictClock   time.Time                  `json:"conflictClock"`
	Sources         map[string]string          `json:"sourceBindings"`
	Snapshots       []goldenNativeSnapshot     `json:"nativeSnapshots"`
	Conflict        goldenNativeSnapshot       `json:"conflictSnapshot"`
	Routes          map[string]json.RawMessage `json:"nativeRedfishRoutes"`
	ConflictRoutes  map[string]json.RawMessage `json:"conflictRedfishRoutes"`
	Kernel          json.RawMessage            `json:"nativeKernelRows"`
	RuntimeConflict json.RawMessage            `json:"runtimeConflictRows"`
}
type sp05GoldenExpected struct {
	Primary              string            `json:"primaryCanonicalId"`
	HardwareRoot         string            `json:"hardwareRootCanonicalId"`
	NodeStatus           map[string]string `json:"nodeStatus"`
	NodePartial          map[string]bool   `json:"nodePartial"`
	NodeCandidate        string            `json:"nodeCandidateType"`
	Entities             []string          `json:"nativeEntityCanonicalIds"`
	Rules                []string          `json:"semanticFindingRules"`
	Relations            []string          `json:"relationKinds"`
	FindingCount         int               `json:"findingCount"`
	IncidentCount        int               `json:"incidentCount"`
	EvidenceCount        int               `json:"evidenceCount"`
	PrimaryEvidenceCount int               `json:"primaryEvidenceCount"`
	DerivedEvidenceCount int               `json:"derivedEvidenceCount"`
	FindingState         string            `json:"findingState"`
	IncidentState        string            `json:"incidentState"`
	Candidate            string            `json:"candidateType"`
	Direct               []string          `json:"impactDirect"`
	Indirect             []string          `json:"impactIndirect"`
	Variants             map[string]struct {
		Status  string `json:"status"`
		Partial bool   `json:"partial"`
	} `json:"variantSemantics"`
}

func TestSP05FrozenNonvirtualGoldenVerticalAndReplay(t *testing.T) {
	for _, scenario := range []string{"dimm-failure", "pvc-csi-failure", "node-failure"} {
		for _, mode := range []string{"valid", "missing", "conflict", "degraded"} {
			t.Run(scenario+"/"+mode, func(t *testing.T) { sp05GoldenVertical(t, scenario, mode) })
		}
	}
}

func TestSP05FrozenNodeHardwareUpstreamVerticalAndReplay(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "conflict", "degraded"} {
		t.Run(mode, func(t *testing.T) { sp05GoldenVertical(t, "node-hardware-upstream", mode) })
	}
}

func sp05GoldenVertical(t *testing.T, scenario, mode string, reviewFence ...bool) {
	input, expected := loadSP05Golden(t, scenario)
	if len(reviewFence) > 1 && reviewFence[1] {
		input = sp06CurrentGoldenInput(t, input)
	}
	ctx, db, pool, b := sp05Database(t, sp05Seed{Tenant: uuid.MustParse(input.Tenant), Source: uuid.MustParse(input.Sources["kubernetes"]), ClusterUID: input.Cluster, Namespace: input.Namespace, Backend: "sp05-golden-kubernetes"})
	clock := input.Clock
	archives := sp05GoldenArchive(t, ctx, pool, b.TenantID)
	var registryKey ed25519.PrivateKey
	registry, trust := sp05GoldenRegistry(t, ctx, db, pool, b.TenantID, &registryKey)
	cluster := app.SP04Cluster{Tenant: input.Tenant, ClusterUID: input.Cluster, SourceID: b.SourceID.String(), SourceRevision: 1, BackendLogicalID: "sp05-golden-kubernetes"}
	required := []kube.GVR{}
	for _, s := range input.Snapshots {
		required = append(required, s.GVR)
	}
	g := graph.NewWithClock(input.Tenant, input.Cluster, "frozen-golden-worker", required, func() time.Time { return clock })
	g.SetOwner(1, clock.Add(10*time.Minute))
	repo := resourcestore.Repository{Pool: pool, ExpectedRevision: 1, BackendLogicalID: cluster.BackendLogicalID}
	// The projection/identity write, Inspector and reducer below are exactly the
	// entry points used by StartSP04Worker, not substitute business-table writes.
	applyNative := func(native goldenNativeSnapshot, inspect bool, publish ...bool) {
		objects := []unstructured.Unstructured{}
		for _, raw := range native.Objects {
			object := unstructured.Unstructured{Object: raw}
			objects = append(objects, *object.DeepCopy())
		}
		snapshot := kube.Snapshot{GVR: native.GVR, Objects: objects, State: kube.GVRState{LastListCompletedAt: clock, LastConnectivityProbeAt: clock, WatchConnected: true, WatchContinuous: true}}
		if _, err := repo.SyncSnapshot(ctx, input.Tenant, cluster.SourceID, input.Cluster, "", "core", snapshot, map[string]string{}); err != nil {
			t.Fatal(err)
		}
		filtered, err := repo.FilterSnapshot(ctx, input.Tenant, cluster.SourceID, input.Cluster, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if len(publish) == 0 || publish[0] {
			if err := g.Replace(ctx, 1, filtered); err != nil {
				t.Fatal(err)
			}
		}
		if inspect {
			if err := app.InspectSP05Snapshot(ctx, cluster, filtered, archives); err != nil {
				t.Fatal(err)
			}
		}
	}
	bindings := map[string]evidence.Binding{}
	kubernetesBinding, bindingErr := (evidence.Repository{Pool: pool}).RegisteredBinding(ctx, evidence.Binding{Tenant: input.Tenant, SourceID: cluster.SourceID, SourceType: "kubernetes", Revision: 1, BackendLogicalID: cluster.BackendLogicalID})
	if bindingErr != nil {
		t.Fatal(bindingErr)
	}
	bindings["kubernetes"] = kubernetesBinding
	for _, kind := range []string{"redfish", "victorialogs"} {
		id := input.Sources[kind]
		mapping := datascope.Mapping{RequiredLabels: map[string]string{"tenant": input.Tenant}, Scopes: map[string][]string{"cluster": {input.Cluster}, "namespace": {input.Namespace}}}
		encoded, _ := json.Marshal(mapping)
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,allowed_schemas,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,$4,$4,'openbao://golden/readonly',ARRAY['finding-envelope/v2'],$5,$6)`, b.TenantID, id, b.ClusterID, kind, "sp05-golden-"+kind, encoded); err != nil {
			t.Fatal(err)
		}
		bindings[kind] = evidence.Binding{Tenant: input.Tenant, SourceID: id, SourceType: kind, Revision: 1, BackendLogicalID: "sp05-golden-" + kind, ScopeMapping: mapping}
	}
	hardware := func(routes map[string]json.RawMessage, missing bool, publish ...bool) {
		hardwareRepo := resourcestore.Repository{Pool: pool, ExpectedRevision: 1, BackendLogicalID: bindings["redfish"].BackendLogicalID}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Error("non-read Gofish request")
				w.WriteHeader(405)
				return
			}
			body, ok := routes[r.URL.Path]
			if !ok || (missing && strings.HasSuffix(r.URL.Path, "/MemoryMetrics")) {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		}))
		defer server.Close()
		inventory, err := redfish.Collect(ctx, redfish.Config{Endpoint: server.URL, Client: server.Client(), Tenant: input.Tenant, Scope: input.Cluster, SourceID: bindings["redfish"].SourceID, Diagnostics: true, Clock: func() time.Time { return clock }})
		if err != nil {
			t.Fatal(err)
		}
		if !missing && inventory.Partial {
			t.Fatalf("frozen native hardware input incomplete: %v", inventory.Warnings)
		}
		if err := hardwareRepo.SaveHardware(ctx, bindings["redfish"], inventory.Entities); err != nil {
			t.Fatal(err)
		}
		if len(publish) == 0 || publish[0] {
			if err := g.ReplaceHardware(ctx, bindings["redfish"].SourceID, inventory.Entities, clock); err != nil {
				t.Fatal(err)
			}
		}
		relations, err := hardwareRepo.HardwareRelations(ctx, bindings["redfish"], inventory.Entities)
		if err != nil {
			t.Fatal(err)
		}
		if len(publish) == 0 || publish[0] {
			if err := g.ReplaceExternalSource(1, bindings["redfish"].SourceID, relations); err != nil {
				t.Fatal(err)
			}
		}
		for _, fact := range inventory.DiagnosticFacts {
			c := finding.FindingCandidate{ResourceCanonicalID: fact.ResourceCanonicalID, RuleID: fact.RuleID, RuleFamily: "hardware", NormalizedSymptom: fact.Symptom, State: fact.State, NativeIdentity: fact.NativeURI + "/" + evidence.Digest(fact.Data), IndependenceGroup: fact.NativeURI, ObservedAt: fact.ObservedAt, TimeReliable: true, QueryTemplateVersion: "sp05-hardware/v1", Data: fact.Data}
			if err := app.SubmitSP05Candidate(ctx, archives, bindings["redfish"], c); err != nil {
				t.Fatal(err)
			}
		}
	}

	var logRows []map[string]any
	if len(input.Kernel) > 0 && json.Unmarshal(input.Kernel, &logRows) != nil {
		t.Fatal("frozen kernel rows invalid")
	}
	if scenario == "node-hardware-upstream" && mode == "conflict" {
		var extra []map[string]any
		if json.Unmarshal(input.RuntimeConflict, &extra) != nil {
			t.Fatal("frozen conflict rows invalid")
		}
		logRows = append(logRows, extra...)
	}
	logServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/select/logsql/query" {
			w.WriteHeader(405)
			return
		}
		query := r.URL.Query().Get("query")
		for _, term := range []string{`_TRANSPORT="kernel"`, `tenant="` + input.Tenant + `"`, `cluster="` + input.Cluster + `"`, `uid="node-a"`, `namespace=""`} {
			if !strings.Contains(query, term) {
				t.Errorf("native query lacks binding: %s", term)
				w.WriteHeader(403)
				return
			}
		}
		for _, row := range logRows {
			native := map[string]any{}
			for k, v := range row {
				native[k] = v
			}
			native["tenant"] = input.Tenant
			native["cluster"] = input.Cluster
			native["uid"] = "node-a"
			native["namespace"] = ""

			json.NewEncoder(w).Encode(native)
		}
	}))
	defer logServer.Close()
	logBinding := bindings["victorialogs"]
	logBinding.Endpoint = logServer.URL
	logRepo := evidence.Repository{Pool: pool}
	logAdapter, err := evidence.NewVictoria("victorialogs", logBinding, logServer.Client(), logRepo.Authorize)
	if err != nil {
		t.Fatal(err)
	}
	kernelQuery := evidence.Query{ResourceCanonicalID: expected.Primary, Template: "node-kernel-logs/v1", From: input.Clock.Add(-time.Minute), To: input.Clock, Limit: 64, MaxBytes: 64 << 10, TimeoutMillis: 3000, Scope: graph.Scope{Tenant: input.Tenant, Cluster: input.Cluster, ClusterScoped: true, Namespaces: []string{input.Namespace}, AuthorizationRevision: "golden-native-query/v3"}}
	if (scenario == "node-failure" && mode != "missing") || scenario == "node-hardware-upstream" {
		if err := logRepo.VerifyVictoria(ctx, logAdapter, kernelQuery); err != nil {
			t.Fatal(err)
		}
	}
	nativeKernel := func() {
		result, err := logAdapter.Query(ctx, kernelQuery)
		if err != nil || result.Partial || len(result.Evidence) != 1 {
			t.Fatalf("actual scoped native log query: partial=%t count=%d err=%v", result.Partial, len(result.Evidence), err)
		}
		if err := app.SubmitSP05KernelEvidence(ctx, archives, logBinding, result.Evidence[0]); err != nil {
			t.Fatal(err)
		}
	}

	authorities := []graph.SourceAuthority{}
	for _, kind := range []string{"kubernetes", "redfish"} {
		if binding, ok := bindings[kind]; ok {
			authorities = append(authorities, graph.SourceAuthority{SourceRegistrationID: binding.SourceID, Revision: binding.Revision, ScopeDigest: evidence.BindingScopeDigest(binding)})
		}
	}
	reducer := app.SP05Reducer{Pool: pool, Archive: archives, Cluster: cluster, Handler: graph.InternalHandler{Graph: g, SourceAuthorities: authorities}, Registry: registry, Trust: trust, Clock: func() time.Time { return clock }}
	var semanticBaseline string
	for replay := 0; replay < 2; replay++ {
		clock = input.Clock
		// Old transport events are replayed into ingestion, while the current native
		// fact source remains its latest complete observation. A real GET cannot
		// rewind a Node/PVC or hardware source to an old event's historical state.
		// Re-publish the latest complete state below; never substitute a result row.
		publishInitial := !(replay == 1 && mode == "conflict" && scenario != "node-hardware-upstream")
		for _, snapshot := range input.Snapshots {
			inspect := !(mode == "missing" && snapshot.GVR.Resource == "events")
			applyNative(snapshot, inspect, publishInitial)
		}
		if scenario == "dimm-failure" || scenario == "node-hardware-upstream" {
			hardware(input.Routes, mode == "missing", publishInitial)
		}
		if (scenario == "node-failure" && mode != "missing") || scenario == "node-hardware-upstream" {
			nativeKernel()
		}
		if mode == "conflict" && scenario != "node-hardware-upstream" {
			clock = input.ConflictClock
			if scenario == "dimm-failure" {
				hardware(input.ConflictRoutes, false)
			} else {
				applyNative(input.Conflict, true)
			}
		}
		if mode == "degraded" {
			g.SetSourceDegraded("fixture-required-source", "fixture_required_source_unavailable")
		}
		if err := archives.MaintenancePass(ctx, map[string]evidence.Adapter{}); err != nil {
			t.Fatal(err)
		}
		if err := (finding.Service{Pool: pool}).RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
			t.Fatal(err)
		}
		if replay == 1 {
			// Advance only the operational scheduling marker, as though the next
			// post-check tick were due. The second replay must run the actual reducer;
			// it may not pass merely because the 30-second poll gate suppressed it.
			if _, err := db.ExecContext(ctx, `UPDATE incident.records SET last_rca_checked_at=NULL WHERE tenant_id=$1`, b.TenantID); err != nil {
				t.Fatal(err)
			}
		}
		if err := reducer.Pass(ctx); err != nil {
			t.Fatal(err)
		}
		if err := archives.ReconcileTenantProtection(ctx, b.TenantID); err != nil {
			t.Fatal(err)
		}
		semantic := assertSP05Golden(t, ctx, db, archives, input, expected, mode)
		if replay == 0 {
			semanticBaseline = semantic
		} else if semantic != semanticBaseline {
			t.Fatalf("frozen replay changed semantic IDs/revisions/roots/impact: first=%s second=%s", semanticBaseline, semantic)
		}
	}
	if len(reviewFence) > 0 && reviewFence[0] {
		sp05GoldenReviewFences(t, ctx, db, pool, archives, registry, trust, g, cluster, b, scopeForGolden(input), expected.Primary)
	}
	if len(reviewFence) > 1 && reviewFence[1] {
		sp06GoldenInvestigationValidator(t, ctx, db, pool, archives, trust, g, b, expected.Primary, registryKey)
	}
	t.Logf("%s/%s: fixed clock, admitted native adapters -> actual identity/Graph and unified ingestion -> PostgreSQL reducer -> signed published Recipe -> real immutable OpenBao/SeaweedFS Evidence -> append-only RCA and bounded Impact, twice replay; protocol Fixture, no physical BMC/Node failure claim", scenario, mode)
}

func loadSP05Golden(t *testing.T, scenario string) (sp05GoldenInput, sp05GoldenExpected) {
	t.Helper()
	version := "v3"
	if scenario == "node-failure" {
		version = "v4"
	}
	if scenario == "node-hardware-upstream" {
		version = "v1"
	}
	dir := filepath.Join("../fixtures/incidents", scenario, version)
	var manifest struct {
		Files []struct {
			Path string `json:"path"`
			SHA  string `json:"sha256"`
		} `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("frozen manifest unavailable")
	}
	values := map[string][]byte{}
	for _, file := range manifest.Files {
		raw, err := os.ReadFile(filepath.Join(dir, file.Path))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != file.SHA {
			t.Fatal("frozen native input/expectation changed")
		}
		values[file.Path] = raw
	}
	var input sp05GoldenInput
	var expected sp05GoldenExpected
	if json.Unmarshal(values["input.json"], &input) != nil || json.Unmarshal(values["expected.json"], &expected) != nil {
		t.Fatal("frozen fixture invalid")
	}
	if scenario == "node-hardware-upstream" {
		expected.FindingState = "firing"
		expected.IncidentState = "open"
		expected.Candidate = expected.NodeCandidate
	}
	return input, expected
}

func sp05GoldenArchive(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID) *evidence.ArchiveService {
	t.Helper()
	for _, key := range []string{"SP03_TEST_OPENBAO_URL", "SP03_TEST_OPENBAO_CA_FILE", "SP03_TEST_OPENBAO_TOKEN"} {
		if os.Getenv(key) == "" {
			t.Fatalf("required live archive dependency missing: %s", key)
		}
	}
	ca, err := os.ReadFile(os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("SP03_TEST_OPENBAO_URL"), ServerName: "localhost", CACertBundle: ca, Token: os.Getenv("SP03_TEST_OPENBAO_TOKEN"), ServiceDomain: "ops-system.svc.cluster.local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bao.ConfigureTransit(ctx); err != nil {
		t.Fatal(err)
	}
	bucket := "sp05-golden-" + uuid.NewString()[:8]
	fixture := newRoleTenantS3Fixture(t, []uuid.UUID{tenant}, bucket)
	credentials, err := os.ReadFile(fixture.CredentialFile)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := s3.NewTenantClient(s3.Config{Endpoint: fixture.Endpoint, CACertBundle: fixture.CA, Bucket: bucket}, credentials)
	if err != nil {
		t.Fatal(err)
	}
	store, err := archive.NewStore(backend, 128<<10)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := protect.NewTransitProtector(bao, "evidence-archive")
	if err != nil {
		t.Fatal(err)
	}
	return &evidence.ArchiveService{Pool: pool, Store: store, Protector: protector, BackendLogicalID: "sp05-golden-archive"}
}

func sp05GoldenRegistry(t *testing.T, ctx context.Context, db *sql.DB, pool *pgxpool.Pool, tenant uuid.UUID, capture ...*ed25519.PrivateKey) (*configregistry.Service, configregistry.Ed25519TrustStore) {
	t.Helper()
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, pool.Config().ConnConfig.ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(apiPool.Close)
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name) VALUES($1,$2,'golden-admin','platform_admin')`, tenant, uuid.New()); err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	if len(capture) == 1 {
		*capture[0] = priv
	}
	trust := configregistry.Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{"integration-key": pub}}
	compiler, err := policy.NewBundleCompiler(trust)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := configregistry.NewService(apiPool, trust, compiler)
	if err != nil {
		t.Fatal(err)
	}
	actor := auth.RequestContext{TenantID: tenant, Subject: "golden-admin", Roles: []auth.Role{auth.PlatformAdmin}}
	for _, name := range []string{"dimm-failure", "pvc-csi-failure", "node-failure"} {
		recipe, _ := rca.Builtin(name)
		content, _ := json.Marshal(recipe)
		draft, err := createRegistryDraft(ctx, apiPool, registry, actor, configregistry.DraftCommand{Kind: configregistry.KindRecipe, LogicalName: name, Content: content})
		if err != nil {
			t.Fatal(err)
		}
		version, err := signAndPublish(ctx, apiPool, registry, actor, draft, 1, priv)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := activateRegistryVersion(ctx, apiPool, registry, actor, version, configregistry.KindRecipe, name, configregistry.Scope{Type: configregistry.ScopeTenant}, 0); err != nil {
			t.Fatal(err)
		}
	}
	// Worker consumers run under their actual database role as in production.
	workerRegistry, err := configregistry.NewService(pool, trust, compiler)
	if err != nil {
		t.Fatal(err)
	}
	return workerRegistry, trust
}

func assertSP05Golden(t *testing.T, ctx context.Context, db *sql.DB, archives *evidence.ArchiveService, input sp05GoldenInput, expected sp05GoldenExpected, mode string) string {
	t.Helper()
	tenant := uuid.MustParse(input.Tenant)
	var findings, incidents, evidenceCount int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM finding.records WHERE tenant_id=$1),(SELECT count(*) FROM incident.records WHERE tenant_id=$1),(SELECT count(*) FROM platform.evidence_metadata WHERE tenant_id=$1)`, tenant).Scan(&findings, &incidents, &evidenceCount); err != nil {
		t.Fatal(err)
	}
	if mode == "valid" || (mode == "degraded" && input.Scenario != "node-hardware-upstream") {
		if findings != expected.FindingCount || incidents != expected.IncidentCount || evidenceCount != expected.EvidenceCount {
			t.Fatalf("frozen counts finding=%d incident=%d evidence=%d expected=%d/%d/%d", findings, incidents, evidenceCount, expected.FindingCount, expected.IncidentCount, expected.EvidenceCount)
		}
	}
	if mode == "valid" || (mode == "degraded" && input.Scenario != "node-hardware-upstream") {
		ruleRows, err := db.QueryContext(ctx, `SELECT payload->>'ruleId',lifecycle_state FROM finding.records WHERE tenant_id=$1 ORDER BY payload->>'ruleId'`, tenant)
		if err != nil {
			t.Fatal(err)
		}
		actualRules := []string{}
		for ruleRows.Next() {
			var rule, state string
			if err := ruleRows.Scan(&rule, &state); err != nil {
				t.Fatal(err)
			}
			actualRules = append(actualRules, rule)
			if state != expected.FindingState {
				t.Fatalf("frozen Finding state=%s expected=%s", state, expected.FindingState)
			}
		}
		if err := ruleRows.Err(); err != nil {
			t.Fatal(err)
		}
		ruleRows.Close()
		expectedRules := slices.Clone(expected.Rules)
		slices.Sort(actualRules)
		slices.Sort(expectedRules)
		if !slices.Equal(actualRules, expectedRules) {
			t.Fatalf("frozen Finding rules actual=%v expected=%v", actualRules, expectedRules)
		}
		var primaryCount, derivedCount, wrongIncidentState int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE jsonb_array_length(metadata->'derivationEvidenceRefs')=0),count(*) FILTER(WHERE jsonb_array_length(metadata->'derivationEvidenceRefs')>0) FROM platform.evidence_metadata WHERE tenant_id=$1`, tenant).Scan(&primaryCount, &derivedCount); err != nil {
			t.Fatal(err)
		}
		if primaryCount != expected.PrimaryEvidenceCount || derivedCount != expected.DerivedEvidenceCount {
			t.Fatalf("frozen independent/derived Evidence counts=%d/%d expected=%d/%d", primaryCount, derivedCount, expected.PrimaryEvidenceCount, expected.DerivedEvidenceCount)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM incident.records WHERE tenant_id=$1 AND state<>$2`, tenant, expected.IncidentState).Scan(&wrongIncidentState); err != nil || wrongIncidentState > 0 {
			t.Fatalf("frozen Incident state mismatch: %v", err)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT canonical_id FROM platform.resource_entities WHERE tenant_id=$1 AND deleted_at IS NULL ORDER BY canonical_id`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	actualEntities := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		actualEntities = append(actualEntities, id)
	}
	rows.Close()
	slices.Sort(actualEntities)
	expectedEntities := slices.Clone(expected.Entities)
	slices.Sort(expectedEntities)
	if !slices.Equal(actualEntities, expectedEntities) {
		t.Fatalf("frozen native identity projection: actual=%v expected=%v", actualEntities, expected.Entities)
	}
	rows, err = db.QueryContext(ctx, `SELECT r.result FROM incident.records i JOIN incident.rca_revisions r ON r.tenant_id=i.tenant_id AND r.incident_id=i.incident_id AND r.revision=i.current_rca_revision WHERE i.tenant_id=$1 ORDER BY i.incident_id`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	revisions := []rca.Revision{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var revision rca.Revision
		if json.Unmarshal(raw, &revision) != nil {
			t.Fatal("actual RCA revision")
		}
		revisions = append(revisions, revision)
	}
	rows.Close()
	if len(revisions) != incidents || incidents == 0 {
		t.Fatalf("actual reducer did not publish all incident RCAs: %d/%d", len(revisions), incidents)
	}
	checkedPrimary := 0
	for _, revision := range revisions {
		raw, err := json.Marshal(revision)
		if err != nil {
			t.Fatal(err)
		}
		if err := contract.Validate("https://ops.local/schemas/rca-revision/v2", raw); err != nil {
			t.Fatalf("actual frozen RCA output Contract: %v", err)
		}
		for _, e := range revision.InputManifest.Evidence {
			data, ref, err := archives.Read(ctx, tenant, uuid.MustParse(e.EvidenceID))
			if err != nil || evidence.Digest(data) != e.ContentDigest || ref.PlaintextDigest != e.ContentDigest {
				t.Fatalf("RCA reference not actually readable/verifiable: %s %v", e.EvidenceID, err)
			}
			var retain bool
			if err := db.QueryRowContext(ctx, `WITH RECURSIVE closure AS (SELECT $2::uuid AS evidence_id UNION SELECT d.referrer_id FROM platform.evidence_dependencies d JOIN closure c ON d.dependency_id=c.evidence_id WHERE d.tenant_id=$1) SELECT EXISTS(SELECT 1 FROM closure c JOIN platform.evidence_retention_references r ON r.tenant_id=$1 AND r.evidence_id=c.evidence_id WHERE r.retain_until>=clock_timestamp()+interval '364 days')`, tenant, e.EvidenceID).Scan(&retain); err != nil || !retain || ref.Object.RetainUntil.Before(time.Now().Add(364*24*time.Hour)) {
				t.Fatalf("RCA 365-day dependency closure missing: %s %v", e.EvidenceID, err)
			}
		}
		required := expected.Variants[mode]
		result := revision.Result
		isNodeUpstream := input.Scenario == "node-hardware-upstream"
		if isNodeUpstream {
			if revision.InputManifest.ResourceCanonicalID != expected.Primary {
				continue
			}
			required.Status = expected.NodeStatus[mode]
			required.Partial = expected.NodePartial[mode]
		}
		if result.Status != required.Status || result.Partial != required.Partial {
			t.Fatalf("frozen %s status=%s partial=%t expected=%s/%t missing=%v degraded=%v", mode, result.Status, result.Partial, required.Status, required.Partial, result.Bundle.Missing, result.Bundle.DegradedSources)
		}
		if mode == "valid" {
			if len(result.Candidates) != 1 || result.Candidates[0].Type != expected.Candidate {
				t.Fatalf("actual Candidate mismatch: %+v", result.Candidates)
			}
			checkedPrimary++
			if isNodeUpstream && result.Candidates[0].ResourceCanonicalID != expected.HardwareRoot {
				t.Fatalf("frozen upstream hardware root mismatch: %+v", result.Candidates)
			}
			if isNodeUpstream {
				if !slices.Contains(result.Impact.DependencyOnly, expected.HardwareRoot) || len(result.Impact.DirectlyAffected) != 1 || !strings.HasSuffix(result.Impact.DirectlyAffected[0], "/Pod/pod-a") {
					t.Fatalf("native Node hardware dependency / Pod impact missing: %+v", result.Impact)
				}
				for _, kind := range []string{"component_of", "hosts", "scheduled_on"} {
					if !slices.ContainsFunc(result.Impact.Edges, func(e resource.Relation) bool {
						return e.Kind == kind && e.Provenance.SourceRegistrationID != "" && e.Provenance.RuleVersion != ""
					}) {
						t.Fatalf("native causal path provenance missing: %s", kind)
					}
				}
			}
			alias := func(ids []string) []string {
				out := []string{}
				for _, id := range ids {
					parsed, err := resource.ParseCanonicalID(id)
					if err != nil {
						t.Fatal(err)
					}
					name := parsed.StableID
					if parsed.Kind == "PhysicalServer" {
						name = "server-a"
					}
					out = append(out, name)
				}
				slices.Sort(out)
				return out
			}
			if !isNodeUpstream {
				direct, indirect := slices.Clone(expected.Direct), slices.Clone(expected.Indirect)
				slices.Sort(direct)
				slices.Sort(indirect)
				if !slices.Equal(alias(result.Impact.DirectlyAffected), direct) || !slices.Equal(alias(result.Impact.IndirectlyAffected), indirect) {
					t.Fatalf("frozen Impact mismatch direct=%v indirect=%v", result.Impact.DirectlyAffected, result.Impact.IndirectlyAffected)
				}
				for _, kind := range expected.Relations {
					if !slices.ContainsFunc(result.Impact.Edges, func(edge resource.Relation) bool {
						return edge.Kind == kind && edge.Provenance.SourceRegistrationID != "" && edge.Provenance.RuleVersion != ""
					}) {
						t.Fatalf("required native relation/provenance absent: %s", kind)
					}
				}
			}
		}

	}
	if mode == "valid" && checkedPrimary == 0 {
		t.Fatal("no frozen primary RCA was actually verified")
	}
	// Compare immutable business identities/current revision and semantic roots,
	// avoiding ciphertext randomness and monotonic collector graph generations.
	var semantic string
	if err := db.QueryRowContext(ctx, `SELECT jsonb_build_object('finding',(SELECT jsonb_agg(jsonb_build_array(finding_id,lifecycle_state,aggregate_revision) ORDER BY finding_id) FROM finding.records WHERE tenant_id=$1),'incident',(SELECT jsonb_agg(jsonb_build_array(incident_id,state,current_rca_revision) ORDER BY incident_id) FROM incident.records WHERE tenant_id=$1),'evidence',(SELECT jsonb_agg(evidence_id ORDER BY evidence_id) FROM platform.evidence_metadata WHERE tenant_id=$1))::text`, tenant).Scan(&semantic); err != nil {
		t.Fatal(err)
	}
	return semantic
}
