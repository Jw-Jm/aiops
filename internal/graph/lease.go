package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log/slog"
	"net/url"
	"ops-platform/internal/integrations/kubernetes"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type LeaseDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name            string            `json:"name"`
		Namespace       string            `json:"namespace"`
		UID             string            `json:"uid"`
		ResourceVersion string            `json:"resourceVersion"`
		Annotations     map[string]string `json:"annotations"`
		Labels          map[string]string `json:"labels,omitempty"`
	} `json:"metadata"`
	Spec struct {
		HolderIdentity       string           `json:"holderIdentity"`
		LeaseDurationSeconds int              `json:"leaseDurationSeconds"`
		RenewTime            metav1.MicroTime `json:"renewTime"`
		AcquireTime          metav1.MicroTime `json:"acquireTime"`
	} `json:"spec"`
}
type OwnershipMirror struct {
	Tenant, Cluster, Endpoint, Instance, LeaseUID string
	Epoch                                         int64
	ExpiresAt                                     time.Time
}
type OwnershipRepository interface {
	Load(context.Context, string, string) (OwnershipMirror, error)
	Record(context.Context, OwnershipMirror) error
}

// ownershipSnapshot is the immutable owner identity a query may rely on. It is
// published only after a successful Lease CAS and mirror write, and cleared when
// the renewal loop can no longer prove ownership.
type ownershipSnapshot struct {
	epoch    int64
	leaseUID string
	instance string
}

type Lease struct {
	// renewal is a one-slot mutex held only by the renewal loop. Query paths do
	// not queue behind a renewal: they read the published ownership snapshot and
	// re-read the Lease document itself.
	renewal                   chan struct{}
	renewOnce                 sync.Once
	Client                    *kubernetes.Client
	Graph                     *Graph
	Mirror                    OwnershipRepository
	Namespace, Name, Endpoint string
	Logger                    *slog.Logger
	pending                   atomic.Pointer[ownershipSnapshot]
	// Renewal-local state, only mutated while holding renewal.
	leaseUID string
	epoch    int64
}

// Process identity belongs to the Graph and is recreated on every startup.
// No database CAS, polling or stored route can confer ownership.
func (l *Lease) lockRenewal(ctx context.Context) error {
	l.renewOnce.Do(func() {
		l.renewal = make(chan struct{}, 1)
		l.renewal <- struct{}{}
	})
	select {
	case <-l.renewal:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Lease) unlockRenewal() { l.renewal <- struct{}{} }

// renewing reports whether the renewal loop currently holds the fence. It is
// observability only: a query is never blocked or failed because of it.
func (l *Lease) renewing() bool {
	l.renewOnce.Do(func() {
		l.renewal = make(chan struct{}, 1)
		l.renewal <- struct{}{}
	})
	return len(l.renewal) == 0
}

type leaseStage struct {
	name  string
	start time.Time
}

func (l *Lease) Tick(ctx context.Context) error {
	if err := l.lockRenewal(ctx); err != nil {
		return err
	}
	defer l.unlockRenewal()
	if l.Client == nil || l.Graph == nil || l.Mirror == nil {
		l.loseOwnership()
		return ErrNotReady
	}
	started := time.Now()
	stage := leaseStage{name: "lease_read", start: started}
	next := func(name string) {
		now := time.Now()
		if elapsed := now.Sub(stage.start); elapsed > 200*time.Millisecond && l.Logger != nil {
			l.Logger.WarnContext(ctx, "Graph Lease renewal slow stage", "stage", stage.name, "elapsedMs", elapsed.Milliseconds())
		}
		stage = leaseStage{name: name, start: now}
	}
	failure := func(err error, reason string) error {
		l.loseOwnership()
		if l.Logger != nil {
			l.Logger.WarnContext(ctx, "Graph Lease renewal unavailable", "stage", stage.name, "reason", reason, "elapsedMs", time.Since(started).Milliseconds(), "epoch", l.epoch)
		}
		return err
	}
	path := "/apis/coordination.k8s.io/v1/namespaces/" + url.PathEscape(l.Namespace) + "/leases/" + url.PathEscape(l.Name)
	res, err := l.Client.Do(ctx, "GET", path, nil)
	if err != nil {
		return failure(ErrNotReady, "lease_transport_unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return failure(ErrNotReady, "lease_http_"+strconv.Itoa(res.StatusCode))
	}
	next("lease_document")
	var lease LeaseDocument
	if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&lease) != nil || lease.Metadata.UID == "" || lease.Metadata.ResourceVersion == "" {
		return failure(ErrNotReady, "lease_document_invalid")
	}
	epoch, err := strconv.ParseInt(lease.Metadata.Annotations["ops.platform/owner-epoch"], 10, 64)
	if err != nil || epoch < 0 {
		return failure(ErrNotReady, "lease_epoch_invalid")
	}
	next("route_mirror_read")
	mirror, err := l.Mirror.Load(ctx, l.Graph.tenant, l.Graph.cluster)
	if err != nil {
		return failure(err, "route_mirror_unavailable")
	}
	next("lease_identity")
	if (l.leaseUID != "" && l.leaseUID != lease.Metadata.UID) || (mirror.LeaseUID != "" && mirror.LeaseUID != lease.Metadata.UID) || epoch < mirror.Epoch {
		return failure(errors.New("Lease recovery audit required"), "lease_identity_changed")
	}
	l.leaseUID = lease.Metadata.UID
	now := time.Now()
	isHolder := lease.Spec.HolderIdentity == l.Graph.instance
	next("lease_holder")
	if !isHolder {
		if lease.Spec.HolderIdentity != "" && now.Before(lease.Spec.RenewTime.Add(time.Duration(lease.Spec.LeaseDurationSeconds)*time.Second)) {
			return failure(ErrNotReady, "lease_held_by_other")
		}
		if !l.Graph.Qualified(now) {
			return failure(ErrNotReady, "graph_not_qualified")
		}
		if epoch == int64(^uint64(0)>>1) {
			return failure(ErrNotReady, "lease_epoch_exhausted")
		}
		epoch++
		lease.Spec.AcquireTime = metav1.NewMicroTime(now)
		lease.Spec.HolderIdentity = l.Graph.instance
	}
	lease.Spec.RenewTime = metav1.NewMicroTime(now)
	lease.Spec.LeaseDurationSeconds = 15
	if lease.Metadata.Annotations == nil {
		lease.Metadata.Annotations = map[string]string{}
	}
	lease.Metadata.Annotations["ops.platform/owner-epoch"] = strconv.FormatInt(epoch, 10)
	encoded, _ := json.Marshal(lease)
	next("lease_cas")
	updated, err := l.Client.Do(ctx, "PUT", path, bytes.NewReader(encoded))
	if err != nil {
		return failure(ErrNotReady, "lease_cas_transport_unavailable")
	}
	updated.Body.Close()
	if updated.StatusCode != 200 {
		return failure(fmt.Errorf("Lease CAS failed: %d", updated.StatusCode), "lease_cas_http_"+strconv.Itoa(updated.StatusCode))
	}
	l.epoch = epoch
	deadline := now.Add(8 * time.Second)
	l.Graph.SetOwner(epoch, deadline)
	// Mirror expiry is shorter than both Lease validity and the renew deadline.
	next("route_mirror_write")
	if err := l.Mirror.Record(ctx, OwnershipMirror{Tenant: l.Graph.tenant, Cluster: l.Graph.cluster, Endpoint: l.Endpoint, Instance: l.Graph.instance, LeaseUID: l.leaseUID, Epoch: epoch, ExpiresAt: now.Add(6 * time.Second)}); err != nil {
		return failure(err, "route_mirror_write_unavailable")
	}
	l.pending.Store(&ownershipSnapshot{epoch: epoch, leaseUID: l.leaseUID, instance: l.Graph.instance})
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond && l.Logger != nil {
		l.Logger.WarnContext(ctx, "Graph Lease renewal slow", "elapsedMs", elapsed.Milliseconds(), "lastStage", stage.name)
	}
	return nil
}

func (l *Lease) loseOwnership() {
	if l.Graph != nil {
		l.Graph.InvalidateOwner()
	}
	l.pending.Store(nil)
}

// Confirm proves the current Lease document still matches the published owner
// identity without renewing it. Only the background Tick performs Lease CAS and
// mirror publication, and the query path never waits for it: waiting turned a
// concurrently running renewal into a spurious GRAPH_NOT_READY. Ownership is
// still proven by the Lease object itself, and every failure is fail-closed.
func (l *Lease) Confirm(ctx context.Context, expected int64) error {
	state := l.pending.Load()
	if l.Client == nil || l.Graph == nil || state == nil {
		return l.confirmFailure(ctx, "owner_state_unavailable")
	}
	if expected != state.epoch {
		return l.confirmFailure(ctx, "owner_epoch_mismatch")
	}
	path := "/apis/coordination.k8s.io/v1/namespaces/" + url.PathEscape(l.Namespace) + "/leases/" + url.PathEscape(l.Name)
	response, err := l.Client.Do(ctx, "GET", path, nil)
	if err != nil {
		// A transport failure proves as little about native Lease ownership as
		// a cancelled caller: fail this read closed while the independent
		// renewal loop and owner deadline keep fencing other requests.
		// Destroying confirmed ownership here would turn one flaky read of a
		// loaded API server into a cascade of degraded in-flight queries.
		return l.confirmFailure(ctx, "lease_transport_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		l.Graph.InvalidateOwner()
		return l.confirmFailure(ctx, "lease_http_"+strconv.Itoa(response.StatusCode))
	}
	var lease LeaseDocument
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&lease) != nil {
		// Same as a transport failure: an unreadable body is not evidence that
		// ownership changed, and the renewal loop still holds the fence.
		return l.confirmFailure(ctx, "lease_document_invalid")
	}
	epoch, err := strconv.ParseInt(lease.Metadata.Annotations["ops.platform/owner-epoch"], 10, 64)
	now := time.Now()
	switch {
	case err != nil || epoch != expected:
		l.Graph.InvalidateOwner()
		return l.confirmFailure(ctx, "lease_epoch_changed")
	case lease.Metadata.UID != state.leaseUID:
		l.Graph.InvalidateOwner()
		return l.confirmFailure(ctx, "lease_uid_changed")
	case lease.Spec.HolderIdentity != state.instance:
		l.Graph.InvalidateOwner()
		return l.confirmFailure(ctx, "lease_holder_changed")
	case lease.Spec.LeaseDurationSeconds != 15:
		l.Graph.InvalidateOwner()
		return l.confirmFailure(ctx, "lease_duration_changed")
	case !now.Before(lease.Spec.RenewTime.Add(8 * time.Second)):
		l.Graph.InvalidateOwner()
		return l.confirmFailure(ctx, "lease_renewal_expired")
	}
	if ctx.Err() != nil {
		return l.confirmFailure(ctx, "caller_cancelled")
	}
	return nil
}

func (l *Lease) confirmFailure(ctx context.Context, reason string) error {
	if l.Logger != nil {
		l.Logger.WarnContext(ctx, "Graph Lease confirm unavailable", "reason", reason, "renewing", l.renewing())
	}
	return ErrNotReady
}
