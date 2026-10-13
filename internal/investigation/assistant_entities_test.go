package investigation

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestAssistantEntityReferencesRequireIdentityManifest(t *testing.T) {
	for _, tc := range []struct {
		tool, data string
		want       []string
	}{
		{"get_dependencies", `{"nodes":[{"canonicalId":"observed","attributes":{"canonicalId":"injected"}},{"canonicalId":"observed"}],"edges":[{"targetCanonicalId":"unobserved"}]}`, []string{"observed"}},
		{"get_assistant_context", `{"binding":{"resourceContext":{"resources":[{"canonicalId":"root"}],"symptom":"invented"}},"message":"other"}`, []string{"root"}},
		{"get_resource_investigation_context", `{"context":{"resources":[{"canonicalId":"root"}]}}`, []string{"root"}},
		{"resolve_resources", `{"candidates":[{"canonicalId":"candidate"}]}`, []string{"candidate"}},
		{"query_logs", `{"nodes":[{"canonicalId":"injected"}]}`, nil},
		{"model", `{"nodes":[{"canonicalId":"invented"}]}`, nil},
	} {
		if got := committedAssistantEntities(tc.tool, json.RawMessage(tc.data)); !slices.Equal(got, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.tool, got, tc.want)
		}
	}
}
