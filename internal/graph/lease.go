package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"ops-platform/internal/integrations/kubernetes"
	"strconv"
	"sync"
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
	} `json:"metadata"`
	Spec struct {
		HolderIdentity       string    `json:"holderIdentity"`
		LeaseDurationSeconds int       `json:"leaseDurationSeconds"`
		RenewTime            time.Time `json:"renewTime"`
		AcquireTime          time.Time `json:"acquireTime"`
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
type Lease struct {
	mu                        sync.Mutex
	Client                    *kubernetes.Client
	Graph                     *Graph
	Mirror                    OwnershipRepository
	Namespace, Name, Endpoint string
	Logger                    *slog.Logger
	leaseUID                  string
	epoch                     int64
	observed                  bool
}

// Process identity belongs to the Graph and is recreated on every startup.
// No database CAS, polling or stored route can confer ownership.
func (l *Lease) Tick(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Client == nil || l.Graph == nil || l.Mirror == nil {
		return ErrNotReady
	}
	stage := "lease_read"
	failure := func(err error) error {
		l.Graph.InvalidateOwner()
		if l.Logger != nil && (l.epoch > 0 || (stage != "lease_holder" && l.Graph.Qualified(time.Now()))) {
			l.Logger.WarnContext(ctx, "Graph Lease renewal unavailable", "stage", stage)
		}
		return err
	}
	path := "/apis/coordination.k8s.io/v1/namespaces/" + url.PathEscape(l.Namespace) + "/leases/" + url.PathEscape(l.Name)
	res, err := l.Client.Do(ctx, "GET", path, nil)
	if err != nil {
		return failure(ErrNotReady)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return failure(ErrNotReady)
	}
	stage = "lease_document"
	var lease LeaseDocument
	if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&lease) != nil || lease.Metadata.UID == "" || lease.Metadata.ResourceVersion == "" {
		return failure(ErrNotReady)
	}
	epoch, err := strconv.ParseInt(lease.Metadata.Annotations["ops.platform/owner-epoch"], 10, 64)
	if err != nil || epoch < 0 {
		return failure(ErrNotReady)
	}
	stage = "route_mirror_read"
	mirror, err := l.Mirror.Load(ctx, l.Graph.tenant, l.Graph.cluster)
	if err != nil {
		return failure(err)
	}
	stage = "lease_identity"
	if (l.leaseUID != "" && l.leaseUID != lease.Metadata.UID) || (mirror.LeaseUID != "" && mirror.LeaseUID != lease.Metadata.UID) || epoch < mirror.Epoch {
		return failure(errors.New("Lease recovery audit required"))
	}
	l.leaseUID = lease.Metadata.UID
	l.observed = true
	now := time.Now()
	isHolder := lease.Spec.HolderIdentity == l.Graph.instance
	stage = "lease_holder"
	if !isHolder {
		if lease.Spec.HolderIdentity != "" && now.Before(lease.Spec.RenewTime.Add(time.Duration(lease.Spec.LeaseDurationSeconds)*time.Second)) {
			return failure(ErrNotReady)
		}
		if !l.Graph.Qualified(now) {
			return failure(ErrNotReady)
		}
		if epoch == int64(^uint64(0)>>1) {
			return failure(ErrNotReady)
		}
		epoch++
		lease.Spec.AcquireTime = now
		lease.Spec.HolderIdentity = l.Graph.instance
	}
	lease.Spec.RenewTime = now
	lease.Spec.LeaseDurationSeconds = 15
	if lease.Metadata.Annotations == nil {
		lease.Metadata.Annotations = map[string]string{}
	}
	lease.Metadata.Annotations["ops.platform/owner-epoch"] = strconv.FormatInt(epoch, 10)
	encoded, _ := json.Marshal(lease)
	stage = "lease_cas"
	updated, err := l.Client.Do(ctx, "PUT", path, bytes.NewReader(encoded))
	if err != nil {
		return failure(ErrNotReady)
	}
	updated.Body.Close()
	if updated.StatusCode != 200 {
		return failure(fmt.Errorf("Lease CAS failed: %d", updated.StatusCode))
	}
	l.epoch = epoch
	deadline := now.Add(8 * time.Second)
	l.Graph.SetOwner(epoch, deadline)
	// Mirror expiry is shorter than both Lease validity and the renew deadline.
	stage = "route_mirror_write"
	if err := l.Mirror.Record(ctx, OwnershipMirror{Tenant: l.Graph.tenant, Cluster: l.Graph.cluster, Endpoint: l.Endpoint, Instance: l.Graph.instance, LeaseUID: l.leaseUID, Epoch: epoch, ExpiresAt: now.Add(6 * time.Second)}); err != nil {
		return failure(err)
	}
	return nil
}

// Confirm reads the sole ownership source without renewing it on the query
// path. Only the background Tick performs Lease CAS and mirror publication.
func (l *Lease) Confirm(ctx context.Context, expected int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Client == nil || l.Graph == nil || expected != l.epoch || !l.observed {
		return ErrNotReady
	}
	failure := func() error { l.Graph.InvalidateOwner(); return ErrNotReady }
	path := "/apis/coordination.k8s.io/v1/namespaces/" + url.PathEscape(l.Namespace) + "/leases/" + url.PathEscape(l.Name)
	response, err := l.Client.Do(ctx, "GET", path, nil)
	if err != nil {
		return failure()
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return failure()
	}
	var lease LeaseDocument
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&lease) != nil {
		return failure()
	}
	epoch, err := strconv.ParseInt(lease.Metadata.Annotations["ops.platform/owner-epoch"], 10, 64)
	now := time.Now()
	if err != nil || epoch != expected || lease.Metadata.UID != l.leaseUID || lease.Spec.HolderIdentity != l.Graph.instance || lease.Spec.LeaseDurationSeconds != 15 || !now.Before(lease.Spec.RenewTime.Add(8*time.Second)) {
		return failure()
	}
	return nil
}
