package app

import (
	"context"
	"errors"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/k8sgpt"
	"ops-platform/internal/observability"
	"slices"
	"sync"
	"time"
)

// Analyzer lives in the same Worker. One invocation handles one current bound
// namespace; round robin includes every configured namespace without a second
// persistent process or an independent Kubernetes request budget.
func startSP05Analyzer(ctx context.Context, archive *evidence.ArchiveService, cluster SP04Cluster, h graph.InternalHandler, config SP05AnalyzerConfig, runtime *observability.Runtime, group *sync.WaitGroup) {
	if !config.Enabled {
		return
	}
	group.Add(1)
	go func() {
		defer group.Done()
		round := 0
		for ctx.Err() == nil {
			pass, cancel := context.WithTimeout(ctx, 25*time.Second)
			err := sp05AnalyzerPass(pass, archive, cluster, h, config, round)
			round++
			cancel()
			runtime.Metrics.SetComponentDegraded("sp05-analyzer/"+cluster.SourceID, err != nil)
			if err != nil {
				runtime.Logger.WarnContext(ctx, "SP05 fixed Analyzer unavailable", "errorClass", collectionErrorClass(err))
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()
}

func sp05AnalyzerPass(ctx context.Context, archive *evidence.ArchiveService, cluster SP04Cluster, h graph.InternalHandler, config SP05AnalyzerConfig, round int) error {
	if h.Lease == nil || h.Lease.Client == nil || h.Graph == nil || !h.Graph.Qualified(time.Now()) {
		return graph.ErrNotReady
	}
	if err := h.Lease.Confirm(ctx, h.Graph.OwnerEpoch()); err != nil {
		return err
	}
	repository := evidence.Repository{Pool: archive.Pool}
	binding, err := repository.RegisteredBinding(ctx, evidence.Binding{Tenant: cluster.Tenant, SourceID: cluster.SourceID, Revision: cluster.SourceRevision, SourceType: "kubernetes", BackendLogicalID: cluster.BackendLogicalID})
	if err != nil {
		h.Graph.SetSourceDegraded(cluster.SourceID+"/analyzer", "analyzer_source_revoked")
		return err
	}
	namespaces := slices.Clone(binding.ScopeMapping.Scopes["namespace"])
	slices.Sort(namespaces)
	if len(namespaces) == 0 {
		return evidence.ErrScopeUnverified
	}
	ns := namespaces[round%len(namespaces)]
	broker := &k8sgpt.ReadBroker{Client: h.Lease.Client, Namespace: ns, RequiredLabels: binding.ScopeMapping.RequiredLabels, Authorize: func(ctx context.Context) error { return repository.CheckBinding(ctx, binding) }}
	path, closeBroker, err := broker.Start(ctx)
	if err != nil {
		return err
	}
	defer closeBroker()
	out, err := (k8sgpt.Adapter{Binary: "/opt/ops/bin/k8sgpt", Kubeconfig: path, SHA256: config.SHA256, Namespace: ns}).Run(ctx)
	if err == nil && !broker.Complete() {
		err = k8sgpt.ErrDrift
	}
	if err != nil {
		h.Graph.SetSourceDegraded(cluster.SourceID+"/analyzer/"+ns, "analyzer_unavailable_or_partial")
		return err
	}
	candidates, err := k8sgpt.Candidates(out, broker.Objects(), cluster.Tenant, cluster.ClusterUID, time.Now().UTC())
	if err == nil {
		for _, candidate := range candidates {
			if e := SubmitSP05Candidate(ctx, archive, binding, candidate); e != nil {
				err = errors.Join(err, e)
			}
		}
	}
	reason := ""
	if err != nil {
		reason = "analyzer_candidate_ingestion_unavailable"
	}
	h.Graph.SetSourceDegraded(cluster.SourceID+"/analyzer/"+ns, reason)
	if err == nil {
		h.Graph.SetSourceDegraded(cluster.SourceID+"/analyzer", "")
	}
	return err
}
