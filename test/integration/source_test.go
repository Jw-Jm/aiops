package integration

import (
	"context"
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
	"ops-platform/internal/auth"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/persistence"
	"ops-platform/internal/source"
)

func TestSourceRegistrationsAreTenantBoundRevisionedAndAudited(t *testing.T) {
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
	actor := auth.RequestContext{TenantID: tenantID, Subject: "source-admin", Roles: []auth.Role{auth.PlatformAdmin}}
	otherActor := auth.RequestContext{TenantID: otherTenantID, Subject: "source-admin-other", Roles: []auth.Role{auth.PlatformAdmin}}
	if _, err := db.ExecContext(dbctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES
		($1, 'source-integration-a', 'Source Integration A'), ($2, 'source-integration-b', 'Source Integration B')`, tenantID, otherTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(dbctx, `INSERT INTO platform.role_bindings (tenant_id, binding_id, subject, role_name) VALUES
		($1, $3, 'source-admin', 'platform_admin'), ($2, $4, 'source-admin-other', 'platform_admin')`, tenantID, otherTenantID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())); err != nil {
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
	verifier := source.CredentialVerifierFunc(func(_ context.Context, ref string, _ source.SourceIdentity, _ source.FindingEnvelope) error {
		if ref == "openbao://kv/platform/sources/vm-prod-a" || ref == "openbao://kv/platform/sources/vm-prod-a-rotated" {
			return nil
		}
		return source.ErrUnauthorized
	})
	service, err := source.NewService(pool, verifier)
	if err != nil {
		t.Fatal(err)
	}

	cluster, err := registerClusterInTenant(ctx, pool, service, actor, source.ClusterCommand{ClusterUID: "cluster-prod-a", DisplayName: "Production A"})
	if err != nil {
		t.Fatalf("register cluster: %v", err)
	}
	duplicateCluster, err := registerClusterInTenant(ctx, pool, service, actor, source.ClusterCommand{ClusterUID: "cluster-prod-a", DisplayName: "Production A"})
	if err != nil || duplicateCluster.ClusterID != cluster.ClusterID {
		t.Fatalf("duplicate cluster registration was not idempotent: cluster=%#v err=%v", duplicateCluster, err)
	}
	if _, err := registerClusterInTenant(ctx, pool, service, actor, source.ClusterCommand{ClusterUID: "cluster-prod-a", DisplayName: "Different Meaning"}); !errors.Is(err, source.ErrIdentityConflict) {
		t.Fatalf("cluster identity redefinition returned %v", err)
	}
	secondCluster, err := registerClusterInTenant(ctx, pool, service, actor, source.ClusterCommand{ClusterUID: "cluster-prod-b", DisplayName: "Production B"})
	if err != nil {
		t.Fatalf("register second cluster: %v", err)
	}
	thirdCluster, err := registerClusterInTenant(ctx, pool, service, actor, source.ClusterCommand{ClusterUID: "cluster-prod-c", DisplayName: "Production C"})
	if err != nil {
		t.Fatalf("register third cluster: %v", err)
	}

	initialAuthRef := "openbao://kv/platform/sources/vm-prod-a"
	register := source.RegisterCommand{SourceType: "victoriametrics", InstanceKey: "vm-prod-a", ClusterID: &cluster.ClusterID, AuthRef: initialAuthRef}
	registration, err := registerSourceInTenant(ctx, pool, service, actor, register)
	if err != nil {
		t.Fatalf("register source: %v", err)
	}
	duplicate, err := registerSourceInTenant(ctx, pool, service, actor, register)
	if err != nil || duplicate.SourceID != registration.SourceID || duplicate.Revision != registration.Revision {
		t.Fatalf("duplicate source registration was not idempotent: source=%#v err=%v", duplicate, err)
	}
	changedRegistration := register
	changedRegistration.ClusterID = &secondCluster.ClusterID
	if _, err := registerSourceInTenant(ctx, pool, service, actor, changedRegistration); !errors.Is(err, source.ErrIdentityConflict) {
		t.Fatalf("duplicate source registration silently changed scope: %v", err)
	}

	envelope := source.FindingEnvelope{
		TenantID: tenantID, ClusterUID: "cluster-prod-a",
		Source: source.EnvelopeSource{System: "victoriametrics", Instance: "vm-prod-a"},
	}
	identity := source.SourceIdentity{TenantID: tenantID, SourceID: registration.SourceID, CredentialRevision: 1, Proof: []byte("test-proof")}
	bound, err := service.AuthenticateEnvelope(ctx, identity, envelope)
	if err != nil || bound.TenantID != tenantID || bound.ClusterID != cluster.ClusterID || bound.ClusterUID != cluster.ClusterUID {
		t.Fatalf("valid registered source was not bound to its tenant and cluster: context=%#v err=%v", bound, err)
	}
	for name, changed := range map[string]source.FindingEnvelope{
		"tenant":  {TenantID: otherTenantID, ClusterUID: envelope.ClusterUID, Source: envelope.Source},
		"cluster": {TenantID: tenantID, ClusterUID: "cluster-prod-b", Source: envelope.Source},
	} {
		t.Run("payload-"+name, func(t *testing.T) {
			if _, err := service.AuthenticateEnvelope(ctx, identity, changed); !errors.Is(err, source.ErrUnauthorized) {
				t.Fatalf("payload-selected %s was accepted: %v", name, err)
			}
		})
	}

	rotated, err := rotateSourceInTenant(ctx, pool, service, actor, registration.SourceID, source.CredentialRotationCommand{
		ExpectedRevision: registration.Revision, AuthRef: "openbao://kv/platform/sources/vm-prod-a-rotated",
	})
	if err != nil || rotated.CredentialRevision != registration.CredentialRevision+1 || rotated.Revision != registration.Revision+1 {
		t.Fatalf("source credential rotation did not create a new revision: source=%#v err=%v", rotated, err)
	}
	if _, err := service.AuthenticateEnvelope(ctx, identity, envelope); !errors.Is(err, source.ErrUnauthorized) {
		t.Fatalf("rotated credential revision was accepted: %v", err)
	}
	currentIdentity := identity
	currentIdentity.CredentialRevision = rotated.CredentialRevision
	if _, err := service.AuthenticateEnvelope(ctx, currentIdentity, envelope); err != nil {
		t.Fatalf("new credential revision was rejected: %v", err)
	}

	updated, err := updateSourceInTenant(ctx, pool, service, actor, registration.SourceID, source.SourceUpdateCommand{
		ExpectedRevision: rotated.Revision, ClusterID: &secondCluster.ClusterID, ClusterIDSet: true,
	})
	if err != nil || updated.Revision != rotated.Revision+1 || updated.ClusterUID != secondCluster.ClusterUID {
		t.Fatalf("source scope update did not produce a new revision: source=%#v err=%v", updated, err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan error, 2)
	for _, target := range []*uuid.UUID{&cluster.ClusterID, &thirdCluster.ClusterID} {
		target := target
		go func() {
			defer wg.Done()
			_, updateErr := updateSourceInTenant(ctx, pool, service, actor, registration.SourceID, source.SourceUpdateCommand{
				ExpectedRevision: updated.Revision, ClusterID: target, ClusterIDSet: true,
			})
			results <- updateErr
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for updateErr := range results {
		switch {
		case updateErr == nil:
			successes++
		case errors.Is(updateErr, source.ErrRevisionConflict):
			conflicts++
		default:
			t.Fatalf("concurrent source scope update failed unexpectedly: %v", updateErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent source revision results = success:%d conflict:%d, want one of each", successes, conflicts)
	}

	disabled, err := updateSourceInTenant(ctx, pool, service, actor, registration.SourceID, source.SourceUpdateCommand{
		ExpectedRevision: updated.Revision + 1, Status: "disabled",
	})
	if err != nil {
		t.Fatalf("disable source: %v", err)
	}
	if _, err := service.AuthenticateEnvelope(ctx, currentIdentity, envelope); !errors.Is(err, source.ErrUnauthorized) {
		t.Fatalf("disabled source credential was accepted: %v", err)
	}
	rolledBack, err := rollbackSourceInTenant(ctx, pool, service, actor, registration.SourceID, source.SourceRegistrationRollbackCommand{
		ExpectedRevision: disabled.Revision, TargetRevision: registration.Revision,
	})
	if err != nil || rolledBack.Revision != disabled.Revision+1 || rolledBack.Status != "active" ||
		rolledBack.ClusterID != cluster.ClusterID || rolledBack.AuthRef != initialAuthRef ||
		rolledBack.CredentialRevision != currentIdentity.CredentialRevision+1 {
		t.Fatalf("source rollback did not append a safe active revision: source=%#v err=%v", rolledBack, err)
	}
	if _, err := service.AuthenticateEnvelope(ctx, currentIdentity, envelope); !errors.Is(err, source.ErrUnauthorized) {
		t.Fatalf("pre-rollback credential generation was accepted: %v", err)
	}
	rolledBackIdentity := currentIdentity
	rolledBackIdentity.CredentialRevision = rolledBack.CredentialRevision
	if _, err := service.AuthenticateEnvelope(ctx, rolledBackIdentity, envelope); err != nil {
		t.Fatalf("restored source auth_ref did not authenticate at its new generation: %v", err)
	}
	otherSources, err := service.ListSources(ctx, otherActor)
	if err != nil || len(otherSources) != 0 {
		t.Fatalf("source registration crossed tenant RLS: count=%d err=%v", len(otherSources), err)
	}

	var sourceRevisions, clusterRevisions, auditRecords int
	if err := db.QueryRowContext(dbctx, `SELECT count(*) FROM platform.source_registration_revisions WHERE tenant_id = $1 AND source_id = $2`, tenantID, registration.SourceID).Scan(&sourceRevisions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(dbctx, `SELECT count(*) FROM platform.cluster_registration_revisions WHERE tenant_id = $1`, tenantID).Scan(&clusterRevisions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(dbctx, `SELECT count(*) FROM audit.records WHERE tenant_id = $1 AND entity_id IN ($2, $3)`, tenantID, registration.SourceID, cluster.ClusterID).Scan(&auditRecords); err != nil {
		t.Fatal(err)
	}
	if sourceRevisions != 6 || clusterRevisions != 3 || auditRecords < 7 {
		t.Fatalf("registration history was incomplete: source_revisions=%d cluster_revisions=%d audit_records=%d", sourceRevisions, clusterRevisions, auditRecords)
	}
	err = persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE platform.cluster_registrations SET cluster_uid = 'cluster-redefined' WHERE tenant_id = $1 AND cluster_id = $2`, tenantID, cluster.ClusterID)
		return err
	})
	if err == nil {
		t.Fatal("runtime role changed an immutable cluster_uid")
	}

	stepUpActor := actor
	stepUpActor.KeycloakSID = "source-admin-session"
	stepUpActor.ACR = auth.StepUpACRLevel2
	stepUpActor.AuthTime = time.Now().UTC()
	if err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		_, err := auth.RecordStepUpSession(ctx, tx, stepUpActor, uuid.Must(uuid.NewV7()), []string{auth.StepUpACRLevel2})
		return err
	}); err != nil {
		t.Fatalf("record isolated source-admin step-up session: %v", err)
	}
	router, err := httpapi.NewSourceAdminRouter(service, pool)
	if err != nil {
		t.Fatalf("construct source admin router: %v", err)
	}
	requestWithActor := func(method, path, body, key string, requestActor auth.RequestContext) *httptest.ResponseRecorder {
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
	if response := requestWithActor(http.MethodGet, "/api/v1/admin/source-registrations", "", "", operator); response.Code != http.StatusForbidden {
		t.Fatalf("operator inherited platform_admin access to source registrations: status=%d body=%s", response.Code, response.Body.String())
	}
	withoutStepUp := actor
	if response := requestWithActor(http.MethodPost, "/api/v1/admin/clusters", `{"clusterUid":"cluster-no-step-up","displayName":"No Step Up"}`, "source-no-step-up", withoutStepUp); response.Code != http.StatusForbidden {
		t.Fatalf("cluster write without step-up was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	if response := requestWithActor(http.MethodPost, "/api/v1/admin/clusters", `{"tenantId":"`+tenantID.String()+`","clusterUid":"cluster-http","displayName":"HTTP Cluster"}`, "source-contract-reject", stepUpActor); response.Code != http.StatusBadRequest {
		t.Fatalf("caller-selected tenant field was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	body := `{"clusterUid":"cluster-http","displayName":"HTTP Cluster"}`
	first := requestWithActor(http.MethodPost, "/api/v1/admin/clusters", body, "source-http-create", stepUpActor)
	if first.Code != http.StatusCreated {
		t.Fatalf("step-up authorized cluster registration failed: status=%d body=%s", first.Code, first.Body.String())
	}
	replay := requestWithActor(http.MethodPost, "/api/v1/admin/clusters", body, "source-http-create", stepUpActor)
	if replay.Code != first.Code || replay.Body.String() != first.Body.String() {
		t.Fatalf("cluster write replay differed from the committed response: first=%d replay=%d", first.Code, replay.Code)
	}
	sourceBody := `{"sourceType":"victoriametrics","instanceKey":"http-vm","clusterId":"` + cluster.ClusterID.String() + `","authRef":"openbao://kv/platform/sources/http-vm"}`
	if response := requestWithActor(http.MethodPost, "/api/v1/admin/source-registrations", `{"tenantId":"`+tenantID.String()+`","sourceType":"victoriametrics","instanceKey":"http-bad","authRef":"openbao://kv/platform/sources/http-bad"}`, "source-http-contract-reject", stepUpActor); response.Code != http.StatusBadRequest {
		t.Fatalf("caller-selected source tenant was accepted: status=%d body=%s", response.Code, response.Body.String())
	}
	createdSource := requestWithActor(http.MethodPost, "/api/v1/admin/source-registrations", sourceBody, "source-http-register", stepUpActor)
	if createdSource.Code != http.StatusCreated {
		t.Fatalf("step-up authorized source registration failed: status=%d body=%s", createdSource.Code, createdSource.Body.String())
	}
	var createdEnvelope struct {
		Data struct {
			SourceID string `json:"sourceId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(createdSource.Body.Bytes(), &createdEnvelope); err != nil {
		t.Fatalf("decode created source response: %v", err)
	}
	if _, err := uuid.Parse(createdEnvelope.Data.SourceID); err != nil {
		t.Fatalf("created source response omitted its identity: %v", err)
	}
	rotatedSource := requestWithActor(http.MethodPost, "/api/v1/admin/source-registrations/"+createdEnvelope.Data.SourceID+"/rotate-credential", `{"expectedRevision":1,"authRef":"openbao://kv/platform/sources/http-vm-rotated"}`, "source-http-rotate", stepUpActor)
	if rotatedSource.Code != http.StatusOK {
		t.Fatalf("step-up authorized source credential rotation failed: status=%d body=%s", rotatedSource.Code, rotatedSource.Body.String())
	}
	updatedSource := requestWithActor(http.MethodPatch, "/api/v1/admin/source-registrations/"+createdEnvelope.Data.SourceID, `{"expectedRevision":2,"status":"disabled"}`, "source-http-update", stepUpActor)
	if updatedSource.Code != http.StatusOK {
		t.Fatalf("step-up authorized source scope/status update failed: status=%d body=%s", updatedSource.Code, updatedSource.Body.String())
	}
	rollbackSource := requestWithActor(http.MethodPost, "/api/v1/admin/source-registrations/"+createdEnvelope.Data.SourceID+"/rollback", `{"expectedRevision":3,"targetRevision":1}`, "source-http-rollback", stepUpActor)
	if rollbackSource.Code != http.StatusOK {
		t.Fatalf("step-up authorized source rollback failed: status=%d body=%s", rollbackSource.Code, rollbackSource.Body.String())
	}
	rollbackReplay := requestWithActor(http.MethodPost, "/api/v1/admin/source-registrations/"+createdEnvelope.Data.SourceID+"/rollback", `{"expectedRevision":3,"targetRevision":1}`, "source-http-rollback", stepUpActor)
	if rollbackReplay.Code != rollbackSource.Code || rollbackReplay.Body.String() != rollbackSource.Body.String() {
		t.Fatalf("source rollback replay differed from committed response: first=%d replay=%d", rollbackSource.Code, rollbackReplay.Code)
	}
}

func registerClusterInTenant(ctx context.Context, pool *pgxpool.Pool, service *source.Service, actor auth.RequestContext, command source.ClusterCommand) (source.ClusterRegistration, error) {
	var result source.ClusterRegistration
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = service.RegisterCluster(ctx, tx, actor, command)
		return err
	})
	return result, err
}

func registerSourceInTenant(ctx context.Context, pool *pgxpool.Pool, service *source.Service, actor auth.RequestContext, command source.RegisterCommand) (source.SourceRegistration, error) {
	var result source.SourceRegistration
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = service.Register(ctx, tx, actor, command)
		return err
	})
	return result, err
}

func rotateSourceInTenant(ctx context.Context, pool *pgxpool.Pool, service *source.Service, actor auth.RequestContext, sourceID uuid.UUID, command source.CredentialRotationCommand) (source.SourceRegistration, error) {
	var result source.SourceRegistration
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = service.RotateCredential(ctx, tx, actor, sourceID, command)
		return err
	})
	return result, err
}

func updateSourceInTenant(ctx context.Context, pool *pgxpool.Pool, service *source.Service, actor auth.RequestContext, sourceID uuid.UUID, command source.SourceUpdateCommand) (source.SourceRegistration, error) {
	var result source.SourceRegistration
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = service.UpdateRegistration(ctx, tx, actor, sourceID, command)
		return err
	})
	return result, err
}

func rollbackSourceInTenant(ctx context.Context, pool *pgxpool.Pool, service *source.Service, actor auth.RequestContext, sourceID uuid.UUID, command source.SourceRegistrationRollbackCommand) (source.SourceRegistration, error) {
	var result source.SourceRegistration
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		result, err = service.RollbackRegistration(ctx, tx, actor, sourceID, command)
		return err
	})
	return result, err
}
