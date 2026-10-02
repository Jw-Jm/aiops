package redfish

import (
	"context"
	"errors"
	"net/http"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

type denyIO struct{ calls int }

func (d *denyIO) RoundTrip(*http.Request) (*http.Response, error) {
	d.calls++
	return nil, errors.New("unexpected BMC I/O")
}
func TestUnsupportedSourceMappingFailsBeforeBMCIO(t *testing.T) {
	for _, dimension := range []string{"nativeTenant", "labels", "account", "project", "organization", "team", "namespace", "cluster"} {
		t.Run(dimension, func(t *testing.T) {
			mapping := datascope.Mapping{Scopes: map[string][]string{"cluster": {"dc-a"}}}
			switch dimension {
			case "nativeTenant":
				mapping.NativeTenant = "account-a"
			case "labels":
				mapping.RequiredLabels = map[string]string{"team": "a"}
			case "cluster":
				mapping.Scopes["cluster"] = []string{"dc-a", "dc-b"}
			default:
				mapping.Scopes[dimension] = []string{"a"}
			}
			transport := &denyIO{}
			a := &Adapter{Binding: evidence.Binding{Tenant: "tenant-a", SourceID: "bmc-a", ScopeMapping: mapping}, Config: Config{Tenant: "tenant-a", Scope: "dc-a", SourceID: "bmc-a", Endpoint: "https://bmc.invalid", Client: &http.Client{Transport: transport}}, Authorize: func(context.Context, evidence.Binding) error { return nil }}
			id := resource.CanonicalID{Tenant: "tenant-a", Domain: "hardware", Scope: "dc-a", APIGroup: "redfish", Kind: "PhysicalServer", StableID: "server-a"}
			now := time.Now()
			_, err := a.Query(context.Background(), evidence.Query{ResourceCanonicalID: id.String(), Scope: graph.Scope{Tenant: "tenant-a", Cluster: "dc-a", ClusterScoped: true}, Template: "hardware-inventory/v1", From: now.Add(-time.Minute), To: now, Limit: 10})
			if !errors.Is(err, evidence.ErrScopeUnverified) {
				t.Fatalf("unsupported mapping accepted: %v", err)
			}
			if _, err = a.Capabilities(context.Background()); !errors.Is(err, evidence.ErrScopeUnverified) {
				t.Fatalf("unsupported capability mapping accepted: %v", err)
			}
			if _, err := a.Collect(context.Background()); !errors.Is(err, evidence.ErrScopeUnverified) {
				t.Fatalf("unsupported inventory collection accepted: %v", err)
			}
			if transport.calls != 0 {
				t.Fatalf("unsupported scope caused %d BMC reads", transport.calls)
			}
		})
	}
}
