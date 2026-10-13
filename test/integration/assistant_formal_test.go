package integration

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestAssistantFormalRevisionReferences(t *testing.T) {
	ctx, db, worker, b := sp05Database(t)
	incidentID, planID := uuid.New(), uuid.New()
	target := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: "pod-formal"}.String()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod','apps','pod-formal','{}',clock_timestamp())`, b.TenantID, target, b.ClusterID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO incident.records(tenant_id,incident_id,state,cluster_uid,namespace,current_rca_revision,resource_canonical_id) VALUES($1,$2,'open',$3,'apps',2,$4)`, b.TenantID, incidentID, b.ClusterUID, target); err != nil {
		t.Fatal(err)
	}
	for version := int64(1); version <= 2; version++ {
		rev := rca.Revision{SchemaVersion: "rca-revision/v1", TenantID: b.TenantID.String(), IncidentID: incidentID.String(), Revision: version, Source: "manual", Actor: "fixture", CreatedAt: time.Now().UTC()}
		raw, _ := json.Marshal(rev)
		if _, err := db.ExecContext(ctx, `INSERT INTO incident.rca_revisions(tenant_id,incident_id,revision,evaluation_key,input_digest,base_incident_revision,recipe_digest,actor,source,provenance,input_manifest,result,superseded) VALUES($1,$2,$3,$4,$5,1,$5,'fixture','manual','{}','{}',$6,false)`, b.TenantID, incidentID, version, uuid.NewString(), graph.ScopeDigest("fixture"), raw); err != nil {
			t.Fatal(err)
		}
	}
	content, _ := json.Marshal(map[string]any{"rcaRevision": 1, "targetCanonicalId": target, "reason": "Fixture only", "suggestedCommand": "hidden-command", "executionProfileId": "hidden-handle"})
	if _, err := db.ExecContext(ctx, `INSERT INTO action.plans(tenant_id,action_plan_id,incident_id,content,state) VALUES($1,$2,$3,$4,'suggested')`, b.TenantID, planID, incidentID, content); err != nil {
		t.Fatal(err)
	}
	job := investigation.Job{TenantID: b.TenantID, IncidentID: incidentID, Subject: "fixture", Scope: graph.Scope{Tenant: b.TenantID.String(), Cluster: b.ClusterUID, Namespaces: []string{"apps"}}, Budget: investigation.DefaultBudget(), Assistant: &investigation.AssistantTask{TaskKind: "incident_followup", AuthorizationDomain: "operational"}}
	var frozen investigation.FormalRead
	if err := persistence.WithTenantTx(ctx, worker, b.TenantID, func(tx pgx.Tx) error {
		latest, _, err := investigation.ReadFormalReferencesTx(ctx, tx, job, 0)
		if err != nil {
			return err
		}
		if latest.Historical || len(latest.FormalReferences) != 1 || latest.FormalReferences[0].Revision != 2 {
			t.Fatal(latest)
		}
		frozen, _, err = investigation.ReadFormalReferencesTx(ctx, tx, job, 1)
		if err != nil {
			return err
		}
		if !frozen.Historical || len(frozen.FormalReferences) != 2 {
			t.Fatal(frozen)
		}
		if _, exists := frozen.Plans[0]["suggestedCommand"]; exists {
			t.Fatal("command exposed")
		}
		if _, exists := frozen.Plans[0]["executionProfileId"]; exists {
			t.Fatal("handle exposed")
		}
		for _, ref := range frozen.FormalReferences {
			if err = investigation.AuthorizeFormalReferenceTx(ctx, tx, job, ref); err != nil {
				return err
			}
		}
		bad := frozen.FormalReferences[0]
		bad.ContentDigest = graph.ScopeDigest("forged")
		if investigation.AuthorizeFormalReferenceTx(ctx, tx, job, bad) == nil {
			t.Fatal("digest accepted")
		}
		bad = frozen.FormalReferences[0]
		bad.IncidentID = uuid.New()
		if investigation.AuthorizeFormalReferenceTx(ctx, tx, job, bad) == nil {
			t.Fatal("unbound incident accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE action.plans SET revision=2 WHERE tenant_id=$1 AND action_plan_id=$2`, b.TenantID, planID); err != nil {
		t.Fatal(err)
	}
	if err := persistence.WithTenantTx(ctx, worker, b.TenantID, func(tx pgx.Tx) error {
		if investigation.AuthorizeFormalReferenceTx(ctx, tx, job, frozen.FormalReferences[1]) == nil {
			t.Fatal("changed plan substituted")
		}
		if err := investigation.AuthorizeFormalReferenceTx(ctx, tx, job, frozen.FormalReferences[0]); err != nil {
			return err
		}
		hidden := job
		hidden.Scope.Namespaces = []string{"private"}
		if _, _, err := investigation.ReadFormalReferencesTx(ctx, tx, hidden, 1); err == nil {
			t.Fatal("namespace hidden RCA disclosed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
