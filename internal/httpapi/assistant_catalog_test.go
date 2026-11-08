package httpapi

import "testing"

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
