package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A persisted native task freezes the complete grant snapshot and a normally
// narrower execution scope. The live projection is the complete grant, so the
// comparison must prove the grant digest and then containment of the execution
// scope; comparing the execution scope with the live grant (the previous
// inversion) rejects every healthy task.
func TestTaskAuthorityHoldsFullGrantWithNarrowedExecutionScope(t *testing.T) {
	tenant := uuid.Must(uuid.NewV7())
	grant := Scope{
		Tenant: tenant.String(), Cluster: "cluster-a",
		Namespaces: []string{"apps", "kube-system"}, Resources: []string{},
		ClusterScoped: true, AuthorizationRevision: "sha256:revision",
	}
	target := "k8s+v1://cluster-a/core/v1/pods/apps/api-0?uid=11111111-1111-1111-1111-111111111111"
	effective := Scope{
		Tenant: tenant.String(), Cluster: "cluster-a",
		Namespaces: []string{"apps"}, Resources: []string{target},
		AuthorizationRevision: "sha256:revision",
	}
	resolution := NativeResolution{
		UserID: uuid.Must(uuid.NewV7()), Subject: "user:" + tenant.String(), AuthorizationVersion: 1,
		Domain: "operational", TaskKind: "incident_followup",
		GrantSnapshot: &grant, EffectiveScope: &effective,
		GrantDigest: ScopeDigest(grant), EffectiveDigest: ScopeDigest(effective),
	}
	if err := taskAuthorityHolds(grant, resolution); err != nil {
		t.Fatalf("healthy task with a narrowed execution scope was rejected: %v", err)
	}
	// The previous rule compared the live grant with the execution scope and
	// therefore failed; keep that as the regression assertion.
	if err := taskAuthorityHolds(effective, resolution); !errors.Is(err, ErrScope) {
		t.Fatalf("narrowed live projection must not satisfy the grant digest: %v", err)
	}
	// A live grant that no longer covers the frozen execution scope is a revoked
	// or narrowed authority, even when its version vector is unchanged.
	shrunk := grant
	shrunk.Namespaces = []string{"kube-system"}
	shrunk.Resources = []string{}
	shrunk.ClusterScoped = false
	shrunkResolution := resolution
	shrunkResolution.GrantDigest = ScopeDigest(shrunk)
	if err := taskAuthorityHolds(shrunk, shrunkResolution); !errors.Is(err, ErrScope) {
		t.Fatalf("live grant without the frozen resource must be refused: %v", err)
	}
	// A restricted live grant whose resources exclude the frozen target is refused.
	restricted := grant
	restricted.Resources = []string{"k8s+v1://cluster-a/core/v1/pods/apps/other?uid=22222222-2222-2222-2222-222222222222"}
	restrictedResolution := resolution
	restrictedResolution.GrantDigest = ScopeDigest(restricted)
	if err := taskAuthorityHolds(restricted, restrictedResolution); !errors.Is(err, ErrScope) {
		t.Fatalf("restricted live grant must not cover an unrestricted execution scope: %v", err)
	}
	if err := taskAuthorityHolds(grant, NativeResolution{}); !errors.Is(err, ErrScope) {
		t.Fatalf("missing frozen digests must fail closed: %v", err)
	}
}

// resolveTaskAuthorityTx must pick the projection from the persisted schema
// version (never from the caller) and re-validate the frozen facts.
func TestResolveTaskAuthoritySelectsPersistedProjection(t *testing.T) {
	tenant := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	grant := Scope{Tenant: tenant.String(), Cluster: "cluster-a", ClusterScoped: true, Namespaces: []string{"apps", "kube-system"}, Resources: []string{}, AuthorizationRevision: "sha256:revision"}
	effective := Scope{Tenant: tenant.String(), Cluster: "cluster-a", Namespaces: []string{"apps"}, Resources: []string{"k8s+v1://cluster-a/core/v1/pods/apps/api-0?uid=11111111-1111-1111-1111-111111111111"}, AuthorizationRevision: "sha256:revision"}
	values := func(kind string, effectiveScope, grantScope Scope) []any {
		effectiveRaw, _ := json.Marshal(effectiveScope)
		grantRaw, _ := json.Marshal(grantScope)
		return []any{
			userID, "user:" + userID.String(), 3, "operational", kind,
			effectiveRaw, grantRaw, ScopeDigest(grantScope), ScopeDigest(effectiveScope),
			"", "", "running", time.Now().Add(time.Hour),
		}
	}
	cases := []struct {
		name       string
		schema     string
		values     []any
		projection string
		wantErr    bool
	}{
		{name: "v2 incident job", schema: "investigation-job/v2", values: values("incident_followup", effective, grant), projection: "native_incident_task_shared"},
		{name: "v3 assistant task", schema: "investigation-job/v3", values: values("data_query", effective, grant), projection: "native_internal_task_shared"},
		{name: "tampered effective digest", schema: "investigation-job/v2", values: append(append([]any{}, values("incident_followup", effective, grant)[:8]...), "sha256:tampered", "", "", "running", time.Now().Add(time.Hour)), projection: "native_incident_task_shared", wantErr: true},
		{name: "execution scope outside grant", schema: "investigation-job/v2", values: values("incident_followup", Scope{Tenant: tenant.String(), Cluster: "cluster-a", Namespaces: []string{"private"}, Resources: []string{}, AuthorizationRevision: "sha256:revision"}, grant), projection: "native_incident_task_shared", wantErr: true},
		{name: "unknown task kind", schema: "investigation-job/v2", values: values("free_form", effective, grant), projection: "native_incident_task_shared", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &scriptedTaskTx{schema: tc.schema, values: tc.values}
			resolution, err := resolveTaskAuthorityTx(context.Background(), tx, tenant, uuid.Must(uuid.NewV7()))
			if len(tx.queries) != 2 || !strings.Contains(tx.queries[1], tc.projection) {
				t.Fatalf("projection queries = %v, want %s", tx.queries, tc.projection)
			}
			if tc.wantErr {
				if !errors.Is(err, ErrScope) {
					t.Fatalf("resolution error = %v, want ErrScope", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolution: %v", err)
			}
			if resolution.GrantSnapshot == nil || resolution.EffectiveScope == nil ||
				ScopeDigest(*resolution.GrantSnapshot) != resolution.GrantDigest ||
				ScopeDigest(*resolution.EffectiveScope) != resolution.EffectiveDigest {
				t.Fatalf("resolution did not carry the frozen scopes: %+v", resolution)
			}
			if err := taskAuthorityHolds(grant, resolution); err != nil {
				t.Fatalf("resolved task must hold against its complete grant: %v", err)
			}
		})
	}
}

// scriptedTaskTx answers the two read-only queries of resolveTaskAuthorityTx.
type scriptedTaskTx struct {
	pgx.Tx
	schema  string
	values  []any
	queries []string
}

func (t *scriptedTaskTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	t.queries = append(t.queries, sql)
	if strings.Contains(sql, "SELECT schema_version") {
		return scriptedRow{values: []any{t.schema}}
	}
	return scriptedRow{values: t.values}
}

type scriptedRow struct {
	values []any
	err    error
}

func (r scriptedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan %d destinations from %d values", len(dest), len(r.values))
	}
	for index := range dest {
		target := reflect.ValueOf(dest[index])
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return fmt.Errorf("destination %d is not a pointer", index)
		}
		value := reflect.ValueOf(r.values[index])
		if !value.IsValid() {
			return fmt.Errorf("value %d is missing", index)
		}
		if value.Type() != target.Elem().Type() {
			return fmt.Errorf("value %d type %s does not fit %s", index, value.Type(), target.Elem().Type())
		}
		target.Elem().Set(value)
	}
	return nil
}
