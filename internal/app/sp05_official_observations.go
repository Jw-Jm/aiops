package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"net"
	"net/http"
	"net/url"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	inspection "ops-platform/internal/inspection/kubernetes"
	kube "ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
	"strings"
	"sync"
	"syscall"
	"time"
)

type OfficialInspectionReport struct {
	ProbeAvailable  bool     `json:"probeAvailable"`
	MetricAvailable bool     `json:"metricAvailable"`
	Candidates      int      `json:"candidates"`
	DegradedSources []string `json:"degradedSources"`
}

func startSP05OfficialObservations(ctx context.Context, archive *evidence.ArchiveService, cluster SP04Cluster, client *kube.Client, runtime *observability.Runtime, group *sync.WaitGroup) {
	group.Add(1)
	go func() {
		defer group.Done()
		round := 0
		for ctx.Err() == nil {
			pass, cancel := context.WithTimeout(ctx, 10*time.Second)
			report, err := InspectSP05OfficialObservations(pass, archive, cluster, client, round)
			round++
			cancel()
			runtime.Metrics.SetComponentDegraded("sp05-control-plane/"+cluster.SourceID, !report.ProbeAvailable || err != nil)
			runtime.Metrics.SetComponentDegraded("sp05-metrics/"+cluster.SourceID, !report.MetricAvailable || err != nil)
			if err != nil {
				runtime.Logger.WarnContext(ctx, "SP05 official observation unavailable", "errorClass", collectionErrorClass(err))
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
		}
	}()
}

// Fixed official GETs share the existing cluster limiter. A round selects one of
// all current admitted Node identities; no separate source/client or scan service
// is introduced. Optional Metrics failures do not withdraw necessary Recipe facts.
func InspectSP05OfficialObservations(ctx context.Context, archive *evidence.ArchiveService, cluster SP04Cluster, client *kube.Client, round int) (OfficialInspectionReport, error) {
	report := OfficialInspectionReport{DegradedSources: []string{}}
	if round < 0 || archive == nil || client == nil {
		return report, inspection.ErrObservation
	}
	repository := evidence.Repository{Pool: archive.Pool}
	binding, err := repository.RegisteredBinding(ctx, evidence.Binding{Tenant: cluster.Tenant, SourceID: cluster.SourceID, Revision: cluster.SourceRevision, SourceType: "kubernetes", BackendLogicalID: cluster.BackendLogicalID})
	if err != nil {
		return report, err
	}
	if cluster.SourceScopeDigest != "" && evidence.BindingScopeDigest(binding) != cluster.SourceScopeDigest {
		return report, evidence.ErrScopeUnverified
	}
	var node unstructured.Unstructured
	err = persistence.WithTenantTx(ctx, archive.Pool, uuid.MustParse(cluster.Tenant), func(tx pgx.Tx) error {
		var name, uid string
		err := tx.QueryRow(ctx, `WITH subjects AS (SELECT DISTINCT e.name,a.alias_value FROM platform.resource_entities e JOIN platform.resource_aliases a USING(tenant_id,canonical_id) WHERE e.tenant_id=$1 AND e.cluster_id=(SELECT cluster_id FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2) AND e.kind='Node' AND e.deleted_at IS NULL AND a.source_id=$3 AND a.alias_kind='uid' AND a.scope=$4), numbered AS (SELECT name,alias_value,row_number() OVER(ORDER BY name,alias_value)-1 AS position,count(*) OVER() AS total FROM subjects) SELECT name,alias_value FROM numbered WHERE position=mod($5::bigint,total) LIMIT 1`, cluster.Tenant, cluster.SourceID, cluster.SourceID, cluster.ClusterUID, round).Scan(&name, &uid)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		node = unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Node"}}
		node.SetName(name)
		node.SetUID(types.UID(uid))
		return nil
	})
	if err != nil {
		return report, err
	}
	status, _, probeErr := fixedOfficialGET(ctx, client, "/version", 64<<10)
	probeClass := officialTransportClass(probeErr)
	if status != 0 && status != 200 {
		probeClass = ""
	}
	if status == 200 && probeErr == nil {
		// The bounded response above includes a valid native gitVersion.
		report.ProbeAvailable = true
	}
	if node.GetUID() == "" {
		if !report.ProbeAvailable {
			report.DegradedSources = append(report.DegradedSources, "kubernetes/control-plane")
		}
		return report, repository.CheckBinding(ctx, binding)
	}
	signals, signalErr := inspection.ControlPlaneSignals(node, status, probeClass, time.Now().UTC())
	if signalErr != nil {
		report.DegradedSources = append(report.DegradedSources, "kubernetes/control-plane")
	}
	if err := repository.CheckBinding(ctx, binding); err != nil {
		return report, err
	}
	for _, c := range signals {
		if err := submitOfficialObservation(ctx, archive, binding, cluster, c, "sp05-controlplane-probe/v1"); err != nil {
			return report, err
		}
		report.Candidates++
	}
	if !report.ProbeAvailable {
		report.DegradedSources = append(report.DegradedSources, "kubernetes/control-plane")
		return report, nil
	}
	// Resolve and revalidate the exact current Node UID before and after the
	// name-based metrics request. Names, labels and untrusted metric UIDs cannot
	// silently attach an old/recreated Node sample to a different identity.
	native, err := readAdmittedNode(ctx, client, node, binding)
	if err != nil {
		report.DegradedSources = append(report.DegradedSources, "kubernetes/node-metrics-identity")
		return report, repository.CheckBinding(ctx, binding)
	}
	_, metrics, err := fixedOfficialGET(ctx, client, "/apis/metrics.k8s.io/v1beta1/nodes/"+url.PathEscape(node.GetName()), 64<<10)
	if err != nil {
		report.DegradedSources = append(report.DegradedSources, "kubernetes/metrics")
		return report, repository.CheckBinding(ctx, binding)
	}
	after, err := readAdmittedNode(ctx, client, node, binding)
	if err != nil {
		report.DegradedSources = append(report.DegradedSources, "kubernetes/node-metrics-identity")
		return report, repository.CheckBinding(ctx, binding)
	}
	// Allocatable changes during the read make the derived ratio ambiguous.
	beforeCapacity, _, _ := unstructured.NestedStringMap(native.Object, "status", "allocatable")
	afterCapacity, _, _ := unstructured.NestedStringMap(after.Object, "status", "allocatable")
	beforeRaw, _ := json.Marshal(beforeCapacity)
	afterRaw, _ := json.Marshal(afterCapacity)
	if string(beforeRaw) != string(afterRaw) {
		report.DegradedSources = append(report.DegradedSources, "kubernetes/metrics-capacity-changed")
		return report, nil
	}
	signals, err = inspection.MetricSignals(after, metrics, time.Now().UTC())
	if err != nil {
		report.DegradedSources = append(report.DegradedSources, "kubernetes/metrics")
		return report, repository.CheckBinding(ctx, binding)
	}
	if err := repository.CheckBinding(ctx, binding); err != nil {
		return report, err
	}
	for _, c := range signals {
		if err := submitOfficialObservation(ctx, archive, binding, cluster, c, "sp05-official-metrics/v1"); err != nil {
			return report, err
		}
		report.Candidates++
	}
	report.MetricAvailable = true
	return report, repository.CheckBinding(ctx, binding)
}

func fixedOfficialGET(ctx context.Context, client *kube.Client, path string, maxBytes int64) (int, unstructured.Unstructured, error) {
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	res, err := client.Do(deadline, "GET", path, nil)
	if err != nil {
		return 0, unstructured.Unstructured{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return res.StatusCode, unstructured.Unstructured{}, inspection.ErrObservation
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if err != nil || int64(len(raw)) > maxBytes {
		return res.StatusCode, unstructured.Unstructured{}, inspection.ErrObservation
	}
	var object unstructured.Unstructured
	if json.Unmarshal(raw, &object.Object) != nil {
		return res.StatusCode, object, inspection.ErrObservation
	}
	if path == "/version" {
		if version, ok := object.Object["gitVersion"].(string); !ok || !strings.HasPrefix(version, "v") {
			return res.StatusCode, object, inspection.ErrObservation
		}
	}
	return res.StatusCode, object, nil
}

func officialTransportClass(err error) string {
	if err == nil {
		return ""
	}
	// Only failures from an issued HTTP request qualify. A rate-budget wait uses
	// a bare context error and cannot become a control-plane incident.
	var request *url.Error
	if !errors.As(err, &request) {
		return "local_or_contract_failure"
	}
	var operation *net.OpError
	if errors.As(request.Err, &operation) && operation.Op == "dial" && errors.Is(operation.Err, syscall.ECONNREFUSED) {
		return "dial_refused"
	}
	var dns *net.DNSError
	if errors.As(request.Err, &dns) {
		return "dns_invalid"
	}
	if request.Timeout() {
		return "request_timeout"
	}
	return "tls_or_transport_invalid"
}

func readAdmittedNode(ctx context.Context, client *kube.Client, expected unstructured.Unstructured, binding evidence.Binding) (unstructured.Unstructured, error) {
	_, node, err := fixedOfficialGET(ctx, client, "/api/v1/nodes/"+url.PathEscape(expected.GetName()), 128<<10)
	if err != nil || node.GetKind() != "Node" || node.GetAPIVersion() != "v1" || node.GetName() != expected.GetName() || node.GetUID() != expected.GetUID() || node.GetNamespace() != "" {
		return node, inspection.ErrObservation
	}
	for key, value := range binding.ScopeMapping.RequiredLabels {
		if node.GetLabels()[key] != value {
			return node, evidence.ErrScopeUnverified
		}
	}
	return node, nil
}

func submitOfficialObservation(ctx context.Context, archive *evidence.ArchiveService, binding evidence.Binding, cluster SP04Cluster, c inspection.Candidate, template string) error {
	return SubmitSP05Candidate(ctx, archive, binding, finding.FindingCandidate{ResourceCanonicalID: c.CanonicalID(cluster.Tenant, cluster.ClusterUID), Namespace: c.Namespace, RuleID: c.RuleID, RuleFamily: c.RuleFamily, NormalizedSymptom: c.NormalizedSymptom, State: c.State, NativeIdentity: finding.Hash([]any{c.ResourceUID, c.NativeData}), IndependenceGroup: c.ResourceUID + "/" + template, ObservedAt: c.ObservedAt, TimeReliable: c.TimeReliable, QueryTemplateVersion: template, Data: c.NativeData})
}
