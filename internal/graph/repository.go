package graph

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
)

type Repository struct{ Pool persistence.TxBeginner }

func (r Repository) Load(ctx context.Context, tenant, cluster string) (OwnershipMirror, error) {
	var out OwnershipMirror
	id, err := uuid.Parse(tenant)
	if err != nil {
		return out, ErrScope
	}
	err = persistence.WithTenantTx(ctx, r.Pool, id, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT owner_epoch,owner_instance,owner_endpoint,lease_uid,route_expires_at FROM platform.graph_ownership WHERE tenant_id=$1 AND cluster_uid=$2`, id, cluster).Scan(&out.Epoch, &out.Instance, &out.Endpoint, &out.LeaseUID, &out.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	out.Tenant = tenant
	out.Cluster = cluster
	return out, err
}
func (r Repository) Record(ctx context.Context, o OwnershipMirror) error {
	tenant, err := uuid.Parse(o.Tenant)
	if err != nil {
		return err
	}
	return persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO platform.graph_ownership(tenant_id,cluster_uid,owner_epoch,owner_instance,owner_endpoint,lease_uid,route_expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,cluster_uid) DO UPDATE SET owner_epoch=EXCLUDED.owner_epoch,owner_instance=EXCLUDED.owner_instance,owner_endpoint=EXCLUDED.owner_endpoint,lease_uid=EXCLUDED.lease_uid,route_expires_at=EXCLUDED.route_expires_at,observed_at=clock_timestamp() WHERE (graph_ownership.owner_epoch<EXCLUDED.owner_epoch OR (graph_ownership.owner_epoch=EXCLUDED.owner_epoch AND graph_ownership.owner_instance=EXCLUDED.owner_instance)) AND graph_ownership.lease_uid=EXCLUDED.lease_uid`, tenant, o.Cluster, o.Epoch, o.Instance, o.Endpoint, o.LeaseUID, o.ExpiresAt)
		if err == nil && tag.RowsAffected() != 1 {
			return ErrStale
		}
		return err
	})
}
