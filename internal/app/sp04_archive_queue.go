package app

import (
	"context"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"sync"
	"time"
)

// One queue per cluster holds only IDs of durable encrypted intents. Backpressure
// forces a collector retry and is visible in Graph health; it never drops a
// short-lived fact while advertising complete collection.
type projectionArchiveQueue struct {
	ctx     context.Context
	cluster SP04Cluster
	graph   *graph.Graph
	archive *evidence.ArchiveService
	jobs    chan projectionJob
	mu      sync.Mutex
	pending map[string]bool
	failed  map[string]bool
}
type projectionJob struct {
	key string
	id  uuid.UUID
}

func newProjectionArchiveQueue(ctx context.Context, cluster SP04Cluster, g *graph.Graph, archive *evidence.ArchiveService, group *sync.WaitGroup) *projectionArchiveQueue {
	q := &projectionArchiveQueue{ctx: ctx, cluster: cluster, graph: g, archive: archive, jobs: make(chan projectionJob, 256), pending: map[string]bool{}, failed: map[string]bool{}}
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() { defer group.Done(); q.run() }()
	}
	return q
}
func (q *projectionArchiveQueue) healthLocked() {
	reason := ""
	if len(q.pending) > 0 {
		reason = "archive_pending"
	}
	if len(q.failed) > 0 {
		reason = "archive_backpressure_or_unavailable"
	}
	q.graph.SetSourceDegraded(q.cluster.SourceID+"/archive", reason)
}
func (q *projectionArchiveQueue) enqueue(o unstructured.Unstructured) error {
	key := string(o.GetUID()) + "/" + o.GetResourceVersion()
	q.mu.Lock()
	if q.pending[key] {
		q.mu.Unlock()
		return nil
	}
	q.mu.Unlock()
	id, err := prepareProjection(q.ctx, q.cluster, o, q.archive)
	if err != nil {
		q.graph.SetSourceDegraded(q.cluster.SourceID+"/archive", "archive_prepare_unavailable")
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending[key] {
		return nil
	}
	select {
	case q.jobs <- projectionJob{key, id}:
		q.pending[key] = true
		delete(q.failed, "backpressure")
		q.healthLocked()
	default:
		q.failed["backpressure"] = true
		q.healthLocked()
		return evidence.ErrBudget
	}
	return nil
}
func (q *projectionArchiveQueue) run() {
	for {
		var job projectionJob
		select {
		case <-q.ctx.Done():
			return
		case job = <-q.jobs:
		}
		for q.ctx.Err() == nil {
			bounded, cancel := context.WithTimeout(q.ctx, 3*time.Second)
			err := q.archive.Recover(bounded, uuid.MustParse(q.cluster.Tenant), job.id)
			cancel()
			q.mu.Lock()
			if err == nil {
				delete(q.pending, job.key)
				delete(q.failed, job.key)
			}
			if err != nil {
				q.failed[job.key] = true
			}
			q.healthLocked()
			q.mu.Unlock()
			if err == nil {
				break
			}
			select {
			case <-q.ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}
}
