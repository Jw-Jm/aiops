package app

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/persistence"
	"ops-platform/internal/source"
)

type SP05Config struct {
	Enabled           bool
	IngestionBindings []httpapi.IngestionBinding
	Analyzer          SP05AnalyzerConfig
}

type SP05AnalyzerConfig struct {
	Enabled bool
	SHA256  string
}

func persistenceSourceContext(ctx context.Context, pool persistence.TxBeginner, b *source.BoundSourceContext) error {
	return persistence.WithTenantTx(ctx, pool, b.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT s.cluster_id,c.cluster_uid,s.credential_revision FROM platform.source_registrations s JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE s.tenant_id=$1 AND s.source_id=$2 AND s.revision=$3 AND s.status='active'`, b.TenantID, b.SourceID, b.RegistrationRevision).Scan(&b.ClusterID, &b.ClusterUID, &b.CredentialRevision)
	})
}

var _ = json.Unmarshal
