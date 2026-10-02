package app

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/redfish"
	"ops-platform/internal/observability"
	"ops-platform/internal/resource"
	"ops-platform/internal/resourcestore"
	"strings"
	"sync"
	"time"
)

func collectHardware(ctx context.Context, group *sync.WaitGroup, adapter *redfish.Adapter, g *graph.Graph, store resourcestore.Repository, archive *evidence.ArchiveService, metrics *observability.Metrics) {
	group.Add(1)
	go func() {
		defer group.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			pass, done := context.WithTimeout(ctx, 8*time.Second)
			err := (evidence.Repository{Pool: store.Pool}).CheckBinding(pass, adapter.Binding)
			if err == nil {
				inventory, collectErr := adapter.Collect(pass)
				if collectErr != nil || len(inventory.Entities) == 0 {
					g.SetSourceDegraded(adapter.Binding.SourceID, "hardware_inventory_unknown_or_partial")
					metrics.RecordOperation("resource", "resolve", "unavailable")
				} else {
					err = store.SaveHardware(pass, adapter.Binding, inventory.Entities)
					if err == nil {
						err = g.ReplaceHardware(pass, adapter.Binding.SourceID, inventory.Entities, time.Now())
					}
					if err == nil {
						relations, relationErr := store.HardwareRelations(pass, adapter.Binding, inventory.Entities)
						if relationErr == nil {
							err = g.ReplaceExternalSource(g.OwnerEpoch(), adapter.Binding.SourceID, relations)
						} else {
							err = relationErr
						}
					}
					if err == nil {
						g.SetSourceDegraded(adapter.Binding.SourceID, "")
						if inventory.Partial {
							g.SetSourceDegraded(adapter.Binding.SourceID, "hardware_inventory_unknown_or_partial")
						} else if adapter.Mode == "fixture_only" {
							g.SetSourceDegraded(adapter.Binding.SourceID, "hardware_fixture_only")
						}
						metrics.RecordOperation("resource", "resolve", "ok")
						archiveFailed := false
						for _, entity := range inventory.Entities {
							if entity.Kind != "PhysicalServer" {
								continue
							}
							boundedInventory := inventory
							boundedInventory.Entities = nil
							root, _ := resource.ParseCanonicalID(entity.CanonicalID)
							for _, item := range inventory.Entities {
								candidate, _ := resource.ParseCanonicalID(item.CanonicalID)
								if candidate.StableID == root.StableID || strings.HasPrefix(candidate.StableID, root.StableID+"/") {
									boundedInventory.Entities = append(boundedInventory.Entities, item)
								}
							}
							if health, ok := entity.Attributes["health"].(string); ok {
								boundedInventory.Health = health
							}
							data, _ := json.Marshal(boundedInventory)
							now := entity.UpdatedAt
							identity, _ := json.Marshal(struct {
								Canonical string
								Digest    string
							}{entity.CanonicalID, evidence.Digest(data)})
							metadata := evidence.Evidence{SourceScopeDigest: evidence.BindingScopeDigest(adapter.Binding), SchemaVersion: "evidence/v2", EvidenceID: sourceFactID(adapter.Binding.SourceID, identity), TenantID: adapter.Binding.Tenant, ResourceCanonicalID: entity.CanonicalID, Type: "hardware", DataClass: "D1", SourceRegistrationID: adapter.Binding.SourceID, SourceRevision: adapter.Binding.Revision, SourceSystem: "redfish", BackendLogicalID: adapter.Binding.BackendLogicalID, QueryTemplateVersion: "hardware-inventory/v1", QueryHash: evidence.Digest(identity), EffectiveScope: graph.Scope{Tenant: adapter.Binding.Tenant, Cluster: adapter.Config.Scope, ClusterScoped: true, AuthorizationRevision: "collector/" + adapter.Binding.SourceID}, EvaluatedAt: now, ObservedFrom: now, ObservedTo: now, SourceRetentionUntil: now, TimeReliable: false, ContentDigest: evidence.Digest(data), IndependenceGroup: adapter.Binding.SourceID, DerivationEvidenceRefs: []string{}, Data: data}
							if err := archive.Capture(pass, metadata, "", now.Add(181*24*time.Hour)); err != nil {
								archiveFailed = true
								metrics.RecordOperation("archive", "write", "unavailable")
							}
						}
						reason := ""
						if archiveFailed {
							reason = "hardware_archive_pending"
						}
						g.SetSourceDegraded(adapter.Binding.SourceID+"/archive", reason)
					}
				}
			}
			if err != nil {
				g.SetSourceDegraded(adapter.Binding.SourceID, "hardware_source_unverified_or_unavailable")
				metrics.RecordOperation("resource", "resolve", "unavailable")
			}
			done()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func sourceFactID(source string, identity []byte) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, append([]byte(source), identity...)).String()
}
