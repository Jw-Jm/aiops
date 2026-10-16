package resourcestore

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"ops-platform/internal/finding"
	"ops-platform/internal/resource"
	"strings"
	"testing"
	"time"
)

type conflictRow struct{}

func (conflictRow) Scan(dest ...any) error {
	*dest[0].(*int64) = 1
	*dest[1].(*time.Time) = time.Now().UTC()
	return nil
}

type conflictTx struct {
	pgx.Tx
	payload []byte
	outbox  bool
}

func (tx *conflictTx) QueryRow(context.Context, string, ...any) pgx.Row { return conflictRow{} }
func (tx *conflictTx) Exec(_ context.Context, q string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(q, "INSERT INTO finding.records") {
		tx.payload = args[5].([]byte)
	}
	if strings.Contains(q, "INSERT INTO finding.outbox") {
		tx.outbox = strings.Contains(q, "'finding/v2'")
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func TestIdentityConflictUsesDeliverableFindingAggregate(t *testing.T) {
	tenant, source := uuid.New(), uuid.New()
	now := time.Now().UTC()
	id := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster", APIGroup: "core", Kind: "Node", StableID: "node-a"}
	tx := &conflictTx{}
	err := identityConflictFinding(t.Context(), tx, tenant, source, nil, resource.SourceRef{Scope: "cluster", Kind: "Node", ObservedAt: now}, resource.Resolution{RuleVersion: "entity-resolver/v1"}, id)
	if err != nil {
		t.Fatal(err)
	}
	var f finding.Finding
	if json.Unmarshal(tx.payload, &f) != nil || f.FindingID == "" || f.AggregateRevision != 1 || f.SchemaVersion != "finding-envelope/v2" || f.TimeReliable || !tx.outbox {
		t.Fatal("incompatible conflict delivery", string(tx.payload))
	}
}
