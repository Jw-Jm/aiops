package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/url"
	"ops-platform/internal/audit"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/persistence"
	"strconv"
	"time"
)

type LeaseRecoveryRequest struct{ Tenant, Cluster, Namespace, Name, ExpectedPreviousLeaseUID, Ticket string }
type LeaseRecoveryReceipt struct {
	IntentID, LeaseUID string
	EpochFloor         int64
}
type RecoveryRepository interface {
	Load(context.Context, string, string) (OwnershipMirror, error)
	BeginRecovery(context.Context, LeaseRecoveryRequest, string, OwnershipMirror, int64) (string, error)
	FinishRecovery(context.Context, LeaseRecoveryRequest, string, string, OwnershipMirror, LeaseRecoveryReceipt) error
}

// RecoverLease is an explicit operator repair, never a background election.
// The API server authenticates the operator and remains the sole Lease source.
// The retained monotonic mirror supplies a floor only; no owner is selected and
// the routing mirror remains expired until a qualified new process claims Lease.
func RecoverLease(ctx context.Context, client *kubernetes.Client, repo RecoveryRepository, request LeaseRecoveryRequest) (LeaseRecoveryReceipt, error) {
	out := LeaseRecoveryReceipt{}
	if client == nil || repo == nil || request.Namespace == "" || request.Name == "" || request.Ticket == "" || len(request.Ticket) > 256 {
		return out, ErrScope
	}
	// Use the API server's authenticated subject rather than caller actor text.
	review := []byte(`{"apiVersion":"authentication.k8s.io/v1","kind":"SelfSubjectReview"}`)
	response, err := client.Do(ctx, "POST", "/apis/authentication.k8s.io/v1/selfsubjectreviews", bytes.NewReader(review))
	if err != nil {
		return out, fmt.Errorf("recovery request unavailable: %w", ErrNotReady)
	}
	var subject struct {
		Status struct {
			UserInfo struct {
				Username string `json:"username"`
			} `json:"userInfo"`
		} `json:"status"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&subject)
	response.Body.Close()
	if err != nil || response.StatusCode != 201 || subject.Status.UserInfo.Username == "" {
		return out, ErrScope
	}
	response, err = client.Do(ctx, "GET", "/api/v1/namespaces/kube-system", nil)
	if err != nil {
		return out, ErrNotReady
	}
	var cluster struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&cluster)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || cluster.Metadata.UID != request.Cluster {
		return out, ErrScope
	}
	retained, err := repo.Load(ctx, request.Tenant, request.Cluster)
	if err != nil || retained.Epoch < 1 || retained.LeaseUID == "" || retained.LeaseUID != request.ExpectedPreviousLeaseUID {
		return out, errors.New("retained epoch floor unavailable or changed")
	}
	path := "/apis/coordination.k8s.io/v1/namespaces/" + url.PathEscape(request.Namespace) + "/leases/" + url.PathEscape(request.Name)
	response, err = client.Do(ctx, "GET", path, nil)
	if err != nil {
		return out, ErrNotReady
	}
	status := response.StatusCode
	document := LeaseDocument{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	document.Metadata.Name = request.Name
	document.Metadata.Namespace = request.Namespace
	if status == 200 {
		err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&document)
	}
	response.Body.Close()
	if err != nil || status != 200 && status != 404 {
		return out, fmt.Errorf("read Lease status=%d: %w", status, ErrNotReady)
	}
	floor := retained.Epoch
	if status == 200 {
		if document.Metadata.UID == "" || document.Metadata.ResourceVersion == "" {
			return out, ErrNotReady
		}
		if document.Spec.HolderIdentity != "" && time.Now().Before(document.Spec.RenewTime.Add(time.Duration(document.Spec.LeaseDurationSeconds)*time.Second)) {
			return out, errors.New("Lease holder is still live")
		}
		epoch, parseErr := strconv.ParseInt(document.Metadata.Annotations["ops.platform/owner-epoch"], 10, 64)
		if parseErr != nil || epoch < 0 {
			return out, ErrNotReady
		}
		floor = max(floor, epoch)
	}
	if floor == int64(^uint64(0)>>1) {
		return out, ErrNotReady
	}
	floor++
	intent, err := repo.BeginRecovery(ctx, request, subject.Status.UserInfo.Username, retained, floor)
	if err != nil {
		return out, err
	}
	document.Metadata.Annotations = map[string]string{"ops.platform/owner-epoch": strconv.FormatInt(floor, 10), "ops.platform/recovery-intent": intent}
	document.Spec.HolderIdentity = ""
	document.Spec.LeaseDurationSeconds = 15
	document.Spec.RenewTime = metav1.NewMicroTime(time.Now().UTC().Add(-time.Minute))
	document.Spec.AcquireTime = document.Spec.RenewTime
	raw, _ := json.Marshal(document)
	method, target := "PUT", path
	if status == 404 {
		method = "POST"
		target = "/apis/coordination.k8s.io/v1/namespaces/" + url.PathEscape(request.Namespace) + "/leases"
	}
	response, err = client.Do(ctx, method, target, bytes.NewReader(raw))
	if err != nil {
		return out, ErrNotReady
	}
	var published LeaseDocument
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&published)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 && response.StatusCode != 201 || published.Metadata.UID == "" || published.Metadata.Annotations["ops.platform/owner-epoch"] != strconv.FormatInt(floor, 10) {
		return out, fmt.Errorf("publish Lease status=%d decode=%v: %w", response.StatusCode, err, ErrNotReady)
	}
	out = LeaseRecoveryReceipt{IntentID: intent, LeaseUID: published.Metadata.UID, EpochFloor: floor}
	if err := repo.FinishRecovery(ctx, request, subject.Status.UserInfo.Username, intent, retained, out); err != nil {
		return out, fmt.Errorf("authority repaired; audit receipt pending: %w", err)
	}
	return out, nil
}

func (r Repository) BeginRecovery(ctx context.Context, request LeaseRecoveryRequest, subject string, previous OwnershipMirror, floor int64) (string, error) {
	id := uuid.Must(uuid.NewV7())
	tenant, err := uuid.Parse(request.Tenant)
	if err != nil {
		return "", ErrScope
	}
	err = persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		_, err := audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: id, EntityID: id, EntityKind: "graph_ownership", EventType: "graph.lease_recovery_requested", Subject: subject, Payload: map[string]any{"clusterUid": request.Cluster, "previousLeaseUid": previous.LeaseUID, "previousEpoch": previous.Epoch, "epochFloor": floor, "ticket": request.Ticket, "namespace": request.Namespace, "leaseName": request.Name}})
		return err
	})
	return id.String(), err
}
func (r Repository) FinishRecovery(ctx context.Context, request LeaseRecoveryRequest, subject, intent string, previous OwnershipMirror, receipt LeaseRecoveryReceipt) error {
	tenant, err := uuid.Parse(request.Tenant)
	if err != nil {
		return ErrScope
	}
	id, err := uuid.Parse(intent)
	if err != nil {
		return ErrScope
	}
	return persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE platform.graph_ownership SET owner_epoch=$5,lease_uid=$6,owner_instance='recovery_pending',route_expires_at=clock_timestamp()-interval '1 second',observed_at=clock_timestamp() WHERE tenant_id=$1 AND cluster_uid=$2 AND lease_uid=$3 AND owner_epoch=$4`, tenant, request.Cluster, previous.LeaseUID, previous.Epoch, receipt.EpochFloor, receipt.LeaseUID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrStale
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: id, EntityKind: "graph_ownership", EventType: "graph.lease_recovery_applied", Subject: subject, Payload: map[string]any{"clusterUid": request.Cluster, "leaseUid": receipt.LeaseUID, "epochFloor": receipt.EpochFloor, "intentId": intent}})
		return err
	})
}
