package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/gen/api"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/persistence"
)

func TestConfigRegistryPublishesActivatesRollsBackAndPreservesHistory(t *testing.T) {
	if testing.Short() || os.Getenv("SP03_TEST_DATABASE_URL") == "" {
		t.Skip("SP03_TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dbctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(dbctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply isolated PostgreSQL role bootstrap: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, dbctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("apply isolated platform migrations: %v", err)
	}
	tenantID, otherTenantID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	actor := auth.RequestContext{TenantID: tenantID, Subject: "registry-admin", Roles: []auth.Role{auth.PlatformAdmin}}
	otherActor := auth.RequestContext{TenantID: otherTenantID, Subject: "registry-admin-other", Roles: []auth.Role{auth.PlatformAdmin}}
	if _, err := db.ExecContext(dbctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES
		($1, 'registry-integration-a', 'Registry Integration A'), ($2, 'registry-integration-b', 'Registry Integration B')`, tenantID, otherTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(dbctx, `INSERT INTO platform.role_bindings (tenant_id, binding_id, subject, role_name) VALUES
		($1, $3, 'registry-admin', 'platform_admin'), ($2, $4, 'registry-admin-other', 'platform_admin')`, tenantID, otherTenantID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())); err != nil {
		t.Fatal(err)
	}
	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE api_runtime_role`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(dbctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	service, err := configregistry.NewService(pool, configregistry.Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{"integration-key": publicKey}})
	if err != nil {
		t.Fatal(err)
	}

	firstContent := []byte(`{"schemaVersion":"policy-registry/v1","name":"safe-default","modules":[{"id":"baseline","content":"package ops.policy\ndefault allow := false"}]}`)
	firstDraft, err := createRegistryDraft(ctx, pool, service, actor, configregistry.DraftCommand{
		Kind: configregistry.KindPolicy, LogicalName: "safe-default", Content: firstContent,
	})
	if err != nil {
		t.Fatalf("create policy draft: %v", err)
	}
	if _, err := publishRegistryDraft(ctx, pool, service, actor, firstDraft, 1, configregistry.PublicationSignature{
		KeyID: "integration-key", Signature: []byte("unsigned"),
	}); !errors.Is(err, configregistry.ErrInvalidSignature) {
		t.Fatalf("unsigned/invalid policy draft publish returned %v", err)
	}
	first, err := signAndPublish(ctx, pool, service, actor, firstDraft, 1, privateKey)
	if err != nil || first.VersionNumber != 1 {
		t.Fatalf("publish signed first policy version: version=%#v err=%v", first, err)
	}
	if _, err := updateRegistryDraft(ctx, pool, service, actor, firstDraft, 2, []byte(`{"schemaVersion":"policy-registry/v1","name":"safe-default","modules":[]}`)); !errors.Is(err, configregistry.ErrImmutable) {
		t.Fatalf("published draft content was mutable: %v", err)
	}
	if err := configregistry.ValidateContent(configregistry.KindPolicy, []byte(`{"schemaVersion":"recipe-registry/v1","name":"wrong","steps":[{"stepId":"collect","toolName":"query_metrics","input":{}}]}`)); err == nil {
		t.Fatal("policy registry accepted a recipe schema payload")
	}

	secondContent := []byte(`{"schemaVersion":"policy-registry/v1","name":"safe-default","modules":[{"id":"baseline","content":"package ops.policy\ndefault allow := false\n# revision 2"}]}`)
	secondDraft, err := createRegistryDraft(ctx, pool, service, actor, configregistry.DraftCommand{
		Kind: configregistry.KindPolicy, LogicalName: "safe-default", Content: secondContent,
	})
	if err != nil {
		t.Fatalf("create second policy draft: %v", err)
	}
	second, err := signAndPublish(ctx, pool, service, actor, secondDraft, 1, privateKey)
	if err != nil || second.VersionNumber != 2 {
		t.Fatalf("publish signed second policy version: version=%#v err=%v", second, err)
	}

	clusterID := uuid.Must(uuid.NewV7())
	if _, err := db.ExecContext(dbctx, `INSERT INTO platform.cluster_registrations (tenant_id, cluster_id, cluster_uid, display_name)
		VALUES ($1, $2, 'registry-cluster', 'Registry Cluster')`, tenantID, clusterID); err != nil {
		t.Fatal(err)
	}
	tenantScope := configregistry.Scope{Type: configregistry.ScopeTenant}
	firstActivation, err := activateRegistryVersion(ctx, pool, service, actor, first, configregistry.KindPolicy, "safe-default", tenantScope, 0)
	if err != nil || firstActivation.Revision != 1 {
		t.Fatalf("activate first policy: activation=%#v err=%v", firstActivation, err)
	}
	if _, err := activateRegistryVersion(ctx, pool, service, actor, second, configregistry.KindPolicy, "safe-default", tenantScope, 0); !errors.Is(err, configregistry.ErrScopeConflict) {
		t.Fatalf("duplicate initial scope activation returned %v", err)
	}
	secondActivation, err := activateRegistryVersion(ctx, pool, service, actor, second, configregistry.KindPolicy, "safe-default", tenantScope, 1)
	if err != nil || secondActivation.Revision != 2 {
		t.Fatalf("activate second policy: activation=%#v err=%v", secondActivation, err)
	}
	atFirst, err := service.ResolveActive(ctx, tenantID, configregistry.KindPolicy, "safe-default", tenantScope, firstActivation.ActivatedAt.Add(time.Microsecond))
	if err != nil || atFirst.VersionID != first.VersionID {
		t.Fatalf("historical resolution did not return first activation: version=%#v err=%v", atFirst, err)
	}
	atSecond, err := service.ResolveActive(ctx, tenantID, configregistry.KindPolicy, "safe-default", tenantScope, secondActivation.ActivatedAt.Add(time.Microsecond))
	if err != nil || atSecond.VersionID != second.VersionID {
		t.Fatalf("current resolution did not return second activation: version=%#v err=%v", atSecond, err)
	}
	thirdContent := []byte(`{"schemaVersion":"policy-registry/v1","name":"safe-default","modules":[{"id":"baseline","content":"package ops.policy\ndefault allow := false\n# revision 3"}]}`)
	thirdDraft, err := createRegistryDraft(ctx, pool, service, actor, configregistry.DraftCommand{
		Kind: configregistry.KindPolicy, LogicalName: "safe-default", Content: thirdContent,
	})
	if err != nil {
		t.Fatalf("create third policy draft: %v", err)
	}
	third, err := signAndPublish(ctx, pool, service, actor, thirdDraft, 1, privateKey)
	if err != nil || third.VersionNumber != 3 {
		t.Fatalf("publish signed third policy version: version=%#v err=%v", third, err)
	}
	type activationResult struct {
		value configregistry.Activation
		err   error
	}
	results := make(chan activationResult, 2)
	var wait sync.WaitGroup
	for _, candidate := range []configregistry.PublishedVersion{first, third} {
		candidate := candidate
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := activateRegistryVersion(ctx, pool, service, actor, candidate, configregistry.KindPolicy, "safe-default", tenantScope, 2)
			results <- activationResult{value: value, err: err}
		}()
	}
	wait.Wait()
	close(results)
	raceSuccess, raceConflicts := 0, 0
	for result := range results {
		if result.err == nil {
			raceSuccess++
			continue
		}
		if errors.Is(result.err, configregistry.ErrRevisionConflict) {
			raceConflicts++
			continue
		}
		t.Fatalf("concurrent activation failed unexpectedly: %v", result.err)
	}
	if raceSuccess != 1 || raceConflicts != 1 {
		t.Fatalf("concurrent activation results = success:%d conflict:%d, want one of each", raceSuccess, raceConflicts)
	}
	rollback, err := activateRegistryVersion(ctx, pool, service, actor, first, configregistry.KindPolicy, "safe-default", tenantScope, 3)
	if err != nil || rollback.Revision != 4 {
		t.Fatalf("rollback by reactivating immutable version failed: activation=%#v err=%v", rollback, err)
	}
	resolved, err := service.ResolveActive(ctx, tenantID, configregistry.KindPolicy, "safe-default", tenantScope, rollback.ActivatedAt.Add(time.Microsecond))
	if err != nil || resolved.VersionID != first.VersionID {
		t.Fatalf("rollback did not resolve to original version: version=%#v err=%v", resolved, err)
	}
	if _, err := retireRegistryVersion(ctx, pool, service, actor, first, first.Digest); !errors.Is(err, configregistry.ErrVersionActive) {
		t.Fatalf("active version retirement returned %v", err)
	}
	retired, err := retireRegistryVersion(ctx, pool, service, actor, second, second.Digest)
	if err != nil || retired.RetiredAt == nil {
		t.Fatalf("retire inactive version failed: version=%#v err=%v", retired, err)
	}
	retiredThird, err := retireRegistryVersion(ctx, pool, service, actor, third, third.Digest)
	if err != nil || retiredThird.RetiredAt == nil {
		t.Fatalf("retire concurrent inactive version failed: version=%#v err=%v", retiredThird, err)
	}

	for _, item := range []struct {
		kind configregistry.Kind
		name string
		v1   []byte
		v2   []byte
	}{
		{configregistry.KindRecipe, "health-check", []byte(`{"schemaVersion":"recipe-registry/v1","name":"health-check","steps":[{"stepId":"collect","toolName":"query_metrics","input":{}}]}`), []byte(`{"schemaVersion":"recipe-registry/v1","name":"health-check","steps":[{"stepId":"collect","toolName":"query_logs","input":{}}]}`)},
		{configregistry.KindTool, "query_metrics", []byte(`{"schemaVersion":"tool-registry/v1","name":"query_metrics","readOnly":true,"inputSchema":{"type":"object"},"outputSchema":{"type":"object"}}`), []byte(`{"schemaVersion":"tool-registry/v1","name":"query_metrics","readOnly":true,"inputSchema":{"type":"object","additionalProperties":false},"outputSchema":{"type":"object"}}`)},
	} {
		draftV1, err := createRegistryDraft(ctx, pool, service, actor, configregistry.DraftCommand{Kind: item.kind, LogicalName: item.name, Content: item.v1})
		if err != nil {
			t.Fatalf("create %s v1 draft: %v", item.kind, err)
		}
		versionV1, err := signAndPublish(ctx, pool, service, actor, draftV1, 1, privateKey)
		if err != nil {
			t.Fatalf("publish %s v1: %v", item.kind, err)
		}
		draftV2, err := createRegistryDraft(ctx, pool, service, actor, configregistry.DraftCommand{Kind: item.kind, LogicalName: item.name, Content: item.v2})
		if err != nil {
			t.Fatalf("create %s v2 draft: %v", item.kind, err)
		}
		versionV2, err := signAndPublish(ctx, pool, service, actor, draftV2, 1, privateKey)
		if err != nil {
			t.Fatalf("publish %s v2: %v", item.kind, err)
		}
		if _, err := activateRegistryVersion(ctx, pool, service, actor, versionV1, item.kind, item.name, tenantScope, 0); err != nil {
			t.Fatalf("activate %s v1: %v", item.kind, err)
		}
		activatedV2, err := activateRegistryVersion(ctx, pool, service, actor, versionV2, item.kind, item.name, tenantScope, 1)
		if err != nil {
			t.Fatalf("activate %s v2: %v", item.kind, err)
		}
		rolledBack, err := activateRegistryVersion(ctx, pool, service, actor, versionV1, item.kind, item.name, tenantScope, 2)
		if err != nil || rolledBack.Revision != 3 {
			t.Fatalf("rollback %s by reactivating v1 failed: activation=%#v err=%v", item.kind, rolledBack, err)
		}
		active, err := service.ResolveActive(ctx, tenantID, item.kind, item.name, tenantScope, rolledBack.ActivatedAt.Add(time.Microsecond))
		if err != nil || active.VersionID != versionV1.VersionID || active.VersionID == activatedV2.VersionID {
			t.Fatalf("%s rollback resolved the wrong immutable version: active=%#v err=%v", item.kind, active, err)
		}
	}

	listed, err := service.ListVersions(ctx, otherActor, configregistry.KindPolicy)
	if err != nil || len(listed) != 0 {
		t.Fatalf("registry versions crossed tenant RLS: count=%d err=%v", len(listed), err)
	}
	var versionRows, activationHistory, auditRecords int
	if err := db.QueryRowContext(dbctx, `SELECT count(*) FROM platform.registry_versions WHERE tenant_id = $1`, tenantID).Scan(&versionRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(dbctx, `SELECT count(*) FROM platform.registry_activation_history WHERE tenant_id = $1`, tenantID).Scan(&activationHistory); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(dbctx, `SELECT count(*) FROM audit.records WHERE tenant_id = $1 AND entity_kind LIKE 'registry_%'`, tenantID).Scan(&auditRecords); err != nil {
		t.Fatal(err)
	}
	if versionRows != 7 || activationHistory != 10 || auditRecords < 26 {
		t.Fatalf("registry history/audit incomplete: versions=%d activation_history=%d audit=%d", versionRows, activationHistory, auditRecords)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE platform.registry_versions SET content = '{}'::jsonb WHERE tenant_id = $1 AND version_id = $2`, tenantID, first.VersionID)
		return err
	}); err == nil {
		t.Fatal("runtime role changed published version content")
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE platform.registry_drafts SET content = '{}'::jsonb, revision = revision + 1 WHERE tenant_id = $1 AND draft_id = $2`, tenantID, firstDraft.DraftID)
		return err
	}); err == nil {
		t.Fatal("runtime role changed a published draft's signed content")
	}

	stepUpActor := actor
	stepUpActor.KeycloakSID = "registry-admin-session"
	stepUpActor.ACR = auth.StepUpACRLevel2
	stepUpActor.AuthTime = time.Now().UTC()
	if err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		_, err := auth.RecordStepUpSession(ctx, tx, stepUpActor, uuid.Must(uuid.NewV7()), []string{auth.StepUpACRLevel2})
		return err
	}); err != nil {
		t.Fatalf("record isolated registry-admin step-up session: %v", err)
	}
	router, err := httpapi.NewConfigAdminRouter(service, pool)
	if err != nil {
		t.Fatalf("construct config registry admin router: %v", err)
	}
	requestAs := func(method, path, body, key string, requestActor auth.RequestContext) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		req = req.WithContext(auth.WithRequestContext(req.Context(), requestActor))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	operator := stepUpActor
	operator.Roles = []auth.Role{auth.Operator}
	if response := requestAs(http.MethodGet, "/api/v1/admin/policy-bundles", "", "", operator); response.Code != http.StatusForbidden {
		t.Fatalf("operator inherited registry administration: status=%d body=%s", response.Code, response.Body.String())
	}
	if response := requestAs(http.MethodPost, "/api/v1/admin/registry-drafts", `{"kind":"policy","logicalName":"no-step-up","content":{}}`, "registry-http-no-step-up", actor); response.Code != http.StatusForbidden {
		t.Fatalf("registry write without step-up was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	httpContent := map[string]interface{}{"schemaVersion": "policy-registry/v1", "name": "http-policy", "modules": []interface{}{map[string]interface{}{"id": "baseline", "content": "package ops.policy\ndefault allow := false"}}}
	httpDraftBody, err := json.Marshal(api.RegistryDraftCreateRequest{Kind: api.RegistryDraftCreateRequestKind(configregistry.KindPolicy), LogicalName: "http-policy", Content: httpContent})
	if err != nil {
		t.Fatal(err)
	}
	createdHTTPDraft := requestAs(http.MethodPost, "/api/v1/admin/registry-drafts", string(httpDraftBody), "registry-http-create", stepUpActor)
	if createdHTTPDraft.Code != http.StatusCreated {
		t.Fatalf("step-up authorized registry draft failed: status=%d body=%s", createdHTTPDraft.Code, createdHTTPDraft.Body.String())
	}
	createdHTTPDraftReplay := requestAs(http.MethodPost, "/api/v1/admin/registry-drafts", string(httpDraftBody), "registry-http-create", stepUpActor)
	if createdHTTPDraftReplay.Code != createdHTTPDraft.Code || createdHTTPDraftReplay.Body.String() != createdHTTPDraft.Body.String() {
		t.Fatalf("registry draft replay differed from committed response: first=%d replay=%d", createdHTTPDraft.Code, createdHTTPDraftReplay.Code)
	}
	var draftEnvelope struct {
		Data struct {
			DraftID string `json:"draftId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(createdHTTPDraft.Body.Bytes(), &draftEnvelope); err != nil {
		t.Fatalf("decode created registry draft: %v", err)
	}
	draftID, err := uuid.Parse(draftEnvelope.Data.DraftID)
	if err != nil {
		t.Fatalf("created registry draft response omitted its identity: %v", err)
	}
	canonicalHTTPContent, err := json.Marshal(httpContent)
	if err != nil {
		t.Fatal(err)
	}
	message, _, err := configregistry.SigningPayload(tenantID, configregistry.KindPolicy, "http-policy", canonicalHTTPContent)
	if err != nil {
		t.Fatal(err)
	}
	publishBody, err := json.Marshal(api.RegistryPublishRequest{ExpectedRevision: 1, SignerKeyId: "integration-key", Signature: ed25519.Sign(privateKey, message)})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/admin/registry-drafts/" + draftID.String() + ":publish"
	publishedHTTPVersion := requestAs(http.MethodPost, path, string(publishBody), "registry-http-publish", stepUpActor)
	if publishedHTTPVersion.Code != http.StatusCreated {
		t.Fatalf("step-up authorized signed registry publish failed: status=%d body=%s", publishedHTTPVersion.Code, publishedHTTPVersion.Body.String())
	}
	var versionEnvelope struct {
		Data struct {
			VersionID string `json:"versionId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(publishedHTTPVersion.Body.Bytes(), &versionEnvelope); err != nil {
		t.Fatalf("decode published registry version: %v", err)
	}
	versionID, err := uuid.Parse(versionEnvelope.Data.VersionID)
	if err != nil {
		t.Fatalf("published registry response omitted version identity: %v", err)
	}
	activationBody, err := json.Marshal(api.RegistryActivationRequest{Kind: api.RegistryActivationRequestKind(configregistry.KindPolicy), LogicalName: "http-policy", VersionId: versionID.String(), ScopeType: api.RegistryActivationRequestScopeType(configregistry.ScopeTenant), ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	activatedHTTPVersion := requestAs(http.MethodPost, "/api/v1/admin/registry-activations", string(activationBody), "registry-http-activate", stepUpActor)
	if activatedHTTPVersion.Code != http.StatusOK {
		t.Fatalf("step-up authorized registry activation failed: status=%d body=%s", activatedHTTPVersion.Code, activatedHTTPVersion.Body.String())
	}
}

func createRegistryDraft(ctx context.Context, pool *pgxpool.Pool, service *configregistry.Service, actor auth.RequestContext, command configregistry.DraftCommand) (configregistry.Draft, error) {
	var result configregistry.Draft
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = service.CreateDraft(ctx, tx, actor, command)
		return err
	})
	return result, err
}

func updateRegistryDraft(ctx context.Context, pool *pgxpool.Pool, service *configregistry.Service, actor auth.RequestContext, draft configregistry.Draft, revision int64, content []byte) (configregistry.Draft, error) {
	var result configregistry.Draft
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = service.UpdateDraft(ctx, tx, actor, configregistry.DraftRef{TenantID: actor.TenantID, DraftID: draft.DraftID}, configregistry.DraftUpdateCommand{ExpectedRevision: revision, Content: content})
		return err
	})
	return result, err
}

func publishRegistryDraft(ctx context.Context, pool *pgxpool.Pool, service *configregistry.Service, actor auth.RequestContext, draft configregistry.Draft, revision int64, signature configregistry.PublicationSignature) (configregistry.PublishedVersion, error) {
	var result configregistry.PublishedVersion
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = service.Publish(ctx, tx, actor, configregistry.DraftRef{TenantID: actor.TenantID, DraftID: draft.DraftID}, revision, signature)
		return err
	})
	return result, err
}

func signAndPublish(ctx context.Context, pool *pgxpool.Pool, service *configregistry.Service, actor auth.RequestContext, draft configregistry.Draft, revision int64, privateKey ed25519.PrivateKey) (configregistry.PublishedVersion, error) {
	message, _, err := configregistry.SigningPayload(actor.TenantID, draft.Kind, draft.LogicalName, draft.Content)
	if err != nil {
		return configregistry.PublishedVersion{}, err
	}
	return publishRegistryDraft(ctx, pool, service, actor, draft, revision, configregistry.PublicationSignature{
		KeyID: "integration-key", Signature: ed25519.Sign(privateKey, message),
	})
}

func activateRegistryVersion(ctx context.Context, pool *pgxpool.Pool, service *configregistry.Service, actor auth.RequestContext, version configregistry.PublishedVersion, kind configregistry.Kind, name string, scope configregistry.Scope, expectedRevision int64) (configregistry.Activation, error) {
	var result configregistry.Activation
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = service.Activate(ctx, tx, actor, configregistry.VersionRef{TenantID: actor.TenantID, VersionID: version.VersionID}, kind, name, scope, expectedRevision)
		return err
	})
	return result, err
}

func retireRegistryVersion(ctx context.Context, pool *pgxpool.Pool, service *configregistry.Service, actor auth.RequestContext, version configregistry.PublishedVersion, expectedDigest string) (configregistry.PublishedVersion, error) {
	var result configregistry.PublishedVersion
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = service.Retire(ctx, tx, actor, configregistry.VersionRef{TenantID: actor.TenantID, VersionID: version.VersionID}, expectedDigest)
		return err
	})
	return result, err
}
