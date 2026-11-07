package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/finding"
	"ops-platform/internal/persistence"
	"ops-platform/internal/source"
)

// reconcile-deleted settles firing findings whose native subject no longer
// exists (retired resource entity), which predates the subject-deletion
// settlement release or escaped its live delta. This is subject-gone
// settlement through the regular finding ingress: it never claims health.
func runReconcileDeleted(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("reconcile-deleted", flag.ContinueOnError)
	dsn := fs.String("database", "", "worker duty DSN (requires worker_runtime_role)")
	apply := fs.Bool("apply", false, "settle findings; without it, print the report only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dsn == "" {
		return fmt.Errorf("--database is required")
	}
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	config, _ := pgxpool.ParseConfig(*dsn)
	if config.ConnConfig.RuntimeParams["role"] != "worker_runtime_role" {
		return fmt.Errorf("the reconcile duty must connect with role=worker_runtime_role")
	}
	type candidate struct {
		Tenant, Source, Finding, Occurrence, Cluster, ClusterID, Canonical, Namespace string
		RegistrationRevision, ClusterRevision                                         int64
		Payload                                                                       json.RawMessage
		Starts                                                                        time.Time
	}
	tenants := []uuid.UUID{}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform.tenants`).Scan(new(int)); err == nil {
		var tid uuid.UUID
		trows, terr := pool.Query(ctx, `SELECT tenant_id FROM platform.tenants WHERE status='active'`)
		if terr != nil {
			return terr
		}
		for trows.Next() {
			if err := trows.Scan(&tid); err != nil {
				trows.Close()
				return err
			}
			tenants = append(tenants, tid)
		}
		trows.Close()
		if err := trows.Err(); err != nil {
			return err
		}
	}
	if len(tenants) == 0 {
		return fmt.Errorf("no active tenants visible to the reconcile duty")
	}
	items := []candidate{}
	for _, tenant := range tenants {
		err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
		SELECT f.tenant_id::text, f.source_id::text, f.finding_id::text, f.occurrence_id::text,
		       f.cluster_uid, f.cluster_id::text, s.revision, s2.revision,
		       f.resource_canonical_id, f.namespace, f.payload,
		       (f.payload->>'startsAt')::timestamptz
		FROM finding.records f
		JOIN platform.resource_entities e
		  ON e.tenant_id = f.tenant_id AND e.canonical_id = f.resource_canonical_id
		JOIN platform.source_registrations s
		  ON s.tenant_id = f.tenant_id AND s.source_id = f.source_id AND s.status = 'active'
		JOIN platform.cluster_registrations s2
		  ON s2.tenant_id = f.tenant_id AND s2.cluster_id = f.cluster_id AND s2.status = 'active'
		WHERE f.lifecycle_state <> 'resolved' AND e.deleted_at IS NOT NULL
			ORDER BY f.finding_id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.Tenant, &c.Source, &c.Finding, &c.Occurrence, &c.Cluster, &c.ClusterID, &c.RegistrationRevision, &c.ClusterRevision, &c.Canonical, &c.Namespace, &c.Payload, &c.Starts); err != nil {
				rows.Close()
				return err
			}
			items = append(items, c)
		}
		rows.Close()
		return rows.Err()
		})
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "firing findings with retired subjects: %d\n", len(items))
	for _, c := range items {
		var payload struct {
			RuleID             string `json:"ruleId"`
			RuleFamily         string `json:"ruleFamily"`
			NormalizedSymptom  string `json:"normalizedSymptom"`
			Severity           string `json:"severity"`
			TimeReliable       bool   `json:"timeReliable"`
			SourceSequence     int64  `json:"sourceSequence"`
		}
		if err := json.Unmarshal(c.Payload, &payload); err != nil {
			fmt.Fprintf(stderr, "skip %s: payload unreadable\n", c.Finding)
			continue
		}
		if payload.RuleID == "" || payload.NormalizedSymptom == "" {
			fmt.Fprintf(stderr, "skip %s: payload missing rule identity\n", c.Finding)
			continue
		}
		fmt.Fprintf(stdout, "  %s %s %s\n", c.Finding, payload.NormalizedSymptom, c.Canonical[len(c.Canonical)-40:])
		if !*apply {
			continue
		}
		bound := source.BoundSourceContext{
			TenantID: uuid.MustParse(c.Tenant), SourceID: uuid.MustParse(c.Source),
			ClusterUID: c.Cluster, ClusterID: uuid.MustParse(c.ClusterID),
			RegistrationRevision: c.RegistrationRevision, CredentialRevision: c.ClusterRevision,
		}
		settled, err := json.Marshal(map[string]any{
			"ruleId": payload.RuleID, "ruleFamily": payload.RuleFamily,
			"normalizedSymptom": payload.NormalizedSymptom, "deleted": true,
		})
		if err != nil {
			return err
		}
		envelope := finding.Envelope{
			SchemaVersion: "finding-envelope/v2", EventID: "reconcile-deleted-" + c.Finding,
			IdempotencyKey: "reconcile-deleted-" + c.Finding, TenantID: c.Tenant,
			SourceRegistrationID: c.Source, ClusterUID: c.Cluster,
			ResourceCanonicalID: c.Canonical, Namespace: c.Namespace,
			RuleID: payload.RuleID, RuleFamily: payload.RuleFamily,
			NormalizedSymptom: payload.NormalizedSymptom, OccurrenceID: c.Occurrence,
			StartsAt: c.Starts, ObservedAt: time.Now().UTC(),
			SourceSequence: payload.SourceSequence, TimeReliable: payload.TimeReliable,
			State: "resolved", Severity: payload.Severity, Payload: settled,
			EvidenceRefs: []string{},
		}
		if _, _, err := (finding.Service{Pool: pool}).Ingest(ctx, bound, envelope); err != nil {
			fmt.Fprintf(stderr, "settle %s failed: %v\n", c.Finding, err)
			continue
		}
		pollKey := finding.Hash([]string{c.Canonical, payload.RuleID})
		if err := (finding.Service{Pool: pool}).ClosePoll(ctx, bound, pollKey, c.Occurrence); err != nil {
			fmt.Fprintf(stderr, "close %s failed: %v\n", c.Finding, err)
			continue
		}
		fmt.Fprintf(stdout, "    settled\n")
	}
	if !*apply {
		fmt.Fprintln(stdout, "dry run; pass --apply to settle")
	}
	return nil
}
