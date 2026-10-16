package analytics

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
)

// ControlPlanePlan contains only a closed server query ID and parameter values.
// SQL, column names, expressions and endpoints never come from QueryIntent.
type ControlPlanePlan struct {
	intent                                                                        QueryIntent
	scope                                                                         graph.Scope
	subject, domain, grantDigest, catalogDigest, digest, dimension, unit, queryID string
	filters                                                                       map[string]string
	sort                                                                          Sort
	limit                                                                         int
}

func (p ControlPlanePlan) Digest() string              { return p.digest }
func (p ControlPlanePlan) ExecutionScope() graph.Scope { return CloneScope(p.scope) }
func CompileControlPlane(intent QueryIntent, binding Binding, domain string) (ControlPlanePlan, error) {
	if intent.NetworkSelection != nil {
		return ControlPlanePlan{}, ErrUnsupported
	}
	var p ControlPlanePlan
	decoded, err := DecodeIntent(rawIntent(intent))
	if err != nil {
		return p, err
	}
	intent = decoded
	if !ValidText(binding.Subject, 512) || binding.Grant.Tenant == "" || binding.Grant.AuthorizationRevision == "" {
		return p, ErrInvalid
	}
	catalog, err := Catalog()
	if err != nil {
		return p, err
	}
	var dataset *Dataset
	for i := range catalog.Datasets {
		if catalog.Datasets[i].ID == intent.DatasetID {
			dataset = &catalog.Datasets[i]
		}
	}
	if intent.ComparisonTimeRange != nil || intent.StepSeconds != 0 || dataset == nil || dataset.Source != "postgresql" || domain != dataset.AuthorizationDomain || intent.Operation != "aggregate" || len(intent.Metrics) != 1 || intent.Metrics[0] != dataset.Metric || len(intent.Dimensions) > 1 {
		return p, ErrUnsupported
	}
	if intent.TimeRange == nil || intent.TimeRange.From.IsZero() || !intent.TimeRange.To.After(intent.TimeRange.From) || intent.TimeRange.To.Sub(intent.TimeRange.From) > 31*24*time.Hour || intent.TimeRange.To.After(time.Now().Add(time.Minute)) || intent.TimeRange.Timezone == "Local" {
		return p, ErrInvalid
	}
	if _, err := time.LoadLocation(intent.TimeRange.Timezone); err != nil {
		return p, ErrInvalid
	}
	scope := CloneScope(binding.Grant)
	if domain == "audit" {
		if scope.Cluster != "" || scope.ClusterScoped || len(scope.Namespaces) > 0 || len(scope.Resources) > 0 || len(binding.Selection.Namespaces) > 0 || len(binding.Selection.CanonicalIDs) > 0 || binding.Selection.IncludeClusterScoped {
			return p, ErrDenied
		}
	} else {
		scope, err = RestrictScope(binding.Grant, binding.Selection)
		if err != nil {
			return p, err
		}
	}
	dimension := ""
	if len(intent.Dimensions) > 0 {
		dimension = intent.Dimensions[0]
		if !slices.Contains(dataset.Dimensions, dimension) {
			return p, ErrUnsupported
		}
	}
	filters := map[string]string{}
	for _, f := range intent.Filters {
		if !slices.Contains(dataset.Filters, f.Field) || f.Operator != "eq" || !ValidText(f.Value, 253) || filters[f.Field] != "" {
			return p, ErrInvalid
		}
		if f.Field == "namespace" && !slices.Contains(scope.Namespaces, f.Value) {
			return p, ErrDenied
		}
		filters[f.Field] = f.Value
	}
	order := Sort{Field: dataset.Metric, Direction: "desc"}
	if len(intent.Sort) > 1 {
		return p, ErrInvalid
	}
	if len(intent.Sort) == 1 {
		order = intent.Sort[0]
		if (order.Field != dataset.Metric && (dimension == "" || order.Field != dimension)) || (order.Direction != "asc" && order.Direction != "desc") {
			return p, ErrInvalid
		}
	}
	if intent.Limit < 1 || intent.Limit > dataset.MaxGroups {
		return p, ErrInvalid
	}
	p = ControlPlanePlan{intent: intent, scope: scope, subject: binding.Subject, domain: domain, grantDigest: graph.ScopeDigest(binding.Grant), catalogDigest: catalogDigest(catalog), dimension: dimension, unit: dataset.Unit, queryID: dataset.TemplateVersion, filters: filters, sort: order, limit: intent.Limit}
	p.digest = graph.ScopeDigest(struct {
		Intent                                    QueryIntent
		Scope                                     graph.Scope
		Subject, Domain, Grant, Catalog, Template string
	}{intent, scope, binding.Subject, domain, p.grantDigest, p.catalogDigest, p.queryID})
	return p, nil
}
func rawIntent(v QueryIntent) []byte { b, _ := json.Marshal(v); return b }

// AuthorizeTx must reload and lock current grant/source authority in this same
// tenant transaction. A nil callback denies, including otherwise valid plans.
type ControlPlaneExecutor struct {
	Pool        persistence.TxBeginner
	AuthorizeTx func(context.Context, pgx.Tx, string, string, graph.Scope, string) error
}

const visibleFinding = `f.cluster_uid=$2 AND ((f.namespace=ANY($3::text[])) OR (f.namespace='' AND $4)) AND (cardinality($5::text[])=0 OR f.resource_canonical_id=ANY($5::text[])) AND COALESCE(f.resource_canonical_id,'') !~ '/(VirtualMachine|VirtualMachineInstance|DataVolume)/'`
const findingsBase = `SELECT f.finding_id::text AS identity,f.namespace,COALESCE(f.lifecycle_state,'unknown') AS state,COALESCE(f.payload->>'severity','unknown') AS severity,''::text AS operation,''::text AS subject,
 CASE WHEN f.schema_version='finding-envelope/v2' AND f.payload->>'timeReliable'='true' THEN (f.payload->>'startsAt')::timestamptz ELSE NULL END AS event_time
 FROM finding.records f WHERE f.tenant_id=$1 AND length($6::text)>0 AND ` + visibleFinding
const incidentsBase = `SELECT i.incident_id::text AS identity,i.namespace,i.state,''::text AS severity,''::text AS operation,''::text AS subject,i.created_at AS event_time
 FROM incident.records i WHERE i.tenant_id=$1 AND length($6::text)>0 AND i.cluster_uid=$2 AND (i.namespace=ANY($3::text[]) OR (i.namespace='' AND $4)) AND (cardinality($5::text[])=0 OR i.resource_canonical_id=ANY($5::text[]))
 AND NOT EXISTS(SELECT 1 FROM incident.finding_links l JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=i.tenant_id AND l.incident_id=i.incident_id AND NOT COALESCE((` + visibleFinding + `),false))`
const executionsBase = `SELECT e.execution_id::text AS identity,r.namespace,e.state,''::text AS severity,''::text AS operation,e.subject,e.created_at AS event_time
 FROM action.executions e JOIN platform.resource_entities r ON r.tenant_id=e.tenant_id AND r.canonical_id=e.target_canonical_id
 JOIN platform.cluster_registrations c ON c.tenant_id=r.tenant_id AND c.cluster_id=r.cluster_id
 WHERE e.tenant_id=$1 AND c.cluster_uid=$2 AND (r.namespace=ANY($3::text[]) OR (r.namespace='' AND $4)) AND (cardinality($5::text[])=0 OR e.target_canonical_id=ANY($5::text[])) AND e.subject=$6 AND e.target_canonical_id !~ '/(VirtualMachine|VirtualMachineInstance|DataVolume)/'`
const auditBase = `SELECT record_id::text AS identity,''::text AS namespace,''::text AS state,''::text AS severity,event_type AS operation,subject,created_at AS event_time FROM audit.records WHERE tenant_id=$1 AND $2::text='' AND cardinality($3::text[])=0 AND NOT $4 AND cardinality($5::text[])=0 AND length($6::text)>0`

// Parameter positions 1..6 are trusted scope/owner; 7..13 are bounded values.
// Aggregate and missing-time coverage share one PostgreSQL statement snapshot.
const aggregateSuffix = `), filtered AS (SELECT * FROM scoped WHERE ($7::text='' OR namespace=$7) AND ($8::text='' OR state=$8) AND ($9::text='' OR severity=$9) AND ($10::text='' OR operation=$10) AND ($11::text='' OR subject=$11)), buckets AS (
 SELECT CASE $12::text WHEN 'namespace' THEN namespace WHEN 'state' THEN state WHEN 'severity' THEN severity WHEN 'operation' THEN operation WHEN 'subject' THEN subject WHEN 'day' THEN to_char(event_time AT TIME ZONE $13,'YYYY-MM-DD') ELSE '' END AS dimension,identity
 FROM filtered WHERE event_time>=$14 AND event_time<$15), grouped AS (SELECT dimension,count(DISTINCT identity)::bigint AS value FROM buckets GROUP BY dimension), coverage AS (SELECT count(*)::bigint AS missing FROM filtered WHERE event_time IS NULL)
 SELECT false AS metadata,dimension,value,coverage.missing,pg_current_snapshot()::text FROM grouped CROSS JOIN coverage
 UNION ALL SELECT true,'',0,coverage.missing,pg_current_snapshot()::text FROM coverage`

func (e ControlPlaneExecutor) Execute(ctx context.Context, p ControlPlanePlan) (QueryResult, error) {
	out := QueryResult{TimeRange: copyQueryTimeRange(p.intent.TimeRange), SchemaVersion: "query-result/v1", DatasetID: p.intent.DatasetID, PlanDigest: p.digest, CatalogDigest: p.catalogDigest, ScopeDigest: graph.ScopeDigest(p.scope), Status: "complete", Exact: true, Dimension: p.dimension, Unit: p.unit, Rows: []Row{}, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}}
	if p.digest == "" || e.Pool == nil || e.AuthorizeTx == nil {
		return out, ErrDenied
	}
	var base string
	suffix := aggregateSuffix
	derived := false
	switch p.queryID {
	case "findings.occurrences/v1":
		base = findingsBase
	case "incidents.records/v1":
		base = incidentsBase
	case "executions.records/v1":
		base = executionsBase
	case "incidents.recovery-duration/v1":
		base = strings.Replace(incidentsBase, "i.created_at AS event_time", "i.resolved_at AS event_time, CASE WHEN i.started_at IS NOT NULL AND i.resolved_at>=i.started_at AND EXISTS(SELECT 1 FROM incident.finding_links start_link JOIN finding.records start_finding USING(tenant_id,finding_id) WHERE start_link.tenant_id=i.tenant_id AND start_link.incident_id=i.incident_id AND start_finding.schema_version='finding-envelope/v2' AND start_finding.payload->>'timeReliable'='true' AND (start_finding.payload->>'startsAt')::timestamptz=i.started_at) THEN extract(epoch FROM (i.resolved_at-i.started_at))*1000 ELSE NULL END AS sample", 1) + " AND i.state IN ('resolved','closed')"
		suffix = recoverySuffix
		derived = true
	case "executions.success-rate/v1":
		base = executionsBase + " AND e.state IN ('succeeded','failed')"
		suffix = successSuffix
		derived = true
	case "audit.events/v1":
		base = auditBase
	default:
		return out, ErrUnsupported
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tenant, err := uuid.Parse(p.scope.Tenant)
	if err != nil {
		return out, ErrInvalid
	}
	err = persistence.WithTenantTx(bounded, e.Pool, tenant, func(tx pgx.Tx) error {
		if err := e.AuthorizeTx(bounded, tx, p.subject, p.domain, p.scope, p.grantDigest); err != nil {
			return ErrDenied
		}
		if _, err := tx.Exec(bounded, `SET LOCAL statement_timeout='2000ms'`); err != nil {
			return err
		}
		namespaces, resources := p.scope.Namespaces, p.scope.Resources
		if namespaces == nil {
			namespaces = []string{}
		}
		if resources == nil {
			resources = []string{}
		}
		args := []any{tenant, p.scope.Cluster, namespaces, p.scope.ClusterScoped, resources, p.subject, p.filters["namespace"], p.filters["state"], p.filters["severity"], p.filters["operation"], p.filters["subject"], p.dimension, p.intent.TimeRange.Timezone, p.intent.TimeRange.From, p.intent.TimeRange.To}
		rows, err := tx.Query(bounded, "WITH scoped AS ("+base+suffix, args...)
		if err != nil {
			return err
		}
		var missing int64
		metadataSeen := false
		for rows.Next() {
			var metadata bool
			var row Row
			var snapshot string
			var omitted int64
			if err := rows.Scan(&metadata, &row.Dimension, &row.Value, &omitted, &snapshot); err != nil {
				rows.Close()
				return err
			}
			if math.IsNaN(row.Value) || math.IsInf(row.Value, 0) || row.Value < 0 || omitted < 0 || snapshot == "" {
				rows.Close()
				return ErrSource
			}
			missing = omitted
			out.ControlPlaneSnapshot = snapshot
			if metadata {
				metadataSeen = true
			} else {
				out.Rows = append(out.Rows, row)
			}
			if len(out.Rows) > 10000 {
				rows.Close()
				return ErrSource
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if !metadataSeen {
			return ErrSource
		}
		out.SnapshotReadAt = time.Now().UTC()
		if missing > 0 {
			out.Exact = false
			out.Status = "partial"
			out.Warnings = append(out.Warnings, "event_time_unreliable_or_legacy_schema")
		}
		if p.dimension == "" && len(out.Rows) == 0 {
			if missing > 0 || derived {
				out.Exact = false
				if derived {
					out.Warnings = append(out.Warnings, "no_valid_samples")
				}
				out.Status = "no_data"
			} else {
				out.Rows = []Row{{Value: 0}}
			}
		}
		if derived && len(out.Rows) == 0 {
			out.Status, out.Exact = "no_data", false
		}
		sort.Slice(out.Rows, func(i, j int) bool {
			a, b := out.Rows[i], out.Rows[j]
			if p.sort.Field == p.intent.Metrics[0] && a.Value != b.Value {
				if p.sort.Direction == "asc" {
					return a.Value < b.Value
				}
				return a.Value > b.Value
			}
			if p.sort.Field == p.dimension && p.sort.Direction == "desc" {
				return a.Dimension > b.Dimension
			}
			return a.Dimension < b.Dimension
		})
		if len(out.Rows) > p.limit {
			out.Truncated = true
			out.Rows = out.Rows[:p.limit]
		}
		return nil
	})
	if err != nil {
		return QueryResult{}, err
	}
	if contract.Validate("https://ops.local/schemas/query-result/v1", rawResult(out)) != nil {
		return QueryResult{}, ErrSource
	}
	return out, nil
}
