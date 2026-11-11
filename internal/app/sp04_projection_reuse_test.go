package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

type projectionReusePool struct{ tx *projectionReuseTx }

func (p projectionReusePool) Begin(context.Context) (pgx.Tx, error) { return p.tx, nil }

type projectionReuseTx struct {
	pgx.Tx
	metadata evidence.Evidence
	mapping  []byte
}
type projectionReuseRow func(...any) error

func (r projectionReuseRow) Scan(dest ...any) error { return r(dest...) }
func (t *projectionReuseTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	return projectionReuseRow(func(dest ...any) error {
		switch {
		case strings.Contains(query, "set_config"):
			*dest[0].(*string) = args[0].(string)
		case strings.Contains(query, "SELECT m.metadata"):
			raw, _ := json.Marshal(t.metadata)
			ref, _ := json.Marshal(t.metadata.ArchiveRef)
			*dest[0].(*[]byte) = raw
			*dest[1].(*string) = "apps"
			*dest[2].(*string) = t.metadata.ReplayState
			*dest[3].(*[]byte) = ref
		case strings.Contains(query, "SELECT s.data_scope_mapping"):
			*dest[0].(*[]byte) = t.mapping
		default:
			return errors.New("unexpected query")
		}
		return nil
	})
}
func (t *projectionReuseTx) Commit(context.Context) error   { return nil }
func (t *projectionReuseTx) Rollback(context.Context) error { return nil }

func TestProjectionRelistReusesOnlyVerifiedMatchingArchive(t *testing.T) {
	tenant := "11111111-1111-4111-8111-111111111111"
	source := "11111111-1111-4111-8111-111111111112"
	mapping := datascope.Mapping{Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}, RequiredLabels: map[string]string{}}
	cluster := SP04Cluster{Tenant: tenant, SourceID: source, ClusterUID: "cluster", SourceRevision: 1, BackendLogicalID: "native"}
	cluster.SourceScopeDigest = evidence.BindingScopeDigest(evidence.Binding{Revision: 1, BackendLogicalID: "native", SourceType: "kubernetes", ScopeMapping: mapping})
	o := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "pod", "namespace": "apps", "uid": "native-uid", "resourceVersion": "123"}}}
	raw, _ := json.Marshal(o.Object)
	id := resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: "cluster", APIGroup: "core", Kind: "Pod", StableID: "native-uid"}.String()
	identity, _ := json.Marshal(struct {
		ID, RV, ProjectionVersion string
		SourceRevision            int64
	}{id, "123", "kubernetes-projection/v4", 1})
	objectID := uuid.NewSHA1(uuid.NameSpaceOID, append([]byte(source), identity...))
	at := time.Now().Add(-time.Hour)
	o.SetAnnotations(map[string]string{"ops.internal/observed-at": at.Format(time.RFC3339Nano)})
	original := evidence.Evidence{TenantID: tenant, EvidenceID: objectID.String(), SourceSystem: "kubernetes", SourceRegistrationID: source, SourceRevision: 1, SourceScopeDigest: cluster.SourceScopeDigest, ResourceCanonicalID: id, ContentDigest: evidence.Digest(raw), QueryHash: evidence.Digest(identity), QueryTemplateVersion: "kubernetes-projection/v4", BackendLogicalID: "native", EffectiveScope: graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}, ClusterScoped: true}, ReplayState: "archived_verified", ArchiveRef: &evidence.ArchiveRef{BackendLogicalID: "archive"}, EvaluatedAt: at}
	for _, tc := range []string{"verified", "pending", "wrong-store", "conflicting-content"} {
		t.Run(tc, func(t *testing.T) {
			previous := original
			previous.ArchiveRef = &evidence.ArchiveRef{BackendLogicalID: "archive"}
			switch tc {
			case "pending":
				previous.ReplayState = "archive_pending"
			case "wrong-store":
				previous.ArchiveRef.BackendLogicalID = "other"
			case "conflicting-content":
				previous.ContentDigest = evidence.Digest([]byte("different"))
			}
			m, _ := json.Marshal(mapping)
			tx := &projectionReuseTx{metadata: previous, mapping: m}
			// No Store or Protector: only a verified, identity-checked replay may avoid preparing new ciphertext.
			archive := &evidence.ArchiveService{Pool: projectionReusePool{tx}, BackendLogicalID: "archive"}
			got, err := prepareProjection(t.Context(), cluster, o, archive)
			if tc == "verified" {
				if err != nil || got != uuid.Nil {
					t.Fatalf("verified replay was resealed: %v", err)
				}
				g := graph.New(tenant, "cluster", "test", nil)
				q := &projectionArchiveQueue{ctx: t.Context(), cluster: cluster, archive: archive, graph: g, jobs: make(chan projectionJob, 1), pending: map[string]bool{}, failed: map[string]bool{}}
				q.jobs <- projectionJob{key: "unrelated", id: uuid.New()}
				if err := q.enqueue(o); err != nil || len(q.jobs) != 1 || len(q.pending) != 0 {
					t.Fatalf("verified replay filled archive queue: %v", err)
				}
			} else if err == nil {
				t.Fatal("unverified or conflicting fact bypassed preparation")
			}
		})
	}
}
