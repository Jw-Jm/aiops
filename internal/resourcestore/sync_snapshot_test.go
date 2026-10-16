package resourcestore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
)

type syncPool struct {
	begins      int
	retirements int
	updateRows  string
}

type syncTx struct {
	pgx.Tx
	pool *syncPool
}

type syncRow struct{ tenant string }

func (r syncRow) Scan(dest ...any) error { *dest[0].(*string) = r.tenant; return nil }

func (p *syncPool) Begin(context.Context) (pgx.Tx, error) {
	p.begins++
	return &syncTx{pool: p}, nil
}
func (tx *syncTx) Commit(context.Context) error   { return nil }
func (tx *syncTx) Rollback(context.Context) error { return nil }
func (tx *syncTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if !strings.Contains(sql, "set_config") {
		panic("unexpected query: " + sql)
	}
	return syncRow{args[0].(string)}
}
func (tx *syncTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "pg_advisory_xact_lock"):
		return pgconn.NewCommandTag("SELECT 1"), nil
	case strings.Contains(sql, "UPDATE platform.resource_entities SET deleted_at"):
		tx.pool.retirements++
		rows := tx.pool.updateRows
		if rows == "" {
			rows = "UPDATE 1"
		}
		return pgconn.NewCommandTag(rows), nil
	default:
		panic("unexpected statement: " + sql)
	}
}

func nativeSnapshot(uids ...string) kubernetes.Snapshot {
	s := kubernetes.Snapshot{State: kubernetes.GVRState{LastListCompletedAt: time.Now().UTC()}}
	for _, uid := range uids {
		obj := unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name": "pod", "uid": uid, "resourceVersion": "1", "namespace": "apps",
			},
		}}
		s.Objects = append(s.Objects, obj)
	}
	return s
}

// A source-scope filter change removes an object from the visible projection but
// is not a native deletion: it must never retire the identity.
func TestSyncSnapshotDoesNotRetireFilteredAbsence(t *testing.T) {
	pool := &syncPool{}
	visible := kubernetes.Snapshot{State: kubernetes.GVRState{LastListCompletedAt: time.Now().UTC()}}
	next, err := (Repository{Pool: pool}).SyncSnapshot(context.Background(), uuid.NewString(), uuid.NewString(), "cluster", "Pod", "core", visible, nativeSnapshot("pod-a"), map[string]string{"pod-a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if pool.retirements != 0 {
		t.Fatalf("filtered absence retired %d identities", pool.retirements)
	}
	// The native UID stays acknowledged so a later real deletion is still seen.
	if _, ok := next["pod-a"]; !ok {
		t.Fatalf("native acknowledgement dropped: %v", next)
	}
}

// A UID missing from a complete native list is a real deletion.
func TestSyncSnapshotRetiresTrulyDeletedUID(t *testing.T) {
	pool := &syncPool{}
	visible := kubernetes.Snapshot{State: kubernetes.GVRState{LastListCompletedAt: time.Now().UTC()}}
	next, err := (Repository{Pool: pool}).SyncSnapshot(context.Background(), uuid.NewString(), uuid.NewString(), "cluster", "Pod", "core", visible, nativeSnapshot(), map[string]string{"pod-a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if pool.retirements != 1 {
		t.Fatalf("retirements=%d, want 1", pool.retirements)
	}
	if _, ok := next["pod-a"]; ok {
		t.Fatalf("retired UID still acknowledged: %v", next)
	}
}

// Without a complete native list there is no proof of deletion.
func TestSyncSnapshotSkipsRetirementWithoutCompleteNativeList(t *testing.T) {
	pool := &syncPool{}
	visible := kubernetes.Snapshot{State: kubernetes.GVRState{LastListCompletedAt: time.Now().UTC()}}
	failed := nativeSnapshot()
	failed.State.LastError = "list_failed"
	next, err := (Repository{Pool: pool}).SyncSnapshot(context.Background(), uuid.NewString(), uuid.NewString(), "cluster", "Pod", "core", visible, failed, map[string]string{"pod-a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if pool.retirements != 0 {
		t.Fatalf("retirements=%d, want 0 without native evidence", pool.retirements)
	}
	if _, ok := next["pod-a"]; !ok {
		t.Fatalf("previous acknowledgement dropped: %v", next)
	}
}

// Retirement is monotonic and idempotent: a missing row must not fail the GVR sync.
func TestRetireIsIdempotentWhenIdentityMissing(t *testing.T) {
	pool := &syncPool{updateRows: "UPDATE 0"}
	tenant := uuid.NewString()
	id := "k8s+v1://" + tenant + "/cluster/core/Pod/pod-a"
	if err := (Repository{Pool: pool}).Retire(context.Background(), uuid.MustParse(tenant), id); err != nil {
		t.Fatalf("missing retirement row must be tolerated: %v", err)
	}
}
