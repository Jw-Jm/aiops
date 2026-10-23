package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"ops-platform/internal/identity"
	"ops-platform/internal/persistence"
)

// This fixture proves the real HTTP/challenge/transaction binding. It does not
// assert policy admission, real resource provenance, dispatch or command output.
func assertNativeCommandProof(t *testing.T, ctx context.Context, api, worker *pgxpool.Pool, client mfaClient, session mfaSession, active identity.ActiveSession, clusterID uuid.UUID, clusterUID, canonicalID, password, secret string) {
	t.Helper()
	tenant := active.TenantID
	namespace := "allowed-ns"
	assessmentID := uuid.Must(uuid.NewV7())
	binding := action.Binding{DigestVersion: "execution-request/v1", TenantID: tenant, Subject: active.Subject, IncidentID: uuid.Must(uuid.NewV7()), Target: canonicalID, TargetUID: "fixture-pod-uid", TargetRevision: 1, ClusterUID: clusterUID, Namespace: &namespace, Shell: "bash", CommandDigest: action.Digest([]byte("printf safe-fixture")), ProfileID: uuid.Must(uuid.NewV7()), ProfileVersion: 1, RiskVersion: action.RiskVersion, PolicyVersion: uuid.Must(uuid.NewV7()).String(), Options: action.Options{TimeoutSeconds: 10, MaxOutputBytes: 4096, Environment: map[string]string{}}}
	err := persistence.WithTenantTx(ctx, worker, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod',$4,'same-name','{"sourceValues":{"uid":"fixture-pod-uid"}}',clock_timestamp())`, tenant, canonicalID, clusterID, namespace)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = persistence.WithTenantTx(ctx, api, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `WITH stamp AS MATERIALIZED (SELECT clock_timestamp() AS created_at) INSERT INTO action.assessments(tenant_id,assessment_id,subject,binding,request_digest,risk,created_at,expires_at) SELECT $1,$2,$3,$4,$5,'{}',created_at,created_at+interval '5 minutes' FROM stamp`, tenant, assessmentID, active.Subject, action.Canonical(binding), binding.Digest())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	target := action.NativeCommandStepUpTarget(binding, assessmentID)
	command := target["command"]
	delete(target, "command")
	status, created, failureBody := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{"operation": "command_execution", "target": target, "command": command, "expectedSessionVersion": session.sessionVersion}, &session)
	if status != 201 {
		t.Fatalf("native command challenge status=%d body=%s", status, failureBody)
	}
	challengeID := mfaData(t, created)["challengeId"].(string)
	for index, method := range []string{"password", "totp"} {
		credential := password
		if method == "totp" {
			credential = mfaCodeAt(t, secret, identity.TOTPStep(time.Now().UTC())+1)
		}
		status, result, _ := client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+challengeID+":verify", map[string]any{"method": method, "credential": credential, "expectedChallengeRevision": index + 1}, &session)
		if status != 200 {
			t.Fatalf("native command factor %s=%d", method, status)
		}
		if method == "totp" && mfaData(t, result)["verified"] != true {
			t.Fatal("command second factor did not verify")
		}
	}
	nativeCtx := auth.WithNativeSession(ctx, auth.NativeSessionContext{Session: active})
	verify := func(b action.Binding) error {
		data, _ := json.Marshal(action.NativeCommandStepUpTarget(b, assessmentID))
		bound := auth.WithNativeStepUpIntent(nativeCtx, auth.NativeStepUpIntent{ChallengeID: uuid.MustParse(challengeID), Operation: "command_execution", Target: data})
		return persistence.WithTenantTx(bound, api, tenant, func(tx pgx.Tx) error {
			_, err := auth.TouchCurrentStepUpSession(bound, tx, auth.RequestContext{TenantID: tenant, Subject: active.Subject}, []string{auth.StepUpACRLevel2})
			return err
		})
	}
	if err := verify(binding); err != nil {
		t.Fatalf("native command proof transaction: %v", err)
	}
	for _, change := range []func(*action.Binding){func(b *action.Binding) { b.CommandDigest = action.Digest([]byte("different command")) }, func(b *action.Binding) { b.TargetUID = "replacement-uid" }, func(b *action.Binding) { b.ProfileVersion++ }, func(b *action.Binding) { b.TargetRevision++ }} {
		altered := binding
		change(&altered)
		if err := verify(altered); err == nil {
			t.Fatal("command proof accepted changed bytes/UID/profile/resource revision")
		}
	}
	err = persistence.WithTenantTx(ctx, worker, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE platform.resource_entities SET metadata=jsonb_set(metadata,'{fixtureState}','"changed"') WHERE tenant_id=$1 AND canonical_id=$2`, tenant, canonicalID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision := mfaTenantIntWithPool(t, ctx, api, tenant, `SELECT resource_revision FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2`, tenant, canonicalID); revision != 2 {
		t.Fatalf("resource revision=%d, want2", revision)
	}
	status, _, _ = client.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{"operation": "command_execution", "target": target, "command": command, "expectedSessionVersion": session.sessionVersion}, &session)
	if status != 409 {
		t.Fatalf("stale resource assessment challenge=%d", status)
	}
	// The public challenge must keep command facts separate from its target.
	if strings.Contains(string(action.Canonical(target)), "actualCommandDigest") {
		t.Fatal("command leaked into public object target")
	}
}
