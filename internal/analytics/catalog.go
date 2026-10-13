package analytics

import (
	"encoding/json"
	"ops-platform/api"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
)

type Dataset struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	AuthorizationDomain string   `json:"authorizationDomain"`
	Source              string   `json:"source"`
	TemplateVersion     string   `json:"templateVersion"`
	Metric              string   `json:"metric"`
	Unit                string   `json:"unit"`
	Calculation         string   `json:"calculation"`
	Dimensions          []string `json:"dimensions"`
	Kinds               []string `json:"kinds"`
	ClusterScopedKinds  []string `json:"clusterScopedKinds"`
	Filters             []string `json:"filters"`
	RequiredFilters     []string `json:"requiredFilters"`
	HealthValues        []string `json:"healthValues"`
	MaxGroups           int      `json:"maxGroups"`
	HistorySupported    bool     `json:"historySupported"`
	ZeroMeaning         string   `json:"zeroMeaning"`
}
type AnalysisCatalog struct {
	SchemaVersion string    `json:"schemaVersion"`
	Version       string    `json:"version"`
	Datasets      []Dataset `json:"datasets"`
}

// Catalog returns a fresh value: mutation by one caller cannot change a later plan.
func Catalog() (AnalysisCatalog, error) {
	var catalog AnalysisCatalog
	raw, err := api.AnalysisCatalog.ReadFile("analytics/catalog-v1.json")
	if err != nil {
		return catalog, err
	}
	if err := contract.Validate("https://ops.local/schemas/analysis-catalog/v1", raw); err != nil {
		return catalog, err
	}
	err = json.Unmarshal(raw, &catalog)
	return catalog, err
}
func catalogDigest(c AnalysisCatalog) string { return graph.ScopeDigest(c) }
