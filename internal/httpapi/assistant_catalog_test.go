package httpapi

import (
	"ops-platform/internal/analytics"
	"testing"
	"time"
)

func TestAssistantExternalQueryCanOnlyNarrowRequestedWindow(t *testing.T) {
	start := time.Date(2026, 11, 8, 4, 10, 0, 0, time.UTC)
	requested := &analytics.QueryTimeRange{From: start, To: start.Add(5 * time.Minute), Timezone: "Asia/Shanghai"}
	for _, dataset := range []string{"metrics.pod-cpu", "logs.records", "network.flows", "incidents.records"} {
		intent := analytics.QueryIntent{DatasetID: dataset, TimeRange: requested}
		if !assistantQueryWindowAllowed(intent, requested) {
			t.Fatal("exact window rejected", dataset)
		}
		narrowed := *requested
		narrowed.To = narrowed.To.Add(-time.Minute)
		intent.TimeRange = &narrowed
		if assistantQueryWindowAllowed(intent, requested) != (dataset != "incidents.records") {
			t.Fatal("subset policy", dataset)
		}
		for _, invalid := range []analytics.QueryTimeRange{
			{From: start.Add(-time.Second), To: requested.To, Timezone: requested.Timezone},
			{From: start, To: requested.To.Add(time.Second), Timezone: requested.Timezone},
			{From: start, To: requested.To, Timezone: "UTC"},
			{From: start, To: start, Timezone: requested.Timezone},
		} {
			intent.TimeRange = &invalid
			if assistantQueryWindowAllowed(intent, requested) {
				t.Fatal("invalid window accepted", dataset, invalid)
			}
		}
	}
}

func TestAssistantContextUsesExactDomainDatasetContracts(t *testing.T) {
	for _, domain := range []string{"operational", "audit"} {
		catalog, err := analysisCatalogForDomain(domain)
		if err != nil || len(catalog.Datasets) == 0 {
			t.Fatal(domain, err)
		}
		for _, dataset := range catalog.Datasets {
			if dataset.AuthorizationDomain != domain {
				t.Fatal("cross-domain dataset exposed", dataset.ID)
			}
			if dataset.ID == "metrics.pod-cpu" && (dataset.Metric != "cpu_cores" || len(dataset.Filters) != 1 || dataset.Filters[0] != "namespace") {
				t.Fatal("model context lost the actual metric/filter contract", dataset)
			}
		}
	}
}
