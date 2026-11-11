package app

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"log/slog"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"sync"
	"time"
)

// One queue per cluster holds only IDs of durable encrypted intents. Backpressure
// forces a collector retry and is visible in Graph health; it never drops a
// short-lived fact while advertising complete collection.
type projectionArchiveQueue struct {
	ctx       context.Context
	cluster   SP04Cluster
	graph     *graph.Graph
	archive   *evidence.ArchiveService
	jobs      chan projectionJob
	mu        sync.Mutex
	pending   map[string]bool
	failed    map[string]bool
	conflicts map[string]bool
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
	if len(q.conflicts) > 0 {
		reason = "projection_archive_identity_conflict"
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
		q.mu.Lock()
		if errors.Is(err, errProjectionArchiveConflict) {
			if q.conflicts == nil {
				q.conflicts = map[string]bool{}
			}
			q.conflicts[key] = true
		}
		q.failed[key] = true
		q.healthLocked()
		q.mu.Unlock()
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending[key] {
		return nil
	}
	delete(q.conflicts, key)
	if id == uuid.Nil {
		delete(q.failed, key)
		q.healthLocked()
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
		reported := false
		for q.ctx.Err() == nil {
			bounded, cancel := context.WithTimeout(q.ctx, projectionArchiveTimeout)
			err := q.archive.Recover(bounded, uuid.MustParse(q.cluster.Tenant), job.id)
			cancel()
			q.mu.Lock()
			if err == nil {
				delete(q.pending, job.key)
				delete(q.failed, job.key)
				if len(q.jobs) < cap(q.jobs) {
					delete(q.failed, "backpressure")
				}
			}
			if err != nil {
				q.failed[job.key] = true
				if !reported {
					slog.Warn("Projection archive recovery unavailable", "evidenceId", job.id, "error", evidence.Redact(err.Error()))
					reported = true
				}
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
