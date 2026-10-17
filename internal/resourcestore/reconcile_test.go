package resourcestore

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/integrations/kubernetes"
	"testing"
)

type inventoryPool struct {
	syncPool
	ids   []string
	valid bool
}
type inventoryTx struct {
	*syncTx
	inventory *inventoryPool
}
type inventoryRow bool

func (r inventoryRow) Scan(dest ...any) error { *dest[0].(*bool) = bool(r); return nil }
func (p *inventoryPool) Begin(context.Context) (pgx.Tx, error) {
	p.begins++
	return &inventoryTx{&syncTx{pool: &p.syncPool}, p}, nil
}
func (tx *inventoryTx) QueryRow(ctx context.Context, q string, args ...any) pgx.Row {
	if len(args) == 5 {
		return inventoryRow(tx.inventory.valid)
	}
	return tx.syncTx.QueryRow(ctx, q, args...)
}

type inventoryRows struct {
	pgx.Rows
	ids []string
	pos int
}

func (r *inventoryRows) Next() bool { return r.pos < len(r.ids) }
func (r *inventoryRows) Scan(dest ...any) error {
	*dest[0].(*string) = r.ids[r.pos]
	r.pos++
	return nil
}
func (r *inventoryRows) Close()     {}
func (r *inventoryRows) Err() error { return nil }
func (tx *inventoryTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return &inventoryRows{ids: tx.inventory.ids}, nil
}

func TestRestartReconcilesOnlyMissingNativeUIDs(t *testing.T) {
	tenant, source := uuid.NewString(), uuid.NewString()
	pool := &inventoryPool{valid: true, ids: []string{
		"k8s+v1://" + tenant + "/cluster/core/Pod/gone",
		"k8s+v1://" + tenant + "/cluster/core/Pod/filtered",
		"k8s+v1://" + tenant + "/cluster/core/Node/node-a",
	}}
	s := nativeSnapshot("filtered")
	s.GVR = kubernetes.GVR{Version: "v1", Resource: "pods", Kind: "Pod"}
	repo := Repository{Pool: pool, ExpectedRevision: 1, BackendLogicalID: "native"}
	if err := repo.ReconcileNativeSnapshot(t.Context(), tenant, source, "cluster", s); err != nil {
		t.Fatal(err)
	}
	if pool.retirements != 1 {
		t.Fatal("wrong kind or visible-only absence retired", pool.retirements)
	}
	pool.retirements = 0
	s.State.LastError = "list_failed"
	if err := repo.ReconcileNativeSnapshot(t.Context(), tenant, source, "cluster", s); err != nil || pool.retirements != 0 {
		t.Fatal("failed snapshot retired identities", err)
	}
	s.State.LastError = ""
	s.ObservationOnly = true
	before := pool.begins
	if err := repo.ReconcileNativeSnapshot(t.Context(), tenant, source, "cluster", s); err != nil || pool.begins != before {
		t.Fatal("standby snapshot reconciled", err)
	}
	s.ObservationOnly = false
	pool.valid = false
	if err := repo.ReconcileNativeSnapshot(t.Context(), tenant, source, "cluster", s); err == nil {
		t.Fatal("revoked source reconciled")
	}
}
